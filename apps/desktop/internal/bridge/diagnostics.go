package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	native "github.com/894x/llm-test-studio/apps/desktop/internal/native"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/diagnostics"
)

// DesktopDiagnosticsSnapshot is an allow-listed operator view. The filesystem
// path stays inside the Go desktop adapter; React can only request that the
// application open the owned directory.
type DesktopDiagnosticsSnapshot struct {
	SchemaVersion      int    `json:"schema_version"`
	Available          bool   `json:"available"`
	Format             string `json:"format"`
	MaxFileBytes       int64  `json:"max_file_bytes"`
	BackupFiles        int    `json:"backup_files"`
	RunCorrelation     bool   `json:"run_correlation"`
	RequestCorrelation bool   `json:"request_correlation"`
}

// FrontendDiagnostic is the bounded, secret-redacted diagnostic payload that
// React may send back to the desktop adapter when a successful binding response
// cannot be consumed locally.
type FrontendDiagnostic struct {
	Operation string `json:"operation"`
	ErrorCode string `json:"error_code"`
	Detail    string `json:"detail"`
}

func (app *DesktopApp) setDiagnostics(snapshot DesktopDiagnosticsSnapshot, open func() error) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.diagnostics = snapshot
	app.openDiagnostics = open
	app.mu.Unlock()
}

func (app *DesktopApp) setFrontendDiagnosticReporter(report func(FrontendDiagnostic) error) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.reportFrontendDiagnostic = report
	app.mu.Unlock()
}

func (app *DesktopApp) GetDiagnostics() DesktopDiagnosticsSnapshot {
	if app == nil {
		return DesktopDiagnosticsSnapshot{}
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.diagnostics
}

func (app *DesktopApp) OpenDiagnosticsDirectory() error {
	if app == nil {
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	}
	app.mu.Lock()
	available := app.diagnostics.Available
	open := app.openDiagnostics
	app.mu.Unlock()
	if !available || open == nil {
		return app.safeBindingError(ErrDiagnosticsUnavailable)
	}
	if err := open(); err != nil {
		return app.safeBindingError(fmt.Errorf("open diagnostics directory: %w", err))
	}
	return nil
}

func (app *DesktopApp) ReportFrontendDiagnostic(diagnostic FrontendDiagnostic) error {
	if app == nil {
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	}
	diagnostic.Operation = strings.TrimSpace(diagnostic.Operation)
	diagnostic.ErrorCode = strings.TrimSpace(diagnostic.ErrorCode)
	diagnostic.Detail = strings.TrimSpace(diagnostic.Detail)
	if !isFrontendDiagnosticOperation(diagnostic.Operation) ||
		!isFrontendDiagnosticCode(diagnostic.ErrorCode) ||
		diagnostic.Detail == "" || len(diagnostic.Detail) > 2048 {
		return DesktopBindingError{Code: desktopCodeInvalidIdentifier}
	}
	app.mu.Lock()
	report := app.reportFrontendDiagnostic
	app.mu.Unlock()
	if report == nil {
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	}
	if err := report(diagnostic); err != nil {
		return DesktopBindingError{Code: desktopCodeOperationFailed}
	}
	return nil
}

func isFrontendDiagnosticOperation(operation string) bool {
	switch operation {
	case "load_workspace", "load_catalog", "load_reports", "load_comparisons":
		return true
	default:
		return false
	}
}

func isFrontendDiagnosticCode(code string) bool {
	switch code {
	case "frontend_data_invalid", "frontend_operation_failed":
		return true
	default:
		return false
	}
}

func ConfigureDiagnostics(app *DesktopApp, operator *diagnostics.Logger, open func(string) error) {
	snapshot := DesktopDiagnosticsSnapshot{
		SchemaVersion: 1, Available: operator != nil, Format: "jsonl",
		MaxFileBytes: diagnostics.DefaultMaxBytes, BackupFiles: diagnostics.DefaultBackups,
		RunCorrelation: true, RequestCorrelation: true,
	}
	if operator == nil || open == nil {
		app.setDiagnostics(snapshot, nil)
		if operator == nil {
			app.setFrontendDiagnosticReporter(nil)
		} else {
			app.setFrontendDiagnosticReporter(desktopFrontendDiagnosticReporter(operator))
		}
		return
	}
	directory := filepath.Dir(operator.Path())
	app.setDiagnostics(snapshot, func() error { return open(directory) })
	app.setFrontendDiagnosticReporter(desktopFrontendDiagnosticReporter(operator))
}

func desktopFrontendDiagnosticReporter(operator *diagnostics.Logger) func(FrontendDiagnostic) error {
	return func(diagnostic FrontendDiagnostic) error {
		if operator == nil {
			return ErrDiagnosticsUnavailable
		}
		return operator.Record(context.Background(), diagnostics.Event{
			Level: diagnostics.LevelError, Message: "frontend desktop operation failed",
			Component: "frontend", Operation: diagnostic.Operation, ErrorCode: diagnostic.ErrorCode,
			Err: errors.New(diagnostic.Detail),
		})
	}
}

func RunDiagnosticReporter(operator *diagnostics.Logger, fallback *log.Logger) func(runs.Diagnostic) {
	return func(diagnostic runs.Diagnostic) {
		level := diagnostics.LevelError
		message := "run operation failed"
		if diagnostic.ErrorCode == "phase_timing" {
			level = diagnostics.LevelInfo
			message = "run phase completed"
		} else if diagnostic.ErrorCode == "diagnostics_dropped" {
			level = diagnostics.LevelWarn
			message = "run diagnostics were dropped"
		}
		if fallback != nil {
			fallback.Printf(
				"%s: run=%s request=%s operation=%s code=%s duration=%s dropped=%d: %s",
				message,
				diagnostic.RunID, diagnostic.RequestID, diagnostic.Operation, diagnostic.ErrorCode,
				diagnostic.Duration,
				diagnostic.DroppedCount,
				diagnostics.RedactText(errorText(diagnostic.Err)),
			)
		}
		if operator != nil {
			if err := operator.Record(context.Background(), diagnostics.Event{
				Level: level, Message: message,
				Component: "runs", Operation: diagnostic.Operation, ErrorCode: diagnostic.ErrorCode,
				RunID: diagnostic.RunID, RequestID: diagnostic.RequestID,
				Duration: diagnostic.Duration, DroppedCount: diagnostic.DroppedCount, Err: diagnostic.Err,
			}); err != nil && fallback != nil {
				fallback.Printf("structured diagnostic degraded: %s", diagnostics.RedactText(err.Error()))
			}
		}
	}
}

func openDesktopDiagnostics(options DiagnosticsOptions) (*diagnostics.Logger, error) {
	if options.UserConfigDir == nil {
		return nil, fmt.Errorf("user configuration directory provider is unavailable")
	}
	root, err := options.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate user configuration directory for diagnostics: %w", err)
	}
	directory, _, err := native.StoragePaths(root)
	if err != nil {
		return nil, err
	}
	return diagnostics.Open(diagnostics.Options{
		Directory:  filepath.Join(directory, "logs"),
		Filename:   "llm-test-studio.log",
		AppVersion: options.AppVersion,
	})
}

