package runs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
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
	fixture.testCase.Definition.Assertions = []domain.TestAssertion{{
		Kind:   domain.AssertionCustom,
		Config: json.RawMessage(`{"driver":"legacy.apiaudit","driver_version":1,"legacy_kind":"chat_sync","body_present":true,"options":{"require_usage":true}}`),
	}}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: fixture.plan.ID, Revision: 1},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: fixture.model.ID, Revision: 1}, Name: fixture.model.Name, Protocol: fixture.model.Protocol},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: fixture.channel.ID, Revision: 1}, Name: fixture.channel.Name, BaseURL: server.URL, Protocol: fixture.channel.Protocol, UpstreamModelName: fixture.mapping.UpstreamModelName},
		Cases:         fixture.plan.Cases, Load: fixture.plan.Load, SLA: fixture.plan.SLA, Environment: fixture.environment,
	}
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

func TestExecutorRouterSelectsLegacyDriverAndRejectsMixedSemantics(t *testing.T) {
	fixture := newRunFixture(t)
	legacyCase := fixture.testCase
	legacyCase.Definition.Assertions = []domain.TestAssertion{{Kind: domain.AssertionCustom, Config: json.RawMessage(`{"driver":"legacy.apiaudit","driver_version":1,"legacy_kind":"chat_sync","body_present":true,"options":{}}`)}}
	legacy := &recordingExecutor{}
	load := &recordingExecutor{}
	router := runs.NewExecutorRouter(legacy, load)
	request := runs.ExecutionRequest{Cases: []domain.TestCase{legacyCase}}
	if err := router.Execute(context.Background(), request, func(runs.ResultDraft) error { return nil }); err != nil {
		t.Fatalf("legacy route error = %v", err)
	}
	if legacy.calls != 1 || load.calls != 0 {
		t.Fatalf("route calls legacy/load = %d/%d", legacy.calls, load.calls)
	}
	request.Cases = []domain.TestCase{legacyCase, fixture.testCase}
	if err := router.Execute(context.Background(), request, func(runs.ResultDraft) error { return nil }); err == nil {
		t.Fatal("mixed legacy and native plan was accepted")
	}
}

type recordingExecutor struct{ calls int }

func (executor *recordingExecutor) Execute(context.Context, runs.ExecutionRequest, func(runs.ResultDraft) error) error {
	executor.calls++
	return nil
}
