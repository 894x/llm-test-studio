package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/testspec"
)

func inputSuite() Suite {
	stamp := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	return Suite{
		EntityMeta: EntityMeta{ID: "123e4567-e89b-42d3-a456-426614174001", SchemaVersion: 1, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp},
		Key:        "input-suite", Name: "Inputs", Protocol: ProtocolOpenAIChat,
		Cases:  []CaseRef{{CaseID: "123e4567-e89b-42d3-a456-426614174002"}, {CaseID: "123e4567-e89b-42d3-a456-426614174003"}},
		Inputs: []SuiteInput{{Key: "size", Label: "Size", Input: testspec.Input{Type: "integer", Default: json.RawMessage(`1000`)}, Bindings: []SuiteInputBinding{{CaseID: "123e4567-e89b-42d3-a456-426614174002", Input: "content_length"}}}},
	}
}

func TestSuiteResolvesOnlyDeclaredInputMappings(t *testing.T) {
	suite := inputSuite()
	supplied := map[string]json.RawMessage{"size": json.RawMessage(`2000`)}
	values, cases, err := suite.ResolveInputs(supplied)
	if err != nil {
		t.Fatal(err)
	}
	if string(values["size"]) != "2000" || string(cases[suite.Cases[0].CaseID]["content_length"]) != "2000" || len(cases[suite.Cases[1].CaseID]) != 0 {
		t.Fatalf("wrong mapping: %#v %#v", values, cases)
	}
	supplied["size"][0] = '9'
	values["size"][0] = '8'
	if string(cases[suite.Cases[0].CaseID]["content_length"]) != "2000" || string(suite.Inputs[0].Default) != "1000" {
		t.Fatal("resolution aliases definitions or arguments")
	}
}

func TestSuiteDefersMembershipCheckButRejectsUnknownBindingAtRuntime(t *testing.T) {
	suite := inputSuite()
	suite.Inputs[0].Bindings[0].CaseID = "123e4567-e89b-42d3-a456-426614174099"
	if err := suite.Validate(); err != nil {
		t.Fatalf("save-time shape rejected UUID reference: %v", err)
	}
	if _, _, err := suite.ResolveInputs(map[string]json.RawMessage{}); err == nil {
		t.Fatal("runtime accepted missing binding member")
	}
}

func TestSuiteRejectsInvalidOrMissingInputAssignments(t *testing.T) {
	suite := inputSuite()
	suite.Inputs[0].Required = true
	suite.Inputs[0].Default = nil
	for _, values := range []map[string]json.RawMessage{{}, {"unknown": json.RawMessage(`1`)}, {"size": json.RawMessage(`"bad"`)}} {
		if _, _, err := suite.ResolveInputs(values); err == nil {
			t.Fatalf("accepted %#v", values)
		}
	}
}

func TestSuiteCloneOwnsBoundsEnumsAndBindings(t *testing.T) {
	suite := inputSuite()
	bound := 1.0
	suite.Inputs[0].Minimum = &bound
	suite.Inputs[0].Enum = []json.RawMessage{json.RawMessage(`1000`)}
	copied := CloneSuiteInputs(suite.Inputs)
	*copied[0].Minimum = 2
	copied[0].Enum[0][0] = '9'
	copied[0].Bindings[0].Input = "changed"
	if *suite.Inputs[0].Minimum != 1 || string(suite.Inputs[0].Enum[0]) != "1000" || suite.Inputs[0].Bindings[0].Input != "content_length" {
		t.Fatal("clone aliases")
	}
}
