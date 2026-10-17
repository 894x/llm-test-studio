package main

import (
	"embed"
	"fmt"
	"log"
	"os"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bootstrap"
	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/apps/desktop/internal/native"
	"github.com/wailsapp/wails/v2"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

// Kept in main for the release workflow's -X linker flag.
var desktopApplicationVersion = "dev"

func main() {
	production := bootstrap.DefaultOptions(desktopApplicationVersion)
	operator, err := bridge.OpenDiagnosticsWithFallback(bridge.DiagnosticsOptions{
		UserConfigDir: production.UserConfigDir, AppVersion: production.AppVersion,
	}, os.Getpid())
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
	report := bridge.ErrorReporter(operator, log.Default())
	production.ReportRunDiagnostic = bridge.RunDiagnosticReporter(operator, log.Default())
	production.ReportCredentialCleanup = report
	app := bridge.New(bootstrap.NewInitializer(production))
	bridge.SetEventEmitter(app, wailsruntime.EventsEmit)
	bridge.ConfigureDiagnostics(app, operator, native.OpenDirectory)
	if err := wails.Run(bridge.WailsOptions(app, frontendAssets, report)); err != nil {
		report(fmt.Errorf("run Wails desktop shell: %w", err))
	}
}
