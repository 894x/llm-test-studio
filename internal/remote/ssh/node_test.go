package ssh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestNodeRangeRequestsValidateBeforeForwarding(t *testing.T) {
	testNodeRequest(t, 0)
}

func TestNodeSlowReportBodyUsesDownloadBudget(t *testing.T) {
	if os.Getenv("LLM_TEST_SLOW_SSH_HTTP_TEST") != "1" {
		t.Skip("explicit 31-second SSH body timeout regression")
	}
	testNodeRequest(t, 31*time.Second)
}

func testNodeRequest(t *testing.T, delay time.Duration) {
	t.Helper()
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	token := []byte(strings.Repeat("private-node-token", 3))
	defer clear(token)
	digest := sha256.Sum256(token)
	info := runnerMetadata()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+string(token) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/runner" {
			if request.Header.Get("Accept-Encoding") != "identity" {
				t.Error("Node.Info changed its identity encoding")
			}
			_ = json.NewEncoder(writer).Encode(info)
			return
		}
		if request.URL.Path == "/json" && request.Header.Get("Accept-Encoding") == "gzip" {
			_, _ = io.WriteString(writer, "{}")
			return
		}
		fixedHeaders := request.Header.Get("Range") == "bytes=1-" &&
			request.Header.Get("Accept-Encoding") == "identity" && request.Header.Get(jobcontract.ContractHeader) == "1"
		if request.URL.Path != "/report" || !fixedHeaders {
			t.Error("fixed request headers or destination changed")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(writer, "{")
		writer.(http.Flusher).Flush()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			_, _ = io.WriteString(writer, "}")
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	port, _ := strconv.ParseUint(strings.TrimPrefix(server.URL, "http://127.0.0.1:"), 10, 16)
	identity := runnerinstall.Identity{Contract: jobcontract.CurrentContract, NodeID: info.NodeID,
		InstallationID: info.NodeInstallationID, ListenPort: uint16(port), TokenSHA256: hex.EncodeToString(digest[:])}
	node, _, err := client.connectPairing(t.Context(), identity, token)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if node.http.Timeout < 2*time.Minute {
		t.Fatal("transport truncates the typed client's download budget")
	}
	negative, start := int64(-1), int64(1)
	for _, input := range []Request{
		{Method: http.MethodGet, Route: "/report", RangeStart: &negative},
		{Method: http.MethodPost, Route: "/report", RangeStart: &start},
		{Method: http.MethodGet, Route: "/report", Body: strings.NewReader("body"), RangeStart: &start},
		{Method: http.MethodGet, Route: "/report", RangeStart: &start, JSONGzip: true},
	} {
		if _, err := node.Do(t.Context(), input); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid Range request reached the tunnel")
		}
	}
	jsonResponse, err := node.Do(t.Context(), Request{Method: http.MethodGet, Route: "/json", JSONGzip: true})
	if err != nil || jsonResponse.StatusCode != http.StatusOK {
		t.Fatal("explicit JSON gzip negotiation was not forwarded")
	}
	_ = jsonResponse.Body.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	response, err := node.Do(ctx, Request{Method: http.MethodGet, Route: "/report", RangeStart: &start})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "{}" {
		t.Fatalf("SSH report body was cut short: %v", err)
	}
}

func TestSharedNodePairingValidatesFreshIdentityAndReleasesPools(t *testing.T) {
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	token := []byte(strings.Repeat("private-node-token", 3))
	defer clear(token)
	digest := sha256.Sum256(token)
	info := runnerMetadata()
	var identityChanged atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+string(token) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path != "/runner" || request.Header.Get(jobcontract.ContractHeader) != "1" {
			writer.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		current := info.Clone()
		if identityChanged.Load() {
			current.NodeInstallationID = "33333333-3333-4333-8333-333333333333"
		}
		_ = json.NewEncoder(writer).Encode(current)
	}))
	defer server.Close()
	port, _ := strconv.ParseUint(strings.TrimPrefix(server.URL, "http://127.0.0.1:"), 10, 16)
	identity := runnerinstall.Identity{
		Contract: jobcontract.CurrentContract, NodeID: info.NodeID, InstallationID: info.NodeInstallationID,
		ListenPort: uint16(port), TokenSHA256: hex.EncodeToString(digest[:]),
	}
	storedToken, err := credentials.NewTemporaryLease(token)
	if err != nil {
		t.Fatal(err)
	}
	defer storedToken.Close()
	_, _, err = client.connectPairing(t.Context(), identity, []byte(strings.Repeat("x", 64)))
	if !errors.Is(err, runnerinstall.ErrPairingRequired) || peer.forwards.Load() != 0 {
		t.Fatal("unverified pairing reached the management plane")
	}
	for range 8 {
		node, observed, err := client.ConnectBoundNode(t.Context(), identity, storedToken)
		if err != nil || observed.RunnerVersion != "another-client-build" {
			t.Fatal("current-contract runner was coupled to a client build or retained stale pools")
		}
		if document, err := json.Marshal(node); err == nil || len(document) != 0 {
			t.Fatal("paired connection became serializable")
		}
		if strings.Contains(fmt.Sprintf("%+v %#v", node, node), string(token)) {
			t.Fatal("node formatting exposed pairing material")
		}
		for _, route := range []string{"https://other/runner", "//other/runner", "runner", "\n/runner"} {
			if _, err := node.Do(t.Context(), Request{Method: http.MethodGet, Route: route}); !errors.Is(err, ErrInvalid) {
				t.Fatal("node accepted an external or malformed destination")
			}
		}
		pairing, err := node.PairingLease()
		if err != nil {
			t.Fatal(err)
		}
		_ = node.Close()
		copied, err := pairing.Bytes()
		if err != nil || string(copied) != string(token) {
			t.Fatal("node close revoked independent pairing lease")
		}
		clear(copied)
		_ = pairing.Close()
		_ = node.Close()
		if _, err := node.Info(t.Context()); !errors.Is(err, ErrConnection) {
			t.Fatal("closed node still used its token")
		}
	}
	identityChanged.Store(true)
	if _, _, err := client.ConnectBoundNode(t.Context(), identity, storedToken); !errors.Is(err, ErrIntegrity) {
		t.Fatal("node silently re-paired a replaced installation")
	}
	if len(client.transports) != 0 {
		t.Fatal("failed live identity verification retained a connection pool")
	}
	_ = storedToken.Close()
	if _, _, err := client.ConnectBoundNode(t.Context(), identity, storedToken); !errors.Is(err, runnerinstall.ErrPairingRequired) {
		t.Fatal("closed stored credential was accepted")
	}
}
