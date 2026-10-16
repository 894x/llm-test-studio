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

type Request struct {
	Method     string
	Route      string
	Body       io.Reader
	Content    *jobcontract.ContentIdentity
	RangeStart *int64
	JSONGzip   bool
}

// Do sends only relative management routes through this node's pinned tunnel.
// Callers own request-body secret cleanup and response-body limits and Close.
func (node *Node) Do(ctx context.Context, input Request) (*http.Response, error) {
	if node == nil || ctx == nil {
		return nil, ErrInvalid
	}
	method, route, body := input.Method, input.Route, input.Body
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
	if start := input.RangeStart; start != nil {
		validRange := method == http.MethodGet && body == nil && *start >= 0 && !input.JSONGzip && input.Content == nil
		if !validRange {
			return nil, ErrInvalid
		}
	}
	httpClient := node.http
	var content jobcontract.ContentIdentity
	if input.Content != nil {
		content = *input.Content
		validContent := content.Validate() == nil && body != nil && !input.JSONGzip
		validRoute := method == http.MethodPut && route == "/assets/"+content.SHA256
		if !validContent || !validRoute {
			return nil, ErrInvalid
		}
		var err error
		httpClient, err = node.parent.bulkRunnerHTTP(node.http)
		if err != nil {
			return nil, err
		}
	}
	token, err := node.token.Bytes()
	if err != nil {
		return nil, ErrConnection
	}
	defer clear(token)
	var cancel context.CancelFunc
	if input.Content != nil {
		ctx, cancel = context.WithTimeout(ctx, jobcontract.AssetUploadTimeout)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		node.baseURL+route,
		body,
	)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set(jobcontract.ContractHeader, strconv.Itoa(jobcontract.CurrentContract))
	request.Header.Set("Accept-Encoding", "identity")
	if input.JSONGzip {
		request.Header.Set("Accept-Encoding", "gzip")
	}
	if input.RangeStart != nil {
		request.Header.Set("Range", "bytes="+strconv.FormatInt(*input.RangeStart, 10)+"-")
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if input.Content != nil {
		request.ContentLength = content.Size
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("Content-Encoding", "identity")
	}
	response, err := httpClient.Do(request)
	if cancel != nil {
		if err != nil {
			cancel()
		} else {
			response.Body = &contentResponse{ReadCloser: response.Body, cancel: cancel}
		}
	}
	return response, err
}

func (node *Node) Info(ctx context.Context) (jobcontract.RunnerInfo, error) {
	if ctx == nil {
		return jobcontract.RunnerInfo{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	response, err := node.Do(ctx, Request{Method: http.MethodGet, Route: "/runner"})
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

type contentResponse struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (response *contentResponse) Close() error {
	err := response.ReadCloser.Close()
	response.cancel()
	return err
}
