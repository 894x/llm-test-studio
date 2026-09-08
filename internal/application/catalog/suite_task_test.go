package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func quickTaskRepository() *fakeRepository {
	repository := validRepository()
	repository.suites[0].ModelTarget = ""
	repository.testCases[0].Definition.Spec = json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200]},"assertions":[]}`)
	repository.suites[0].QuickTest = &domain.SuiteQuickTest{Description: "Connection", TimeoutMS: 30000, Inputs: []domain.SuiteInput{{
		Key: "prompt", Label: "Message", Type: "text", Default: json.RawMessage(`"hello"`),
		Bindings: []domain.SuiteInputBinding{{CaseKey: "T001", Pointer: "/request/body/messages/0/content"}},
	}}}
	repository.plans[0].Suites[0].Parameters = map[string]json.RawMessage{
		"prompt": json.RawMessage(`"hello"`),
	}
	return repository
}

func TestSnapshotQuickTaskOwnsItsInputValues(t *testing.T) {
	repository := quickTaskRepository()
	service := newTestService(t, repository, fixtureTime())
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Suites[0].QuickTest.Inputs[0].Default[1] = 'x'
	snapshot.Suites[0].QuickTest.Inputs[0].Bindings[0].Pointer = "/request/body/missing"
	if string(repository.suites[0].QuickTest.Inputs[0].Default) != `"hello"` || repository.suites[0].QuickTest.Inputs[0].Bindings[0].Pointer != "/request/body/messages/0/content" {
		t.Fatal("snapshot changed authored task data")
	}
}

func TestSnapshotRejectsBrokenQuickTaskBindingsInCurrentAndPinnedSuites(t *testing.T) {
	for _, historical := range []bool{false, true} {
		repository := quickTaskRepository()
		broken := repository.suites[0]
		broken.QuickTest = broken.QuickTest.Clone()
		broken.QuickTest.Inputs[0].Bindings[0].Pointer = "/request/body/missing"
		if historical {
			broken.Revision = 42
			repository.plans[0].Suites[0].SuiteRevision = 42
			repository.suiteRevisions = map[exactSuiteRevisionKey]domain.Suite{{suiteID: broken.ID, revision: 42}: broken}
		} else {
			repository.suites[0] = broken
		}
		service := newTestService(t, repository, fixtureTime())
		if _, err := service.Snapshot(context.Background()); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("historical=%v: invalid binding error = %v; want ErrCorrupt", historical, err)
		}
	}
}

func TestCreateSuiteRejectsBrokenTaskBeforeWriting(t *testing.T) {
	repository := quickTaskRepository()
	profile := repository.suites[0].QuickTest.Clone()
	profile.Inputs[0].Bindings[0].Pointer = "/request/body/missing"
	service := newTestService(t, repository, fixtureTime())
	_, err := service.CreateSuite(context.Background(), CreateSuiteCommand{
		Key: "broken-task", Name: "Broken", Protocol: domain.ProtocolOpenAIChat, QuickTest: profile,
		Cases: []CaseRevisionInput{{CaseID: caseID, Revision: 1}},
	})
	if !errors.Is(err, ErrInvalid) || repository.createdSuite.ID != "" {
		t.Fatalf("invalid task saved: error=%v suite=%+v", err, repository.createdSuite)
	}
}

func TestQuickTaskRejectsDisabledAndManualMembers(t *testing.T) {
	for _, manual := range []bool{false, true} {
		repository := quickTaskRepository()
		if manual {
			repository.testCases[0].ExecutionMode = domain.CaseExecutionManual
		} else {
			repository.testCases[0].Enabled = false
		}
		service := newTestService(t, repository, fixtureTime())
		if _, err := service.Snapshot(context.Background()); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("manual=%v: unavailable task case accepted: %v", manual, err)
		}
	}
}
