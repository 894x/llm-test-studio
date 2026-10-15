package ssh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

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
			if _, err := node.Do(
				t.Context(),
				http.MethodGet,
				route,
				nil,
			); !errors.Is(err, ErrInvalid) {
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
