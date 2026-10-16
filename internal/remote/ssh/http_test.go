package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestRunnerHTTPBoundsEndpointOwnersAndReusesTwoFixedPoolRoles(t *testing.T) {
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	management := make([]*http.Client, 0, maxRunnerEndpoints)
	for range maxRunnerEndpoints {
		current, err := client.RunnerHTTP(44917)
		if err != nil {
			t.Fatal(err)
		}
		management = append(management, current)
		role := current.Transport.(runnerTransport)
		if current.Timeout != 2*time.Minute || role.transport.ResponseHeaderTimeout != 25*time.Second ||
			role.transport.Proxy != nil || role.owner.bulk != nil {
			t.Fatal("management role changed or allocated bulk before use")
		}
		var initial *http.Client
		for range 8 {
			bulk, err := client.bulkRunnerHTTP(current)
			if err != nil {
				t.Fatal(err)
			}
			if initial == nil {
				initial = bulk
			}
			bulkRole := bulk.Transport.(runnerTransport)
			if bulk != initial || bulk.Timeout != 0 || bulkRole.owner != role.owner ||
				bulkRole.transport == role.transport || bulkRole.transport.Proxy != nil ||
				bulkRole.transport.ResponseHeaderTimeout != jobcontract.AssetResponseHeaderTimeout {
				t.Fatal("bulk role was recreated or inherited the management timeout")
			}
		}
	}
	if _, err := client.RunnerHTTP(44918); !errors.Is(err, ErrInvalid) || len(client.endpoints) != 4 {
		t.Fatal("endpoint ownership exceeded its four-owner bound")
	}
	client.releaseRunnerHTTP(management[0])
	if _, err := client.bulkRunnerHTTP(management[0]); !errors.Is(err, ErrConnection) {
		t.Fatal("released endpoint reopened its bulk role")
	}
	if _, err := client.RunnerHTTP(44918); err != nil || len(client.endpoints) != 4 {
		t.Fatal("released owner did not return its capacity")
	}
	_ = client.Close()
	if len(client.endpoints) != 0 {
		t.Fatal("SSH close retained endpoint owners")
	}
	for _, current := range management {
		if _, err := client.bulkRunnerHTTP(current); !errors.Is(err, ErrConnection) {
			t.Fatal("SSH close left a lazy role usable")
		}
	}
}

func TestNodeContentUsesExactBinaryWireAndCanonicalRoute(t *testing.T) {
	content := []byte{0, 255, 1, 0, 10, 11}
	digest := sha256.Sum256(content)
	identity := jobcontract.ContentIdentity{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}
	var uploads atomic.Int32
	node, client := uploadNode(t, func(writer http.ResponseWriter, request *http.Request) {
		uploads.Add(1)
		framing := request.Method == http.MethodPut && request.URL.RequestURI() == "/assets/"+identity.SHA256 &&
			request.ContentLength == identity.Size && len(request.TransferEncoding) == 0
		headers := request.Header.Get("Content-Type") == "application/octet-stream" &&
			request.Header.Get("Content-Encoding") == "identity" && request.Header.Get("Accept-Encoding") == "identity"
		if !framing || !headers || request.Header.Get(jobcontract.ContractHeader) != "1" {
			t.Error("content changed its exact binary framing")
		}
		got, err := io.ReadAll(request.Body)
		if err != nil || !bytes.Equal(got, content) {
			t.Error("content bytes changed in SSH transport")
		}
		writer.WriteHeader(http.StatusCreated)
	})
	start := int64(0)
	zero, tooLarge := identity, identity
	zero.Size, tooLarge.Size = 0, jobcontract.MaxAssetBytes+1
	for _, request := range []Request{
		{Method: http.MethodPost, Route: "/assets/" + identity.SHA256, Body: bytes.NewReader(content), Content: &identity},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256 + "?x=1", Body: bytes.NewReader(content), Content: &identity},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256 + "/", Body: bytes.NewReader(content), Content: &identity},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Content: &identity},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Body: bytes.NewReader(content), Content: &zero},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Body: bytes.NewReader(content), Content: &tooLarge},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Body: bytes.NewReader(content), Content: &identity, JSONGzip: true},
		{Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Body: bytes.NewReader(content), Content: &identity, RangeStart: &start},
	} {
		if _, err := node.Do(t.Context(), request); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid binary request reached SSH")
		}
	}
	owner := node.http.Transport.(runnerTransport).owner
	if uploads.Load() != 0 || owner.bulk != nil {
		t.Fatal("rejected metadata allocated or reached the bulk pool")
	}
	for range 3 {
		response, err := node.Do(t.Context(), Request{
			Method: http.MethodPut, Route: "/assets/" + identity.SHA256,
			Body: bytes.NewReader(content), Content: &identity,
		})
		if err != nil || response.StatusCode != http.StatusCreated {
			t.Fatalf("binary request failed: %v", err)
		}
		_ = response.Body.Close()
	}
	if uploads.Load() != 3 || len(client.endpoints) != 1 || owner.bulk == nil {
		t.Fatal("content allocated per-request endpoint ownership")
	}
	bulk := owner.bulk
	_ = node.Close()
	if len(client.endpoints) != 0 {
		t.Fatal("node close retained bulk ownership")
	}
	if _, err := bulk.Get(node.baseURL + "/runner"); err == nil {
		t.Fatal("closed node left an already-created bulk pool usable")
	}
}

