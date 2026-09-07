package suitebundle_test

import (
	"encoding/json"
	"io/fs"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
)

type miniMaxSuiteDocument struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Protocol    string   `json:"protocol"`
	ModelTarget string   `json:"model_target"`
	CaseKeys    []string `json:"case_keys"`
}

func TestBundleContainsMiniMaxH3ScenarioSuites(t *testing.T) {
	want := map[string]struct {
		key   string
		name  string
		count int
	}{
		"minimax-video/MiniMax-H3-connectivity/suite.json": {
			key: "minimax-video.MiniMax-H3.connectivity", name: "MiniMax H3 连通性测试套件", count: 3,
		},
		"minimax-video/MiniMax-H3-basic/suite.json": {
			key: "minimax-video.MiniMax-H3.basic", name: "MiniMax H3 基本功能测试套件", count: 24,
		},
		"minimax-video/MiniMax-H3-parameter-rejection/suite.json": {
			key: "minimax-video.MiniMax-H3.parameter-rejection", name: "MiniMax H3 参数拒绝测试套件", count: 45,
		},
		"minimax-video/MiniMax-H3-automatic-regression/suite.json": {
			key: "minimax-video.MiniMax-H3.automatic-regression", name: "MiniMax H3 自动化核心回归套件", count: 48,
		},
		"minimax-video/MiniMax-H3/suite.json": {
			key: "minimax-video.MiniMax-H3", name: "MiniMax H3 视频生成完整边界套件", count: 149,
		},
	}

	documents := make(map[string]miniMaxSuiteDocument, len(want))
	for path, expected := range want {
		raw, err := fs.ReadFile(suitebundle.Bundle, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var document miniMaxSuiteDocument
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if document.Key != expected.key || document.Name != expected.name || document.Protocol != "minimax-video" || document.ModelTarget != "MiniMax-H3" || len(document.CaseKeys) != expected.count {
			t.Fatalf("MiniMax H3 suite %s = %#v", path, document)
		}
		assertUniqueKeys(t, path, document.CaseKeys)
		documents[path] = document
	}

	connectivity := documents["minimax-video/MiniMax-H3-connectivity/suite.json"]
	assertExactKeys(t, "connectivity", connectivity.CaseKeys, []string{
		"h3.t2v.min_duration",
		"h3.authorization.required",
		"h3.authorization.invalid",
	})

	basic := documents["minimax-video/MiniMax-H3-basic/suite.json"]
	assertExactKeys(t, "basic", basic.CaseKeys, []string{
		"h3.t2v.min_duration",
		"h3.authorization.required",
		"h3.authorization.invalid",
		"h3.content.required",
		"h3.model.required",
		"h3.resolution.required",
		"h3.duration.required",
		"h3.t2v.ratio_required",
		"h3.content.text_min",
		"h3.resolution.2k",
		"h3.duration.interior",
		"h3.t2v.ratio_9_16",
		"h3.callback_url.valid",
		"h3.aigc_watermark.false",
		"h3.aigc_watermark.true",
		"h3.i2v.role_omitted",
		"h3.i2v.first_frame",
		"h3.i2v.first_last_frame",
		"h3.r2v.reference_image",
		"h3.r2v.reference_video",
		"h3.r2v.reference_audio",
		"h3.r2v.all_media",
		"h3.usage.t2v_seconds",
		"h3.usage.multimodal",
	})

	automaticKeys, rejectedKeys, allKeys := miniMaxH3CaseKeysByExecutionMode(t)
	rejected := documents["minimax-video/MiniMax-H3-parameter-rejection/suite.json"]
	automatic := documents["minimax-video/MiniMax-H3-automatic-regression/suite.json"]
	complete := documents["minimax-video/MiniMax-H3/suite.json"]
	assertExactKeys(t, "parameter rejection", rejected.CaseKeys, rejectedKeys)
	assertExactKeys(t, "automatic regression", automatic.CaseKeys, automaticKeys)
	assertExactKeys(t, "complete", complete.CaseKeys, allKeys)
	assertSubset(t, "connectivity", connectivity.CaseKeys, "basic", basic.CaseKeys)
	assertSubset(t, "basic", basic.CaseKeys, "complete", complete.CaseKeys)
}

func miniMaxH3CaseKeysByExecutionMode(t *testing.T) (automatic []string, rejected []string, all []string) {
	t.Helper()
	matches, err := fs.Glob(casebundle.Bundle, "minimax-video/*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range matches {
		raw, err := fs.ReadFile(casebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Key           string `json:"key"`
			ExecutionMode string `json:"execution_mode"`
			Definition    struct {
				Spec struct {
					Kind string `json:"kind"`
				} `json:"spec"`
			} `json:"definition"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		all = append(all, document.Key)
		if document.ExecutionMode == "automatic" {
			automatic = append(automatic, document.Key)
			if document.Definition.Spec.Kind == "minimax_video_task_rejected" {
				rejected = append(rejected, document.Key)
			}
		}
	}
	return automatic, rejected, all
}

func assertUniqueKeys(t *testing.T, label string, keys []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("%s has duplicate key %q", label, key)
		}
		seen[key] = struct{}{}
	}
}

func assertExactKeys(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSet := make(map[string]struct{}, len(got))
	for _, key := range got {
		gotSet[key] = struct{}{}
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, key := range want {
		wantSet[key] = struct{}{}
	}
	for _, key := range want {
		if _, found := gotSet[key]; !found {
			t.Errorf("%s is missing %q", label, key)
		}
	}
	for _, key := range got {
		if _, found := wantSet[key]; !found {
			t.Errorf("%s unexpectedly contains %q", label, key)
		}
	}
}

func assertSubset(t *testing.T, subsetLabel string, subset []string, supersetLabel string, superset []string) {
	t.Helper()
	available := make(map[string]struct{}, len(superset))
	for _, key := range superset {
		available[key] = struct{}{}
	}
	for _, key := range subset {
		if _, found := available[key]; !found {
			t.Errorf("%s key %q is not in %s", subsetLabel, key, supersetLabel)
		}
	}
}
