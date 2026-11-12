package testspec

import (
	"encoding/json"
	"testing"
)

func TestAuthoredEvidenceGapIsIndeterminateButConflictingDataStillFails(t *testing.T) {
	evidence := Assertion{ID: "ttl_evidence", Source: "response", Pointer: "/ttl", Operator: "exists",
		FailureStatus: VerdictIndeterminate}
	known := Assertion{ID: "known_count", Source: "response", Pointer: "/write", Operator: "equals", Value: json.RawMessage(`5`)}
	checks := []Assertion{{ID: "cache_io", All: []Assertion{known}}, {ID: "cache_ttl", All: []Assertion{evidence}}}
	for _, sample := range []struct {
		body   string
		status VerdictStatus
	}{
		{body: `{"write":5,"ttl":"5m"}`, status: VerdictPassed},
		{body: `{"write":5}`, status: VerdictIndeterminate},
		{body: `{"write":6}`, status: VerdictFailed},
	} {
		observation := Observation{Response: json.RawMessage(sample.body)}
		verdict := Evaluate(checks, observation)
		if verdict.Status != sample.status {
			t.Fatalf("%s: status=%s, want %s", sample.body, verdict.Status, sample.status)
		}
		if HasEvidenceGap(verdict) != (sample.status == VerdictIndeterminate) {
			t.Fatal("evidence warning incorrectly classified")
		}
	}
	for _, status := range []VerdictStatus{VerdictPassed, VerdictFailed, VerdictNotApplicable, "other"} {
		evidence.FailureStatus = status
		if err := validateAssertion(evidence, 0, map[string]bool{}); err == nil {
			t.Fatalf("unsupported failure status accepted: %s", status)
		}
	}
	evidence.FailureStatus = ""
	if Evaluate([]Assertion{evidence}, Observation{Response: json.RawMessage(`{}`)}).Status != VerdictFailed {
		t.Fatal("older assertions changed their default failure behavior")
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["failure_status"]; exists {
		t.Fatal("default field changed older authored JSON")
	}
}
