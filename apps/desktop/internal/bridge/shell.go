package bridge

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"

	"github.com/894x/llm-test-studio/internal/diagnostics"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

func WailsOptions(app *DesktopApp, assets fs.FS, report func(error)) *options.App {
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

func (err CredentialCleanupDiagnosticError) Error() string {
	return fmt.Sprintf("credential cleanup failed: %v", err.Err)
}

func (err frontendRuntimeDiagnosticError) Error() string {
	return fmt.Sprintf("frontend %s (%s): %s", err.Operation, err.ErrorCode, err.Detail)
}

func (logger desktopWailsLogger) Print(message string) { logger.write("print", message, false) }

func (logger desktopWailsLogger) Trace(message string) { logger.write("trace", message, false) }

func (logger desktopWailsLogger) Debug(message string) { logger.write("debug", message, false) }

func (logger desktopWailsLogger) Info(message string) { logger.write("info", message, false) }

func (logger desktopWailsLogger) Warning(message string) { logger.write("warning", message, false) }

func (logger desktopWailsLogger) Error(message string) { logger.write("error", message, true) }

func (logger desktopWailsLogger) Fatal(message string) { logger.write("fatal", message, true) }

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
