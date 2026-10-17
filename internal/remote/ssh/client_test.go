package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/pkg/sftp"
	cryptossh "golang.org/x/crypto/ssh"
)

const fakePassword = "test-secret-for-ssh-only"

type testPeer struct {
	endpoint       Endpoint
	config         *cryptossh.ServerConfig
	listener       net.Listener
	mu             sync.Mutex
	clients        []net.Conn
	auths          atomic.Int32
	forwards       atomic.Int32
	keepalives     atomic.Int32
	stall          atomic.Bool
	stallForward   atomic.Bool
	stallKeepalive atomic.Bool
	userKey        []byte
	execute        func(string, cryptossh.Channel)
	sftpSession    func(cryptossh.Channel)
	keepaliveReply func(*cryptossh.Request)
	forwardOpen    func(cryptossh.NewChannel)
	stop           chan struct{}
	wg             sync.WaitGroup
}

func newTestPeer(t *testing.T) *testPeer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := cryptossh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &testPeer{
		listener: listener, clients: []net.Conn{}, stop: make(chan struct{}),
		endpoint: Endpoint{
			Host: "127.0.0.1", Port: uint16(listener.Addr().(*net.TCPAddr).Port), User: "tester",
			Fingerprint: cryptossh.FingerprintSHA256(signer.PublicKey()),
			HostKeyType: signer.PublicKey().Type(),
		},
	}
	peer.config = &cryptossh.ServerConfig{
		PasswordCallback: func(_ cryptossh.ConnMetadata, password []byte) (*cryptossh.Permissions, error) {
			peer.auths.Add(1)
			if string(password) != fakePassword {
				return nil, errors.New("denied")
			}
			return nil, nil
		},
		PublicKeyCallback: func(_ cryptossh.ConnMetadata, key cryptossh.PublicKey) (*cryptossh.Permissions, error) {
			peer.mu.Lock()
			defer peer.mu.Unlock()
			if bytes.Equal(peer.userKey, key.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	peer.config.AddHostKey(signer)
	peer.wg.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			peer.mu.Lock()
			peer.clients = append(peer.clients, connection)
			peer.mu.Unlock()
			peer.wg.Go(func() { peer.serve(connection) })
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		close(peer.stop)
		peer.mu.Lock()
		for _, connection := range peer.clients {
			_ = connection.Close()
		}
		peer.mu.Unlock()
		peer.wg.Wait()
	})
	return peer
}

func (peer *testPeer) serve(raw net.Conn) {
	defer raw.Close()
	connection, channels, requests, err := cryptossh.NewServerConn(raw, peer.config)
	if err != nil {
		return
	}
	defer connection.Close()
	peer.wg.Go(func() {
		for request := range requests {
			peer.keepalives.Add(1)
			if peer.keepaliveReply != nil {
				peer.keepaliveReply(request)
				continue
			}
			if peer.stallKeepalive.Load() {
				<-peer.stop
				return
			}
			_ = request.Reply(false, nil)
		}
	})
	for channel := range channels {
		peer.wg.Go(func() { peer.channel(channel) })
	}
}

func (peer *testPeer) channel(open cryptossh.NewChannel) {
	if open.ChannelType() == "direct-tcpip" {
		if peer.forwardOpen != nil {
			peer.forwardOpen(open)
			return
		}
		peer.forward(open)
		return
	}
	if open.ChannelType() != "session" {
		_ = open.Reject(cryptossh.UnknownChannelType, "unsupported")
		return
	}
	channel, requests, err := open.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for request := range requests {
		var payload struct{ Value string }
		if cryptossh.Unmarshal(request.Payload, &payload) != nil {
			_ = request.Reply(false, nil)
			continue
		}
		if request.Type == "subsystem" && payload.Value == "sftp" {
			_ = request.Reply(true, nil)
			if peer.sftpSession != nil {
				peer.sftpSession(channel)
				return
			}
			server, err := sftp.NewServer(channel)
			if err == nil {
				defer server.Close()
				_ = server.Serve()
			}
			return
		}
		if request.Type == "exec" && peer.execute != nil {
			_ = request.Reply(true, nil)
			peer.execute(payload.Value, channel)
			return
		}
		if request.Type != "exec" || payload.Value != "/bin/sh -s" {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		script, err := io.ReadAll(io.LimitReader(channel, 8192))
		if err != nil || string(script) != systemProbe {
			return
		}
		if peer.stall.Load() {
			<-peer.stop
			return
		}
		_, _ = io.WriteString(channel, "llm-test-system-probe\n0\nLinux\nx86_64\nsystemd 249 (249.11)\nsystemd\nyes\nyes\nno\n")
		_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{Status: 0}))
		return
	}
}

