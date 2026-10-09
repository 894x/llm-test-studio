package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
	"github.com/894x/llm-test-studio/internal/application/compatibility"
)

func TestHelpAndDefaultMachineDiagnosticsHaveStableContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           []string
		wantCode       int
		wantHelp       string
		wantDiagnostic string
	}{
		{
			name:     "root help",
			args:     []string{"--help"},
			wantCode: 0,
			wantHelp: "Usage: llm-test-studio <doctor|audit|load>",
		},
		{
			name:     "audit help",
			args:     []string{"audit", "--help"},
			wantCode: 0,
			wantHelp: "Usage: llm-test-studio audit <list|run>",
		},
		{
			name:     "doctor help",
			args:     []string{"doctor", "--help"},
			wantCode: 0,
			wantHelp: "Usage: llm-test-studio doctor [options]",
		},
		{
			name:     "audit list help",
			args:     []string{"audit", "list", "--help"},
			wantCode: 0,
			wantHelp: "Usage: llm-test-studio audit list [options]",
		},
		{
			name:           "missing root command",
			wantCode:       2,
			wantDiagnostic: "usage_error",
		},
		{
			name:           "unknown root command does not echo argument",
			args:           []string{"sk-secret-command"},
			wantCode:       2,
			wantDiagnostic: "usage_error",
		},
		{
			name:           "missing audit command",
			args:           []string{"audit"},
			wantCode:       2,
			wantDiagnostic: "usage_error",
		},
		{
			name:           "unknown audit command does not echo argument",
			args:           []string{"audit", "sk-secret-command"},
			wantCode:       2,
			wantDiagnostic: "usage_error",
		},
		{
			name:           "audit list invalid options",
			args:           []string{"audit", "list", "--secret-option", "sk-secret-command"},
			wantCode:       2,
			wantDiagnostic: "usage_error",
		},
		{
			name:           "doctor invalid options",
			args:           []string{"doctor", "--secret-option", "sk-secret-command"},
			wantCode:       2,
			wantDiagnostic: "usage_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, defaultDependencies())
			if code != test.wantCode {
				t.Fatalf("exit code = %d, want %d", code, test.wantCode)
			}
			if test.wantHelp != "" {
				if !strings.Contains(stdout.String(), test.wantHelp) {
					t.Fatalf("stdout = %q, want help substring %q", stdout.String(), test.wantHelp)
				}
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty for help", stderr.String())
				}
			}
			if test.wantDiagnostic != "" {
				if stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want empty for diagnostic", stdout.String())
				}
				if got := decodeDiagnosticCode(t, stderr.Bytes()); got != test.wantDiagnostic {
					t.Fatalf("diagnostic code = %q, want %q", got, test.wantDiagnostic)
				}
			}
			if strings.Contains(stdout.String()+stderr.String(), "sk-secret-command") {
				t.Fatalf("diagnostics echoed unknown command: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestLoadRunUsesGoSchedulerAndNeverEmitsCredential(t *testing.T) {
	t.Parallel()
	const secret = "sk-load-cli-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4}}}`)
	}))
	defer server.Close()
	dependencies := defaultDependencies()
	dependencies.getenv = func(name string) string {
		switch name {
		case "LOADTEST_API_KEY":
			return secret
		case "LOADTEST_MODEL":
			return "test-model"
		default:
			return ""
		}
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"load", "run", "--url", server.URL + "/v1/chat/completions",
		"--requests", "2", "--concurrency", "1",
	}, strings.NewReader(""), &stdout, &stderr, dependencies)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) {
		t.Fatal("load output leaked the credential")
	}
	var response loadRunResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode load output: %v", err)
	}
	if response.SchemaVersion != 1 || response.Type != "load_run" || response.Payload.Metrics.Succeeded != 2 || len(response.Payload.Results) != 2 {
		t.Fatalf("load response = %#v", response)
	}
}

func TestLoadOutputAtomicallyReplacesExistingFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "reports", "load.json")
	if err := writeLoadOutput(filename, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeLoadOutput(filename, []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filename)
	if err != nil || string(contents) != "second\n" {
		t.Fatalf("output = %q, %v", contents, err)
	}
}

func TestLoadEndpointAndCasePathComposeWithoutDuplicatingVersionPrefix(t *testing.T) {
	baseURL, requestPath, err := splitLoadEndpoint("https://gateway.example/v1/chat/completions")
	if err != nil || baseURL != "https://gateway.example/v1" || requestPath != "/chat/completions" {
		t.Fatalf("split = %q %q %v", baseURL, requestPath, err)
	}
	if got := relativeLoadRequestPath(baseURL, "/v1/chat/completions"); got != "/chat/completions" {
		t.Fatalf("relative case path = %q", got)
	}
}

func TestEffectiveLoadRequestCountUsesDurationForOpenLoopByDefault(t *testing.T) {
	if got := effectiveLoadRequestCount(1, false, 2, time.Minute); got != 0 {
		t.Fatalf("implicit request count = %d, want duration-bounded open loop", got)
	}
	if got := effectiveLoadRequestCount(1, true, 2, time.Minute); got != 1 {
		t.Fatalf("explicit request count = %d, want 1", got)
	}
}

