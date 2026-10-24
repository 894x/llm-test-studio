package runnerhttp_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type schedulingMemoryStore struct{ value uint32 }

func (store *schedulingMemoryStore) Save(_ context.Context, expected, limit uint32) error {
	if store.value != expected {
		return jobhost.ErrConflict
	}
	store.value = limit
	return nil
}

func TestHTTPTaskConcurrencyCapabilityAndCurrentRunnerState(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	oldClient := httpManagementClient(t, fixture)
	if _, err := oldClient.SetTaskConcurrency(t.Context(), 1, 4); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("runner without the capability accepted configuration")
	}
	store := &schedulingMemoryStore{value: 1}
	scheduling, err := jobhost.NewScheduling(fixture.host, store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: fixture.host, Maintenance: fixture.maintenance, Scheduling: scheduling,
		Repository: fixture.repository, TokenSHA256: sha256.Sum256([]byte(nodeToken)), Capabilities: capabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.api = httptest.NewServer(handler)
	t.Cleanup(fixture.api.Close)
	client := httpManagementClient(t, fixture)
	info, err := client.SetTaskConcurrency(t.Context(), 1, 4)
	if err != nil || info.Validate() != nil || info.Budget.MaxActiveJobs != 4 || store.value != 4 {
		t.Fatalf("setting was not applied: %v", err)
	}
	if !slices.Contains(info.Capabilities.Features, jobcontract.FeatureTaskConcurrency) {
		t.Fatal("configuration endpoint not advertised")
	}
	actual, err := client.Probe(t.Context())
	if err != nil || actual.Budget.MaxActiveJobs != 4 {
		t.Fatal("GET /runner returned the original task limit")
	}
	if _, err := client.SetTaskConcurrency(t.Context(), 1, 8); err == nil || store.value != 4 {
		t.Fatal("stale setting overwrote the shared node")
	}
	identity := fixture.host.Identity()
	command := jobcontract.TaskConcurrencyCommand{Contract: jobcontract.CurrentContract,
		NodeID: identity.Origin.NodeID, NodeInstallationID: identity.InstallationID,
		ExpectedMaxActiveJobs: 4, MaxActiveJobs: 129}
	document, _ := json.Marshal(command)
	response := fixture.request(t, http.MethodPost, "/runner/task-concurrency", document)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || store.value != 4 {
		t.Fatal("invalid limit changed persistent configuration")
	}
}