func OpenDiagnosticsWithFallback(options DiagnosticsOptions, processID int) (*diagnostics.Logger, error) {
	operator, primaryErr := openDesktopDiagnostics(options)
	if primaryErr == nil {
		return operator, nil
	}
	fallback, fallbackErr := openDesktopFallbackDiagnostics(options, processID)
	if fallbackErr != nil {
		return nil, errors.Join(primaryErr, fmt.Errorf("open fallback desktop diagnostics: %w", fallbackErr))
	}
	if recordErr := fallback.Record(context.Background(), diagnostics.Event{
		Level: diagnostics.LevelError, Message: "primary desktop diagnostics unavailable",
		Component: "desktop", Operation: "diagnostics_startup", ErrorCode: "diagnostics_fallback",
		Err: primaryErr,
	}); recordErr != nil {
		_ = fallback.Close()
		return nil, errors.Join(primaryErr, fmt.Errorf("record fallback desktop diagnostics: %w", recordErr))
	}
	return fallback, primaryErr
}

func openDesktopFallbackDiagnostics(options DiagnosticsOptions, processID int) (*diagnostics.Logger, error) {
	if processID <= 0 {
		return nil, fmt.Errorf("desktop process ID must be positive")
	}
	if options.UserConfigDir == nil {
		return nil, fmt.Errorf("user configuration directory provider is unavailable")
	}
	root, err := options.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate user configuration directory for fallback diagnostics: %w", err)
	}
	directory, _, err := native.StoragePaths(root)
	if err != nil {
		return nil, err
	}
	return diagnostics.Open(diagnostics.Options{
		Directory:  filepath.Join(directory, "logs"),
		Filename:   fmt.Sprintf("llm-test-studio-fallback-%d.log", processID),
		AppVersion: options.AppVersion,
	})
}