func TestLoadEndpointRejectsNonChatRoutes(t *testing.T) {
	for _, endpoint := range []string{
		"https://gateway.example/v1/embeddings",
		"https://gateway.example/v1/models",
	} {
		if _, _, err := splitLoadEndpoint(endpoint); err == nil {
			t.Fatalf("splitLoadEndpoint(%q) unexpectedly succeeded", endpoint)
		}
	}
}

func TestHelpWriteFailureReturnsOutputError(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "root", args: []string{"--help"}},
		{name: "audit", args: []string{"audit", "--help"}},
		{name: "doctor", args: []string{"doctor", "--help"}},
		{name: "audit list", args: []string{"audit", "list", "--help"}},
		{name: "audit run", args: []string{"audit", "run", "--help"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout := &failWriter{}
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, strings.NewReader(""), stdout, &stderr, defaultDependencies())
			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "output_error" {
				t.Fatalf("diagnostic code = %q, want output_error", got)
			}
		})
	}
}

func TestDiagnosticWriteFailureReturnsOneWithoutRecursiveWrite(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "root", args: nil},
		{name: "audit", args: []string{"audit"}},
		{name: "audit list", args: []string{"audit", "list", "--invalid"}},
		{name: "audit run", args: []string{"audit", "run", "--invalid"}},
		{name: "doctor", args: []string{"doctor", "--invalid"}},
		{name: "human diagnostic", args: []string{"audit", "run", "--format", "human", "--invalid"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			stderr := &failWriter{}
			code := run(context.Background(), test.args, strings.NewReader(""), &stdout, stderr, defaultDependencies())
			if code != 1 {
				t.Fatalf("exit code = %d, want stable 1", code)
			}
			if got := stderr.writes.Load(); got != 1 {
				t.Fatalf("stderr write attempts = %d, want exactly 1", got)
			}
		})
	}
}

func TestNilDiagnosticWriterReturnsOneWithoutPanic(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	code := run(context.Background(), nil, strings.NewReader(""), &stdout, nil, defaultDependencies())
	if code != 1 {
		t.Fatalf("exit code = %d, want stable 1", code)
	}
}

func TestNilHelpWriterReturnsOutputErrorWithoutPanic(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--help"}, strings.NewReader(""), nil, &stderr, defaultDependencies())
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "output_error" {
		t.Fatalf("diagnostic code = %q, want output_error", got)
	}
}

func decodeDiagnosticCode(t *testing.T, contents []byte) string {
	t.Helper()
	var diagnostic struct {
		SchemaVersion int    `json:"schema_version"`
		Type          string `json:"type"`
		Payload       struct {
			Code string `json:"code"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(contents, &diagnostic); err != nil {
		t.Fatalf("diagnostic is not JSON: %v; content = %q", err, contents)
	}
	if diagnostic.SchemaVersion != 1 || diagnostic.Type != "error" {
		t.Fatalf("diagnostic metadata = version %d type %q", diagnostic.SchemaVersion, diagnostic.Type)
	}
	return diagnostic.Payload.Code
}

func TestAuditListUsesVersionedResponseEnvelope(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {
          "metadata": "list-private-payload"
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	rawOutput := stdout.String()

	var response struct {
		SchemaVersion int    `json:"schema_version"`
		Type          string `json:"type"`
		Payload       struct {
			Command string `json:"command"`
			Count   int    `json:"count"`
			Cases   []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"cases"`
		} `json:"payload"`
	}
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode list response: %v; output = %q", err, stdout.String())
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("unexpected extra JSON value: %#v", extra)
	}
	if response.SchemaVersion != 1 || response.Type != "audit_case_list" || response.Payload.Command != "list" {
		t.Fatalf("list metadata = version %d type %q command %q", response.SchemaVersion, response.Type, response.Payload.Command)
	}
	if response.Payload.Count != 1 || len(response.Payload.Cases) != 1 || response.Payload.Cases[0].ID != "C001" || response.Payload.Cases[0].Name != "chat sync" {
		t.Fatalf("list payload = %#v", response.Payload)
	}
	if strings.Contains(rawOutput, "list-private-payload") || strings.Contains(rawOutput, "client_secret") {
		t.Fatalf("list response leaked request configuration: %q", rawOutput)
	}
}

