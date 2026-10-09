package casebundle_test

import (
	"encoding/json"
	"io/fs"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
)

func TestBundleContainsMiniMaxH3BoundaryCatalog(t *testing.T) {
	matches, err := fs.Glob(casebundle.Bundle, "minimax-video/*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 149 {
		t.Fatalf("MiniMax H3 embedded cases = %d, want 149", len(matches))
	}
	automatic, manual, disabled := 0, 0, 0
	for _, path := range matches {
		contents, readErr := fs.ReadFile(casebundle.Bundle, path)
		if readErr != nil {
			t.Fatalf("read embedded %s: %v", path, readErr)
		}
		if len(contents) == 0 {
			t.Fatalf("embedded %s is empty", path)
		}
		var document struct {
			Enabled       bool                       `json:"enabled"`
			Default       bool                       `json:"default"`
			ExecutionMode string                     `json:"execution_mode"`
			Definitions   map[string]json.RawMessage `json:"definitions"`
		}
		if err := json.Unmarshal(contents, &document); err != nil {
			t.Fatalf("decode embedded %s: %v", path, err)
		}
		if document.Default || document.Definitions["minimax-video"] == nil {
			t.Fatalf("embedded %s policy = %#v", path, document)
		}
		if !document.Enabled {
			disabled++
			continue
		}
		switch document.ExecutionMode {
		case "automatic":
			automatic++
		case "manual":
			manual++
		default:
			t.Fatalf("embedded %s execution mode = %q", path, document.ExecutionMode)
		}
	}
	if automatic != 43 || manual != 101 || disabled != 5 {
		t.Fatalf("MiniMax H3 policy counts = automatic:%d manual:%d, want 43/101 with 5 disabled binding Cases", automatic, manual)
	}
}
