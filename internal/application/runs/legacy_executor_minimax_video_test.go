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

func TestLegacyAPIAuditExecutorDispatchesMiniMaxVideoAdmissionCase(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(writer, `{"type":"error","error":{"type":"bad_request_error","message":"invalid duration (2013)","http_code":"400"},"request_id":"reject-1"}`)
	}))
	defer server.Close()

	fixture := newRunFixture(t)
	miniMaxProtocol := domain.Protocol("minimax-video")
	fixture.model.Protocol = miniMaxProtocol
	fixture.channel.Protocol = miniMaxProtocol
	fixture.channel.BaseURL = server.URL
	fixture.mapping.UpstreamModelName = "MiniMax-H3"
	fixture.suite.Protocol = miniMaxProtocol
	fixture.suite.ModelTarget = fixture.mapping.UpstreamModelName
	fixture.testCase.Protocol = miniMaxProtocol
	fixture.testCase.Definition = domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          casetypes.TypeLegacyAPIAudit, TypeVersion: 1,
		Spec: json.RawMessage(`{"kind":"minimax_video_task_rejected","request":{"method":"POST","path":"/v2/video_generation","headers":{},"body":{"content":[{"type":"text","text":"cat"}],"resolution":"768P","duration":3,"ratio":"16:9"}},"options":{}}`),
	}
	snapshot := fixture.snapshot()
	run, err := domain.NewRun(domain.EntityMeta{ID: "30000000-0000-4000-8000-000000000096", SchemaVersion: 1, Revision: 1, CreatedAt: fixture.now, UpdatedAt: fixture.now}, fixture.plan.ID, snapshot)
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
	err = executor.Execute(context.Background(), runs.ExecutionRequest{Run: run, Suite: snapshot.Suites[0], Cases: []domain.TestCase{fixture.testCase}, Credential: lease}, func(draft runs.ResultDraft) error {
		drafts = append(drafts, draft)
		return nil
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(drafts) != 1 || !drafts[0].Success.Overall() {
		t.Fatalf("MiniMax drafts = %#v", drafts)
	}
}
