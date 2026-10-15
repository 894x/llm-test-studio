// Package ssh provides pinned-host connections to the runner's private management plane.
package ssh

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	cryptossh "golang.org/x/crypto/ssh"
)

var (
	ErrInvalid        = errors.New("invalid_remote_connection")
	ErrConnection     = errors.New("remote_connection_failed")
	ErrAuthentication = errors.New("remote_authentication_failed")
	ErrHostUnverified = errors.New("remote_host_key_confirmation_required")
	ErrHostChanged    = errors.New("remote_host_key_changed")
	ErrTransfer       = errors.New("remote_file_transfer_failed")
	ErrIntegrity      = errors.New("remote_file_integrity_failed")
	ErrExists         = errors.New("remote_file_already_exists")
	ErrUnsupported    = errors.New("remote_operation_unsupported")
)

const (
	connectTimeout   = 15 * time.Second
	operationTimeout = 30 * time.Second
	keepAliveEvery   = 15 * time.Second
	keepAliveTimeout = 5 * time.Second
)

type Endpoint struct {
	Host        string
	Port        uint16
	User        string
	Fingerprint string
	HostKeyType string
}

type HostKeyError struct {
	Fingerprint string
	KeyType     string
	Changed     bool
}

func (err *HostKeyError) Error() string { return err.Unwrap().Error() }

func (err *HostKeyError) Unwrap() error {
	if err.Changed {
		return ErrHostChanged
	}
	return ErrHostUnverified
}

type Authentication struct {
	password   *credentials.Lease
	privateKey *credentials.Lease
	passphrase *credentials.Lease
}

func PasswordAuthentication(password *credentials.Lease) Authentication {
	return Authentication{password: password}
}

func PrivateKeyAuthentication(key, passphrase *credentials.Lease) Authentication {
	return Authentication{privateKey: key, passphrase: passphrase}
}

func (Authentication) String() string               { return "ssh.Authentication{redacted}" }
func (Authentication) GoString() string             { return "ssh.Authentication{redacted}" }
func (Authentication) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

type Client struct {
	connection *cryptossh.Client
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	closed     bool
	transports []*http.Transport
}

func (*Client) String() string   { return "ssh.Client{redacted}" }
func (*Client) GoString() string { return "ssh.Client{redacted}" }

func (endpoint Endpoint) Validate() error {
	validHost := validHostname(endpoint.Host) || net.ParseIP(endpoint.Host) != nil
	validUser := len(endpoint.User) > 0 && len(endpoint.User) <= 64
	for _, character := range endpoint.User {
		validCharacter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		validCharacter = validCharacter || character >= '0' && character <= '9'
		validUser = validUser && (validCharacter || character == '_' || character == '-')
	}
	validPin := endpoint.Fingerprint == "" && endpoint.HostKeyType == ""
	if endpoint.Fingerprint != "" {
		validPin = validFingerprint(endpoint.Fingerprint) && len(hostAlgorithms(endpoint.HostKeyType)) > 0
		validPin = validPin && endpoint.HostKeyType != ""
	}
	invalid := !validHost || endpoint.Port == 0 || !validUser || !validPin
	if invalid {
		return ErrInvalid
	}
	return nil
}

func hostAlgorithms(keyType string) []string {
	switch keyType {
	case "":
		return []string{
			cryptossh.KeyAlgoED25519, cryptossh.KeyAlgoECDSA256, cryptossh.KeyAlgoECDSA384,
			cryptossh.KeyAlgoECDSA521, cryptossh.KeyAlgoRSASHA512, cryptossh.KeyAlgoRSASHA256,
		}
	case cryptossh.KeyAlgoED25519, cryptossh.KeyAlgoECDSA256, cryptossh.KeyAlgoECDSA384, cryptossh.KeyAlgoECDSA521:
		return []string{keyType}
	case cryptossh.KeyAlgoRSA:
		// The stored RSA key type does not enable SHA-1 signatures.
		return []string{cryptossh.KeyAlgoRSASHA512, cryptossh.KeyAlgoRSASHA256}
	default:
		return []string{}
	}
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		valid := len(label) > 0 && len(label) <= 63 && label[0] != '-' && label[len(label)-1] != '-'
		if !valid {
			return false
		}
		for _, character := range label {
			letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
			digit := character >= '0' && character <= '9'
			allowed := letter || digit || character == '-'
			if !allowed {
				return false
			}
		}
	}
	return true
}

