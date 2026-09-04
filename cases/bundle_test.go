package casebundle_test

import (
	"io/fs"
	"testing"

	casebundle "github.com/894x/llm-test-studio/cases"
)

func TestBundleContainsCompleteV2Catalog(t *testing.T) {
	t.Parallel()

	protocols := map[string]int{
		"openai-chat": 44,
		"kimi-k3":     87,
		"seedance":    6,
		"wan-video":   213,
	}
	for protocol, want := range protocols {
		matches, err := fs.Glob(casebundle.Bundle, protocol+"/*/case.json")
		if err != nil {
			t.Fatalf("glob %s cases: %v", protocol, err)
		}
		if got := len(matches); got != want {
			t.Fatalf("%s embedded cases = %d, want %d", protocol, got, want)
		}
		for _, path := range matches {
			contents, readErr := fs.ReadFile(casebundle.Bundle, path)
			if readErr != nil {
				t.Fatalf("read embedded %s: %v", path, readErr)
			}
			if len(contents) == 0 {
				t.Fatalf("embedded %s is empty", path)
			}
		}
	}

	if _, err := fs.ReadFile(casebundle.Bundle, "kimi-k3/load-profile-32k.json"); err != nil {
		t.Fatalf("read embedded deferred load profile: %v", err)
	}

	for _, path := range []string{
		"kimi-k3/R001-reasoning-effort-low/case.json",
		"kimi-k3/R004-reasoning-effort-invalid/case.json",
		"kimi-k3/P011-fixed-sampling-values/case.json",
		"kimi-k3/P034-top-logprobs-requires-logprobs/case.json",
		"kimi-k3/P043-stop-item-33-bytes/case.json",
		"kimi-k3/P052-max-completion-over-model-limit/case.json",
		"kimi-k3/F032-json-schema-missing-name/case.json",
		"kimi-k3/F040-dynamic-tool/case.json",
		"kimi-k3/F046-tool-name-129-chars/case.json",
		"kimi-k3/F050-partial-mode/case.json",
		"kimi-k3/F057-message-empty-content/case.json",
	} {
		if _, err := fs.ReadFile(casebundle.Bundle, path); err != nil {
			t.Fatalf("read embedded K3 boundary case %s: %v", path, err)
		}
	}
}
