package testspec

import (
	"encoding/json"
	"testing"
)

func TestSequenceControlsValidationAndRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name, steps string
		valid       bool
	}{
		{name: "legacy", steps: `{"id":"next","request":{"body":{}}}`, valid: true},
		{name: "replay", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":360000}`, valid: true},
		{name: "render_after_delay", steps: `{"id":"next","request":{"body":{}},"delay_ms":1}`, valid: true},
		{
			name: "chain", valid: true,
			steps: `{"id":"next","reuse_request_from":"initial"},{"id":"last","reuse_request_from":"next"}`,
		},
		{name: "max_wait", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":86400000}`, valid: true},
		{name: "negative", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":-1}`},
		{name: "fractional", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":1.5}`},
		{name: "string", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":"1"}`},
		{name: "overflow", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":18446744073709551616}`},
		{name: "too_long", steps: `{"id":"next","reuse_request_from":"initial","delay_ms":86400001}`},
		{
			name: "total_too_long",
			steps: `{"id":"next","reuse_request_from":"initial","delay_ms":86400000},` +
				`{"id":"last","reuse_request_from":"next","delay_ms":1}`,
		},
		{name: "unknown", steps: `{"id":"next","reuse_request_from":"missing"}`},
		{name: "self", steps: `{"id":"next","reuse_request_from":"next"}`},
		{name: "future", steps: `{"id":"next","reuse_request_from":"last"},{"id":"last","request":{"body":{}}}`},
		{name: "ambiguous", steps: `{"id":"next","reuse_request_from":"initial","request":{"body":{}}}`},
		{name: "missing_body", steps: `{"id":"next","delay_ms":1}`},
		{
			name:  "duplicate",
			steps: `{"id":"next","reuse_request_from":"initial"},{"id":"next","reuse_request_from":"initial"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"inputs":{},"request":{"body":{}},"assertions":[],` +
				`"workflow":{"mode":"sequence","steps":[` + test.steps + `]}}`)
			spec, err := Decode(raw)
			if (err == nil) != test.valid {
				t.Fatalf("Decode error = %v, want valid=%t", err, test.valid)
			}
			if !test.valid {
				return
			}
			encoded, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(encoded); err != nil {
				t.Fatalf("round trip rejected: %v", err)
			}
			if test.name == "legacy" && string(encoded) != string(raw) {
				t.Fatalf("legacy encoding changed: %s", encoded)
			}
		})
	}
}

func TestSequenceTimeoutKeepsLegacyBudgetAndAddsControlledWaits(t *testing.T) {
	legacy := Spec{Workflow: &Workflow{Mode: "sequence", Steps: []Step{{ID: "next"}}}}
	if legacy.UsesSequenceControls() || legacy.ExecutionTimeoutMS(1000) != 1000 {
		t.Fatal("legacy whole-Case timeout changed")
	}
	controlled := Spec{Workflow: &Workflow{Mode: "sequence", Steps: []Step{
		{ID: "next", DelayMS: 360000, ReuseRequestFrom: "initial"},
		{ID: "last", DelayMS: 2000, ReuseRequestFrom: "next"},
	}}}
	if !controlled.UsesSequenceControls() || controlled.ExecutionTimeoutMS(1000) != 365000 {
		t.Fatal("controlled timeout did not include waits and each HTTP request")
	}
	if controlled.ExecutionTimeoutMS(0) != 2162000 {
		t.Fatal("default request timeout missing")
	}
}
