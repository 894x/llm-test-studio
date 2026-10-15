package ssh

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

// Node owns a temporary node-token lease and an HTTP pool, not its parent SSH
// connection or accepted jobs. Close releases only these client-side resources.
type Node struct {
	parent   *Client
	identity runnerinstall.Identity
	token    *credentials.Lease
	http     *http.Client
	baseURL  string
}

func (*Node) String() string               { return "ssh.Node{redacted}" }
func (*Node) GoString() string             { return "ssh.Node{redacted}" }
func (*Node) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

// ConnectNode reuses the server's shared pairing without changing configuration,
// restarting its service, uploading an artifact, or comparing client builds.
func (client *Client) ConnectNode(ctx context.Context) (*Node, jobcontract.RunnerInfo, error) {
	document, err := client.ReadPrivate(ctx, runnerinstall.IdentityPath, 0)
	if err != nil {
		return nil, jobcontract.RunnerInfo{}, err
	}
	defer clear(document)
	identity, err := runnerinstall.DecodeIdentity(document)
	if err != nil {
		return nil, jobcontract.RunnerInfo{}, err
	}
	token, err := client.ReadPrivate(ctx, runnerinstall.PairingPath, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInvalid) {
			err = runnerinstall.ErrPairingRequired
		}
		return nil, jobcontract.RunnerInfo{}, err
	}
	defer clear(token)
	return client.connectPairing(ctx, identity, token)
}

// ConnectBoundNode uses the desktop's saved node-level authorization and exact
// installation identity. It never reads or silently replaces server pairing.
func (client *Client) ConnectBoundNode(
	ctx context.Context,
	identity runnerinstall.Identity,
	token *credentials.Lease,
) (*Node, jobcontract.RunnerInfo, error) {
	value, err := token.Bytes()
	if err != nil {
		return nil, jobcontract.RunnerInfo{}, runnerinstall.ErrPairingRequired
	}
	defer clear(value)
	return client.connectPairing(ctx, identity, value)
}

func (client *Client) connectPairing(
	ctx context.Context,
	identity runnerinstall.Identity,
	token []byte,
) (*Node, jobcontract.RunnerInfo, error) {
	if err := identity.Validate(); err != nil {
		return nil, jobcontract.RunnerInfo{}, err
	}
	digest := sha256.Sum256(token)
	encoded := hex.EncodeToString(digest[:])
	validSize := len(token) >= 32 && len(token) <= 128
	if !validSize || subtle.ConstantTimeCompare([]byte(encoded), []byte(identity.TokenSHA256)) != 1 {
		return nil, jobcontract.RunnerInfo{}, runnerinstall.ErrPairingRequired
	}
	lease, err := credentials.NewTemporaryLease(token)
	if err != nil {
		return nil, jobcontract.RunnerInfo{}, runnerinstall.ErrPairingRequired
	}
	httpClient, err := client.RunnerHTTP(identity.ListenPort)
	if err != nil {
		_ = lease.Close()
		return nil, jobcontract.RunnerInfo{}, err
	}
	node := &Node{
		parent: client, identity: identity, token: lease, http: httpClient,
		baseURL: "http://127.0.0.1:" + strconv.Itoa(int(identity.ListenPort)),
	}
	info, err := node.Info(ctx)
	if err != nil {
		_ = node.Close()
		return nil, jobcontract.RunnerInfo{}, err
	}
	return node, info, nil
}

func (node *Node) Identity() runnerinstall.Identity { return node.identity }

// PairingLease copies the shared credential for the local OS Store. The caller
// owns this short-lived lease; closing either lease does not close the other.
func (node *Node) PairingLease() (*credentials.Lease, error) {
	if node == nil {
		return nil, runnerinstall.ErrPairingRequired
	}
	value, err := node.token.Bytes()
	if err != nil {
		return nil, runnerinstall.ErrPairingRequired
	}
	defer clear(value)
	return credentials.NewTemporaryLease(value)
}

// Do sends only relative management routes through this node's pinned tunnel.
// Callers own request-body secret cleanup and response-body limits and Close.
func (node *Node) Do(ctx context.Context, method, route string, body io.Reader) (*http.Response, error) {
	invalidRequest := node == nil || ctx == nil || len(route) > 4096
	if invalidRequest {
		return nil, ErrInvalid
	}
	parsed, err := url.ParseRequestURI(route)
	validRoute := err == nil && strings.HasPrefix(route, "/") && !strings.HasPrefix(route, "//")
	localRoute := validRoute && !parsed.IsAbs() && parsed.Host == ""
	if !localRoute {
		return nil, ErrInvalid
	}
	token, err := node.token.Bytes()
	if err != nil {
		return nil, ErrConnection
	}
	defer clear(token)
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		node.baseURL+route,
		body,
	)
	if err != nil {
		return nil, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set(jobcontract.ContractHeader, strconv.Itoa(jobcontract.CurrentContract))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return node.http.Do(request)
}

func (node *Node) Info(ctx context.Context) (jobcontract.RunnerInfo, error) {
	response, err := node.Do(
		ctx,
		http.MethodGet,
		"/runner",
		nil,
	)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return jobcontract.RunnerInfo{}, runnerinstall.ErrPairingRequired
	case http.StatusUpgradeRequired:
		return jobcontract.RunnerInfo{}, runnerinstall.ErrUpgradeRequired
	case http.StatusOK:
	default:
		return jobcontract.RunnerInfo{}, runnerinstall.ErrService
	}
	info, err := jobcontract.DecodeRunnerInfo(response.Body)
	if err != nil {
		if ctx.Err() != nil {
			return jobcontract.RunnerInfo{}, ctx.Err()
		}
		invalidContract := errors.Is(err, jobcontract.ErrInvalid) || errors.Is(err, jobcontract.ErrUnsupported)
		if !invalidContract {
			return jobcontract.RunnerInfo{}, connectionError(ctx, err)
		}
		return jobcontract.RunnerInfo{}, runnerinstall.ErrUpgradeRequired
	}
	validIdentity := info.NodeID == node.identity.NodeID && info.NodeInstallationID == node.identity.InstallationID
	validPlatform := info.OS == "linux" && (info.Arch == "amd64" || info.Arch == "arm64")
	if !validIdentity || !validPlatform {
		return jobcontract.RunnerInfo{}, ErrIntegrity
	}
	return info, nil
}

func (node *Node) Close() error {
	if node != nil {
		err := node.token.Close()
		node.parent.releaseRunnerHTTP(node.http)
		return err
	}
	return nil
}

func (client *Client) releaseRunnerHTTP(httpClient *http.Client) {
	transport, ok := httpClient.Transport.(runnerTransport)
	if !ok {
		return
	}
	transport.CloseIdleConnections()
	client.mu.Lock()
	defer client.mu.Unlock()
	for index, current := range client.transports {
		if current == transport.transport {
			client.transports = append(client.transports[:index], client.transports[index+1:]...)
			return
		}
	}
}
