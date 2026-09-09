package testspec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFixedNegativeRequestIsValidAndAssertionsAreOptional(t *testing.T) {
	spec, err := Decode(json.RawMessage(`{"inputs":{},"request":{"body":{"temperature":"invalid","messages":null}},"assertions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := Generate(spec, nil, RandomContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temperature":"invalid"`) {
		t.Fatalf("negative input changed: %s", body)
	}
	result := Evaluate(spec.Assertions, Observation{})
	if result.Status != VerdictNotApplicable || result.Assertions == nil {
		t.Fatalf("empty assertions: %+v", result)
	}
}

func TestCurrentSpecRejectsSupersededAndBindingOwnedFields(t *testing.T) {
	tests := []string{
		`{"kind":"chat_sync","request":{"body":{}},"options":{}}`,
		`{"inputs":{},"request":{"method":"POST","body":{}},"assertions":[]}`,
		`{"inputs":{},"request":{"path":"/v1/chat/completions","body":{}},"assertions":[]}`,
		`{"inputs":{},"request":{"headers":{},"body":{}},"assertions":[]}`,
		`{"inputs":{},"request":{"body":{"model":"fixed"}},"assertions":[]}`,
		`{"inputs":{},"request":{"body":{}},"assertions":null}`,
		`{"inputs":{},"request":{"body":{}},"assertions":[],"seed":1}`,
		`{"inputs":{},"request":{"body":{"x":{"$input":"unknown"}}},"assertions":[]}`,
	}
	for _, test := range tests {
		if _, err := Decode(json.RawMessage(test)); err == nil {
			t.Errorf("accepted unsupported configuration: %s", test)
		}
	}
}

func TestGeneratorUnicodeAndStableRandomContexts(t *testing.T) {
	spec, err := Decode(json.RawMessage(`{
		"inputs":{"length":{"type":"integer","default":13,"minimum":1,"maximum":100}},
		"request":{"body":{"fixed":"$input stays literal","repeat":{"$generate":"repeat_text","text":"测试😀","length":{"$input":"length"}},
		"random":{"$generate":"random_text","alphabet":"abcdefghijklm","length":{"$input":"length"}}}},"assertions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	context := RandomContext{Seed: 42, ExecutionItemID: "entry-a", MemberID: "member-a", Iteration: 5}
	first, err := Generate(spec, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		again, err := Generate(spec, nil, context)
		if err != nil || string(again) != string(first) {
			t.Fatal("random generation depends on previous calls")
		}
	}
	otherContext := context
	otherContext.Iteration++
	other, _ := Generate(spec, nil, otherContext)
	if string(first) == string(other) {
		t.Fatal("repeated instances reused identical random stream")
	}
	var body map[string]string
	if err := json.Unmarshal(first, &body); err != nil {
		t.Fatal(err)
	}
	if len([]rune(body["repeat"])) != 13 || body["fixed"] != "$input stays literal" {
		t.Fatalf("wrong generated content: %v", body)
	}
	if !RequiresRandom(spec) {
		t.Fatal("random requirement was not discovered")
	}
	if _, err := Generate(spec, map[string]json.RawMessage{"length": json.RawMessage(`101`)}, context); err == nil {
		t.Fatal("out of range input accepted")
	}
	if _, err := Generate(spec, map[string]json.RawMessage{"other": json.RawMessage(`1`)}, context); err == nil {
		t.Fatal("undeclared input accepted")
	}
}

func TestSequenceReferencesOnlyPreviousExplicitResponses(t *testing.T) {
	spec, err := Decode(json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[]}},"assertions":[],"workflow":{"mode":"sequence","steps":[{"id":"followup","request":{"body":{"messages":[{"$response":"initial","pointer":"/choices/0/message"},{"role":"user","content":"Next"}]}}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	responses := map[string]json.RawMessage{"initial": json.RawMessage(`{"choices":[{"message":{"role":"assistant","content":"First"}}]}`)}
	body, err := Render(spec.Workflow.Steps[0].Request, nil, RandomContext{}, responses)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"content":"First"`) {
		t.Fatalf("missing extracted response: %s", body)
	}
	spec.Workflow.Steps[0].Request.Body = json.RawMessage(`{"messages":[{"$response":"followup","pointer":""}]}`)
	if err := spec.Validate(); err == nil {
		t.Fatal("cyclic response reference accepted")
	}
}

func TestAssertionVerdictUsesConfiguredHTTPStatusAndPartialSources(t *testing.T) {
	status := 400
	observation := Observation{HTTPStatus: &status}
	assertions := []Assertion{{ID: "status", Source: "http.status", Operator: "equals", Value: json.RawMessage(`400`)}}
	if got := Evaluate(assertions, observation); got.Status != VerdictPassed {
		t.Fatalf("expected rejection did not pass: %+v", got)
	}
	status = 200
	if got := Evaluate(assertions, observation); got.Status != VerdictFailed {
		t.Fatalf("unexpected success did not fail: %+v", got)
	}
	assertions = append(assertions, Assertion{ID: "code", Source: "response", Pointer: "/error/code", Operator: "exists"})
	status = 400
	if got := Evaluate(assertions, observation); got.Status != VerdictIndeterminate {
		t.Fatalf("missing response was not indeterminate: %+v", got)
	}
	observation.Response = json.RawMessage(`{"error":{}}`)
	if got := Evaluate(assertions, observation); got.Status != VerdictFailed {
		t.Fatalf("missing field was skipped: %+v", got)
	}
}

func TestAssertionGroupsSupportAdmissionOrTerminalFailure(t *testing.T) {
	assertion := Assertion{ID: "rejected", Any: []Assertion{
		{ID: "admission", Source: "http.status", Operator: "equals", Value: json.RawMessage(`400`)},
		{ID: "terminal", Source: "task", Pointer: "/status", Operator: "equals", Value: json.RawMessage(`"failed"`)},
	}}
	status := 200
	observation := Observation{HTTPStatus: &status, Task: &Task{ID: "task", Status: "failed", Terminal: true}}
	if got := Evaluate([]Assertion{assertion}, observation); got.Status != VerdictPassed || len(got.Assertions[0].Children) != 2 {
		t.Fatalf("wrong grouped result: %+v", got)
	}
	observation.Task = nil
	if got := Evaluate([]Assertion{assertion}, observation); got.Status != VerdictIndeterminate {
		t.Fatalf("unavailable terminal source: %+v", got)
	}
}

func TestInputExpansionHasAggregateBudget(t *testing.T) {
	value, _ := json.Marshal(strings.Repeat("a", MaxDocumentBytes/2))
	request := Request{Body: json.RawMessage(`{"a":{"$input":"x"},"b":{"$input":"x"},"c":{"$input":"x"}}`)}
	if _, err := Render(request, map[string]json.RawMessage{"x": value}, RandomContext{}, nil); err == nil {
		t.Fatal("unbounded repeated input expansion")
	}
}
