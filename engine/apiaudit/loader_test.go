package apiaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSuiteAcceptsWanVideoTaskKinds(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct {
		dir  string
		kind string
	}{
		{dir: "success", kind: "wan_task_success"},
		{dir: "rejected", kind: "wan_task_rejected"},
	} {
		dir := filepath.Join(root, "wan-video", item.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := `{"id":"` + item.dir + `","name":"` + item.dir + `","dimension":"parameters","protocol":"wan-video","model_targets":["wan3.0-video"],"kind":"` + item.kind + `","request":{"method":"POST","path":"/api/v1/services/aigc/video-generation/video-synthesis","headers":{"X-DashScope-Async":"enable"},"body":{"input":{"prompt":"cat"}}}}`
		if err := os.WriteFile(filepath.Join(dir, "case.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases, err := LoadSuite(root, "wan-video")
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	if len(cases) != 2 || cases[0].Request.Headers["X-DashScope-Async"] != "enable" {
		t.Fatalf("cases = %#v", cases)
	}
}

func TestLoadSuiteRejectsWanCaseWithoutVersionTargets(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "wan-video", "unscoped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":"unscoped","name":"unscoped","dimension":"parameters","protocol":"wan-video","kind":"wan_task_rejected","request":{"method":"POST","path":"/api/v1/services/aigc/video-generation/video-synthesis","headers":{"X-DashScope-Async":"enable"},"body":{"input":{"prompt":"cat"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "case.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadSuite(root, "wan-video"); err == nil {
		t.Fatal("LoadSuite accepted an unscoped Wan case")
	}
}

func TestLoadSuiteAcceptsMiniMaxVideoTaskKinds(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct {
		dir  string
		kind string
	}{
		{dir: "success", kind: "minimax_video_task_success"},
		{dir: "rejected", kind: "minimax_video_task_rejected"},
	} {
		dir := filepath.Join(root, "minimax-video", item.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := `{"id":"` + item.dir + `","name":"` + item.dir + `","dimension":"parameters","protocol":"minimax-video","model_targets":["MiniMax-H3"],"kind":"` + item.kind + `","request":{"method":"POST","path":"/v2/video_generation","headers":{},"body":{"content":[{"type":"text","text":"cat"}],"resolution":"768P","duration":4,"ratio":"16:9"}}}`
		if err := os.WriteFile(filepath.Join(dir, "case.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases, err := LoadSuite(root, "minimax-video")
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	if len(cases) != 2 || cases[0].Request.Path != "/v2/video_generation" {
		t.Fatalf("cases = %#v", cases)
	}
}

func TestLoadSuiteRejectsUnscopedMiniMaxVideoCase(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "minimax-video", "unscoped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":"unscoped","name":"unscoped","dimension":"parameters","protocol":"minimax-video","kind":"minimax_video_task_rejected","request":{"method":"POST","path":"/v2/video_generation","headers":{},"body":{"content":[]}}}`
	if err := os.WriteFile(filepath.Join(dir, "case.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadSuite(root, "minimax-video"); err == nil {
		t.Fatal("LoadSuite() accepted an unscoped paid MiniMax video case")
	}
}

func TestLoadSuiteAcceptsMiniMaxBoundaryOptionsAndAuthenticationKind(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "minimax-video", "auth-missing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":"auth-missing","name":"auth missing","dimension":"authorization","protocol":"minimax-video","model_targets":["MiniMax-H3"],"kind":"minimax_video_auth_rejected","request":{"method":"POST","path":"/v2/video_generation","headers":{},"body":{"content":[]}},"options":{"model_mode":"omit","omit_authorization":true,"require_video_usage":false,"expected_resolution":"2K","expected_duration":15,"expected_ratio":"9:16"}}`
	if err := os.WriteFile(filepath.Join(dir, "case.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	cases, err := LoadSuite(root, "minimax-video")
	if err != nil || len(cases) != 1 || cases[0].Kind != "minimax_video_auth_rejected" {
		t.Fatalf("LoadSuite() = %#v, %v", cases, err)
	}
}

func TestLoadSuiteReadsRepositoryV2WanCasesAndFiltersByModel(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "cases"))
	cases, err := LoadSuite(root, "wan-video")
	if err != nil {
		t.Fatalf("LoadSuite(repository Wan cases): %v", err)
	}
	if len(cases) != 96 {
		t.Fatalf("runnable Wan case count = %d, want 96", len(cases))
	}
	wan26 := FilterCasesForModel(cases, "wan2.6-t2v")
	if len(wan26) != 4 {
		t.Fatalf("Wan 2.6 case count = %d, want 4", len(wan26))
	}
	for _, definition := range wan26 {
		if len(definition.ModelTargets) != 1 || definition.ModelTargets[0] != "wan2.6-t2v" {
			t.Fatalf("case %s targets = %#v", definition.ID, definition.ModelTargets)
		}
		if definition.Request.Headers["X-DashScope-Async"] != "enable" {
			t.Fatalf("case %s lost async header", definition.ID)
		}
	}
}

func TestValidateCaseOptionsRejectsInvalidWanBoundaryControls(t *testing.T) {
	for _, item := range []struct {
		name    string
		options map[string]any
		want    string
	}{
		{name: "model mode type", options: map[string]any{"model_mode": true}, want: "model_mode must be a string"},
		{name: "model mode value", options: map[string]any{"model_mode": "replace"}, want: "model_mode must be one of"},
		{name: "prompt length type", options: map[string]any{"prompt_length": "20000"}, want: "prompt_length must be a number"},
		{name: "prompt length range", options: map[string]any{"prompt_length": float64(0)}, want: "prompt_length must be an integer between 1 and 20001"},
		{name: "usage evidence shape", options: map[string]any{"expected_usage": []any{"SR"}}, want: "expected_usage must be an object"},
	} {
		t.Run(item.name, func(t *testing.T) {
			err := validateCaseOptions(CaseDefinition{ID: "wan-boundary", Options: item.options})
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("error = %v, want substring %q", err, item.want)
			}
		})
	}
}