func ErrorReporter(operator *diagnostics.Logger, fallback *log.Logger) func(error) {
	return func(err error) {
		if err == nil {
			return
		}
		level := diagnostics.LevelError
		component := "desktop"
		operation := "wails_boundary"
		errorCode := desktopCodeOperationFailed
		message := "desktop operation failed"
		reportID := ""
		duration := time.Duration(0)
		failureCount := uint64(0)
		eventErr := err
		var quickDiagnostic quickTestDiagnosticEvent
		var frontendDiagnostic frontendRuntimeDiagnosticError
		var catalogDiagnostic catalogCommandDiagnosticError
		var credentialCleanupDiagnostic CredentialCleanupDiagnosticError
		if errors.As(err, &frontendDiagnostic) {
			component = "frontend"
			operation = frontendDiagnostic.Operation
			errorCode = frontendDiagnostic.ErrorCode
			message = "frontend desktop operation failed"
			eventErr = errors.New(frontendDiagnostic.Detail)
		} else if errors.As(err, &quickDiagnostic) {
			level = diagnostics.LevelWarn
			component = "quick_test"
			operation = quickDiagnostic.Operation
			errorCode = quickDiagnostic.ErrorCode
			message = "quick test operation failed"
			if operation == quickTestDiagnosticArchiveOperation {
				message = "quick performance archive failed"
			}
			reportID = quickDiagnostic.ReportID
			duration = quickDiagnostic.Duration
			failureCount = quickDiagnostic.FailureCount
			eventErr = nil
		} else if errors.As(err, &credentialCleanupDiagnostic) {
			level = diagnostics.LevelWarn
			component = "credentials"
			operation = "delete_obsolete_channel_credential"
			errorCode = "credential_cleanup_failed"
			message = "credential cleanup failed"
			eventErr = credentialCleanupDiagnostic.Err
		} else if errors.As(err, &catalogDiagnostic) {
			component = "catalog"
			operation = strings.ReplaceAll(strings.TrimSpace(catalogDiagnostic.operation), " ", "_")
			message = "catalog operation failed"
			eventErr = catalogDiagnostic.err
			switch {
			case catalogDiagnostic.committed:
				errorCode = desktopCodeCatalogSavedRefreshFailed
				message = "catalog saved but refresh failed"
			case errors.Is(err, catalog.ErrPlanProtocolMismatch):
				errorCode = desktopCodePlanProtocolMismatch
			case errors.Is(err, catalog.ErrInvalid):
				errorCode = desktopCodeCatalogInvalid
			case errors.Is(err, catalog.ErrConflict):
				errorCode = desktopCodeCatalogConflict
			case errors.Is(err, catalog.ErrNotFound):
				errorCode = desktopCodeCatalogNotFound
			}
		} else if errors.Is(err, catalog.ErrPlanProtocolMismatch) {
			component = "catalog"
			operation = "save_plan"
			errorCode = desktopCodePlanProtocolMismatch
			message = "plan save failed"
		} else if errors.Is(err, ErrDesktopStartup) {
			operation = "startup"
			errorCode = desktopCodeStartupFailed
			message = "desktop startup failed"
		}
		if fallback != nil {
			fallback.Printf("desktop: %s", diagnostics.RedactText(err.Error()))
		}
		if operator != nil {
			if writeErr := operator.Record(context.Background(), diagnostics.Event{
				Level: level, Message: message,
				Component: component, Operation: operation, ErrorCode: errorCode,
				ReportID: reportID, Duration: duration, FailureCount: failureCount, Err: eventErr,
			}); writeErr != nil && fallback != nil {
				fallback.Printf("structured diagnostic degraded: %s", diagnostics.RedactText(writeErr.Error()))
			}
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type frontendRuntimeDiagnosticError struct {
	Operation string
	ErrorCode string
	Detail    string
}

type CredentialCleanupDiagnosticError struct{ Err error }

func (err CredentialCleanupDiagnosticError) Unwrap() error { return err.Err }

func parseFrontendRuntimeDiagnostic(message string) (frontendRuntimeDiagnosticError, bool) {
	var payload struct {
		Component string `json:"component"`
		Operation string `json:"operation"`
		ErrorCode string `json:"error_code"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(message), &payload); err != nil {
		return frontendRuntimeDiagnosticError{}, false
	}
	payload.Component = strings.TrimSpace(payload.Component)
	payload.Operation = strings.TrimSpace(payload.Operation)
	payload.ErrorCode = strings.TrimSpace(payload.ErrorCode)
	payload.Detail = strings.TrimSpace(payload.Detail)
	if payload.Component != "frontend" ||
		!isFrontendDiagnosticOperation(payload.Operation) ||
		!isFrontendDiagnosticCode(payload.ErrorCode) ||
		payload.Detail == "" || len(payload.Detail) > 2048 {
		return frontendRuntimeDiagnosticError{}, false
	}
	return frontendRuntimeDiagnosticError{
		Operation: payload.Operation,
		ErrorCode: payload.ErrorCode,
		Detail:    payload.Detail,
	}, true
}
