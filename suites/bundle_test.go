package suitebundle_test

import (
	"encoding/json"
	"io/fs"
	"testing"

	suitebundle "github.com/894x/llm-test-studio/suites"
)

func TestBundleContainsVersionScopedWanSuites(t *testing.T) {
	t.Parallel()

	matches, err := fs.Glob(suitebundle.Bundle, "wan-video/*/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 17 {
		t.Fatalf("Wan suite count = %d, want 17", len(matches))
	}
	wantTargets := map[string]bool{
		"wan3.0-video": false, "wan3.0-video-prime": false, "wan2.7-t2v": false, "wan2.7-t2v-2026-06-12": false,
		"wan2.6-t2v": false, "wan2.5-t2v-preview": false, "wan2.2-t2v-plus": false, "wanx2.1-t2v-turbo": false, "wanx2.1-t2v-plus": false,
	}
	wantWan3Scenarios := map[string]struct {
		target string
		count  int
	}{
		"wan-video.wan3.0-video.connectivity":       {target: "wan3.0-video", count: 1},
		"wan-video.wan3.0-video.basic":              {target: "wan3.0-video", count: 6},
		"wan-video.wan3.0-video.negative":           {target: "wan3.0-video", count: 39},
		"wan-video.wan3.0-video.automatic":          {target: "wan3.0-video", count: 74},
		"wan-video.wan3.0-video":                    {target: "wan3.0-video", count: 191},
		"wan-video.wan3.0-video-prime.connectivity": {target: "wan3.0-video-prime", count: 1},
		"wan-video.wan3.0-video-prime.basic":        {target: "wan3.0-video-prime", count: 6},
		"wan-video.wan3.0-video-prime.negative":     {target: "wan3.0-video-prime", count: 39},
		"wan-video.wan3.0-video-prime.automatic":    {target: "wan3.0-video-prime", count: 74},
		"wan-video.wan3.0-video-prime":              {target: "wan3.0-video-prime", count: 191},
	}
	seenWan3Scenarios := make(map[string]bool, len(wantWan3Scenarios))
	wan3ScenarioCases := make(map[string][]string, len(wantWan3Scenarios))
	for _, path := range matches {
		raw, readErr := fs.ReadFile(suitebundle.Bundle, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var document struct {
			Key         string   `json:"key"`
			Protocol    string   `json:"protocol"`
			ModelTarget string   `json:"model_target"`
			CaseKeys    []string `json:"case_keys"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if document.Protocol != "wan-video" || len(document.CaseKeys) == 0 {
			t.Fatalf("invalid Wan suite %s: %#v", path, document)
		}
		if _, known := wantTargets[document.ModelTarget]; !known {
			t.Fatalf("unexpected Wan suite target %q", document.ModelTarget)
		}
		wantTargets[document.ModelTarget] = true
		if want, exists := wantWan3Scenarios[document.Key]; exists {
			if document.ModelTarget != want.target || len(document.CaseKeys) != want.count {
				t.Fatalf("Wan3 scenario suite %s = target %q, %d cases; want %q, %d", document.Key, document.ModelTarget, len(document.CaseKeys), want.target, want.count)
			}
			seenWan3Scenarios[document.Key] = true
			wan3ScenarioCases[document.Key] = append([]string(nil), document.CaseKeys...)
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("missing version-scoped suite for %s", target)
		}
	}
	for key := range wantWan3Scenarios {
		if !seenWan3Scenarios[key] {
			t.Errorf("missing Wan3 scenario suite %s", key)
		}
	}
	for _, target := range []string{"wan3.0-video", "wan3.0-video-prime"} {
		prefix := "wan-video." + target
		assertExactCaseKeys(t, wan3ScenarioCases[prefix+".connectivity"], []string{"wan30.t2v.min_duration"})
		assertExactCaseKeys(t, wan3ScenarioCases[prefix+".basic"], []string{
			"wan30.t2v.min_duration",
			"wan30.input.prompt_or_media_required",
			"wan30.ratio.16_9",
			"wan30.audio.true",
			"wan30.prompt_extend.true",
			"wan30.watermark.true",
		})
		assertCaseSubset(t, prefix+" connectivity", wan3ScenarioCases[prefix+".connectivity"], wan3ScenarioCases[prefix+".basic"])
		assertCaseSubset(t, prefix+" basic", wan3ScenarioCases[prefix+".basic"], wan3ScenarioCases[prefix+".automatic"])
		assertCaseSubset(t, prefix+" negative", wan3ScenarioCases[prefix+".negative"], wan3ScenarioCases[prefix+".automatic"])
		assertCaseSubset(t, prefix+" automatic", wan3ScenarioCases[prefix+".automatic"], wan3ScenarioCases[prefix])
	}
}

func assertExactCaseKeys(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("case keys = %#v, want %#v", got, want)
	}
	gotSet := make(map[string]bool, len(got))
	for _, key := range got {
		gotSet[key] = true
	}
	for _, key := range want {
		if !gotSet[key] {
			t.Fatalf("case keys = %#v, missing %s", got, key)
		}
	}
}

func assertCaseSubset(t *testing.T, name string, subset, superset []string) {
	t.Helper()
	supersetKeys := make(map[string]bool, len(superset))
	for _, key := range superset {
		supersetKeys[key] = true
	}
	for _, key := range subset {
		if !supersetKeys[key] {
			t.Fatalf("%s case %s is not present in its parent suite", name, key)
		}
	}
}