func TestAuditListCanRenderHumanOutput(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot, "--format", "human"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"ID", "DEFAULT", "DIMENSION", "NAME", "C001", "chat sync"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestAuditListConfigurationFailureUsesStructuredDiagnostic(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"audit", "list", "--suite", "missing", "--cases-root", t.TempDir()},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no machine result", stdout.String())
	}
	var diagnostic struct {
		SchemaVersion int    `json:"schema_version"`
		Type          string `json:"type"`
		Payload       struct {
			Code string `json:"code"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
		t.Fatalf("stderr is not a JSON diagnostic: %v; stderr = %q", err, stderr.String())
	}
	if diagnostic.SchemaVersion != 1 || diagnostic.Type != "error" || diagnostic.Payload.Code != "config_error" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestAuditListCancellationAndDeadlineUseCanceledExit(t *testing.T) {
	t.Parallel()

	casesRoot := writeManualAuditCase(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "caller cancellation", ctx: canceled},
		{name: "deadline", ctx: expired},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(
				test.ctx,
				[]string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot},
				strings.NewReader(""),
				&stdout,
				&stderr,
				defaultDependencies(),
			)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr=%q", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "canceled" {
				t.Fatalf("diagnostic code = %q, want canceled", got)
			}
		})
	}
}

func TestAuditListRejectsUnusedJSONLFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"audit", "list", "--suite", "openai-chat", "--cases-root", writeManualAuditCase(t),
			"--jsonl",
		},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "usage_error" {
		t.Fatalf("diagnostic code = %q, want usage_error", got)
	}
}

func writeTestCase(t *testing.T, root, suite, id, contents string) {
	t.Helper()
	directory := filepath.Join(root, suite, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", directory, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "case.json"), []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(case.json): %v", err)
	}
}

func TestAuditRunDefaultsToCanonicalEventEnvelopes(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "manual review",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": []
    }
  }
}`)
	outputDir := filepath.Join(t.TempDir(), "report")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"audit", "run",
			"--suite", "openai-chat",
			"--cases-root", casesRoot,
			"--base-url", "https://gateway.example",
			"--model", "test-model",
			"--case", "C001",
			"--dry-run",
			"--poll-interval", "1s",
			"--timeout", "30s",
			"--concurrency", "1",
			"--output", outputDir,
		},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	decoder := json.NewDecoder(&stdout)
	wantTypes := []string{"plan", "progress", "final"}
	var runID string
	eventIDs := map[string]bool{}
	for index, wantType := range wantTypes {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode event %d: %v; output = %q", index, err, stdout.String())
		}
		if event["schema_version"] != float64(1) || event["type"] != wantType || event["sequence"] != float64(index+1) {
			t.Fatalf("event %d metadata = %#v", index, event)
		}
		eventID, _ := event["event_id"].(string)
		if eventID == "" || eventIDs[eventID] {
			t.Fatalf("event %d event_id = %q; seen = %#v", index, eventID, eventIDs)
		}
		eventIDs[eventID] = true
		gotRunID, _ := event["run_id"].(string)
		if index == 0 {
			runID = gotRunID
		}
		if gotRunID == "" || gotRunID != runID {
			t.Fatalf("event %d run_id = %q, want %q", index, gotRunID, runID)
		}
		occurredAt, _ := event["occurred_at"].(string)
		if _, err := time.Parse(time.RFC3339Nano, occurredAt); err != nil {
			t.Fatalf("event %d occurred_at = %q: %v", index, occurredAt, err)
		}
		payload, ok := event["payload"].(map[string]any)
		if !ok {
			t.Fatalf("event %d payload = %#v", index, event["payload"])
		}
		for _, legacyField := range []string{"total", "runs", "result", "report_dir", "verdict"} {
			if _, exists := event[legacyField]; exists {
				t.Fatalf("event %d contains legacy flattened field %q: %#v", index, legacyField, event)
			}
		}
		if wantType == "final" && payload["report_dir"] != outputDir {
			t.Fatalf("final payload = %#v", payload)
		}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra event decode = %v, value = %#v", err, extra)
	}
	for _, report := range []string{"report.json", "report.html"} {
		if _, err := os.Stat(filepath.Join(outputDir, report)); err != nil {
			t.Fatalf("%s was not written: %v", report, err)
		}
	}
}

func TestAuditRunJSONFormatIsOneVersionedEventCollection(t *testing.T) {
	t.Parallel()

	casesRoot := writeManualAuditCase(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
			"--base-url", "https://gateway.example", "--model", "test-model", "--dry-run",
			"--output", filepath.Join(t.TempDir(), "report"), "--format", "json",
		},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	var response struct {
		SchemaVersion int    `json:"schema_version"`
		Type          string `json:"type"`
		Payload       struct {
			Events []map[string]any `json:"events"`
		} `json:"payload"`
	}
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode JSON collection: %v; output = %q", err, stdout.String())
	}
	if response.SchemaVersion != 1 || response.Type != "audit_run_events" {
		t.Fatalf("collection metadata = version %d type %q", response.SchemaVersion, response.Type)
	}
	if len(response.Payload.Events) != 3 {
		t.Fatalf("event count = %d, want 3", len(response.Payload.Events))
	}
	for index, wantType := range []string{"plan", "progress", "final"} {
		if response.Payload.Events[index]["type"] != wantType || response.Payload.Events[index]["sequence"] != float64(index+1) {
			t.Fatalf("event %d = %#v", index, response.Payload.Events[index])
		}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON format emitted more than one value: value=%#v err=%v", extra, err)
	}
}

