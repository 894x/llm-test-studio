package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type quickTaskCatalog struct {
	suite   domain.Suite
	channel domain.Channel
}

func (catalog quickTaskCatalog) GetSuiteRevision(context.Context, string, uint64) (domain.Suite, error) {
	return catalog.suite, nil
}
func (catalog quickTaskCatalog) GetChannel(context.Context, string) (domain.Channel, error) {
	return catalog.channel, nil
}

func quickTaskSuite(fixture runFixture) domain.Suite {
	return domain.Suite{EntityMeta: fixture.suite.EntityMeta, Key: "connection", Name: "Connection", Protocol: fixture.testCase.Protocol,
		Cases: fixture.suite.Cases, QuickTest: &domain.SuiteQuickTest{Description: "Connect", TimeoutMS: 1000, Inputs: []domain.SuiteInput{{
			Key: "prompt", Label: "Message", Type: "text", Default: json.RawMessage(`"default"`),
			Bindings: []domain.SuiteInputBinding{{CaseKey: fixture.testCase.Key, Pointer: "/request/body/messages/0/content"}},
		}}},
	}
}

func TestQuickTaskUsesSharedLifecycleAndKeepsAuthoredSourceSeparate(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	suite := quickTaskSuite(fixture)
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})}
	service, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{suite: suite}, Credentials: credentials.NewMemoryStore(), Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	id, err := service.PrepareQuickTask(context.Background(), runs.QuickTaskCommand{SuiteID: suite.ID, SuiteRevision: suite.Revision,
		Model: "temporary-model", BaseURL: "https://example.test/v1", APIKey: "temporary-secret",
		Inputs: map[string]json.RawMessage{"prompt": json.RawMessage(`"edited"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if repository.run.Status() != domain.RunQueued || repository.selectedModelID != "" || repository.selectedChannelID != "" {
		t.Fatal("quick task used authored Plan target selection")
	}
	select {
	case <-executor.entered:
		t.Fatal("prepared task started early")
	default:
	}
	if err := service.ActivateRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	request := <-executor.entered
	snapshot := request.Run.Snapshot()
	if snapshot.QuickTask == nil || snapshot.QuickTask.Suite.ID != suite.ID || string(snapshot.QuickTask.Inputs["prompt"]) != `"edited"` || request.Run.PlanID() != id {
		t.Fatalf("task provenance missing: %+v", snapshot)
	}
	if !strings.Contains(string(snapshot.CaseDefinitions[0].Definition.Spec), `"hi"`) || !strings.Contains(string(request.Cases[0].Definition.Spec), `"edited"`) {
		t.Fatal("effective and authored request values mixed")
	}
	encoded, err := json.Marshal(request.Run)
	if err != nil || strings.Contains(string(encoded), "temporary-secret") {
		t.Fatalf("unsafe run snapshot: %v", err)
	}
	var roundTrip domain.Run
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Snapshot().QuickTask.Suite.ID != suite.ID {
		t.Fatal("task source lost on round trip")
	}
	close(executor.release)
	waitForStatus(t, repository, domain.RunCompleted)
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Credential.Bytes(); !errors.Is(err, credentials.ErrClosed) {
		t.Fatal("temporary credential lease remains open")
	}
}

func TestQuickTaskSavedChannelResolvesItsCredentialWithoutAuthoredModel(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	suite := quickTaskSuite(fixture)
	store := credentials.NewMemoryStore()
	ref, _ := credentials.StoreRefFromCredential(fixture.credential)
	if err := store.Set(context.Background(), ref, []byte("stored-secret")); err != nil {
		t.Fatal(err)
	}
	service, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{suite: suite, channel: fixture.channel}, Credentials: store, Executor: &recordingExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	id, err := service.PrepareQuickTask(context.Background(), runs.QuickTaskCommand{SuiteID: suite.ID, SuiteRevision: suite.Revision, Model: "temporary-model", ChannelID: fixture.channel.ID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := repository.run.Snapshot()
	if snapshot.Channel.ID != fixture.channel.ID || snapshot.QuickTask.SavedChannelID != fixture.channel.ID || snapshot.Channel.UpstreamModelName != "temporary-model" {
		t.Fatal("saved channel target not captured")
	}
	if err := service.CancelRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, repository, domain.RunCancelled)
}

func TestQuickTaskHistoryReplaysPinnedDefinitionsAfterCatalogChanges(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	suite := quickTaskSuite(fixture)
	service, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{suite: suite}, Credentials: credentials.NewMemoryStore(), Executor: &controlledExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	command := runs.QuickTaskCommand{SuiteID: suite.ID, SuiteRevision: suite.Revision, Model: "temporary-model", BaseURL: "https://example.test/v1", APIKey: "temporary-secret", Inputs: map[string]json.RawMessage{"prompt": json.RawMessage(`"last input"`)}}
	id, err := service.PrepareQuickTask(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CancelRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	history, err := service.QuickTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if history.RunID != id || history.Suite.Revision != suite.Revision || string(history.Inputs["prompt"]) != `"last input"` || history.Model != command.Model || history.BaseURL != command.BaseURL {
		t.Fatalf("history did not retain the invocation: %+v", history)
	}
	encoded, _ := json.Marshal(history)
	if strings.Contains(string(encoded), "temporary-secret") || strings.Contains(string(encoded), "case_definitions") {
		t.Fatal("history leaked secret or raw definitions")
	}
	history.Inputs["prompt"][0] = 'x'
	history.Suite.QuickTest.Inputs[0].Label = "mutated"
	again, err := service.QuickTask(context.Background(), id)
	if err != nil || string(again.Inputs["prompt"]) != `"last input"` || again.Suite.QuickTest.Inputs[0].Label != "Message" {
		t.Fatal("history query did not isolate snapshot data")
	}
	for _, source := range []string{"invalid", "123e4567-e89b-42d3-a456-426614174099"} {
		if _, err := service.QuickTask(context.Background(), source); err == nil {
			t.Fatal("invalid history identity accepted")
		}
	}
	wrong := command
	wrong.SourceRunID, wrong.SuiteRevision = id, suite.Revision+1
	if _, err := service.PrepareQuickTask(context.Background(), wrong); !errors.Is(err, runs.ErrNotRunnable) {
		t.Fatalf("mismatched replay revision accepted: %v", err)
	}
	// Replay must work even when the current catalog no longer contains this Suite.
	replayer, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{}, Credentials: credentials.NewMemoryStore(), Executor: &controlledExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
	if err != nil {
		t.Fatal(err)
	}
	defer replayer.Close()
	command.SourceRunID = id
	path, err := replayer.QuickTaskPerformancePath(context.Background(), command)
	if err != nil || path != "/chat/completions" {
		t.Fatalf("historical performance path=%q err=%v", path, err)
	}
	command.Inputs = map[string]json.RawMessage{"prompt": json.RawMessage(`"edited again"`)}
	replayID, err := replayer.PrepareQuickTask(context.Background(), command)
	if err != nil || replayID == id {
		t.Fatalf("replay id=%s err=%v", replayID, err)
	}
	if string(repository.run.Snapshot().QuickTask.Inputs["prompt"]) != `"edited again"` {
		t.Fatal("replay ignored edits")
	}
}

func TestQuickTaskRejectsInvalidSelectionBeforeDurableState(t *testing.T) {
	for _, scenario := range []string{"unknown input", "wrong type", "wrong revision", "not a task", "scoped model", "insecure endpoint", "mixed channel credentials", "disabled channel"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newRunFixture(t)
			repository := &fakeRepository{fixture: fixture}
			suite := quickTaskSuite(fixture)
			command := runs.QuickTaskCommand{SuiteID: suite.ID, SuiteRevision: suite.Revision, Model: "temporary-model", BaseURL: "https://example.test/v1", APIKey: "secret-never-in-errors"}
			switch scenario {
			case "unknown input":
				command.Inputs = map[string]json.RawMessage{"missing": json.RawMessage(`"value"`)}
			case "wrong type":
				command.Inputs = map[string]json.RawMessage{"prompt": json.RawMessage(`42`)}
			case "wrong revision":
				command.SuiteRevision++
			case "not a task":
				suite.QuickTest = nil
			case "scoped model":
				suite.ModelTarget = "different-model"
			case "insecure endpoint":
				command.BaseURL = "http://example.test"
			case "mixed channel credentials":
				command.ChannelID = fixture.channel.ID
			case "disabled channel":
				command.ChannelID, command.BaseURL, command.APIKey = fixture.channel.ID, "", ""
				fixture.channel.Enabled = false
			}
			service, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{suite: suite, channel: fixture.channel}, Credentials: credentials.NewMemoryStore(), Executor: &recordingExecutor{}, Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if _, err := service.PrepareQuickTask(context.Background(), command); err == nil || strings.Contains(err.Error(), "secret-never-in-errors") {
				t.Fatalf("unsafe or missing rejection: %v", err)
			}
			if repository.run.Meta().ID != "" {
				t.Fatal("invalid task persisted a run")
			}
		})
	}
}

func TestQuickTaskPreparesVideoSelectionsWithoutAnExtraConfirmation(t *testing.T) {
	for _, test := range []struct {
		protocol domain.Protocol
		kind     string
		count    int
	}{
		{domain.ProtocolSeedance, "seedance_task", 1}, {domain.ProtocolSeedance, "seedance_task", 2},
		{domain.ProtocolWanVideo, "wan_task_success", 1}, {domain.ProtocolMiniMaxVideo, "minimax_video_task_success", 1},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			fixture := newRunFixture(t)
			fixture.testCase.Protocol = test.protocol
			fixture.testCase.ModelTargets = []string{"upstream-model"}
			fixture.testCase.Definition.Type = casetypes.TypeLegacyAPIAudit
			fixture.testCase.Definition.Spec = json.RawMessage(`{"kind":"` + test.kind + `","request":{"method":"POST","path":"/tasks","headers":{},"body":{"prompt":"hello"}},"options":{}}`)
			repository := &fakeRepository{fixture: fixture, testCases: map[string]domain.TestCase{fixture.testCase.ID: fixture.testCase}}
			suite := quickTaskSuite(fixture)
			suite.ModelTarget = "upstream-model"
			suite.QuickTest.Inputs = []domain.SuiteInput{}
			if test.count == 2 {
				second := fixture.testCase
				second.ID, second.Key = "30000000-0000-4000-8000-000000000009", "second"
				repository.testCases[second.ID] = second
				suite.Cases = append(suite.Cases, domain.CaseRevisionRef{CaseID: second.ID, Revision: second.Revision})
			}
			service, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{suite: suite}, Credentials: credentials.NewMemoryStore(), Executor: &recordingExecutor{}, Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			command := runs.QuickTaskCommand{SuiteID: suite.ID, SuiteRevision: suite.Revision, Model: "upstream-model", BaseURL: "https://example.test", APIKey: "temporary-key"}
			id, err := service.PrepareQuickTask(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.CancelRun(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
