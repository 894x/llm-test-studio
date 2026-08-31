package main

import (
	"context"
	"errors"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/quicktest"
	"github.com/894x/llm-studio/internal/domain"
)

const (
	quickTestNameLimit   = 256
	quickTestURLLimit    = 4096
	quickTestAPIKeyLimit = 16384
)

var errQuickTestOperationFailed = errors.New("quick test operation failed")

const (
	quickTestDiagnosticConnectionOperation  = "connection_test"
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

// SaveQuickTestConnectionCommand is intentionally separate from the
// zero-persistence test command. The UI must call it explicitly after a
// successful test; merely running a quick test never writes catalog state.
type SaveQuickTestConnectionCommand struct {
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	ModelID         string `json:"model_id"`
	ModelName       string `json:"model_name"`
	ExistingModelID string `json:"existing_model_id,omitempty"`
	ChannelName     string `json:"channel_name"`
}

func (app *DesktopApp) RunQuickTest(command quicktest.Command) (quicktest.Result, error) {
	lease, err := app.acquire(desktopRequirements{quickTests: true})
	if err != nil {
		return quicktest.Result{}, app.safeBindingError(err)
	}
	defer lease.release()
	result, err := lease.quickTests.Run(lease.ctx, command)
	if err != nil {
		// The runner's error is deliberately not logged or returned. Quick-test
		// diagnostics belong in the allowlisted Result; an interface-level error
		// may otherwise contain a provider URL or credential.
		return quicktest.Result{}, app.safeBindingError(errQuickTestOperationFailed)
	}
	if !result.Success {
		app.reportQuickTestDiagnostic(quickTestDiagnosticEvent{
			Operation: quickTestDiagnosticConnectionOperation,
			ErrorCode: safeQuickTestDiagnosticCode(result.ErrorCode),
			Duration:  quickTestDiagnosticDuration(result.E2EMS),
		})
	}
	return result, nil
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
	} else if runner, ok := lease.quickTests.(QuickTestProgressRunner); ok {
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

func (app *DesktopApp) SaveQuickTestConnection(command SaveQuickTestConnectionCommand) (catalog.Snapshot, error) {
	if err := validateSaveQuickTestConnection(command); err != nil {
		return catalog.Snapshot{}, app.safeBindingError(catalog.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{catalog: true, catalogCommands: true})
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()

	before, err := lease.catalog.Snapshot(lease.ctx)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(errQuickTestOperationFailed)
	}
	modelID := command.ExistingModelID
	var createdModel catalog.MutationResult
	if modelID != "" {
		model, found := quickTestCatalogModel(before, modelID)
		if !found {
			return catalog.Snapshot{}, app.safeBindingError(catalog.ErrNotFound)
		}
		if model.Protocol != domain.ProtocolOpenAIChat {
			return catalog.Snapshot{}, app.safeBindingError(catalog.ErrInvalid)
		}
	}
	if quickTestConnectionConflicts(before, command) {
		return catalog.Snapshot{}, app.safeBindingError(catalog.ErrConflict)
	}
	if modelID == "" {
		createdModel, err = lease.catalogCommands.CreateModel(lease.ctx, catalog.CreateModelCommand{
			Name: command.ModelName, Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"},
		})
		if err != nil {
			return catalog.Snapshot{}, app.safeBindingError(safeQuickTestMutationError(err))
		}
		if !validMutationResult(createdModel) {
			return catalog.Snapshot{}, app.safeBindingError(ErrQuickTestSavePartial)
		}
		modelID = createdModel.ID
	}

	channel, err := lease.catalogCommands.CreateChannel(lease.ctx, catalog.CreateChannelCommand{
		Name: command.ChannelName, BaseURL: command.BaseURL, APIKey: command.APIKey,
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		if createdModel.ID != "" {
			rollbackErr := lease.catalogCommands.DeleteModel(lease.ctx, catalog.DeleteCommand{
				ID: createdModel.ID, ExpectedRevision: createdModel.Revision,
			})
			if rollbackErr != nil {
				return catalog.Snapshot{}, app.safeBindingError(ErrQuickTestSavePartial)
			}
		}
		return catalog.Snapshot{}, app.safeBindingError(safeQuickTestMutationError(err))
	}
	if !validMutationResult(channel) {
		// The channel owns an OS credential at this point. Catalog deletion does
		// not currently remove that credential, so retaining the visible channel
		// is safer than creating an inaccessible credential orphan.
		return catalog.Snapshot{}, app.safeBindingError(ErrQuickTestSavePartial)
	}

	_, err = lease.catalogCommands.CreateChannelModel(lease.ctx, catalog.CreateChannelModelCommand{
		ChannelID: channel.ID, ModelID: modelID, UpstreamModelName: command.ModelID,
	})
	if err != nil {
		// Keep the usable model and credential-owning channel visible. The caller
		// receives an explicit partial-save code and can finish or remove them.
		return catalog.Snapshot{}, app.safeBindingError(ErrQuickTestSavePartial)
	}

	after, err := lease.catalog.Snapshot(lease.ctx)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(ErrQuickTestSavePartial)
	}
	return after, nil
}

func validateSaveQuickTestConnection(command SaveQuickTestConnectionCommand) error {
	if domain.ProtocolOpenAIChat.Validate() != nil ||
		!validQuickTestString(command.ModelID, quickTestNameLimit) ||
		!validQuickTestString(command.ChannelName, quickTestNameLimit) ||
		!validQuickTestString(command.APIKey, quickTestAPIKeyLimit) ||
		!validQuickTestString(command.BaseURL, quickTestURLLimit) {
		return catalog.ErrInvalid
	}
	if command.ExistingModelID == "" {
		if !validQuickTestString(command.ModelName, quickTestNameLimit) {
			return catalog.ErrInvalid
		}
	} else if !domain.IsUUID(command.ExistingModelID) {
		return catalog.ErrInvalid
	}
	parsed, err := url.Parse(command.BaseURL)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.RawPath != "" ||
		strings.Contains(command.BaseURL, "\\") || strings.HasSuffix(command.BaseURL, "/") ||
		strings.HasSuffix(parsed.Path, "/chat/completions") {
		return catalog.ErrInvalid
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && quickTestLoopbackHost(parsed.Hostname())) {
		return catalog.ErrInvalid
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return catalog.ErrInvalid
		}
	}
	return nil
}

func validQuickTestString(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func quickTestLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func quickTestConnectionConflicts(snapshot catalog.Snapshot, command SaveQuickTestConnectionCommand) bool {
	if command.ExistingModelID == "" {
		for _, model := range snapshot.Models {
			if strings.EqualFold(model.Name, command.ModelName) {
				return true
			}
		}
	}
	for _, channel := range snapshot.Channels {
		if strings.EqualFold(channel.Name, command.ChannelName) ||
			(channel.Protocol == domain.ProtocolOpenAIChat && channel.BaseURL == command.BaseURL) {
			return true
		}
	}
	return false
}

func quickTestCatalogModel(snapshot catalog.Snapshot, id string) (catalog.ModelSummary, bool) {
	for _, model := range snapshot.Models {
		if model.ID == id {
			return model, true
		}
	}
	return catalog.ModelSummary{}, false
}

func validMutationResult(result catalog.MutationResult) bool {
	return domain.IsUUID(result.ID) && result.Revision > 0
}

func safeQuickTestMutationError(err error) error {
	switch {
	case errors.Is(err, catalog.ErrInvalid):
		return catalog.ErrInvalid
	case errors.Is(err, catalog.ErrConflict):
		return catalog.ErrConflict
	case errors.Is(err, catalog.ErrNotFound):
		return catalog.ErrNotFound
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return errQuickTestOperationFailed
	}
}