func (peer *testPeer) forward(open cryptossh.NewChannel) {
	if peer.stallForward.Load() {
		<-peer.stop
		return
	}
	var destination struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if cryptossh.Unmarshal(open.ExtraData(), &destination) != nil || destination.Host != "127.0.0.1" {
		_ = open.Reject(cryptossh.Prohibited, "not loopback")
		return
	}
	address := net.JoinHostPort(destination.Host, strconv.Itoa(int(destination.Port)))
	upstream, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		_ = open.Reject(cryptossh.ConnectionFailed, "unavailable")
		return
	}
	defer upstream.Close()
	channel, requests, err := open.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	peer.forwards.Add(1)
	peer.wg.Go(func() { cryptossh.DiscardRequests(requests) })
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, channel)
		_ = upstream.Close()
		close(copied)
	}()
	_, _ = io.Copy(channel, upstream)
	_ = channel.Close()
	<-copied
}

func passwordLease(t *testing.T, password string) *credentials.Lease {
	t.Helper()
	lease, err := credentials.NewTemporaryLease([]byte(password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	return lease
}

func dialPeer(t *testing.T, peer *testPeer) *Client {
	t.Helper()
	client, err := Dial(t.Context(), peer.endpoint, PasswordAuthentication(passwordLease(t, fakePassword)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestHostPinIsConfirmedBeforeAnyAuthenticationAndChangesAreBlocked(t *testing.T) {
	peer := newTestPeer(t)
	for _, pin := range []string{"", "SHA256:" + strings.Repeat("A", 43)} {
		endpoint := peer.endpoint
		endpoint.Fingerprint = pin
		if pin == "" {
			endpoint.HostKeyType = ""
		}
		_, err := Dial(t.Context(), endpoint, PasswordAuthentication(passwordLease(t, fakePassword)))
		var observed *HostKeyError
		if !errors.As(err, &observed) || observed.Fingerprint != peer.endpoint.Fingerprint {
			t.Fatal("host key was not returned for explicit confirmation")
		}
		if observed.Changed != (pin != "") || peer.auths.Load() != 0 {
			t.Fatal("unverified host received authentication material")
		}
	}
	client := dialPeer(t, peer)
	info, err := client.Probe(t.Context())
	validInfo := info.Arch == "amd64" && info.SystemdVersion == 249 && info.SystemdPID1
	if err != nil || !validInfo {
		t.Fatalf("fixed system probe failed: %v", err)
	}
	if peer.auths.Load() != 1 {
		t.Fatal("successful pinned connection did not authenticate exactly once")
	}
}

func TestSSHAuthenticationIsRedactedAndWrongSecretsFailSafely(t *testing.T) {
	peer := newTestPeer(t)
	authentication := PasswordAuthentication(passwordLease(t, "private-wrong-password"))
	_, err := Dial(t.Context(), peer.endpoint, authentication)
	if !errors.Is(err, ErrAuthentication) || strings.Contains(err.Error(), "private-wrong-password") {
		t.Fatal("authentication error was not safe and specific")
	}
	if document, err := json.Marshal(authentication); err == nil || len(document) != 0 {
		t.Fatal("authentication object was serializable")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", authentication, authentication), "private-wrong-password") {
		t.Fatal("authentication formatting exposed its secret")
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	block, err := cryptossh.MarshalPrivateKeyWithPassphrase(key, "", []byte("correct-key-password"))
	if err != nil {
		t.Fatal(err)
	}
	keyLease, _ := credentials.NewTemporaryLease(pem.EncodeToMemory(block))
	defer keyLease.Close()
	_, err = Dial(t.Context(), peer.endpoint, PrivateKeyAuthentication(keyLease, passwordLease(t, "wrong-passphrase")))
	if !errors.Is(err, ErrAuthentication) {
		t.Fatal("bad encrypted private key was not rejected safely")
	}
}

func TestSSHTunnelUsesOnlyRunnerLoopbackAndDoesNotFollowRedirects(t *testing.T) {
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path == "/redirect" {
			http.Redirect(
				writer,
				request,
				"http://example.invalid/blocked",
				http.StatusFound,
			)
			return
		}
		_, _ = writer.Write([]byte("runner-ok"))
	}))
	defer server.Close()
	port := uint16(server.Listener.Addr().(*net.TCPAddr).Port)
	httpClient, err := client.RunnerHTTP(port)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	request, _ := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL,
		nil,
	)
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	cancel()
	if string(body) != "runner-ok" {
		t.Fatal("runner response did not use the SSH tunnel")
	}
	response, err = httpClient.Get(server.URL + "/redirect")
	if err != nil || response.StatusCode != http.StatusFound {
		t.Fatal("management redirect was followed")
	}
	_ = response.Body.Close()
	if _, err := httpClient.Get("http://example.invalid/"); err == nil {
		t.Fatal("runner transport forwarded an arbitrary destination")
	}
	if calls.Load() != 2 || peer.forwards.Load() == 0 {
		t.Fatal("unexpected tunneled request count")
	}
	_ = client.Close()
	if _, err := httpClient.Get(server.URL); err == nil {
		t.Fatal("closed SSH client left its tunnel usable")
	}
}

func TestSSHContextCancelsStalledHandshakeAndCommand(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			_, _ = io.Copy(io.Discard, connection)
		}
	}()
	endpoint := Endpoint{Host: "127.0.0.1", Port: uint16(listener.Addr().(*net.TCPAddr).Port), User: "tester"}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	authentication := PasswordAuthentication(passwordLease(t, fakePassword))
	if _, err := Dial(ctx, endpoint, authentication); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("stalled SSH handshake ignored its deadline")
	}
	<-done
	peer := newTestPeer(t)
	peer.stall.Store(true)
	client := dialPeer(t, peer)
	ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Probe(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("stalled SSH command ignored its deadline")
	}
}

