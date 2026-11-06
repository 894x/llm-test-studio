package bridge

import (
	"encoding/base64"

	"github.com/894x/llm-test-studio/internal/application/benchmarks"
	"github.com/894x/llm-test-studio/internal/application/reporting"
)

func (app *DesktopApp) GetBenchmarkCatalog() (benchmarks.Catalog, error) {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return benchmarks.Catalog{}, app.safeBindingError(err)
	}
	defer lease.release()
	return lease.benchmarks.Catalog(), nil
}

func (app *DesktopApp) PrepareBenchmarkRuntime() error {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.benchmarks.Prepare())
}

func (app *DesktopApp) StartBenchmark(command benchmarks.StartCommand) (benchmarks.Run, error) {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return benchmarks.Run{}, app.safeBindingError(err)
	}
	defer lease.release()
	run, err := lease.benchmarks.Start(lease.ctx, command)
	return run, app.safeBindingError(err)
}

func (app *DesktopApp) GetBenchmarks(after string) (benchmarks.Page, error) {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return benchmarks.Page{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.benchmarks.Page(after)
	return page, app.safeBindingError(err)
}

func (app *DesktopApp) GetBenchmark(id string) (benchmarks.Run, error) {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return benchmarks.Run{}, app.safeBindingError(err)
	}
	defer lease.release()
	run, err := lease.benchmarks.Get(id)
	return run, app.safeBindingError(err)
}

func (app *DesktopApp) CancelBenchmark(id string) error {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.benchmarks.Cancel(id))
}

func (app *DesktopApp) GetBenchmarkReport(runID, reportID, after string) (benchmarks.Report, error) {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return benchmarks.Report{}, app.safeBindingError(err)
	}
	defer lease.release()
	report, err := lease.benchmarks.Report(runID, reportID, after)
	return report, app.safeBindingError(err)
}

func (app *DesktopApp) ExportBenchmarkReport(runID, reportID string) (reporting.ExportedDocument, error) {
	lease, err := app.acquire(desktopRequirements{benchmarks: true})
	if err != nil {
		return reporting.ExportedDocument{}, app.safeBindingError(err)
	}
	defer lease.release()
	payload, err := lease.benchmarks.Export(runID, reportID)
	if err != nil {
		return reporting.ExportedDocument{}, app.safeBindingError(err)
	}
	return reporting.ExportedDocument{Filename: "benchmark-" + reportID + ".json", MediaType: "application/json",
		DataBase64: base64.StdEncoding.EncodeToString(payload)}, nil
}
