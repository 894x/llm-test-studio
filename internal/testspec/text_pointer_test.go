package testspec

import (
	"encoding/json"
	"testing"
)

func textObservation(text string) Observation {
	return Observation{Text: &text, Response: json.RawMessage(`{"choices":[{"message":{"tool_calls":[{"function":{"arguments":"{\"city\":\"北京\"}"}}]}}]}`)}
}

func evaluateOneJSON(t *testing.T, raw string, observation Observation) AssertionResult {
	t.Helper()
	var assertion Assertion
	if err := json.Unmarshal([]byte(raw), &assertion); err != nil {
		t.Fatalf("assertion %s: %v", raw, err)
	}
	return evaluateOne(assertion, observation)
}

// The text source is a plain string, so a pointer only means something inside its decoded JSON.
func TestTextPointerLooksInsideTransformedJSON(t *testing.T) {
	answer := textObservation(`{"person":{"name":"小林","age":28},"tags":["工程师","跑者"],"contact":null}`)
	for _, raw := range []string{
		`{"id":"person","source":"text","pointer":"/person","operator":"type","value":"object","transform":"json"}`,
		`{"id":"tags","source":"text","pointer":"/tags","operator":"type","value":"array","transform":"json"}`,
		`{"id":"age","source":"text","pointer":"/person/age","operator":"equals","value":28,"transform":"json"}`,
		`{"id":"contact","source":"text","pointer":"/contact","operator":"type","value":"null","transform":"json"}`,
	} {
		if got := evaluateOneJSON(t, raw, answer); got.Status != VerdictPassed {
			t.Errorf("%s: status %s reason %q", raw, got.Status, got.Reason)
		}
	}
}

func TestTextPointerReportsMissingFieldAndBadJSON(t *testing.T) {
	missing := evaluateOneJSON(t, `{"id":"p","source":"text","pointer":"/person","operator":"type","value":"object","transform":"json"}`,
		textObservation(`{"tags":[]}`))
	if missing.Status != VerdictFailed || missing.Reason != "field_missing" {
		t.Errorf("missing field: status %s reason %q", missing.Status, missing.Reason)
	}
	bad := evaluateOneJSON(t, `{"id":"p","source":"text","pointer":"/person","operator":"type","value":"object","transform":"json"}`,
		textObservation("not json"))
	if bad.Status != VerdictFailed || bad.Reason != "transform_failed" {
		t.Errorf("bad JSON: status %s reason %q", bad.Status, bad.Reason)
	}
	exists := evaluateOneJSON(t, `{"id":"p","source":"text","pointer":"/person","operator":"exists","transform":"json"}`,
		textObservation(`{"person":{}}`))
	if exists.Status != VerdictPassed {
		t.Errorf("exists inside text JSON: status %s reason %q", exists.Status, exists.Reason)
	}
}

// Every other source keeps pointer-then-transform, and text without a pointer is unchanged.
func TestPointerThenTransformStillAppliesElsewhere(t *testing.T) {
	observation := textObservation(`{"name":"小林","age":28}`)
	for _, raw := range []string{
		`{"id":"args","source":"response","pointer":"/choices/0/message/tool_calls/0/function/arguments","operator":"equals","value":{"city":"北京"},"transform":"json"}`,
		`{"id":"whole","source":"text","operator":"type","value":"object","transform":"json"}`,
		`{"id":"name","source":"text","operator":"contains","value":"name","transform":"json"}`,
		`{"id":"trim","source":"text","operator":"non_empty","transform":"trim"}`,
	} {
		if got := evaluateOneJSON(t, raw, observation); got.Status != VerdictPassed {
			t.Errorf("%s: status %s reason %q", raw, got.Status, got.Reason)
		}
	}
}
