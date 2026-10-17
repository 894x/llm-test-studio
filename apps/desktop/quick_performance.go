package main

import (
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	quickTestDiagnosticPerformanceOperation = "performance_test"
	quickTestDiagnosticArchiveOperation     = "performance_archive"
	quickTestDiagnosticFallbackCode         = domain.ErrorCode("quick_test_failed")
	quickTestDiagnosticArchiveFailedCode    = domain.ErrorCode("quick_performance_archive_failed")
)

type quickTestDiagnosticEvent struct {
	Operation    string
	ErrorCode    string
	ReportID     string
	Duration     time.Duration
	FailureCount uint64
}

func (event quickTestDiagnosticEvent) Error() string {
	return "quick test diagnostic: operation=" + event.Operation + " code=" + event.ErrorCode
}

func (app *DesktopApp) reportQuickPerformanceDiagnostics(report quicktest.PerformanceReport) {
	reportID := ""
	if domain.IsUUID(report.ReportID) {
		reportID = report.ReportID
	}
	duration := quickTestDiagnosticDuration(report.Progress.TotalDurationMS)
	if report.ErrorCode != "" {
		app.reportQuickTestDiagnostic(quickTestDiagnosticEvent{
			Operation: quickTestDiagnosticPerformanceOperation,
			ErrorCode: safeQuickTestDiagnosticCode(report.ErrorCode),
			ReportID:  reportID, Duration: duration, FailureCount: report.Progress.Failed,
		})
	}
	for _, failure := range report.Failures {
		app.reportQuickTestDiagnostic(quickTestDiagnosticEvent{
			Operation: quickTestDiagnosticPerformanceOperation,
			ErrorCode: safeQuickTestDiagnosticCode(failure.ErrorCode),
			ReportID:  reportID, Duration: duration, FailureCount: failure.Count,
		})
	}
	if !report.Success && report.ErrorCode == "" && len(report.Failures) == 0 {
		app.reportQuickTestDiagnostic(quickTestDiagnosticEvent{
			Operation: quickTestDiagnosticPerformanceOperation,
			ErrorCode: string(quickTestDiagnosticFallbackCode),
			ReportID:  reportID, Duration: duration, FailureCount: report.Progress.Failed,
		})
	}
	if report.ArchiveStatus == quicktest.PerformanceArchiveFailed {
		app.reportQuickTestDiagnostic(quickTestDiagnosticEvent{
			Operation: quickTestDiagnosticArchiveOperation,
			ErrorCode: string(quickTestDiagnosticArchiveFailedCode),
			ReportID:  reportID,
		})
	}
}

func (app *DesktopApp) reportQuickTestDiagnostic(event quickTestDiagnosticEvent) {
	if app == nil {
		return
	}
	app.mu.Lock()
	report := app.reportError
	app.mu.Unlock()
	if report != nil {
		report(event)
	}
}

func safeQuickTestDiagnosticCode(code domain.ErrorCode) string {
	if code.Validate() == nil {
		return string(code)
	}
	return string(quickTestDiagnosticFallbackCode)
}

func quickTestDiagnosticDuration(milliseconds float64) time.Duration {
	if milliseconds <= 0 || math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds > float64((24*time.Hour)/time.Millisecond) {
		return 0
	}
	return time.Duration(milliseconds * float64(time.Millisecond))
}
