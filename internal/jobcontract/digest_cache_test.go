package jobcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCachedDigestRechecksChangedAndInvalidSpec(t *testing.T) {
	spec, err := comparisonSpec(t, 2).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	first, err := spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	again, err := spec.Digest()
	if err != nil || first != again {
		t.Fatalf("unchanged cached digest: %v", err)
	}
	for index := range spec.Spec.Comparison.Runs {
		entry := &spec.Spec.Comparison.Runs[index].Snapshot.Entries[0]
		entry.CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
			`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"changed"}]}},"assertions":[]}`)
	}
	changed, err := spec.Digest()
	if err != nil || changed == first {
		t.Fatalf("changed bytes reused digest: %v", err)
	}
	entry := &spec.Spec.Comparison.Runs[0].Snapshot.Entries[0]
	entry.CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{"api_key":"secret"}`)
	if _, err := spec.Digest(); err == nil {
		t.Fatal("cache admitted invalid edited spec")
	}
}

func TestDigestPreservesFrozenContractCanonicalization(t *testing.T) {
	for _, spec := range []JobSpec{performanceSpec(t), comparisonSpec(t, 2)} {
		frozen, err := spec.Freeze()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(frozen)
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var normalized any
		if err := decoder.Decode(&normalized); err != nil {
			t.Fatal(err)
		}
		canonical, err := json.Marshal(normalized)
		if err != nil {
			t.Fatal(err)
		}
		expected := sha256.Sum256(canonical)
		got, err := spec.Digest()
		if err != nil || got != hex.EncodeToString(expected[:]) {
			t.Fatalf("published contract digest changed: %v", err)
		}
	}
}
