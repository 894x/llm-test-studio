package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

func TestDesktopOptionsConfigureNativeStaticWorkspaceShell(t *testing.T) {
	assets := fstest.MapFS{
		"frontend/dist/index.html": {Data: []byte("<!doctype html><title>LLM Test Studio</title>")},
	}
	closeFailure := errors.New("repository close failure")
	closeCalls := 0
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{
			query: &recordingWorkspaceQuery{snapshot: workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}},
			close: func() error {
				closeCalls++
				return closeFailure
			},
		}, nil
	})
	var reported error

	configured := desktopOptions(app, assets, func(err error) { reported = err })

	if configured.Title != "LLM Test Studio" {
		t.Fatalf("title = %q, want LLM Test Studio", configured.Title)
	}
	if configured.Width != 1360 || configured.Height != 820 {
		t.Fatalf("window size = %dx%d, want 1360x820", configured.Width, configured.Height)
	}
	if configured.MinWidth != 960 || configured.MinHeight != 640 {
		t.Fatalf("minimum window size = %dx%d, want 960x640", configured.MinWidth, configured.MinHeight)
	}
	if configured.DisableResize || configured.Fullscreen || configured.Frameless {
		t.Fatal("desktop shell is not an ordinary resizable native window")
	}
	if configured.BackgroundColour == nil || *configured.BackgroundColour != (struct {
		R uint8 `json:"r"`
		G uint8 `json:"g"`
		B uint8 `json:"b"`
		A uint8 `json:"a"`
	}{R: 26, G: 26, B: 26, A: 255}) {
		t.Fatalf("background colour = %+v, want opaque #1a1a1a", configured.BackgroundColour)
	}
	if configured.AssetServer == nil || configured.AssetServer.Assets == nil {
		t.Fatal("desktop shell has no embedded static assets")
	}
	if configured.AssetServer.Handler != nil || configured.AssetServer.Middleware != nil || configured.AssetsHandler != nil {
		t.Fatal("desktop shell configured a custom or HTTP fallback asset handler")
	}
	if _, err := configured.AssetServer.Assets.Open("frontend/dist/index.html"); err != nil {
		t.Fatalf("open configured embedded index: %v", err)
	}
	if len(configured.Bind) != 1 || configured.Bind[0] != app {
		t.Fatalf("bound objects = %v, want only DesktopApp", configured.Bind)
	}
	if configured.OnStartup == nil || configured.OnShutdown == nil {
		t.Fatal("desktop lifecycle callbacks are incomplete")
	}

	configured.OnStartup(context.Background())
	if _, err := app.GetWorkspace(); err != nil {
		t.Fatalf("workspace after OnStartup error = %v", err)
	}
	configured.OnShutdown(context.Background())
	if closeCalls != 1 {
		t.Fatalf("resource close calls = %d, want 1", closeCalls)
	}
	if !errors.Is(reported, closeFailure) {
		t.Fatalf("reported shutdown error = %v, want close failure", reported)
	}
}

func TestDesktopOptionsRoutesWailsRuntimeErrorsToDiagnostics(t *testing.T) {
	app := newDesktopApp(nil)
	var reported error
	configured := desktopOptions(app, fstest.MapFS{}, func(err error) { reported = err })

	if configured.Logger == nil {
		t.Fatal("desktop shell has no Wails runtime logger")
	}
	configured.Logger.Error(`{"component":"frontend","operation":"load_catalog","error_code":"frontend_data_invalid","detail":"catalog payload invalid"}`)

	if reported == nil || !strings.Contains(reported.Error(), "load_catalog") || !strings.Contains(reported.Error(), "catalog payload invalid") {
		t.Fatalf("reported runtime error = %v, want frontend load failure", reported)
	}
}

func TestFrontendEmbedMarkerSurvivesCleanCheckoutAndFrontendBuild(t *testing.T) {
	const markerPath = "wails-embed.txt"
	source, err := os.ReadFile(filepath.Join("frontend", "public", markerPath))
	if err != nil {
		t.Fatalf("read public embed marker: %v", err)
	}
	embedded, err := frontendAssets.ReadFile(filepath.ToSlash(filepath.Join("frontend", "dist", markerPath)))
	if err != nil {
		t.Fatalf("read embedded dist marker: %v", err)
	}
	if string(embedded) != string(source) {
		t.Fatalf("embedded marker = %q, want public marker %q", embedded, source)
	}
}

