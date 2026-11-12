package jobcontract

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestOwnedSpecProofAndCachedEncodingRejectMutation(t *testing.T) {
	spec, err := comparisonSpec(t, 2).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	first, err := DecodeSpec(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	first.Spec.Comparison.Runs[0].Snapshot.Channel.UpstreamModelName = "changed"
	if err := first.Validate(); err == nil {
		t.Fatal("mutable input inherited stale validation proof")
	}
	second, err := DecodeSpec(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := second.FrozenRuns()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := frozen[0].Snapshot()
	snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{"api_key":"poison"}`)
	if err := second.Validate(); err != nil {
		t.Fatalf("immutable run exposed internal snapshot: %v", err)
	}
	canonical, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	canonical[0] = '['
	again, err := second.CanonicalJSON()
	if err != nil || !json.Valid(again) {
		t.Fatalf("canonical encoding buffer leaked: %v", err)
	}
}
