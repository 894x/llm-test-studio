package casebundle_test

import (
	"encoding/json"
	"io/fs"
	"testing"

	casebundle "github.com/894x/llm-test-studio/cases"
)

func TestBundleContainsMiniMaxH3BoundaryCatalog(t *testing.T) {
	matches, err := fs.Glob(casebundle.Bundle, "minimax-video/*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 149 {
		t.Fatalf("MiniMax H3 embedded cases = %d, want 149", len(matches))
	}
	automatic, manual := 0, 0
	for _, path := range matches {
		contents, readErr := fs.ReadFile(casebundle.Bundle, path)
		if readErr != nil {
			t.Fatalf("read embedded %s: %v", path, readErr)
		}
		if len(contents) == 0 {
			t.Fatalf("embedded %s is empty", path)
		}
		var document struct {
			Enabled       bool     `json:"enabled"`
			Default       bool     `json:"default"`
			ExecutionMode string   `json:"execution_mode"`
			ModelTargets  []string `json:"model_targets"`
		}
		if err := json.Unmarshal(contents, &document); err != nil {
			t.Fatalf("decode embedded %s: %v", path, err)
		}
		if !document.Enabled || document.Default || len(document.ModelTargets) != 1 || document.ModelTargets[0] != "MiniMax-H3" {
			t.Fatalf("embedded %s policy = %#v", path, document)
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
	if automatic != 48 || manual != 101 {
		t.Fatalf("MiniMax H3 policy counts = automatic:%d manual:%d, want 48/101", automatic, manual)
	}
}