func TestSSHPrivateReadRejectsPublicFilesAndMissingFsyncFailsExplicitly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("remote Linux file ownership and permissions")
	}
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.ToSlash(filepath.Join(directory, "node-token"))
	if err := os.WriteFile(name, []byte("private-pairing-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	document, err := client.ReadPrivate(t.Context(), name, uint32(os.Getuid()))
	if err != nil || !bytes.Equal(document, []byte("private-pairing-fixture")) {
		t.Fatalf("private read failed: %v", err)
	}
	clear(document)
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadPrivate(t.Context(), name, uint32(os.Getuid())); !errors.Is(err, ErrInvalid) {
		t.Fatal("pairing file in a public parent directory was accepted")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadPrivate(t.Context(), name, uint32(os.Getuid())+1); !errors.Is(err, ErrInvalid) {
		t.Fatal("pairing file with an unexpected parent owner was accepted")
	}
	if err := os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadPrivate(t.Context(), name, uint32(os.Getuid())); !errors.Is(err, ErrInvalid) {
		t.Fatal("public pairing file was accepted")
	}
	if err := os.Symlink(name, filepath.Join(directory, "alias")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.ToSlash(filepath.Join(directory, "alias"))
	if _, err := client.ReadPrivate(t.Context(), alias, uint32(os.Getuid())); !errors.Is(err, ErrInvalid) {
		t.Fatal("pairing read followed a symlink")
	}
	// pkg/sftp's test server does not implement fsync; OpenSSH live validation
	// separately verifies the successful upload and readback path.
	err = client.UploadNew(t.Context(), Upload{
		Path: filepath.ToSlash(filepath.Join(directory, "program")), Source: strings.NewReader("binary"),
		Size: 6, SHA256: strings.Repeat("a", 64), Mode: 0700,
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing fsync extension was silently ignored")
	}
}

func TestSSHEndpointRejectsURLsCommandsAndMalformedPins(t *testing.T) {
	valid := Endpoint{Host: "server.example", Port: 22, User: "runner"}
	for _, host := range []string{"server.example", "127.0.0.1", "::1"} {
		endpoint := valid
		endpoint.Host = host
		if endpoint.Validate() != nil {
			t.Fatal("valid SSH host rejected")
		}
	}
	for _, host := range []string{"", "https://server.example", "-server", "a..b", "host;id", "user@host", "host\nname"} {
		endpoint := valid
		endpoint.Host = host
		if endpoint.Validate() == nil {
			t.Fatal("invalid SSH host accepted")
		}
	}
	valid.Fingerprint = "SHA256:not-a-digest"
	if valid.Validate() == nil {
		t.Fatal("invalid SSH fingerprint accepted")
	}
}

func TestFixedCommandOutputCannotBypassItsByteBudgetThroughIOCopy(t *testing.T) {
	output := &boundedOutput{limit: 4096}
	_, err := io.Copy(output, strings.NewReader(strings.Repeat("x", 4097)))
	if !errors.Is(err, ErrInvalid) || !output.exceeded || len(output.Bytes()) > 4096 {
		t.Fatal("remote command output bypassed its byte budget")
	}
}

func TestRunnerChannelOpenCancellationPreservesOtherResponsesAndClosesLateChannel(t *testing.T) {
	peer := newTestPeer(t)
	withheld, releaseOpen, lateClosed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var forwards atomic.Int32
	peer.forwardOpen = func(open cryptossh.NewChannel) {
		if forwards.Add(1) != 2 {
			peer.forward(open)
			return
		}
		close(withheld)
		select {
		case <-releaseOpen:
		case <-peer.stop:
			return
		}
		channel, requests, err := open.Accept()
		if err != nil {
			return
		}
		defer channel.Close()
		peer.wg.Go(func() { cryptossh.DiscardRequests(requests) })
		_, _ = io.Copy(io.Discard, channel)
		close(lateClosed)
	}
	client := dialPeer(t, peer)
	releaseBody := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow" {
			_, _ = io.WriteString(writer, "first")
			writer.(http.Flusher).Flush()
			select {
			case <-releaseBody:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(writer, "second")
			return
		}
		_, _ = io.WriteString(writer, "ok")
	}))
	defer server.Close()
	var releaseBodyOnce, releaseOpenOnce sync.Once
	defer releaseBodyOnce.Do(func() { close(releaseBody) })
	defer releaseOpenOnce.Do(func() { close(releaseOpen) })
	port := uint16(server.Listener.Addr().(*net.TCPAddr).Port)
	httpClient, err := client.RunnerHTTP(port)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpClient.Get(server.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, prefix); err != nil || string(prefix) != "first" {
		t.Fatal("existing response did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL+"/runner",
		nil,
	)
	if _, err := httpClient.Do(request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("withheld channel-open reply ignored the request deadline")
	}
	select {
	case <-withheld:
	case <-time.After(time.Second):
		t.Fatal("second channel was not withheld")
	}
	if _, err := client.Probe(t.Context()); err != nil {
		t.Fatal("one cancelled channel closed the shared SSH connection")
	}
	releaseBodyOnce.Do(func() { close(releaseBody) })
	if suffix, err := io.ReadAll(response.Body); err != nil || string(suffix) != "second" {
		t.Fatalf("another request lost its active response: %q %v", suffix, err)
	}
	releaseOpenOnce.Do(func() { close(releaseOpen) })
	select {
	case <-lateClosed:
	case <-time.After(time.Second):
		t.Fatal("late channel reply was not closed")
	}
	ready, err := httpClient.Get(server.URL + "/ready")
	if err != nil {
		t.Fatal("channel cancellation prevented later requests")
	}
	_ = ready.Body.Close()
	if _, err := client.Probe(t.Context()); err != nil {
		t.Fatal("late channel cleanup closed the shared SSH connection")
	}
}

func TestRunnerChannelOpensRemainBoundedAndCloseJoinsPendingOpens(t *testing.T) {
	peer := newTestPeer(t)
	opened := make(chan struct{}, maxChannelOpens+1)
	peer.forwardOpen = func(cryptossh.NewChannel) {
		opened <- struct{}{}
		<-peer.stop
	}
	client := dialPeer(t, peer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan error, maxChannelOpens)
	for range maxChannelOpens {
		go func() { _, err := client.dialRunner(ctx, "127.0.0.1:44917"); results <- err }()
	}
	for range maxChannelOpens {
		select {
		case <-opened:
		case <-time.After(time.Second):
			t.Fatal("pending channel opens did not fill their bound")
		}
	}
	cancel()
	for range maxChannelOpens {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatal("channel caller did not observe cancellation")
		}
	}
	if len(client.opening) != maxChannelOpens {
		t.Fatal("cancelled opens released their slots before their SSH replies")
	}
	boundedCtx, boundedCancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer boundedCancel()
	if _, err := client.dialRunner(boundedCtx, "127.0.0.1:44917"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting for a channel slot ignored its deadline")
	}
	select {
	case <-opened:
		t.Fatal("pending channel-open limit was exceeded")
	default:
	}
	waiting := make(chan error, 1)
	go func() { _, err := client.dialRunner(t.Context(), "127.0.0.1:44917"); waiting <- err }()
	closed := make(chan struct{})
	go func() { _ = client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt and join pending channel opens")
	}
	if err := <-waiting; !errors.Is(err, ErrConnection) || len(client.opening) != 0 {
		t.Fatal("Close retained pending opens or admitted a new one")
	}
}

func TestSSHPrivateKeyAuthenticationSupportsPlainAndEncryptedKeys(t *testing.T) {
	peer := newTestPeer(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := cryptossh.NewSignerFromKey(key)
	peer.mu.Lock()
	peer.userKey = signer.PublicKey().Marshal()
	peer.mu.Unlock()
	for _, encrypted := range []bool{false, true} {
		var block *pem.Block
		var err error
		var phrase *credentials.Lease
		if encrypted {
			block, err = cryptossh.MarshalPrivateKeyWithPassphrase(key, "", []byte("key-password"))
			phrase = passwordLease(t, "key-password")
		} else {
			block, err = cryptossh.MarshalPrivateKey(key, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		document := pem.EncodeToMemory(block)
		keyLease, _ := credentials.NewTemporaryLease(document)
		clear(document)
		ctx, cancel := context.WithCancel(t.Context())
		client, err := Dial(ctx, peer.endpoint, PrivateKeyAuthentication(keyLease, phrase))
		cancel()
		_ = keyLease.Close()
		if err != nil {
			t.Fatal("valid private-key authentication failed")
		}
		if _, err := client.Probe(t.Context()); err != nil {
			_ = client.Close()
			t.Fatal("closing handshake context or secret lease abandoned the established connection")
		}
		_ = client.Close()
	}
}

func TestSSHKeepaliveAcceptsFailureRepliesAndClosesAnUnresponsivePeer(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		peer := newTestPeer(t)
		peer.stallKeepalive.Store(stalled)
		client := dialPeer(t, peer)
		// Replace the idle production ticker with short test intervals before it
		// has sent a request. The same keepalive owner and Close path are used.
		close(client.stop)
		<-client.done
		client.stop, client.done = make(chan struct{}), make(chan struct{})
		go client.keepAlive(10*time.Millisecond, 30*time.Millisecond)
		if stalled {
			select {
			case <-client.done:
			case <-time.After(time.Second):
				t.Fatal("unresponsive keepalive did not close the connection")
			}
			if _, err := client.Probe(t.Context()); err == nil {
				t.Fatal("failed keepalive left the connection usable")
			}
			continue
		}
		deadline := time.Now().Add(time.Second)
		for peer.keepalives.Load() < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if peer.keepalives.Load() < 2 {
			t.Fatal("keepalive was not sent")
		}
		if _, err := client.Probe(t.Context()); err != nil {
			t.Fatal("SSH failure reply was mistaken for a disconnected peer")
		}
	}
}

func TestSSHKeepaliveWaitsForManagementSilenceBudget(t *testing.T) {
	if keepAliveTimeout < operationTimeout {
		t.Fatal("keepalive can close SSH before the current management operation budget expires")
	}
	const timeout = 300 * time.Millisecond
	const oldSilenceWindow = timeout / 6
	for _, fullSilence := range []bool{false, true} {
		t.Run(strconv.FormatBool(fullSilence), func(t *testing.T) {
			peer := newTestPeer(t)
			seen, release := make(chan struct{}), make(chan struct{})
			var first atomic.Bool
			peer.keepaliveReply = func(request *cryptossh.Request) {
				if first.CompareAndSwap(false, true) {
					close(seen)
					select {
					case <-release:
					case <-peer.stop:
						return
					}
				}
				_ = request.Reply(false, nil)
			}
			client := dialPeer(t, peer)
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			close(client.stop)
			<-client.done
			client.stop, client.done = make(chan struct{}), make(chan struct{})
			started := time.Now()
			go client.keepAlive(5*time.Millisecond, timeout)
			select {
			case <-seen:
			case <-time.After(time.Second):
				t.Fatal("keepalive was not sent")
			}
			if fullSilence {
				select {
				case <-client.done:
				case <-time.After(time.Second):
					t.Fatal("a full silent management window did not close SSH")
				}
				if time.Since(started) < timeout {
					t.Fatal("keepalive closed SSH before its silence budget")
				}
				if _, err := client.Probe(t.Context()); err == nil {
					t.Fatal("silent SSH connection remained usable")
				}
				return
			}
			timer := time.NewTimer(2 * oldSilenceWindow)
			defer timer.Stop()
			select {
			case <-client.done:
				t.Fatal("temporary silence closed SSH before its management budget")
			case <-timer.C:
			}
			if peer.keepalives.Load() != 1 {
				t.Fatal("waiting for a reply started another keepalive")
			}
			releaseOnce.Do(func() { close(release) })
			if _, err := client.Probe(t.Context()); err != nil {
				t.Fatal("a late keepalive reply within the management budget closed SSH")
			}
		})
	}
}
