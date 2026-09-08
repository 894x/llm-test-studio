package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPrepareTargetStartsMiniMaxDirectlyAndPreservesModelScope(t *testing.T) {
	fixture := newRunFixture(t)
	fixture.model.Protocol = domain.ProtocolMiniMaxVideo
	fixture.channel.Protocol = domain.ProtocolMiniMaxVideo
	fixture.mapping.UpstreamModelName = "MiniMax-H3"
	fixture.suite.Protocol = domain.ProtocolMiniMaxVideo
	fixture.suite.ModelTarget = fixture.mapping.UpstreamModelName
	fixture.testCase.Protocol = domain.ProtocolMiniMaxVideo
	fixture.testCase.ModelTargets = []string{"MiniMax-H3"}
	fixture.testCase.Definition = domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          casetypes.TypeLegacyAPIAudit, TypeVersion: 1,
		Spec: json.RawMessage(`{"kind":"minimax_video_task_rejected","request":{"method":"POST","path":"/v2/video_generation","headers":{},"body":{"content":[{"type":"text","text":"cat"}],"resolution":"768P","duration":3,"ratio":"16:9"}},"options":{}}`),
	}
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	storeRef, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), storeRef, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	command := runs.StartCommand{PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID}
	if id, err := service.PrepareTarget(context.Background(), command); err != nil || !domain.IsUUID(id) {
		t.Fatalf("PrepareTarget() = %q, %v", id, err)
	}
	repository.fixture.testCase.ModelTargets = nil
	if _, err := service.PrepareTarget(context.Background(), command); !errors.Is(err, runs.ErrNotRunnable) {
		t.Fatalf("unscoped PrepareTarget() error = %v, want ErrNotRunnable", err)
	}
}
