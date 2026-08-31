package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-studio/internal/application/runs"
)

func TestDesktopErrorReporterPersistsStructuredDiagnosticsUnderUserConfig(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	report := desktopErrorReporter(operator, log.New(io.Discard, "", 0))
	report(errors.New("database unavailable"))
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	path := filepath.Join(root, "llm-studio", "logs", "llm-studio.log")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v; log = %s", err, contents)
	}
	for key, expected := range map[string]any{
		"component":  "desktop",
		"operation":  "wails_boundary",
		"error_code": "operation_failed",
		"error":      "database unavailable",
	} {
		if got := entry[key]; got != expected {
			t.Errorf("entry[%q] = %#v, want %#v", key, got, expected)
		}
	}
}

func TestDesktopRunDiagnosticReporterPersistsRunCorrelation(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	report := desktopRunDiagnosticReporter(operator, log.New(io.Discard, "", 0))
	report(runs.Diagnostic{
		RunID: "run-1", RequestID: "request-17", Operation: "execute", ErrorCode: "run_execution_failed",
		Err: errors.New("executor unavailable"),
	})
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	path := filepath.Join(root, "llm-studio", "logs", "llm-studio.log")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v; log = %s", err, contents)
	}
	for key, expected := range map[string]any{
		"component":  "runs",
		"operation":  "execute",
		"error_code": "run_execution_failed",
		"run_id":     "run-1",
		"request_id": "request-17",
		"error":      "executor unavailable",
	} {
		if got := entry[key]; got != expected {
			t.Errorf("entry[%q] = %#v, want %#v", key, got, expected)
		}
	}
}

func TestDesktopRunDiagnosticReporterLabelsDroppedSummary(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	desktopRunDiagnosticReporter(operator, log.New(io.Discard, "", 0))(runs.Diagnostic{
		Operation: "diagnostic_dispatch", ErrorCode: "diagnostics_dropped", DroppedCount: 7,
	})
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(root, "llm-studio", "logs", "llm-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if entry["msg"] != "run diagnostics were dropped" || entry["level"] != "WARN" || entry["dropped_count"] != float64(7) {
		t.Fatalf("dropped diagnostic = %#v", entry)
	}
}

func TestDesktopErrorReporterClassifiesStartupFailure(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	desktopErrorReporter(operator, log.New(io.Discard, "", 0))(
		fmt.Errorf("%w: database unavailable", ErrDesktopStartup),
	)
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(root, "llm-studio", "logs", "llm-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if entry["operation"] != "startup" || entry["error_code"] != desktopCodeStartupFailed {
		t.Fatalf("startup diagnostic = %#v, want dedicated operation and code", entry)
	}
}

func TestDesktopDiagnosticFallbacksRedactSecrets(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	fallback := log.New(&output, "", 0)
	desktopErrorReporter(nil, fallback)(errors.New(`startup password="desktop secret"`))
	desktopRunDiagnosticReporter(nil, fallback)(runs.Diagnostic{
		RunID: "run-1", Operation: "execute", ErrorCode: "run_execution_failed",
		Err: errors.New("Authorization: Bearer run-secret"),
	})

	got := output.String()
	for _, secret := range []string{"desktop secret", "run-secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("fallback output leaked %q: %s", secret, got)
		}
	}
	if strings.Count(got, "[REDACTED]") < 2 {
		t.Errorf("fallback output = %q, want redaction markers for both errors", got)
	}
}

func TestConfigureDesktopDiagnosticsKeepsPathInsideGoBoundary(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	defer operator.Close()
	app := newDesktopApp(nil)
	var opened string
	configureDesktopDiagnostics(app, operator, func(path string) error {
		opened = path
		return nil
	})

	status := app.GetDiagnostics()
	if status.SchemaVersion != 1 || !status.Available || status.Format != "jsonl" ||
		status.MaxFileBytes <= 0 || status.BackupFiles <= 0 ||
		!status.RunCorrelation || !status.RequestCorrelation {
		t.Fatalf("GetDiagnostics() = %#v", status)
	}
	if err := app.OpenDiagnosticsDirectory(); err != nil {
		t.Fatalf("OpenDiagnosticsDirectory() error = %v", err)
	}
	want := filepath.Join(root, "llm-studio", "logs")
	if opened != want {
		t.Fatalf("opened directory = %q, want %q", opened, want)
	}
}

func TestConfigureDesktopDiagnosticsMarksUnavailableLogger(t *testing.T) {
	app := newDesktopApp(nil)
	configureDesktopDiagnostics(app, nil, nil)

	if status := app.GetDiagnostics(); status.Available {
		t.Fatalf("GetDiagnostics() = %#v, want unavailable", status)
	}
	assertBindingErrorCode(t, app.OpenDiagnosticsDirectory(), "diagnostics_unavailable")
}
