package runs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestLegacyAPIAuditExecutorRunsImportedDriverWithoutPython(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
	}))
	defer server.Close()

	fixture := newRunFixture(t)
	fixture.channel.BaseURL = server.URL
	fixture.testCase.Definition = domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          casetypes.TypeLegacyAPIAudit, TypeVersion: 1,
		Spec: json.RawMessage(`{"kind":"chat_sync","request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hi"}]}},"options":{"require_usage":true}}`),
	}
	snapshot := fixture.snapshot()
	snapshot.Channel.BaseURL = server.URL
	snapshot.CaseDefinitions[0] = fixture.testCase
	run, err := domain.NewRun(domain.EntityMeta{ID: "30000000-0000-4000-8000-000000000098", SchemaVersion: 1, Revision: 1, CreatedAt: fixture.now, UpdatedAt: fixture.now}, fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	store := credentials.NewMemoryStore()
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	lease, _ := store.Get(context.Background(), storeRef)
	defer lease.Close()

	executor := runs.NewLegacyAPIAuditExecutor(server.Client())
	var drafts []runs.ResultDraft
	err = executor.Execute(context.Background(), runs.ExecutionRequest{Run: run, Cases: []domain.TestCase{fixture.testCase}, Credential: lease}, func(draft runs.ResultDraft) error {
		drafts = append(drafts, draft)
		return nil
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(drafts) != 1 || !drafts[0].Success.Overall() || drafts[0].CaseID != fixture.testCase.ID {
		t.Fatalf("legacy drafts = %#v", drafts)
	}
}

func TestExecutorRouterDispatchesMixedCaseTypesToTheirDrivers(t *testing.T) {
	fixture := newRunFixture(t)
	legacyCase := fixture.testCase
	legacyCase.Definition = domain.TestCaseDefinition{SchemaVersion: 2, Type: casetypes.TypeLegacyAPIAudit, TypeVersion: 1, Spec: json.RawMessage(`{"kind":"chat_sync","request":{"method":"POST","path":"/chat/completions","headers":{},"body":{}},"options":{}}`)}
	legacy := &recordingExecutor{}
	load := &recordingExecutor{}
	router := runs.MustExecutorRouter(casetypes.MustBuiltinRegistry(), map[domain.CaseType]runs.Executor{
		casetypes.TypeLegacyAPIAudit: legacy,
		casetypes.TypeRequestSingle:  load,
	})
	request := runs.ExecutionRequest{Cases: []domain.TestCase{legacyCase}}
	if err := router.Execute(context.Background(), request, func(runs.ResultDraft) error { return nil }); err != nil {
		t.Fatalf("legacy route error = %v", err)
	}
	if legacy.calls != 1 || load.calls != 0 {
		t.Fatalf("route calls legacy/load = %d/%d", legacy.calls, load.calls)
	}
	request.Cases = []domain.TestCase{legacyCase, fixture.testCase}
	if err := router.Execute(context.Background(), request, func(runs.ResultDraft) error { return nil }); err != nil {
		t.Fatalf("mixed case route error = %v", err)
	}
	if legacy.calls != 2 || load.calls != 1 || len(legacy.caseCounts) != 2 || legacy.caseCounts[1] != 1 || len(load.caseCounts) != 1 || load.caseCounts[0] != 1 {
		t.Fatalf("mixed route calls/counts legacy=%d/%v load=%d/%v", legacy.calls, legacy.caseCounts, load.calls, load.caseCounts)
	}
}

type recordingExecutor struct {
	calls      int
	caseCounts []int
}

func (executor *recordingExecutor) Execute(_ context.Context, request runs.ExecutionRequest, _ func(runs.ResultDraft) error) error {
	executor.calls++
	executor.caseCounts = append(executor.caseCounts, len(request.Cases))
	return nil
}