func TestAuditRunJSONLFormatAndCompatibilityAliasAreExplicit(t *testing.T) {
	t.Parallel()

	casesRoot := writeManualAuditCase(t)
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "format jsonl", args: []string{"--format", "jsonl"}},
		{name: "compatibility alias", args: []string{"--jsonl"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			args := []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model", "--dry-run",
				"--output", filepath.Join(t.TempDir(), "report"),
			}
			args = append(args, test.args...)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, defaultDependencies())
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			decoder := json.NewDecoder(&stdout)
			for index, wantType := range []string{"plan", "progress", "final"} {
				var event map[string]any
				if err := decoder.Decode(&event); err != nil {
					t.Fatalf("decode event %d: %v; output=%q", index, err, stdout.String())
				}
				if event["type"] != wantType {
					t.Fatalf("event %d type = %q, want %q", index, event["type"], wantType)
				}
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected extra JSONL value: %#v err=%v", extra, err)
			}
		})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
			"--base-url", "https://gateway.example", "--model", "test-model", "--dry-run",
			"--format", "json", "--jsonl",
		},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 2 {
		t.Fatalf("conflicting format exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("conflicting format stdout = %q, want empty", stdout.String())
	}
	if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "usage_error" {
		t.Fatalf("diagnostic code = %q, want usage_error", got)
	}
}

func writeManualAuditCase(t *testing.T) string {
	t.Helper()
	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "manual review",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": []
    }
  }
}`)
	return casesRoot
}

func TestAuditRunCanRenderHumanLifecycle(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "manual review",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": []
    }
  }
}`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
			"--base-url", "https://gateway.example", "--model", "test-model", "--dry-run",
			"--output", filepath.Join(t.TempDir(), "report"), "--format", "human",
		},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"PLAN 1 case run(s)", "DONE 1/1 C001", "REPORT ", "VERDICT "} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), `"schema_version"`) {
		t.Fatalf("human output unexpectedly contains JSON: %q", stdout.String())
	}
}

func TestAuditRunClassifiesConfigCredentialAndReportErrorsWithoutSecrets(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "manual review",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": []
    }
  }
}`)
	tests := []struct {
		name         string
		args         []string
		configure    func(*dependencies)
		wantCode     int
		wantError    string
		forbidOutput []string
	}{
		{
			name: "credential-bearing URL is a configuration error",
			args: []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://user:password@gateway.example?api_key=query-secret",
				"--model", "test-model", "--dry-run",
			},
			wantCode:     2,
			wantError:    "config_error",
			forbidOutput: []string{"user", "password", "query-secret", "api_key"},
		},
		{
			name: "missing configured credential",
			args: []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model",
				"--api-key-env", "TEST_GATEWAY_KEY",
			},
			wantCode:  2,
			wantError: "missing_credential",
		},
		{
			name: "report writer failure",
			args: []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model", "--dry-run",
				"--api-key-env", "TEST_GATEWAY_KEY", "--output", filepath.Join(t.TempDir(), "report"),
			},
			configure: func(dependencies *dependencies) {
				dependencies.getenv = func(name string) string {
					if name == "TEST_GATEWAY_KEY" {
						return "sk-report-secret"
					}
					return ""
				}
				dependencies.applicationDependencies.WriteReport = func(string, apiaudit.Report) error {
					return errors.New("write https://user:password@example.test?token=query-secret api_key=sk-report-secret client_secret=request-client-secret")
				}
			},
			wantCode:     1,
			wantError:    "report_error",
			forbidOutput: []string{"user", "password", "query-secret", "sk-report-secret", "request-client-secret", "api_key", "client_secret"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dependencies := defaultDependencies()
			if test.configure != nil {
				test.configure(&dependencies)
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, dependencies)
			if code != test.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, test.wantCode, stderr.String())
			}
			var diagnostic struct {
				SchemaVersion int    `json:"schema_version"`
				Type          string `json:"type"`
				Payload       struct {
					Code string `json:"code"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
				t.Fatalf("stderr is not a JSON diagnostic: %v; stderr = %q", err, stderr.String())
			}
			if diagnostic.SchemaVersion != 1 || diagnostic.Type != "error" || diagnostic.Payload.Code != test.wantError {
				t.Fatalf("diagnostic = %#v, want code %q", diagnostic, test.wantError)
			}
			combined := strings.ToLower(stdout.String() + stderr.String())
			for _, secret := range test.forbidOutput {
				if strings.Contains(combined, strings.ToLower(secret)) {
					t.Errorf("output contains forbidden value %q: stdout=%q stderr=%q", secret, stdout.String(), stderr.String())
				}
			}
		})
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (do httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return do(request)
}

