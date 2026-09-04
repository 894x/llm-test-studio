package runs

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/casetypes"
)

func TestClassifyProbeResponseRejectsTrailingData(t *testing.T) {
	if _, err := classifyProbeResponse([]byte(`{"provider":"a"} trailing`), nil); err == nil {
		t.Fatal("classifyProbeResponse() accepted trailing non-JSON data")
	}
}

func TestClassifyProbeResponseMarksOverlappingSignaturesAmbiguous(t *testing.T) {
	signatures := []casetypes.ResponseProbeSignature{
		{Label: "provider-a", Match: []casetypes.ResponseProbeMatcher{{Pointer: "/provider", Operator: "exists"}}},
		{Label: "provider-b", Match: []casetypes.ResponseProbeMatcher{{Pointer: "/provider", Operator: "equals", Value: json.RawMessage(`"a"`)}}},
	}

	dimensions, err := classifyProbeResponse([]byte(`{"provider":"a"}`), signatures)
	if err != nil {
		t.Fatalf("classifyProbeResponse() error = %v", err)
	}
	if dimensions["probe_bucket"] != "ambiguous" || dimensions["probe_classification"] != "ambiguous" {
		t.Fatalf("dimensions = %#v", dimensions)
	}
}