func TestDesktopBindingErrorCodesMatchFrontendContract(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "not started", err: ErrDesktopNotStarted, want: "desktop_not_started"},
		{name: "startup", err: ErrDesktopStartup, want: "desktop_startup_failed"},
		{name: "stopped", err: ErrDesktopStopped, want: "desktop_stopped"},
		{name: "workspace", err: ErrWorkspaceUnavailable, want: "workspace_unavailable"},
		{name: "catalog", err: ErrCatalogUnavailable, want: "catalog_unavailable"},
		{name: "reports", err: ErrReportingUnavailable, want: "reports_unavailable"},
		{name: "commands", err: ErrRunCommandsUnavailable, want: "run_commands_unavailable"},
		{name: "comparisons", err: ErrComparisonUnavailable, want: "comparison_unavailable"},
		{name: "quick test", err: ErrQuickTestUnavailable, want: "quick_test_unavailable"},
		{name: "identifier", err: ErrInvalidIdentifier, want: "invalid_identifier"},
		{name: "plan protocol mismatch", err: catalog.ErrPlanProtocolMismatch, want: "plan_protocol_mismatch"},
		{name: "catalog invalid", err: catalog.ErrInvalid, want: "catalog_invalid"},
		{name: "catalog conflict", err: catalog.ErrConflict, want: "catalog_revision_conflict"},
		{name: "catalog not found", err: catalog.ErrNotFound, want: "catalog_not_found"},
		{name: "catalog saved refresh failed", err: ErrCatalogSavedRefreshFailed, want: "catalog_saved_refresh_failed"},
		{name: "cancelled", err: context.Canceled, want: "operation_cancelled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "operation_cancelled"},
		{name: "unknown", err: errors.New("unknown"), want: "operation_failed"},
	}
	app := NewDesktopApp(nil, nil)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := app.safeBindingError(test.err)
			assertBindingErrorCode(t, err, test.want)
			if err.Error() != test.want {
				t.Fatalf("binding error string = %q, want code only %q", err, test.want)
			}
		})
	}

	source, err := os.ReadFile(filepath.Join("frontend", "src", "app", "desktop-client.ts"))
	if err != nil {
		t.Fatalf("read frontend desktop error contract: %v", err)
	}
	contract := string(source)
	start := strings.Index(contract, "export type DesktopErrorCode =")
	end := strings.Index(contract, "const PUBLIC_ERROR_MESSAGES")
	if start < 0 || end <= start {
		t.Fatal("frontend DesktopErrorCode contract markers are missing")
	}
	matches := regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(contract[start:end], -1)
	gotCodes := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		gotCodes[match[1]] = struct{}{}
	}
	wantCodes := map[string]struct{}{
		desktopCodeNotStarted:                {},
		desktopCodeStartupFailed:             {},
		desktopCodeStopped:                   {},
		desktopCodeWorkspaceMissing:          {},
		desktopCodeCatalogMissing:            {},
		desktopCodeReportsMissing:            {},
		desktopCodeCommandsMissing:           {},
		desktopCodeComparisonMissing:         {},
		desktopCodeDiagnosticsMissing:        {},
		desktopCodeQuickTestMissing:          {},
		desktopCodeQuickTaskCredential:       {},
		desktopCodeInvalidIdentifier:         {},
		desktopCodeOperationCancelled:        {},
		desktopCodeOperationFailed:           {},
		desktopCodeRunInvalid:                {},
		desktopCodeRunNotRunnable:            {},
		desktopCodePlanProtocolMismatch:      {},
		desktopCodeCatalogInvalid:            {},
		desktopCodeCatalogConflict:           {},
		desktopCodeCatalogNotFound:           {},
		desktopCodeCatalogSavedRefreshFailed: {},
	}
	if len(gotCodes) != len(wantCodes) {
		t.Fatalf("frontend error codes = %v, want exact backend code set %v", gotCodes, wantCodes)
	}
	for code := range wantCodes {
		if _, ok := gotCodes[code]; !ok {
			t.Errorf("frontend DesktopErrorCode is missing backend code %q", code)
		}
	}
}

func TestDesktopErrorFormatterProducesStringCodeWailsCanPreserve(t *testing.T) {
	const sensitive = "https://provider.example/v1 api-key=sk-sensitive-value"
	internal := errors.New(sensitive)
	var reported error
	app := NewDesktopApp(nil, nil)
	configured := desktopOptions(app, fstest.MapFS{
		"frontend/dist/index.html": {Data: []byte("<!doctype html>")},
	}, func(err error) { reported = err })
	if configured.ErrorFormatter == nil {
		t.Fatal("Wails error formatter is nil")
	}

	formatted := configured.ErrorFormatter(internal)
	callback, err := json.Marshal(struct {
		Error any `json:"error"`
	}{Error: formatted})
	if err != nil {
		t.Fatalf("marshal Wails callback error: %v", err)
	}

	if got := string(callback); got != `{"error":"operation_failed"}` {
		t.Fatalf("Wails callback error = %s, want string stable code", got)
	}
	if strings.Contains(string(callback), "provider.example") || strings.Contains(string(callback), "sk-sensitive-value") {
		t.Fatalf("Wails callback leaked sensitive detail: %s", callback)
	}
	if !errors.Is(reported, internal) {
		t.Fatalf("locally reported formatter error = %v, want internal error", reported)
	}
}