func TestAuditRunPropagatesCallerCancellation(t *testing.T) {
	casesRoot := t.TempDir()
	writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {
          "messages": [
            {
              "role": "user",
              "content": "hello"
            }
          ]
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)
	requestStarted := make(chan struct{})
	dependencies := defaultDependencies()
	dependencies.getenv = func(name string) string {
		if name == "TEST_GATEWAY_KEY" {
			return "sk-live-secret"
		}
		return ""
	}
	dependencies.httpDoer = httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- run(
			ctx,
			[]string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model",
				"--api-key-env", "TEST_GATEWAY_KEY", "--timeout", "1m",
			},
			strings.NewReader(""),
			&stdout,
			&stderr,
			dependencies,
		)
	}()
	select {
	case <-requestStarted:
		cancel()
	case code := <-result:
		t.Fatalf("run returned before request started with code %d", code)
	case <-time.After(2 * time.Second):
		t.Fatal("live request did not start")
	}
	select {
	case code := <-result:
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop after caller cancellation")
	}
	var diagnostic struct {
		Payload struct {
			Code string `json:"code"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
		t.Fatalf("stderr is not a JSON diagnostic: %v; stderr = %q", err, stderr.String())
	}
	if diagnostic.Payload.Code != "canceled" {
		t.Fatalf("diagnostic code = %q, want canceled", diagnostic.Payload.Code)
	}
	decoder := json.NewDecoder(&stdout)
	var plan map[string]any
	if err := decoder.Decode(&plan); err != nil || plan["type"] != "plan" {
		t.Fatalf("stdout plan = %#v, err = %v; stdout = %q", plan, err, stdout.String())
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected post-cancellation event %#v, err = %v", extra, err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "sk-live-secret") {
		t.Fatalf("output contains live API key: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type failWriter struct {
	writes atomic.Int32
}

func (writer *failWriter) Write([]byte) (int, error) {
	writer.writes.Add(1)
	return 0, errors.New("injected output failure")
}

type shortThenStallWriter struct {
	writes atomic.Int32
}

func (writer *shortThenStallWriter) Write(contents []byte) (int, error) {
	if writer.writes.Add(1) == 1 && len(contents) > 0 {
		return 1, nil
	}
	return 0, nil
}

func TestAuditRunHumanPlanNilOrTruncatedWriterStopsBeforeHTTPAndReport(t *testing.T) {
	for _, test := range []struct {
		name   string
		stdout func() io.Writer
	}{
		{name: "short write then stall", stdout: func() io.Writer { return &shortThenStallWriter{} }},
		{name: "nil writer", stdout: func() io.Writer { return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			casesRoot := t.TempDir()
			writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {
          "messages": [
            {
              "role": "user",
              "content": "hello"
            }
          ]
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)
			var httpCalls atomic.Int32
			var reportCalls atomic.Int32
			dependencies := defaultDependencies()
			dependencies.getenv = func(string) string { return "sk-paid-secret" }
			dependencies.httpDoer = httpDoerFunc(func(*http.Request) (*http.Response, error) {
				httpCalls.Add(1)
				return nil, errors.New("must not be called after output failure")
			})
			dependencies.applicationDependencies.WriteReport = func(string, apiaudit.Report) error {
				reportCalls.Add(1)
				return nil
			}
			args := []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model",
				"--format", "human", "--output", filepath.Join(t.TempDir(), "report"),
			}
			var stderr bytes.Buffer
			code := run(context.Background(), args, strings.NewReader(""), test.stdout(), &stderr, dependencies)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
			}
			if got := httpCalls.Load(); got != 0 {
				t.Fatalf("HTTP calls = %d, want 0", got)
			}
			if got := reportCalls.Load(); got != 0 {
				t.Fatalf("report calls = %d, want 0", got)
			}
			if !strings.Contains(stderr.String(), "OUTPUT ERROR") {
				t.Fatalf("human diagnostic = %q, want OUTPUT ERROR", stderr.String())
			}
		})
	}
}

func TestHumanListAndDoctorNilOrTruncatedWritersReturnOutputError(t *testing.T) {
	casesRoot := writeManualAuditCase(t)
	commands := []struct {
		name string
		args []string
	}{
		{
			name: "audit list",
			args: []string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot, "--format", "human"},
		},
		{
			name: "doctor",
			args: []string{"doctor", "--cases-root", casesRoot, "--format", "human"},
		},
	}
	writers := []struct {
		name   string
		stdout func() io.Writer
	}{
		{name: "short write then stall", stdout: func() io.Writer { return &shortThenStallWriter{} }},
		{name: "nil writer", stdout: func() io.Writer { return nil }},
	}
	for _, command := range commands {
		for _, writer := range writers {
			t.Run(command.name+"/"+writer.name, func(t *testing.T) {
				var stderr bytes.Buffer
				code := run(context.Background(), command.args, strings.NewReader(""), writer.stdout(), &stderr, defaultDependencies())
				if code != 1 {
					t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
				}
				if !strings.Contains(stderr.String(), "OUTPUT ERROR") {
					t.Fatalf("human diagnostic = %q, want OUTPUT ERROR", stderr.String())
				}
			})
		}
	}
}

func TestAuditRunFirstPlanWriteFailureCancelsBeforeHTTP(t *testing.T) {
	for _, test := range []struct {
		name       string
		formatArgs []string
		wantJSON   bool
	}{
		{name: "default jsonl", wantJSON: true},
		{name: "single document json", formatArgs: []string{"--format", "json"}, wantJSON: true},
		{name: "human", formatArgs: []string{"--format", "human"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			casesRoot := t.TempDir()
			writeTestCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 2,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "id": "3a1000c6-a20a-5f31-b8b3-c9a434a76657",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {
        "body": {
          "messages": [
            {
              "role": "user",
              "content": "hello"
            }
          ]
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)
			var httpCalls atomic.Int32
			dependencies := defaultDependencies()
			dependencies.getenv = func(string) string { return "sk-paid-secret" }
			dependencies.httpDoer = httpDoerFunc(func(*http.Request) (*http.Response, error) {
				httpCalls.Add(1)
				return nil, errors.New("must not be called after output failure")
			})
			outputDir := filepath.Join(t.TempDir(), "report")
			args := []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model",
				"--output", outputDir,
			}
			args = append(args, test.formatArgs...)
			stdout := &failWriter{}
			var stderr bytes.Buffer
			code := run(context.Background(), args, strings.NewReader(""), stdout, &stderr, dependencies)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
			}
			if stdout.writes.Load() == 0 {
				t.Fatal("stdout was never attempted")
			}
			if got := httpCalls.Load(); got != 0 {
				t.Fatalf("HTTP calls = %d, want 0", got)
			}
			if _, err := os.Stat(filepath.Join(outputDir, "report.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("report was written after output failure: %v", err)
			}
			if test.wantJSON {
				if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "output_error" {
					t.Fatalf("diagnostic code = %q, want output_error", got)
				}
			} else if !strings.Contains(stderr.String(), "OUTPUT ERROR") {
				t.Fatalf("human diagnostic = %q, want OUTPUT ERROR", stderr.String())
			}
		})
	}
}

func TestListAndDoctorReturnOutputErrorForJSONAndHumanWriters(t *testing.T) {
	t.Parallel()

	casesRoot := writeManualAuditCase(t)
	for _, test := range []struct {
		name     string
		args     []string
		wantJSON bool
	}{
		{
			name:     "list json",
			args:     []string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot},
			wantJSON: true,
		},
		{
			name: "list human",
			args: []string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot, "--format", "human"},
		},
		{
			name:     "doctor json",
			args:     []string{"doctor", "--cases-root", casesRoot},
			wantJSON: true,
		},
		{
			name: "doctor human",
			args: []string{"doctor", "--cases-root", casesRoot, "--format", "human"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout := &failWriter{}
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, strings.NewReader(""), stdout, &stderr, defaultDependencies())
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
			}
			if stdout.writes.Load() == 0 {
				t.Fatal("stdout was never attempted")
			}
			if test.wantJSON {
				if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "output_error" {
					t.Fatalf("diagnostic code = %q, want output_error", got)
				}
			} else if !strings.Contains(stderr.String(), "OUTPUT ERROR") {
				t.Fatalf("human diagnostic = %q, want OUTPUT ERROR", stderr.String())
			}
		})
	}
}

func TestNilMachineOutputWritersReturnOutputErrorWithoutPanic(t *testing.T) {
	t.Parallel()

	casesRoot := writeManualAuditCase(t)
	for _, test := range []struct {
		name string
		args []string
	}{
		{
			name: "audit list",
			args: []string{"audit", "list", "--suite", "openai-chat", "--cases-root", casesRoot},
		},
		{
			name: "audit run jsonl",
			args: []string{
				"audit", "run", "--suite", "openai-chat", "--cases-root", casesRoot,
				"--base-url", "https://gateway.example", "--model", "test-model", "--dry-run",
				"--output", filepath.Join(t.TempDir(), "report"),
			},
		},
		{
			name: "doctor",
			args: []string{"doctor", "--cases-root", casesRoot},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, strings.NewReader(""), nil, &stderr, defaultDependencies())
			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if got := decodeDiagnosticCode(t, stderr.Bytes()); got != "output_error" {
				t.Fatalf("diagnostic code = %q, want output_error", got)
			}
		})
	}
}

func TestDoctorReportsRequiredAndNotConfiguredChecksAsVersionedJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"doctor", "--cases-root", writeManualAuditCase(t)},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	var response struct {
		SchemaVersion int    `json:"schema_version"`
		Type          string `json:"type"`
		Payload       struct {
			Status string `json:"status"`
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"payload"`
	}
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode doctor response: %v; output = %q", err, stdout.String())
	}
	if decoder.More() {
		t.Fatal("doctor emitted more than one JSON value")
	}
	if response.SchemaVersion != 1 || response.Type != "doctor" || response.Payload.Status != "ready" {
		t.Fatalf("doctor metadata = version %d type %q status %q", response.SchemaVersion, response.Type, response.Payload.Status)
	}
	statuses := make(map[string]string, len(response.Payload.Checks))
	for _, check := range response.Payload.Checks {
		statuses[check.Name] = check.Status
	}
	want := map[string]string{
		"binary":           "pass",
		"go_core":          "pass",
		"cases":            "pass",
		"database":         "not_configured",
		"credential_store": "not_configured",
	}
	for name, wantStatus := range want {
		if statuses[name] != wantStatus {
			t.Errorf("check %q status = %q, want %q; all = %#v", name, statuses[name], wantStatus, statuses)
		}
	}
}

