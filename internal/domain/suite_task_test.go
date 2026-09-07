package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQuickTestClonePreservesEmptyInputsAndOwnsMutableValues(t *testing.T) {
	empty := &SuiteQuickTest{Description: "Connection", TimeoutMS: 30000, Inputs: []SuiteInput{}}
	encoded, err := json.Marshal(empty.Clone())
	if err != nil || !strings.Contains(string(encoded), `"inputs":[]`) {
		t.Fatalf("empty input list changed on clone: %s, %v", encoded, err)
	}
	profile := &SuiteQuickTest{Description: "Connection", TimeoutMS: 30000, Inputs: []SuiteInput{{
		Key: "prompt", Label: "Message", Type: "text", Default: json.RawMessage(`"hello"`),
		Bindings: []SuiteInputBinding{{CaseKey: "T001", Pointer: "/request/body/messages/0/content"}},
	}}}
	cloned := profile.Clone()
	cloned.Inputs[0].Default[1] = 'j'
	cloned.Inputs[0].Bindings[0].Pointer = "/request/body/missing"
	if string(profile.Inputs[0].Default) != `"hello"` || profile.Inputs[0].Bindings[0].Pointer != "/request/body/messages/0/content" {
		t.Fatal("cloned quick task aliases its original")
	}
}

func TestQuickTestRequiresExplicitInputList(t *testing.T) {
	profile := &SuiteQuickTest{Description: "Connection", TimeoutMS: 30000}
	if err := profile.Clone().Validate(); err == nil {
		t.Fatal("missing inputs accepted; use an empty array for a fixed task")
	}
}
