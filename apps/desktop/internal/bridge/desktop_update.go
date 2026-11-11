package bridge

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/apps/desktop/internal/updater"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func ConfigureUpdates(app *DesktopApp, service *updater.Service, quit func(context.Context)) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.updater, app.quitForUpdate = service, quit
}

func (app *DesktopApp) updateService() (*updater.Service, context.Context) {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.updater, app.startupParent
}

func unavailableUpdate() updater.View {
	return updater.View{SchemaVersion: 1, Status: "unsupported", ErrorCode: "unavailable", InstallKind: "unavailable"}
}

func (app *DesktopApp) GetDesktopUpdate() updater.View {
	service, _ := app.updateService()
	if service == nil {
		return unavailableUpdate()
	}
	return service.Snapshot()
}

func (app *DesktopApp) CheckDesktopUpdate() updater.View {
	service, ctx := app.updateService()
	if service == nil || ctx == nil {
		return unavailableUpdate()
	}
	return service.Check(ctx)
}

func (app *DesktopApp) DownloadDesktopUpdate() updater.View {
	service, ctx := app.updateService()
	if service == nil || ctx == nil {
		return unavailableUpdate()
	}
	return service.Download(ctx)
}

func (app *DesktopApp) CancelDesktopUpdate() updater.View {
	service, _ := app.updateService()
	if service == nil {
		return unavailableUpdate()
	}
	return service.Cancel()
}

func (app *DesktopApp) OpenDesktopUpdateRelease() {
	_, ctx := app.updateService()
	if ctx != nil {
		wailsruntime.BrowserOpenURL(ctx, "https://github.com/894x/llm-test-studio/releases/latest")
	}
}

func (app *DesktopApp) SetDesktopUpdatePreferences(autoCheck, skip bool) updater.View {
	service, _ := app.updateService()
	if service == nil {
		return unavailableUpdate()
	}
	return service.SetPreferences(autoCheck, skip)
}

func (app *DesktopApp) InstallDesktopUpdate() updater.View {
	service, ctx := app.updateService()
	if service == nil || ctx == nil {
		return unavailableUpdate()
	}
	view := service.Snapshot()
	if view.Status != "ready" {
		return view
	}
	// Block new leases before inspecting activity. An already leased launch can
	// never race the activity read; we leave it running and defer this update.
	app.mu.Lock()
	busy := app.active > 0 || app.updatePreparing || app.starting || app.stopping || app.stopped
	if busy {
		app.mu.Unlock()
		view.ErrorCode = "tasks_active"
		return view
	}
	app.updatePreparing = true
	query, workspace, benchmarkService, quit := app.jobQuery, app.query, app.benchmarks, app.quitForUpdate
	reportGeneration := app.reportGeneration
	app.mu.Unlock()
	installed := false
	defer func() {
		if !installed {
			app.mu.Lock()
			app.updatePreparing = false
			app.mu.Unlock()
		}
	}()
	if benchmarkService != nil && benchmarkService.HasBackgroundWork() {
		view.ErrorCode = "tasks_active"
		return view
	}
	if !isNilInterface(reportGeneration) {
		for _, progress := range reportGeneration.GenerationSnapshot().Runs {
			if progress.Phase != "ready" && progress.Phase != "failed" {
				view.ErrorCode = "tasks_active"
				return view
			}
		}
	}
	checkContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	view.ErrorCode = updateActivity(checkContext, query, workspace)
	if view.ErrorCode != "" {
		return view
	}
	if quit == nil && view.InstallKind == "restart" {
		view.ErrorCode = "unavailable"
		return view
	}
	view = service.Install()
	if view.Status == "installing" {
		installed = true
		// runtime.Quit must return through the binding before Wails tears it down.
		go quit(ctx)
	}
	return view
}

func updateActivity(ctx context.Context, query jobs.Query, workspace WorkspaceQuery) string {
	if isNilInterface(query) || isNilInterface(workspace) {
		return "tasks_unknown"
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return "tasks_unknown"
	}
	for _, run := range snapshot.Runs {
		if !updateTerminal(run.Status) {
			return "tasks_active"
		}
	}
	cursor := ""
	// Bound the read; reaching the cap defers installation rather than assuming
	// an unobserved task is safe to interrupt.
	for range 100 {
		page, err := query.Jobs(ctx, cursor, 100)
		if err != nil {
			return "tasks_unknown"
		}
		for _, job := range page.Jobs {
			if job.Execution != nil {
				if !updateTerminal(job.Execution.Status) {
					return "tasks_active"
				}
				continue
			}
			if job.Submission == nil || job.Submission.Confirmation == nil {
				return "tasks_unknown"
			}
			if !updateTerminal(job.Submission.Confirmation.Status) {
				return "tasks_active"
			}
		}
		if !page.HasMore {
			return ""
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return "tasks_unknown"
		}
		cursor = page.NextCursor
	}
	return "tasks_unknown"
}

func updateTerminal(status domain.RunStatus) bool {
	return status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCancelled
}
