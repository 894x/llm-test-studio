package main

import (
	"errors"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

var errQuickTestOperationFailed = errors.New("quick test operation failed")

const (
	quickTestDiagnosticPerformanceOperation = "performance_test"
	quickTestDiagnosticArchiveOperation     = "performance_archive"
	quickTestDiagnosticFallbackCode         = domain.ErrorCode("quick_test_failed")
	quickTestDiagnosticArchiveFailedCode    = domain.ErrorCode("quick_performance_archive_failed")
)

const quickPerformanceProgressEventName = "quick-performance-progress"

const quickPerformanceProgressInterval = 100 * time.Millisecond

type quickPerformanceProgressEvent struct {
	ProgressID string                        `json:"progress_id"`
	Progress   quicktest.PerformanceProgress `json:"progress"`
}

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

func (app *DesktopApp) RunQuickPerformanceTest(command quicktest.PerformanceCommand, progressID string) (quicktest.PerformanceReport, error) {
	lease, err := app.acquire(desktopRequirements{quickTests: true})
	if err != nil {
		return quicktest.PerformanceReport{}, app.safeBindingError(err)
	}
	defer lease.release()
	var report quicktest.PerformanceReport
	if progressID == "" {
		report, err = lease.quickTests.RunPerformance(lease.ctx, command)
	} else if !domain.IsUUID(progressID) {
		return quicktest.PerformanceReport{}, app.safeBindingError(ErrInvalidIdentifier)
	} else if runner, ok := lease.quickTests.(QuickPerformanceProgressRunner); ok {
		lastEmitted := time.Time{}
		lastPhase := quicktest.PerformancePhaseNotStarted
		report, err = runner.RunPerformanceWithProgress(lease.ctx, command, func(progress quicktest.PerformanceProgress) {
			now := time.Now()
			phaseChanged := progress.Phase != lastPhase
			if !lastEmitted.IsZero() && !phaseChanged && now.Sub(lastEmitted) < quickPerformanceProgressInterval {
				return
			}
			lastEmitted = now
			lastPhase = progress.Phase
			app.emitDesktopEvent(lease.ctx, quickPerformanceProgressEventName, quickPerformanceProgressEvent{
				ProgressID: progressID,
				Progress:   progress,
			})
		})
	} else {
		report, err = lease.quickTests.RunPerformance(lease.ctx, command)
	}
	if err != nil {
		// As with connectivity testing, provider and credential details must stay
		// behind the allowlisted performance report boundary.
		return quicktest.PerformanceReport{}, app.safeBindingError(errQuickTestOperationFailed)
	}
	app.reportQuickPerformanceDiagnostics(report)
	return report, nil
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
