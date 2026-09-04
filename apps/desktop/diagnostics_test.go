package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
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

	path := filepath.Join(root, "llm-test-studio", "logs", "llm-test-studio.log")
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

func TestDesktopDiagnosticsFallsBackToProcessLogWhenPrimaryLogIsOwned(t *testing.T) {
	root := t.TempDir()
	options := productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	}
	primary, err := openDesktopDiagnostics(options)
	if err != nil {
		t.Fatalf("open primary diagnostics: %v", err)
	}
	defer primary.Close()

	fallback, primaryErr := openDesktopDiagnosticsWithFallback(options, 4242)
	if fallback == nil {
		t.Fatalf("fallback diagnostics = nil; error = %v", primaryErr)
	}
	if primaryErr == nil {
		t.Fatal("primary diagnostics error = nil, want ownership failure")
	}
	path := fallback.Path()
	if err := fallback.Close(); err != nil {
		t.Fatalf("close fallback diagnostics: %v", err)
	}
	if filepath.Base(path) != "llm-test-studio-fallback-4242.log" {
		t.Fatalf("fallback path = %q", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fallback diagnostics: %v", err)
	}
	for _, want := range []string{"primary desktop diagnostics unavailable", "diagnostics_startup", "diagnostics_fallback"} {
		if !bytes.Contains(contents, []byte(want)) {
			t.Fatalf("fallback log missing %q: %s", want, contents)
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

	path := filepath.Join(root, "llm-test-studio", "logs", "llm-test-studio.log")
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

	contents, err := os.ReadFile(filepath.Join(root, "llm-test-studio", "logs", "llm-test-studio.log"))
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

	contents, err := os.ReadFile(filepath.Join(root, "llm-test-studio", "logs", "llm-test-studio.log"))
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

func TestDesktopErrorReporterClassifiesPlanProtocolMismatch(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	desktopErrorReporter(operator, log.New(io.Discard, "", 0))(
		fmt.Errorf("create plan: %w", catalog.ErrPlanProtocolMismatch),
	)
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(root, "llm-test-studio", "logs", "llm-test-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if entry["msg"] != "plan save failed" || entry["operation"] != "save_plan" || entry["error_code"] != desktopCodePlanProtocolMismatch {
		t.Fatalf("plan protocol diagnostic = %#v, want dedicated message, operation, and code", entry)
	}
	if entry["error"] != "create plan: catalog: invalid input: plan target protocol mismatch" {
		t.Fatalf("plan protocol diagnostic error = %q, want safe actionable cause", entry["error"])
	}
}

func TestDesktopFrontendDiagnosticPersistsClearRedactedReason(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	app := newDesktopApp(nil)
	configureDesktopDiagnostics(app, operator, nil)

	err = app.ReportFrontendDiagnostic(FrontendDiagnostic{
		Operation: "load_catalog",
		ErrorCode: "frontend_data_invalid",
		Detail:    `桌面目录测试用例数据无效 api_key="sk-private-frontend-key"`,
	})
	if err != nil {
		t.Fatalf("ReportFrontendDiagnostic() error = %v", err)
	}
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries, contents := readDesktopDiagnosticEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("diagnostic entries = %d, want 1; log = %s", len(entries), contents)
	}
	for key, want := range map[string]any{
		"level":      "ERROR",
		"msg":        "frontend desktop operation failed",
		"component":  "frontend",
		"operation":  "load_catalog",
		"error_code": "frontend_data_invalid",
	} {
		if got := entries[0][key]; got != want {
			t.Errorf("frontend diagnostic[%q] = %#v, want %#v", key, got, want)
		}
	}
	loggedError, _ := entries[0]["error"].(string)
	if !strings.Contains(loggedError, "桌面目录测试用例数据无效") || !strings.Contains(loggedError, "[REDACTED]") {
		t.Errorf("frontend diagnostic error = %q, want clear reason and redaction marker", loggedError)
	}
	if strings.Contains(contents, "sk-private-frontend-key") {
		t.Fatalf("frontend diagnostic leaked a credential: %s", contents)
	}
}

func TestWailsRuntimeFallbackPreservesFrontendDiagnosticFields(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	report := desktopErrorReporter(operator, log.New(io.Discard, "", 0))
	configured := desktopOptions(newDesktopApp(nil), nil, report)
	configured.Logger.Error(`{"component":"frontend","operation":"load_workspace","error_code":"frontend_operation_failed","detail":"Wails desktop binding did not become ready before startup timeout"}`)
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries, contents := readDesktopDiagnosticEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("diagnostic entries = %d, want 1; log = %s", len(entries), contents)
	}
	for key, want := range map[string]any{
		"msg":        "frontend desktop operation failed",
		"component":  "frontend",
		"operation":  "load_workspace",
		"error_code": "frontend_operation_failed",
		"error":      "Wails desktop binding did not become ready before startup timeout",
	} {
		if got := entries[0][key]; got != want {
			t.Errorf("runtime fallback diagnostic[%q] = %#v, want %#v", key, got, want)
		}
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

func TestQuickTestFailureProducesSafeStructuredDiagnostic(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	runner := &recordingQuickTestRunner{result: quicktest.Result{
		SchemaVersion: quicktest.SchemaVersion,
		Success:       false,
		ErrorCode:     quicktest.ErrorAuthenticationFailed,
		E2EMS:         125,
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{quickTests: runner}, nil
	})
	app.setErrorReporter(desktopErrorReporter(operator, log.New(io.Discard, "", 0)))
	app.onStartup(context.Background())

	_, err = app.RunQuickTest(quicktest.Command{
		URL: "https://private-provider.example/v1", APIKey: "sk-private-quick-key", ModelID: "private-model",
	})
	if err != nil {
		t.Fatalf("RunQuickTest() error = %v", err)
	}
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries, contents := readDesktopDiagnosticEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("diagnostic entries = %d, want 1; log = %s", len(entries), contents)
	}
	entry := entries[0]
	for key, want := range map[string]any{
		"level":       "WARN",
		"component":   "quick_test",
		"operation":   "connection_test",
		"error_code":  "authentication_failed",
		"duration_ms": float64(125),
	} {
		if got := entry[key]; got != want {
			t.Errorf("entry[%q] = %#v, want %#v", key, got, want)
		}
	}
	for _, secret := range []string{"private-provider.example", "sk-private-quick-key", "private-model"} {
		if strings.Contains(contents, secret) {
			t.Errorf("quick-test diagnostic leaked %q: %s", secret, contents)
		}
	}
}

func TestQuickPerformanceFailuresAndArchiveFailureProduceCorrelatedDiagnostics(t *testing.T) {
	root := t.TempDir()
	operator, err := openDesktopDiagnostics(productionOptions{
		userConfigDir: func() (string, error) { return root, nil },
		appVersion:    "test-version",
	})
	if err != nil {
		t.Fatalf("openDesktopDiagnostics() error = %v", err)
	}
	const reportID = "77777777-7777-4777-8777-777777777777"
	runner := &recordingQuickTestRunner{performanceReport: quicktest.PerformanceReport{
		SchemaVersion: quicktest.PerformanceSchemaVersion,
		ReportID:      reportID,
		ArchiveStatus: quicktest.PerformanceArchiveFailed,
		Success:       false,
		Progress: quicktest.PerformanceProgress{
			Phase: "completed", Completed: 2, Failed: 2, TotalDurationMS: 1250,
		},
		Failures: []quicktest.PerformanceFailure{{
			ErrorCode: quicktest.ErrorAuthenticationFailed,
			Count:     2,
		}},
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{quickTests: runner}, nil
	})
	app.setErrorReporter(desktopErrorReporter(operator, log.New(io.Discard, "", 0)))
	app.onStartup(context.Background())

	_, err = app.RunQuickPerformanceTest(quicktest.PerformanceCommand{
		URL: "https://private-performance.example/v1", APIKey: "sk-private-performance-key", ModelID: "private-performance-model",
	}, "")
	if err != nil {
		t.Fatalf("RunQuickPerformanceTest() error = %v", err)
	}
	if err := operator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries, contents := readDesktopDiagnosticEntries(t, root)
	if len(entries) != 2 {
		t.Fatalf("diagnostic entries = %d, want 2; log = %s", len(entries), contents)
	}
	for key, want := range map[string]any{
		"level":         "WARN",
		"component":     "quick_test",
		"operation":     "performance_test",
		"error_code":    "authentication_failed",
		"report_id":     reportID,
		"duration_ms":   float64(1250),
		"failure_count": float64(2),
	} {
		if got := entries[0][key]; got != want {
			t.Errorf("performance entry[%q] = %#v, want %#v", key, got, want)
		}
	}
	for key, want := range map[string]any{
		"level":      "WARN",
		"component":  "quick_test",
		"operation":  "performance_archive",
		"error_code": "quick_performance_archive_failed",
		"report_id":  reportID,
	} {
		if got := entries[1][key]; got != want {
			t.Errorf("archive entry[%q] = %#v, want %#v", key, got, want)
		}
	}
	for _, secret := range []string{"private-performance.example", "sk-private-performance-key", "private-performance-model"} {
		if strings.Contains(contents, secret) {
			t.Errorf("quick-performance diagnostic leaked %q: %s", secret, contents)
		}
	}
}

func readDesktopDiagnosticEntries(t *testing.T, root string) ([]map[string]any, string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "llm-test-studio", "logs", "llm-test-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := strings.TrimSpace(string(contents))
	if text == "" {
		return nil, text
	}
	lines := strings.Split(text, "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("Unmarshal() error = %v; line = %s", err, line)
		}
		entries = append(entries, entry)
	}
	return entries, text
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
	want := filepath.Join(root, "llm-test-studio", "logs")
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