type doctorFileSystemFunc func(context.Context, string) (bool, error)

func (filesystem doctorFileSystemFunc) DirectoryHasEntries(ctx context.Context, path string) (bool, error) {
	return filesystem(ctx, path)
}

type applicationStub struct {
	list func(context.Context, compatibility.ListRequest) ([]compatibility.CaseDefinition, error)
	run  func(context.Context, compatibility.RunRequest) (compatibility.FinalEvent, error)
}

func TestLoadRunDiagnosticDetailIncludesRedactedConfigurationCause(t *testing.T) {
	t.Parallel()

	const secret = "sk-load-detail-secret"
	requestFile := filepath.Join(t.TempDir(), "api_key="+secret)
	dependencies := defaultDependencies()
	dependencies.getenv = func(name string) string {
		switch name {
		case "LOADTEST_API_KEY":
			return "sk-runtime-secret"
		case "LOADTEST_MODEL":
			return "test-model"
		default:
			return ""
		}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{
		"load", "run", "--url", "https://gateway.example/v1/chat/completions",
		"--request-file", requestFile, "--diagnostic-detail",
	}, strings.NewReader(""), &stdout, &stderr, dependencies)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), secret) || !strings.Contains(stderr.String(), "[REDACTED]") || !strings.Contains(stderr.String(), "cannot find") {
		t.Fatalf("diagnostic detail was not useful and redacted: %q", stderr.String())
	}
}

