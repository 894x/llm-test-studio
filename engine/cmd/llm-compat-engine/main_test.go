package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type failIfCalledDoer struct{}

func (failIfCalledDoer) Do(*http.Request) (*http.Response, error) {
	panic("dry-run must not perform an HTTP request")
}

func TestRunDryRunJSONLCompatibility(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	caseDir := filepath.Join(casesRoot, "openai-chat", "C001")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	caseJSON := `{
  "schema_version": 3,
  "key": "C001",
  "name": "manual review",
  "dimension": "protocol",
  "protocol": "openai-chat",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "definition": {
    "schema_version": 2,
    "type": "openai-chat",
    "type_version": 1,
    "spec": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": []
    }
  }
}`
	if err := os.WriteFile(filepath.Join(caseDir, "case.json"), []byte(caseJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	outputDir := filepath.Join(t.TempDir(), "report")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"run",
		"--suite", "openai-chat",
		"--cases-root", casesRoot,
		"--base-url", "https://gateway.example",
		"--model", "test-model",
		"--dry-run",
		"--output", outputDir,
		"--jsonl",
	}, func(string) string { return "" }, &stdout, &stderr, failIfCalledDoer{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	decoder := json.NewDecoder(&stdout)
	wantTypes := []string{"plan", "progress", "final"}
	for index, wantType := range wantTypes {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode event %d: %v", index, err)
		}
		if event["schema_version"] != float64(1) || event["type"] != wantType {
			t.Fatalf("event %d = %#v", index, event)
		}
		if wantType == "final" {
			if event["report_dir"] != outputDir || event["report_html"] != filepath.Join(outputDir, "report.html") {
				t.Fatalf("final event = %#v", event)
			}
		}
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("unexpected extra event: %#v", extra)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "report.json")); err != nil {
		t.Fatalf("report.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "report.html")); err != nil {
		t.Fatalf("report.html: %v", err)
	}
}

func TestRunConfigurationExitCodeCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "invalid suite is reported before credential",
			args:       []string{"run", "--suite", "invalid", "--base-url", "https://gateway.example", "--model", "test-model"},
			wantStderr: "CONFIG ERROR: --suite must be openai-chat, seedance, wan-video, or minimax-video\n",
		},
		{
			name:       "missing live credential names configured environment variable",
			args:       []string{"run", "--suite", "openai-chat", "--base-url", "https://gateway.example", "--model", "test-model", "--api-key-env", "TEST_GATEWAY_KEY"},
			wantStderr: "CONFIG ERROR: environment variable TEST_GATEWAY_KEY is required for a live run\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := run(test.args, func(string) string { return "" }, &stdout, &stderr, failIfCalledDoer{})
			if exitCode != 2 {
				t.Fatalf("exit code = %d, want 2", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
			if stderr.String() != test.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}
