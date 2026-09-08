package runs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestResponseProbeExecutorClassifiesKnownAndStableUnknownShapes(t *testing.T) {
	responses := []string{
		`{"provider":"a","payload":{"answer":"first"}}`,
		`{"usage":{"prompt_tokens_details":{"cached_tokens":3}},"payload":{"answer":"second"}}`,
		`{"gateway":{"route":"mystery-1"},"payload":{"answer":"third"}}`,
		`{"gateway":{"route":"mystery-2"},"payload":{"answer":"fourth"}}`,
	}
	var requestIndex atomic.Uint64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body["model"] != "upstream-model" {
			t.Errorf("model = %#v", body["model"])
		}
		index := requestIndex.Add(1) - 1
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, responses[index])
	}))
	defer server.Close()

	fixture := newRunFixture(t)
	fixture.channel.BaseURL = server.URL
	fixture.plan.Suites[0].Load = domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: 4, RequestTimeoutMS: 1000}
	fixture.testCase.Dimension = "routing"
	fixture.testCase.Definition = domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          casetypes.TypeResponseProbe,
		TypeVersion:   1,
		Spec: json.RawMessage(`{
			"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"probe"}],"stream":false}},
			"signatures":[
				{"label":"provider-a","match":[{"pointer":"/provider","operator":"equals","value":"a"}]},
				{"label":"provider-b","match":[{"pointer":"/usage/prompt_tokens_details","operator":"type","value":"object"}]}
			]
		}`),
	}
	snapshot := fixture.snapshot()
	run, err := domain.NewRun(domain.EntityMeta{ID: "30000000-0000-4000-8000-000000000098", SchemaVersion: 1, Revision: 1, CreatedAt: fixture.now, UpdatedAt: fixture.now}, fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	store := credentials.NewMemoryStore()
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	lease, _ := store.Get(context.Background(), storeRef)
	defer lease.Close()

	executor := runs.NewResponseProbeExecutor(server.Client().Transport)
	var drafts []runs.ResultDraft
	err = executor.Execute(context.Background(), runs.ExecutionRequest{
		Run: run, Suite: snapshot.Suites[0], Cases: []domain.TestCase{fixture.testCase}, Credential: lease, StopSending: make(chan struct{}),
	}, func(draft runs.ResultDraft) error {
		drafts = append(drafts, draft)
		return nil
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(drafts) != 4 {
		t.Fatalf("draft count = %d", len(drafts))
	}
	wantBuckets := []string{"provider-a", "provider-b", "unknown", "unknown"}
	for index, draft := range drafts {
		if !draft.Success.Overall() || draft.Dimensions["probe_bucket"] != wantBuckets[index] || draft.Dimensions["probe_format"] != "json" {
			t.Fatalf("draft %d = %#v", index, draft)
		}
		if draft.Dimensions["probe_case_id"] != fixture.testCase.ID {
			t.Fatalf("draft %d probe case id = %q", index, draft.Dimensions["probe_case_id"])
		}
		if draft.Dimensions["probe_shape"] == "" {
			t.Fatalf("draft %d has no structural fingerprint", index)
		}
	}
	if drafts[2].Dimensions["probe_shape"] != drafts[3].Dimensions["probe_shape"] {
		t.Fatalf("same response shape fingerprints differ: %q / %q", drafts[2].Dimensions["probe_shape"], drafts[3].Dimensions["probe_shape"])
	}
}