func validFingerprint(fingerprint string) bool {
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		return false
	}
	digest, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(fingerprint, "SHA256:"))
	return err == nil && len(digest) == 32 && "SHA256:"+base64.RawStdEncoding.EncodeToString(digest) == fingerprint
}

// Dial does not own the supplied leases. Empty pins return the observed key for
// explicit confirmation before authentication; changed pins always fail closed.
// The connection is independent of ctx once its handshake has completed.
func Dial(ctx context.Context, endpoint Endpoint, authentication Authentication) (*Client, error) {
	if ctx == nil || endpoint.Validate() != nil {
		return nil, ErrInvalid
	}
	auth, clearAuth, err := authentication.method()
	if err != nil {
		return nil, err
	}
	defer clearAuth()
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	address := net.JoinHostPort(endpoint.Host, strconv.Itoa(int(endpoint.Port)))
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, connectionError(ctx, err)
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stopCancellation()
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	var keyError *HostKeyError
	config := &cryptossh.ClientConfig{
		User: endpoint.User, Auth: []cryptossh.AuthMethod{auth},
		HostKeyAlgorithms: hostAlgorithms(endpoint.HostKeyType),
		HostKeyCallback: func(_ string, _ net.Addr, key cryptossh.PublicKey) error {
			observed := cryptossh.FingerprintSHA256(key)
			if endpoint.Fingerprint == "" {
				keyError = &HostKeyError{Fingerprint: observed, KeyType: key.Type()}
				return keyError
			}
			if subtle.ConstantTimeCompare([]byte(endpoint.Fingerprint), []byte(observed)) != 1 {
				keyError = &HostKeyError{Fingerprint: observed, KeyType: key.Type(), Changed: true}
				return keyError
			}
			return nil
		},
	}
	connection, channels, requests, err := cryptossh.NewClientConn(raw, address, config)
	if err != nil {
		_ = raw.Close()
		if keyError != nil {
			return nil, keyError
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var rejected *cryptossh.ServerAuthError
		if errors.As(err, &rejected) || strings.Contains(err.Error(), "unable to authenticate") {
			return nil, ErrAuthentication
		}
		return nil, connectionError(ctx, err)
	}
	if !stopCancellation() || ctx.Err() != nil {
		_ = connection.Close()
		return nil, connectionError(ctx, nil)
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		_ = connection.Close()
		return nil, ErrConnection
	}
	client := &Client{
		connection: cryptossh.NewClient(connection, channels, requests),
		stop:       make(chan struct{}), done: make(chan struct{}), transports: []*http.Transport{},
	}
	go client.keepAlive(keepAliveEvery, keepAliveTimeout)
	return client, nil
}

func (authentication Authentication) method() (cryptossh.AuthMethod, func(), error) {
	if authentication.password != nil {
		if authentication.privateKey != nil || authentication.passphrase != nil {
			return nil, nil, ErrInvalid
		}
		password, err := authentication.password.Bytes()
		validSize := len(password) > 0 && len(password) <= 8192
		if err != nil || !validSize {
			clear(password)
			return nil, nil, ErrInvalid
		}
		return cryptossh.PasswordCallback(func() (string, error) { return string(password), nil }),
			func() { clear(password) }, nil
	}
	key, err := authentication.privateKey.Bytes()
	validSize := len(key) > 0 && len(key) <= 1<<20
	if err != nil || !validSize {
		clear(key)
		return nil, nil, ErrInvalid
	}
	defer clear(key)
	var signer cryptossh.Signer
	if authentication.passphrase != nil {
		passphrase, readErr := authentication.passphrase.Bytes()
		validSize := len(passphrase) > 0 && len(passphrase) <= 8192
		if readErr != nil || !validSize {
			clear(passphrase)
			return nil, nil, ErrInvalid
		}
		defer clear(passphrase)
		signer, err = cryptossh.ParsePrivateKeyWithPassphrase(key, passphrase)
	} else {
		signer, err = cryptossh.ParsePrivateKey(key)
	}
	if err != nil {
		return nil, nil, ErrAuthentication
	}
	return cryptossh.PublicKeys(signer), func() {}, nil
}

func connectionError(ctx context.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var timeout net.Error
	if errors.As(cause, &timeout) && timeout.Timeout() {
		return context.DeadlineExceeded
	}
	return ErrConnection
}

func (client *Client) keepAlive(interval, timeout time.Duration) {
	defer close(client.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-client.stop:
			return
		case <-ticker.C:
		}
		result := make(chan error, 1)
		go func() {
			// An SSH failure reply still proves that the peer is responsive.
			_, _, err := client.connection.SendRequest("keepalive@openssh.com", true, nil)
			result <- err
		}()
		timer := time.NewTimer(timeout)
		select {
		case err := <-result:
			timer.Stop()
			if err != nil {
				_ = client.connection.Close()
				return
			}
		case <-client.stop:
			timer.Stop()
			_ = client.connection.Close()
			<-result
			return
		case <-timer.C:
			_ = client.connection.Close()
			<-result
			return
		}
	}
}

