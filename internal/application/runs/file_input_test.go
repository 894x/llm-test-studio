package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func fileRunFixture(t *testing.T) runFixture {
	t.Helper()
	fixture := newRunFixture(t)
	fixture.testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{
		"inputs":{"image":{"type":"file","required":true,"file":{"encoding":"image_data_url","media_types":["image/png"],"max_bytes":1048576}}},
		"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`)
	fixture.suite.Inputs = []domain.SuiteInput{{
		Key: "image", Label: "Image",
		Input: testspec.Input{Type: "file", Required: true, File: &testspec.FileInput{
			Encoding: "image_data_url", MediaTypes: []string{"image/png"}, MaxBytes: testspec.MaxFileInputBytes,
		}},
		Bindings: []domain.SuiteInputBinding{{CaseID: fixture.testCase.ID, Input: "image"}},
	}}
	fixture.plan.Entries[0].Parameters["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
	return fixture
}

func TestPreparersRejectFileInputsBeforeCreatingIdentityOrLease(t *testing.T) {
	for _, caseType := range []string{"file", "object"} {
		t.Run(caseType, func(t *testing.T) {
			fixture := fileRunFixture(t)
			if caseType == "object" {
				fixture.testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{
					"inputs":{"image":{"type":"object","required":true}},
					"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`)
			}
			assertFilePreparationRejected(t, fixture, caseType == "file")
		})
	}
}

func assertFilePreparationRejected(t *testing.T, fixture runFixture, unsupported bool) {
	t.Helper()
	repository := &fakeRepository{fixture: fixture}
	preparer, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: readOnlyPreparationRepository{delegate: repository},
		QuickTasks: quickTaskCatalog{suite: fixture.suite}, Credentials: credentials.NewMemoryStore(),
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preparer.Close)
	prepared, lease, err := preparer.BuildTarget(context.Background(), runs.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID,
	})
	validRejection := errors.Is(err, runs.ErrNotRunnable) && errors.Is(err, testspec.ErrFileInputsUnsupported) == unsupported
	hasPreparedState := lease != nil || prepared.Meta.ID != ""
	if !validRejection || hasPreparedState {
		t.Fatalf("file target preparation: prepared=%+v, lease=%v, err=%v", prepared, lease, err)
	}
	prepared, lease, err = preparer.BuildQuickTask(context.Background(), runs.QuickTaskCommand{
		SuiteID: fixture.suite.ID, Model: "model", BaseURL: "https://example.test", APIKey: "test-secret",
		RequestTimeoutMS: 1000, Inputs: fixture.plan.Entries[0].Parameters,
	})
	validRejection = errors.Is(err, runs.ErrNotRunnable) && errors.Is(err, testspec.ErrFileInputsUnsupported) == unsupported
	hasPreparedState = lease != nil || prepared.Meta.ID != ""
	if !validRejection || hasPreparedState {
		t.Fatalf("file quick task preparation: prepared=%+v, lease=%v, err=%v", prepared, lease, err)
	}
	if repository.run.Meta().ID != "" || len(repository.results) != 0 {
		t.Fatal("rejected file preparation persisted operational data")
	}
}

func TestProtocolExecutorRejectsFileSnapshotWithoutTransportOrResults(t *testing.T) {
	fixture := fileRunFixture(t)
	snapshot := fixture.snapshot()
	snapshot.Entries[0].CaseInputs = map[string]map[string]json.RawMessage{
		fixture.testCase.ID: {"image": json.RawMessage(`{"asset_ref":"image-1"}`)},
	}
	run, err := domain.NewRun(fixture.plan.EntityMeta, fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := credentials.NewTemporaryLease([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	calls, emitted := 0, 0
	executor := runs.NewProtocolExecutor(protocolRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected HTTP call")
	}))
	err = executor.Execute(context.Background(), runs.ExecutionRequest{
		Run: run, Entry: snapshot.Entries[0], Cases: []domain.TestCase{fixture.testCase}, Credential: lease,
	}, func(runs.ResultDraft) error {
		emitted++
		return nil
	})
	if !errors.Is(err, testspec.ErrFileInputsUnsupported) || calls != 0 || emitted != 0 {
		t.Fatalf("file executor error=%v, HTTP calls=%d, emitted=%d", err, calls, emitted)
	}
}
