package runs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestLoadExecutorRunsThePinnedProfileAndMapsEveryObservation(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		mu.Lock()
		requestCount++
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	fixture := newRunFixture(t)
	fixture.channel.BaseURL = server.URL
	fixture.plan.Load = domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 2, RequestCount: 3, RequestTimeoutMS: 1000}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: fixture.plan.ID, Revision: 1},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: fixture.model.ID, Revision: 1}, Name: fixture.model.Name, Protocol: fixture.model.Protocol},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: fixture.channel.ID, Revision: 1}, Name: fixture.channel.Name, BaseURL: server.URL, Protocol: fixture.channel.Protocol, UpstreamModelName: fixture.mapping.UpstreamModelName},
		Cases:         fixture.plan.Cases, Load: fixture.plan.Load, SLA: fixture.plan.SLA, Environment: fixture.environment,
	}
	run, err := domain.NewRun(domain.EntityMeta{ID: "30000000-0000-4000-8000-000000000099", SchemaVersion: 1, Revision: 1, CreatedAt: fixture.now, UpdatedAt: fixture.now}, fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	store := credentials.NewMemoryStore()
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	lease, _ := store.Get(context.Background(), storeRef)
	defer lease.Close()

	executor := runs.NewLoadExecutor(server.Client().Transport)
	var drafts []runs.ResultDraft
	err = executor.Execute(context.Background(), runs.ExecutionRequest{
		Run: run, Cases: []domain.TestCase{fixture.testCase}, Credential: lease, StopSending: make(chan struct{}),
	}, func(draft runs.ResultDraft) error {
		drafts = append(drafts, draft)
		return nil
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if requestCount != 3 || len(drafts) != 3 {
		t.Fatalf("requests = %d, drafts = %d", requestCount, len(drafts))
	}
	for _, draft := range drafts {
		if draft.CaseID != fixture.testCase.ID || !draft.Success.Overall() || draft.Metrics["e2e_ms"] <= 0 {
			t.Fatalf("draft = %#v", draft)
		}
	}
}