// RunnerHTTP opens SSH direct-tcpip channels to one loopback port. It creates no
// local listener and cannot forward requests to another destination or a proxy.
func (client *Client) RunnerHTTP(port uint16) (*http.Client, error) {
	if client == nil || port == 0 {
		return nil, ErrInvalid
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil, ErrConnection
	}
	if len(client.transports) >= 4 {
		return nil, ErrInvalid
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
			if network != "tcp" || requested != address {
				return nil, ErrInvalid
			}
			ctx, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			// net/http detaches its dial context from request cancellation so a
			// connection may be pooled. A withheld SSH channel reply must instead
			// remain bounded by the request that initiated it.
			if owner, ok := ctx.Value(dialOwnerKey{}).(context.Context); ok {
				stop := context.AfterFunc(owner, cancel)
				defer stop()
			}
			connection, err := client.dialRunner(ctx, address)
			if err != nil {
				return nil, connectionError(ctx, err)
			}
			return connection, nil
		},
		MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16,
		IdleConnTimeout: time.Minute, ResponseHeaderTimeout: 25 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
	}
	client.transports = append(client.transports, transport)
	return &http.Client{
		Transport: runnerTransport{transport: transport}, Timeout: operationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

type dialOwnerKey struct{}

type runnerTransport struct{ transport *http.Transport }

func (transport runnerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := context.WithValue(request.Context(), dialOwnerKey{}, request.Context())
	return transport.transport.RoundTrip(request.Clone(ctx))
}

func (transport runnerTransport) CloseIdleConnections() { transport.transport.CloseIdleConnections() }

func (client *Client) dialRunner(ctx context.Context, address string) (net.Conn, error) {
	type opened struct {
		connection net.Conn
		err        error
	}
	result := make(chan opened, 1)
	go func() {
		connection, err := client.connection.Dial("tcp", address)
		result <- opened{connection: connection, err: err}
	}()
	select {
	case opened := <-result:
		if err := ctx.Err(); err != nil {
			if opened.connection != nil {
				_ = opened.connection.Close()
			}
			return nil, err
		}
		return opened.connection, opened.err
	case <-ctx.Done():
		// A peer may answer keepalives while withholding channel-open replies.
		// Closing the parent connection interrupts and joins that blocked open;
		// a bare ssh.DialContext would leave its Dial goroutine pending.
		_ = client.connection.Close()
		opened := <-result
		if opened.connection != nil {
			_ = opened.connection.Close()
		}
		return nil, ctx.Err()
	}
}

func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.once.Do(func() {
		client.mu.Lock()
		client.closed = true
		for _, transport := range client.transports {
			transport.CloseIdleConnections()
		}
		client.transports = []*http.Transport{}
		client.mu.Unlock()
		close(client.stop)
		_ = client.connection.Close()
		<-client.done
	})
	return nil
}
