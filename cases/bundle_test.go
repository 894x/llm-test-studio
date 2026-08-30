package casebundle_test

import (
	"io/fs"
	"testing"

	casebundle "github.com/894x/llm-test/cases"
)

func TestBundleContainsCompleteLegacyCatalog(t *testing.T) {
	t.Parallel()

	protocols := map[string]int{
		"openai-chat": 43,
		"kimi-k3":     40,
		"seedance":    6,
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
}
