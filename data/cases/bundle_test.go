package casebundle_test

import (
	"bytes"
	"io/fs"
	"os"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
)

func TestBundleContainsCompleteV2Catalog(t *testing.T) {
	t.Parallel()

	source := os.DirFS(".")
	matches, err := fs.Glob(source, "*/*/case.json")
	if err != nil || len(matches) == 0 {
		t.Fatalf("discover source cases: %v", err)
	}
	embedded, err := fs.Glob(casebundle.Bundle, "*/*/case.json")
	if err != nil || len(embedded) != len(matches) {
		t.Fatalf("embedded cases = %d, source cases = %d, err = %v", len(embedded), len(matches), err)
	}
	for _, path := range matches {
		contents, readErr := fs.ReadFile(casebundle.Bundle, path)
		if readErr != nil {
			t.Fatalf("read embedded %s: %v", path, readErr)
		}
		expected, readErr := fs.ReadFile(source, path)
		if readErr != nil || len(contents) == 0 || !bytes.Equal(contents, expected) {
			t.Fatalf("embedded %s does not match source: %v", path, readErr)
		}
	}

	if _, err := fs.ReadFile(casebundle.Bundle, "kimi-k3/load-profile-32k.json"); err != nil {
		t.Fatalf("read embedded deferred load profile: %v", err)
	}

	for _, path := range []string{
		"openai-chat/R001-reasoning-effort-low/case.json",
		"openai-chat/R004-reasoning-effort-invalid/case.json",
		"openai-chat/P011-fixed-sampling-values/case.json",
		"openai-chat/P034-top-logprobs-requires-logprobs/case.json",
		"openai-chat/P043-stop-item-33-bytes/case.json",
		"openai-chat/P052-max-completion-over-model-limit/case.json",
		"openai-chat/F032-json-schema-missing-name/case.json",
		"openai-chat/F040-dynamic-tool/case.json",
		"openai-chat/F046-tool-name-129-chars/case.json",
		"openai-chat/F050-partial-mode/case.json",
		"openai-chat/F057-message-empty-content/case.json",
	} {
		if _, err := fs.ReadFile(casebundle.Bundle, path); err != nil {
			t.Fatalf("read embedded K3 boundary case %s: %v", path, err)
		}
	}
}
