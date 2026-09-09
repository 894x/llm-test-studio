package catalog

import (
	"context"
	"encoding/json"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
	"testing"
)

func TestSuiteInputSnapshotsAreIndependent(t *testing.T) {
	repository := validRepository()
	repository.suites[0].Inputs = []domain.SuiteInput{{Key: "prompt", Label: "Prompt", Input: testspec.Input{Type: "string", Default: json.RawMessage(`"hello"`)}, Bindings: []domain.SuiteInputBinding{{CaseID: caseID, Input: "message"}}}}
	service := newTestService(t, repository, fixtureTime())
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Suites[0].Inputs[0].Default[1] = 'x'
	snapshot.Suites[0].Inputs[0].Bindings[0].Input = "changed"
	if string(repository.suites[0].Inputs[0].Default) != `"hello"` || repository.suites[0].Inputs[0].Bindings[0].Input != "message" {
		t.Fatal("input aliases")
	}
}

func TestSuiteSaveDoesNotResolveMembersOrMappings(t *testing.T) {
	repository := validRepository()
	repository.testCases = nil
	service := newTestService(t, repository, fixtureTime())
	command := CreateSuiteCommand{Key: "future", Name: "Future", Protocol: domain.ProtocolOpenAIChat, Cases: []CaseInput{{CaseID: caseID}}, Inputs: []domain.SuiteInput{{Key: "prompt", Label: "Prompt", Input: testspec.Input{Type: "string", Required: true}, Bindings: []domain.SuiteInputBinding{{CaseID: caseID, Input: "future_input"}}}}}
	if _, err := service.CreateSuite(context.Background(), command); err != nil {
		t.Fatalf("saving refs: %v", err)
	}
	if repository.testCaseRevisionCalls != 0 {
		t.Fatal("suite save resolved cases")
	}
}