func TestBulkPoolRetainsPinnedDestinationRedirectAndChannelCancellation(t *testing.T) {
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Location", "http://example.invalid/blocked")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	management, err := client.RunnerHTTP(uint16(server.Listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	bulk, err := client.bulkRunnerHTTP(management)
	if err != nil {
		t.Fatal(err)
	}
	response, err := bulk.Get(server.URL)
	if err != nil || response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatal("bulk pool followed a redirect")
	}
	_ = response.Body.Close()
	if _, err := bulk.Get("http://example.invalid/blocked"); err == nil || requests.Load() != 1 {
		t.Fatal("bulk pool forwarded an unpinned destination")
	}
	// Use a fresh connection so this request must open a direct-tcpip channel.
	bulk.CloseIdleConnections()
	peer.stallForward.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if _, err := bulk.Do(request); err == nil {
		t.Fatal("bulk channel-open ignored request cancellation")
	}
	ended := make(chan struct{})
	go func() { _ = client.connection.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("withheld bulk channel-open retained the SSH dial goroutine")
	}
}

func TestNodeAndSSHCloseCancelAndJoinActiveContentRequests(t *testing.T) {
	for _, closeParent := range []bool{false, true} {
		node, client := uploadNode(t, func(writer http.ResponseWriter, request *http.Request) {
			_, _ = io.Copy(io.Discard, request.Body)
			writer.WriteHeader(http.StatusCreated)
		})
		source := &blockedNodeSource{started: make(chan struct{}), closed: make(chan struct{}), exited: make(chan struct{})}
		identity := jobcontract.ContentIdentity{SHA256: strings.Repeat("a", 64), Size: 1}
		result := make(chan error, 1)
		go func() {
			response, err := node.Do(t.Context(), Request{
				Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Body: source, Content: &identity,
			})
			if response != nil {
				_ = response.Body.Close()
			}
			result <- err
		}()
		select {
		case <-source.started:
		case <-time.After(time.Second):
			t.Fatal("content request did not start")
		}
		closed := make(chan struct{})
		go func() {
			if closeParent {
				_ = client.Close()
			} else {
				_ = node.Close()
			}
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("close retained an active bulk request")
		}
		select {
		case <-source.exited:
		default:
			t.Fatal("close returned before cooperative read exited")
		}
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("closed upload was acknowledged")
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled request goroutine did not exit")
		}
	}
}

func TestRunnerHTTPCloseReclaimsAnUnclosedResponse(t *testing.T) {
	started := make(chan struct{})
	node, client := uploadNode(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(200)
		_, _ = io.WriteString(writer, "a")
		writer.(http.Flusher).Flush()
		close(started)
		<-request.Context().Done()
	})
	response, err := node.Do(t.Context(), Request{Method: http.MethodGet, Route: "/response"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	closed := make(chan struct{})
	go func() { _ = node.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("unclosed response prevented endpoint shutdown")
	}
	if _, err := io.ReadAll(response.Body); err == nil || len(client.endpoints) != 0 {
		t.Fatal("endpoint shutdown retained a readable network response")
	}
	_ = response.Body.Close()
}

func TestRunnerHTTPClosedOwnerClosesRequestBodyOutsideItsGate(t *testing.T) {
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	management, err := client.RunnerHTTP(44917)
	if err != nil {
		t.Fatal(err)
	}
	owner := management.Transport.(runnerTransport).owner
	client.releaseRunnerHTTP(management)
	body := &closedEndpointBody{owner: owner}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:44917/test", body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := management.Do(request); !errors.Is(err, ErrConnection) {
		t.Fatalf("closed endpoint request did not fail safely: %v", err)
	}
	if body.reads != 0 || body.closes != 1 || !body.unlocked {
		t.Fatalf("closed endpoint leaked its body or held the gate: reads=%d closes=%d unlocked=%v",
			body.reads, body.closes, body.unlocked)
	}
	owner.mu.Lock()
	active := len(owner.requests)
	owner.mu.Unlock()
	if active != 0 {
		t.Fatal("rejected request was registered as active")
	}
	done := make(chan struct{})
	go func() { owner.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unregistered request changed the owner's wait group")
	}
}

type closedEndpointBody struct {
	owner    *runnerHTTP
	reads    int
	closes   int
	unlocked bool
}

func (body *closedEndpointBody) Read([]byte) (int, error) {
	body.reads++
	return 0, io.EOF
}

func (body *closedEndpointBody) Close() error {
	body.closes++
	if body.owner.mu.TryLock() {
		body.unlocked = true
		body.owner.mu.Unlock()
	}
	return nil
}

func uploadNode(t *testing.T, handler http.HandlerFunc) (*Node, *Client) {
	t.Helper()
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	token := []byte(strings.Repeat("private-node-token", 3))
	digest := sha256.Sum256(token)
	info := runnerMetadata()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+string(token) {
			t.Error("request lost node authorization")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path == "/runner" {
			_ = json.NewEncoder(writer).Encode(info)
			return
		}
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	port := server.Listener.Addr().(*net.TCPAddr).Port
	identity := runnerinstall.Identity{
		Contract: jobcontract.CurrentContract, NodeID: info.NodeID, InstallationID: info.NodeInstallationID,
		ListenPort: uint16(port), TokenSHA256: hex.EncodeToString(digest[:]),
	}
	node, _, err := client.connectPairing(t.Context(), identity, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	if node.baseURL != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatal("node moved away from its pinned loopback endpoint")
	}
	return node, client
}

type blockedNodeSource struct {
	started chan struct{}
	closed  chan struct{}
	exited  chan struct{}
	start   sync.Once
	close   sync.Once
}

func (source *blockedNodeSource) Read([]byte) (int, error) {
	source.start.Do(func() { close(source.started) })
	<-source.closed
	close(source.exited)
	return 0, io.ErrClosedPipe
}

func (source *blockedNodeSource) Close() error {
	source.close.Do(func() { close(source.closed); <-source.exited })
	return nil
}
