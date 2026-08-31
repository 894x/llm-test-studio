package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"

	"github.com/894x/llm-studio/internal/application/runs"
	"github.com/894x/llm-studio/internal/diagnostics"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

func main() {
	production := defaultProductionOptions()
	operator, err := openDesktopDiagnostics(production)
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
	if err := wails.Run(desktopOptions(app, frontendAssets, report)); err != nil {
		report(fmt.Errorf("run Wails desktop shell: %w", err))
	}
}

func desktopRunDiagnosticReporter(operator *diagnostics.Logger, fallback *log.Logger) func(runs.Diagnostic) {
	return func(diagnostic runs.Diagnostic) {
		if fallback != nil {
			fallback.Printf("run %s %s: %s", diagnostic.RunID, diagnostic.Operation, diagnostics.RedactText(errorText(diagnostic.Err)))
		}
		if operator != nil {
			if err := operator.Record(context.Background(), diagnostics.Event{
				Level: diagnostics.LevelError, Message: "run operation failed",
				Component: "runs", Operation: diagnostic.Operation, ErrorCode: diagnostic.ErrorCode,
				RunID: diagnostic.RunID, Err: diagnostic.Err,
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
		Filename:   "llm-studio.log",
		AppVersion: options.appVersion,
	})
}

func desktopErrorReporter(operator *diagnostics.Logger, fallback *log.Logger) func(error) {
	return func(err error) {
		if err == nil {
			return
		}
		operation := "wails_boundary"
		errorCode := desktopCodeOperationFailed
		message := "desktop operation failed"
		if errors.Is(err, ErrDesktopStartup) {
			operation = "startup"
			errorCode = desktopCodeStartupFailed
			message = "desktop startup failed"
		}
		if fallback != nil {
			fallback.Printf("desktop: %s", diagnostics.RedactText(err.Error()))
		}
		if operator != nil {
			if writeErr := operator.Record(context.Background(), diagnostics.Event{
				Level: diagnostics.LevelError, Message: message,
				Component: "desktop", Operation: operation, ErrorCode: errorCode, Err: err,
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
		Title:            "llm-studio",
		Width:            1360,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: options.NewRGB(26, 26, 26),
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

type desktopErrorPayload struct {
	Code string `json:"code"`
}

func desktopErrorFormatter(report func(error)) options.ErrorFormatter {
	return func(err error) any {
		var bindingError DesktopBindingError
		if errors.As(err, &bindingError) && isDesktopBindingCode(bindingError.Code) {
			return desktopErrorPayload{Code: bindingError.Code}
		}
		if err != nil && report != nil {
			report(err)
		}
		return desktopErrorPayload{Code: desktopCodeOperationFailed}
	}
}