func (application applicationStub) List(ctx context.Context, request compatibility.ListRequest) ([]compatibility.CaseDefinition, error) {
	return application.list(ctx, request)
}

func (application applicationStub) Run(ctx context.Context, request compatibility.RunRequest) (compatibility.FinalEvent, error) {
	if application.run != nil {
		return application.run(ctx, request)
	}
	return compatibility.FinalEvent{}, errors.New("Run must not be called by doctor")
}

func TestAuditRunDiagnosticDetailIsOptInAndRedacted(t *testing.T) {
	t.Parallel()

	const secret = "sk-diagnostic-secret"
	cause := errors.New("dial gateway: timeout; Authorization: Bearer " + secret)
	for _, test := range []struct {
		name       string
		args       []string
		wantDetail bool
	}{
		{
			name: "default output remains stable",
			args: []string{"audit", "run", "--suite", "openai-chat", "--base-url", "https://gateway.example", "--model", "test-model", "--dry-run"},
		},
		{
			name:       "explicit diagnostic detail includes redacted cause",
			args:       []string{"audit", "run", "--suite", "openai-chat", "--base-url", "https://gateway.example", "--model", "test-model", "--dry-run", "--diagnostic-detail"},
			wantDetail: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dependencies := defaultDependencies()
			dependencies.newApplication = func(compatibility.Dependencies) compatibilityApplication {
				return applicationStub{run: func(context.Context, compatibility.RunRequest) (compatibility.FinalEvent, error) {
					return compatibility.FinalEvent{}, cause
				}}
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, dependencies); code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
			}
			var diagnostic struct {
				Payload struct {
					Code   string `json:"code"`
					Detail string `json:"detail"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
				t.Fatalf("stderr is not a JSON diagnostic: %v; stderr = %q", err, stderr.String())
			}
			if diagnostic.Payload.Code != "run_error" {
				t.Fatalf("diagnostic code = %q, want run_error", diagnostic.Payload.Code)
			}
			if strings.Contains(stderr.String(), secret) {
				t.Fatalf("diagnostic leaked secret: %q", stderr.String())
			}
			if test.wantDetail {
				if !strings.Contains(diagnostic.Payload.Detail, "timeout") || !strings.Contains(diagnostic.Payload.Detail, "[REDACTED]") {
					t.Fatalf("diagnostic detail = %q, want useful redacted cause", diagnostic.Payload.Detail)
				}
			} else if diagnostic.Payload.Detail != "" {
				t.Fatalf("diagnostic detail = %q, want omitted by default", diagnostic.Payload.Detail)
			}
		})
	}
}

func TestAuditListDiagnosticDetailIncludesRedactedCause(t *testing.T) {
	t.Parallel()

	const secret = "sk-list-diagnostic-secret"
	dependencies := defaultDependencies()
	dependencies.newApplication = func(compatibility.Dependencies) compatibilityApplication {
		return applicationStub{list: func(context.Context, compatibility.ListRequest) ([]compatibility.CaseDefinition, error) {
			return nil, errors.New("read case catalog: api_key=" + secret)
		}}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"audit", "list", "--suite", "openai-chat", "--diagnostic-detail"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		dependencies,
	)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), secret) || !strings.Contains(stderr.String(), "[REDACTED]") || !strings.Contains(stderr.String(), "read case catalog") {
		t.Fatalf("diagnostic detail was not useful and redacted: %q", stderr.String())
	}
}

func TestHumanDiagnosticDetailUsesSeparateLineAndRedactsSecrets(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := writeDiagnosticDetail(&output, "human", "run_error", "audit run failed", "Authorization: Bearer sk-human-secret"); err != nil {
		t.Fatalf("writeDiagnosticDetail() error = %v", err)
	}
	if got := output.String(); got != "RUN ERROR: audit run failed\nDETAIL: Authorization: [REDACTED]\n" {
		t.Fatalf("human diagnostic = %q", got)
	}
}

func TestDoctorCLIAdaptsReusableInspectionService(t *testing.T) {
	t.Parallel()

	var filesystemCalls atomic.Int32
	var catalogCalls atomic.Int32
	dependencies := defaultDependencies()
	dependencies.doctorFileSystem = doctorFileSystemFunc(func(_ context.Context, path string) (bool, error) {
		filesystemCalls.Add(1)
		if path != "virtual-cases" {
			t.Fatalf("filesystem path = %q", path)
		}
		return true, nil
	})
	dependencies.newApplication = func(compatibility.Dependencies) compatibilityApplication {
		return applicationStub{list: func(_ context.Context, request compatibility.ListRequest) ([]compatibility.CaseDefinition, error) {
			catalogCalls.Add(1)
			if request.CasesRoot != "virtual-cases" || request.Suite != "openai-chat" {
				t.Fatalf("catalog request = %#v", request)
			}
			return []compatibility.CaseDefinition{{ID: "C001"}}, nil
		}}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"doctor", "--cases-root", "virtual-cases"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		dependencies,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q output=%q", code, stderr.String(), stdout.String())
	}
	if filesystemCalls.Load() != 1 || catalogCalls.Load() != 1 {
		t.Fatalf("adapter calls = filesystem %d catalog %d", filesystemCalls.Load(), catalogCalls.Load())
	}
	if got := doctorCheckStatus(t, stdout.Bytes(), "cases"); got != "pass" {
		t.Fatalf("cases status = %q, want pass", got)
	}
}

func TestDoctorRejectsEmptyOrNonSuiteCaseRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(*testing.T) string
		wantCheck string
	}{
		{
			name: "empty root",
			prepare: func(t *testing.T) string {
				return t.TempDir()
			},
			wantCheck: "fail",
		},
		{
			name: "nonempty root without a loadable suite",
			prepare: func(t *testing.T) string {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "README.txt"), []byte("not a case suite"), 0o644); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantCheck: "fail",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(
				context.Background(),
				[]string{"doctor", "--cases-root", test.prepare(t)},
				strings.NewReader(""),
				&stdout,
				&stderr,
				defaultDependencies(),
			)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; output=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if got := doctorCheckStatus(t, stdout.Bytes(), "cases"); got != test.wantCheck {
				t.Fatalf("cases status = %q, want %q", got, test.wantCheck)
			}
		})
	}
}

func TestDoctorNilContextAndDependenciesFailWithoutPanic(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		nil,
		[]string{"doctor", "--cases-root", "unused"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		dependencies{},
	)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := doctorCheckStatus(t, stdout.Bytes(), "go_core"); got != "fail" {
		t.Fatalf("go_core status = %q, want fail", got)
	}
	if got := doctorCheckStatus(t, stdout.Bytes(), "cases"); got != "fail" {
		t.Fatalf("cases status = %q, want fail", got)
	}
}

func doctorCheckStatus(t *testing.T, contents []byte, name string) string {
	t.Helper()
	var response struct {
		Payload struct {
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(contents, &response); err != nil {
		t.Fatalf("doctor output is not JSON: %v; content=%q", err, contents)
	}
	for _, check := range response.Payload.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	t.Fatalf("doctor check %q was not present", name)
	return ""
}

func TestDoctorUnreadableCasesPathFailsWithoutPretendingIntegrationsPassed(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"doctor", "--cases-root", missing},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no duplicate diagnostic", stderr.String())
	}

	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("doctor output is not JSON: %v; output = %q", err, stdout.String())
	}
	payload, ok := response["payload"].(map[string]any)
	if !ok || payload["status"] != "failed" {
		t.Fatalf("payload = %#v, want failed", response["payload"])
	}
	checks, ok := payload["checks"].([]any)
	if !ok {
		t.Fatalf("checks = %#v", payload["checks"])
	}
	statuses := map[string]string{}
	for _, item := range checks {
		check := item.(map[string]any)
		statuses[check["name"].(string)] = check["status"].(string)
	}
	if statuses["cases"] != "fail" || statuses["database"] != "not_configured" || statuses["credential_store"] != "not_configured" {
		t.Fatalf("check statuses = %#v", statuses)
	}
}

func TestDoctorCanRenderHumanOutput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"doctor", "--cases-root", writeManualAuditCase(t), "--format", "human"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		defaultDependencies(),
	)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"Go core", "PASS", "Database", "NOT CONFIGURED", "Credential store"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("human output = %q, want substring %q", stdout.String(), want)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("human output unexpectedly JSON: %q", stdout.String())
	}
}
