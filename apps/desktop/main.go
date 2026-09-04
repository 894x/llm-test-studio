package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/diagnostics"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

func main() {
	production := defaultProductionOptions()
	operator, err := openDesktopDiagnosticsWithFallback(production, os.Getpid())
	if err != nil {
		log.Printf("desktop diagnostics unavailable: %v", err)
	}
	if operator != nil {
		defer func() {
			if err := operator.Close(); err != nil {
				log.Printf("close desktop diagnostics: %v", err)
			}
		}()
	}
	report := desktopErrorReporter(operator, log.Default())
	production.reportRunDiagnostic = desktopRunDiagnosticReporter(operator, log.Default())
	app := newDesktopApp(newProductionInitializer(production))
	configureDesktopDiagnostics(app, operator, openDirectory)
	if err := wails.Run(desktopOptions(app, frontendAssets, report)); err != nil {
		report(fmt.Errorf("run Wails desktop shell: %w", err))
	}
}

func configureDesktopDiagnostics(app *DesktopApp, operator *diagnostics.Logger, open func(string) error) {
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

func desktopRunDiagnosticReporter(operator *diagnostics.Logger, fallback *log.Logger) func(runs.Diagnostic) {
	return func(diagnostic runs.Diagnostic) {
		level := diagnostics.LevelError
		message := "run operation failed"
		if diagnostic.ErrorCode == "diagnostics_dropped" {
			level = diagnostics.LevelWarn
			message = "run diagnostics were dropped"
		}
		if fallback != nil {
			fallback.Printf(
				"%s: run=%s request=%s operation=%s code=%s dropped=%d: %s",
				message,
				diagnostic.RunID, diagnostic.RequestID, diagnostic.Operation, diagnostic.ErrorCode,
				diagnostic.DroppedCount,
				diagnostics.RedactText(errorText(diagnostic.Err)),
			)
		}
		if operator != nil {
			if err := operator.Record(context.Background(), diagnostics.Event{
				Level: level, Message: message,
				Component: "runs", Operation: diagnostic.Operation, ErrorCode: diagnostic.ErrorCode,
				RunID: diagnostic.RunID, RequestID: diagnostic.RequestID,
				DroppedCount: diagnostic.DroppedCount, Err: diagnostic.Err,
			}); err != nil && fallback != nil {
				fallback.Printf("structured diagnostic degraded: %s", diagnostics.RedactText(err.Error()))
			}
		}
	}
}

func openDesktopDiagnostics(options productionOptions) (*diagnostics.Logger, error) {
	if options.userConfigDir == nil {
		return nil, fmt.Errorf("user configuration directory provider is unavailable")
	}
	root, err := options.userConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate user configuration directory for diagnostics: %w", err)
	}
	directory, _, err := productionStoragePaths(root)
	if err != nil {
		return nil, err
	}
	return diagnostics.Open(diagnostics.Options{
		Directory:  filepath.Join(directory, "logs"),
		Filename:   "llm-test-studio.log",
		AppVersion: options.appVersion,
	})
}

func openDesktopDiagnosticsWithFallback(options productionOptions, processID int) (*diagnostics.Logger, error) {
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

func openDesktopFallbackDiagnostics(options productionOptions, processID int) (*diagnostics.Logger, error) {
	if processID <= 0 {
		return nil, fmt.Errorf("desktop process ID must be positive")
	}
	if options.userConfigDir == nil {
		return nil, fmt.Errorf("user configuration directory provider is unavailable")
	}
	root, err := options.userConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate user configuration directory for fallback diagnostics: %w", err)
	}
	directory, _, err := productionStoragePaths(root)
	if err != nil {
		return nil, err
	}
	return diagnostics.Open(diagnostics.Options{
		Directory:  filepath.Join(directory, "logs"),
		Filename:   fmt.Sprintf("llm-test-studio-fallback-%d.log", processID),
		AppVersion: options.appVersion,
	})
}

func desktopErrorReporter(operator *diagnostics.Logger, fallback *log.Logger) func(error) {
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
		} else if errors.Is(err, catalog.ErrPlanProtocolMismatch) {
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

func desktopOptions(app *DesktopApp, assets fs.FS, report func(error)) *options.App {
	app.setErrorReporter(report)
	return &options.App{
		Title:            "LLM Test Studio",
		Width:            1360,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: options.NewRGB(26, 26, 26),
		Logger:           desktopWailsLogger{report: report, fallback: log.Default()},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.onStartup,
		OnShutdown: func(context.Context) {
			if err := app.shutdown(); err != nil && report != nil {
				report(err)
			}
		},
		Bind:           []interface{}{app},
		ErrorFormatter: desktopErrorFormatter(report),
	}
}

type desktopWailsLogger struct {
	report   func(error)
	fallback *log.Logger
}

type frontendRuntimeDiagnosticError struct {
	Operation string
	ErrorCode string
	Detail    string
}

func (err frontendRuntimeDiagnosticError) Error() string {
	return fmt.Sprintf("frontend %s (%s): %s", err.Operation, err.ErrorCode, err.Detail)
}

func (logger desktopWailsLogger) Print(message string)   { logger.write("print", message, false) }
func (logger desktopWailsLogger) Trace(message string)   { logger.write("trace", message, false) }
func (logger desktopWailsLogger) Debug(message string)   { logger.write("debug", message, false) }
func (logger desktopWailsLogger) Info(message string)    { logger.write("info", message, false) }
func (logger desktopWailsLogger) Warning(message string) { logger.write("warning", message, false) }
func (logger desktopWailsLogger) Error(message string)   { logger.write("error", message, true) }
func (logger desktopWailsLogger) Fatal(message string)   { logger.write("fatal", message, true) }

func (logger desktopWailsLogger) write(level string, message string, report bool) {
	redacted := diagnostics.RedactText(message)
	if report && logger.report != nil {
		if diagnostic, ok := parseFrontendRuntimeDiagnostic(redacted); ok {
			logger.report(diagnostic)
			return
		}
		logger.report(fmt.Errorf("Wails runtime %s: %s", level, redacted))
		return
	}
	if logger.fallback != nil {
		logger.fallback.Printf("Wails runtime %s: %s", level, redacted)
	}
}

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

func desktopErrorFormatter(report func(error)) options.ErrorFormatter {
	return func(err error) any {
		var bindingError DesktopBindingError
		if errors.As(err, &bindingError) && isDesktopBindingCode(bindingError.Code) {
			return bindingError.Code
		}
		if err != nil && report != nil {
			report(err)
		}
		return desktopCodeOperationFailed
	}
}
