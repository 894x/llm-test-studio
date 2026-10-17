package bridge

import (
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (app *DesktopApp) GetReportGeneration() (reporting.GenerationSnapshot, error) {
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		return reporting.GenerationSnapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	app.mu.Lock()
	source := app.reportGeneration
	app.mu.Unlock()
	if source == nil {
		return reporting.GenerationSnapshot{SchemaVersion: 1, Runs: []reporting.GenerationProgress{}}, nil
	}
	return source.GenerationSnapshot(), nil
}

func (app *DesktopApp) GetReports() (reporting.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{reports: true})
	if err != nil {
		return reporting.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.reports.Snapshot(lease.ctx)
	if err != nil {
		return reporting.Snapshot{}, app.safeBindingError(fmt.Errorf("query desktop reports: %w", err))
	}
	return snapshot, nil
}

func (app *DesktopApp) GetReportDetail(reportID string) (reporting.Detail, error) {
	if !domain.IsUUID(reportID) {
		return reporting.Detail{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{reports: true})
	if err != nil {
		return reporting.Detail{}, app.safeBindingError(err)
	}
	defer lease.release()
	documents, ok := lease.reports.(ReportDocumentQuery)
	if !ok || isNilInterface(documents) {
		return reporting.Detail{}, app.safeBindingError(ErrReportingUnavailable)
	}
	detail, err := documents.Detail(lease.ctx, reportID)
	if err != nil {
		return reporting.Detail{}, app.safeBindingError(fmt.Errorf("query desktop report detail: %w", err))
	}
	return detail, nil
}

func (app *DesktopApp) ExportReport(reportID, format, watermark, locale string) (reporting.ExportedDocument, error) {
	if !domain.IsUUID(reportID) {
		return reporting.ExportedDocument{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	if locale != "zh-CN" && locale != "en-US" {
		return reporting.ExportedDocument{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	exportFormat := reporting.ExportFormat(format)
	switch exportFormat {
	case reporting.ExportJSON, reporting.ExportHTML, reporting.ExportPNG, reporting.ExportPDF:
	default:
		return reporting.ExportedDocument{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{reports: true})
	if err != nil {
		return reporting.ExportedDocument{}, app.safeBindingError(err)
	}
	defer lease.release()
	documents, ok := lease.reports.(ReportDocumentQuery)
	if !ok || isNilInterface(documents) {
		return reporting.ExportedDocument{}, app.safeBindingError(ErrReportingUnavailable)
	}
	var exported reporting.ExportedDocument
	if localized, ok := lease.reports.(LocalizedReportExporter); ok && !isNilInterface(localized) {
		exported, err = localized.ExportLocalized(lease.ctx, reportID, exportFormat, watermark, locale)
	} else {
		exported, err = documents.Export(lease.ctx, reportID, exportFormat, watermark)
	}
	if err != nil {
		return reporting.ExportedDocument{}, app.safeBindingError(fmt.Errorf("export desktop report: %w", err))
	}
	return exported, nil
}
