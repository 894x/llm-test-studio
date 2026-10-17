package bridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobfiles"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

var (
	ErrDesktopNotStarted           = errors.New("desktop application has not started")
	ErrDesktopStartup              = errors.New("desktop application startup failed")
	ErrDesktopStopped              = errors.New("desktop application is shutting down or stopped")
	ErrWorkspaceUnavailable        = errors.New("workspace query is unavailable")
	ErrCatalogUnavailable          = errors.New("catalog query is unavailable")
	ErrReportingUnavailable        = errors.New("reporting query is unavailable")
	ErrQuickTaskServiceUnavailable = errors.New("run commands are unavailable")
	ErrComparisonUnavailable       = errors.New("comparison service is unavailable")
	ErrDiagnosticsUnavailable      = errors.New("desktop diagnostics are unavailable")
	ErrQuickTestUnavailable        = errors.New("quick test service is unavailable")
	ErrRemoteNodesUnavailable      = errors.New("remote node manager is unavailable")
	ErrCatalogSavedRefreshFailed   = errors.New("catalog mutation was saved but the refreshed snapshot is unavailable")
	ErrInvalidIdentifier           = errors.New("desktop command identifier is invalid")
)

const (
	desktopCodeNotStarted                = "desktop_not_started"
	desktopCodeStartupFailed             = "desktop_startup_failed"
	DesktopCodeStopped                   = "desktop_stopped"
	desktopCodeWorkspaceMissing          = "workspace_unavailable"
	desktopCodeCatalogMissing            = "catalog_unavailable"
	desktopCodeReportsMissing            = "reports_unavailable"
	desktopCodeCommandsMissing           = "run_commands_unavailable"
	desktopCodeComparisonMissing         = "comparison_unavailable"
	desktopCodeDiagnosticsMissing        = "diagnostics_unavailable"
	desktopCodeQuickTestMissing          = "quick_test_unavailable"
	desktopCodeInvalidIdentifier         = "invalid_identifier"
	desktopCodeOperationCancelled        = "operation_cancelled"
	desktopCodeOperationFailed           = "operation_failed"
	desktopCodeRunInvalid                = "run_invalid"
	desktopCodeRunNotRunnable            = "run_not_runnable"
	desktopCodeRunNotActive              = "run_not_active"
	desktopCodeQuickTaskCredential       = "quick_task_credential_unavailable"
	desktopCodePlanProtocolMismatch      = "plan_protocol_mismatch"
	desktopCodeCatalogInvalid            = "catalog_invalid"
	desktopCodeCatalogConflict           = "catalog_revision_conflict"
	desktopCodeCatalogNotFound           = "catalog_not_found"
	desktopCodeCatalogSavedRefreshFailed = "catalog_saved_refresh_failed"
	desktopCodeComparisonInvalid         = "comparison_invalid"
	desktopCodeComparisonNotReady        = "comparison_not_ready"
	desktopCodeComparisonTargetUnmapped  = "comparison_target_unmapped"
	desktopCodeComparisonChannelNotReady = "comparison_channel_not_ready"
	desktopCodeRemoteNodesMissing        = "remote_nodes_unavailable"
	DesktopCodeRemoteNodeInvalid         = "remote_node_invalid"
	desktopCodeRemoteNodeConflict        = "remote_node_revision_conflict"
	DesktopCodeRemoteNodeNotFound        = "remote_node_not_found"
	desktopCodeRemoteNodeRePair          = "remote_node_re_pair_required"
	DesktopCodeRemoteNodeReferenced      = "remote_node_still_referenced"
	DesktopCodeRemoteNodeUsageUnknown    = "remote_node_usage_unavailable"
	desktopCodeRemoteNodeCredential      = "remote_node_credential_unavailable"
	desktopCodeRemoteNodeBusy            = "remote_node_busy"
	desktopCodeRemoteNodeUnconfirmed     = "remote_node_commit_unconfirmed"
	desktopCodeRemoteNodeUpgrade         = "remote_node_upgrade_required"
	desktopCodeRemoteNodeIdentity        = "remote_node_identity_conflict"
)

// DesktopBindingError is the complete error surface exposed to JavaScript.
// It intentionally does not unwrap internal errors, so provider, credential,
// filesystem, and database details cannot cross the Wails boundary.
type DesktopBindingError struct {
	Code string `json:"code"`
}

func (err DesktopBindingError) Error() string {
	if isDesktopBindingCode(err.Code) {
		return err.Code
	}
	return desktopCodeOperationFailed
}

func (err catalogCommandDiagnosticError) Error() string {
	return fmt.Sprintf("%s: %v", err.operation, err.err)
}

func (app *DesktopApp) safeBindingError(internal error) error {
	if internal == nil {
		return nil
	}
	if app != nil && !errors.Is(internal, ErrDesktopStartup) {
		app.mu.Lock()
		report := app.reportError
		app.mu.Unlock()
		if report != nil {
			report(internal)
		}
	}

	switch {
	case errors.Is(internal, jobfiles.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeFileInputInvalid}
	case errors.Is(internal, jobfiles.ErrTooLarge):
		return DesktopBindingError{Code: desktopCodeFileInputTooLarge}
	case errors.Is(internal, jobfiles.ErrLimit):
		return DesktopBindingError{Code: desktopCodeFileInputLimit}
	case errors.Is(internal, jobfiles.ErrBusy):
		return DesktopBindingError{Code: desktopCodeFileInputBusy}
	case errors.Is(internal, jobfiles.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeFileInputNotFound}
	case errors.Is(internal, jobfiles.ErrClosed):
		return DesktopBindingError{Code: desktopCodeFileInputUnavailable}
	case errors.Is(internal, ErrJobCommitUnconfirmed):
		return DesktopBindingError{Code: desktopCodeJobCommitUnconfirmed}
	case errors.Is(internal, ErrJobQueryUnavailable):
		return DesktopBindingError{Code: desktopCodeJobQueryUnavailable}
	case errors.Is(internal, ErrJobCommandsUnavailable):
		return DesktopBindingError{Code: desktopCodeJobCommandsUnavailable}
	case errors.Is(internal, jobs.ErrUpgradeRequired):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUpgrade}
	case errors.Is(internal, jobs.ErrTargetChanged):
		return DesktopBindingError{Code: desktopCodeRemoteNodeRePair}
	case errors.Is(internal, jobs.ErrPending):
		return DesktopBindingError{Code: desktopCodeJobPending}
	case errors.Is(internal, jobs.ErrConflict):
		return DesktopBindingError{Code: desktopCodeJobConflict}
	case errors.Is(internal, jobs.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeJobNotFound}
	case errors.Is(internal, jobs.ErrUnavailable):
		return DesktopBindingError{Code: desktopCodeJobUnavailable}
	case errors.Is(internal, jobs.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeJobInvalid}
	case errors.Is(internal, ErrDesktopNotStarted):
		return DesktopBindingError{Code: desktopCodeNotStarted}
	case errors.Is(internal, ErrDesktopStartup):
		return DesktopBindingError{Code: desktopCodeStartupFailed}
	case errors.Is(internal, ErrDesktopStopped):
		return DesktopBindingError{Code: DesktopCodeStopped}
	case errors.Is(internal, ErrWorkspaceUnavailable):
		return DesktopBindingError{Code: desktopCodeWorkspaceMissing}
	case errors.Is(internal, ErrCatalogUnavailable):
		return DesktopBindingError{Code: desktopCodeCatalogMissing}
	case errors.Is(internal, ErrReportingUnavailable):
		return DesktopBindingError{Code: desktopCodeReportsMissing}
	case errors.Is(internal, ErrQuickTaskServiceUnavailable):
		return DesktopBindingError{Code: desktopCodeCommandsMissing}
	case errors.Is(internal, ErrComparisonUnavailable):
		return DesktopBindingError{Code: desktopCodeComparisonMissing}
	case errors.Is(internal, ErrDiagnosticsUnavailable):
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	case errors.Is(internal, ErrQuickTestUnavailable):
		return DesktopBindingError{Code: desktopCodeQuickTestMissing}
	case errors.Is(internal, ErrRemoteNodesUnavailable):
		return DesktopBindingError{Code: desktopCodeRemoteNodesMissing}
	case errors.Is(internal, remotenodes.ErrInvalid):
		return DesktopBindingError{Code: DesktopCodeRemoteNodeInvalid}
	case errors.Is(internal, remotenodes.ErrUpgradeIdentity):
		return DesktopBindingError{Code: desktopCodeRemoteNodeIdentity}
	case errors.Is(internal, remotenodes.ErrConflict):
		return DesktopBindingError{Code: desktopCodeRemoteNodeConflict}
	case errors.Is(internal, remotenodes.ErrNotFound):
		return DesktopBindingError{Code: DesktopCodeRemoteNodeNotFound}
	case errors.Is(internal, remotenodes.ErrRePairRequired), errors.Is(internal, remotenodes.ErrDuplicate):
		return DesktopBindingError{Code: desktopCodeRemoteNodeRePair}
	case errors.Is(internal, remotenodes.ErrReferenced):
		return DesktopBindingError{Code: DesktopCodeRemoteNodeReferenced}
	case errors.Is(internal, remotenodes.ErrUsageUnknown):
		return DesktopBindingError{Code: DesktopCodeRemoteNodeUsageUnknown}
	case errors.Is(internal, remotenodes.ErrCredential):
		return DesktopBindingError{Code: desktopCodeRemoteNodeCredential}
	case errors.Is(internal, remotenodes.ErrBusy), errors.Is(internal, runnerinstall.ErrBusy):
		return DesktopBindingError{Code: desktopCodeRemoteNodeBusy}
	case errors.Is(internal, remotenodes.ErrCommitUnconfirmed):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUnconfirmed}
	case errors.Is(internal, remotenodes.ErrUnsupported), errors.Is(internal, runnerinstall.ErrUpgradeRequired):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUpgrade}
	case errors.Is(internal, ErrInvalidIdentifier):
		return DesktopBindingError{Code: desktopCodeInvalidIdentifier}
	case errors.Is(internal, ErrCatalogSavedRefreshFailed):
		return DesktopBindingError{Code: desktopCodeCatalogSavedRefreshFailed}
	case errors.Is(internal, catalog.ErrPlanProtocolMismatch):
		return DesktopBindingError{Code: desktopCodePlanProtocolMismatch}
	case errors.Is(internal, catalog.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeCatalogInvalid}
	case errors.Is(internal, catalog.ErrConflict):
		return DesktopBindingError{Code: desktopCodeCatalogConflict}
	case errors.Is(internal, catalog.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeCatalogNotFound}
	case errors.Is(internal, comparisons.ErrTargetUnmapped):
		return DesktopBindingError{Code: desktopCodeComparisonTargetUnmapped}
	case errors.Is(internal, comparisons.ErrChannelNotReady):
		return DesktopBindingError{Code: desktopCodeComparisonChannelNotReady}
	case errors.Is(internal, comparisons.ErrNotReady):
		return DesktopBindingError{Code: desktopCodeComparisonNotReady}
	case errors.Is(internal, comparisons.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeComparisonInvalid}
	case errors.Is(internal, runs.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeRunInvalid}
	case errors.Is(internal, runs.ErrNotRunnable):
		return DesktopBindingError{Code: desktopCodeRunNotRunnable}
	case errors.Is(internal, runs.ErrNotActive):
		return DesktopBindingError{Code: desktopCodeRunNotActive}
	case errors.Is(internal, runs.ErrClosed):
		return DesktopBindingError{Code: desktopCodeCommandsMissing}
	case errors.Is(internal, runs.ErrQuickTaskCredential):
		return DesktopBindingError{Code: desktopCodeQuickTaskCredential}
	case errors.Is(internal, context.Canceled), errors.Is(internal, context.DeadlineExceeded):
		return DesktopBindingError{Code: desktopCodeOperationCancelled}
	default:
		return DesktopBindingError{Code: desktopCodeOperationFailed}
	}
}

func isDesktopBindingCode(code string) bool {
	switch code {
	case desktopCodeFileInputInvalid, desktopCodeFileInputTooLarge, desktopCodeFileInputLimit,
		desktopCodeFileInputBusy, desktopCodeFileInputNotFound, desktopCodeFileInputUnavailable:
		return true
	case desktopCodeJobInvalid, desktopCodeJobNotFound, desktopCodeJobConflict, desktopCodeJobUnavailable,
		desktopCodeJobPending, desktopCodeJobCommitUnconfirmed, desktopCodeJobQueryUnavailable, desktopCodeJobCommandsUnavailable,
		desktopCodeNotStarted,
		desktopCodeStartupFailed,
		DesktopCodeStopped,
		desktopCodeWorkspaceMissing,
		desktopCodeCatalogMissing,
		desktopCodeReportsMissing,
		desktopCodeCommandsMissing,
		desktopCodeComparisonMissing,
		desktopCodeDiagnosticsMissing,
		desktopCodeQuickTestMissing,
		desktopCodeInvalidIdentifier,
		desktopCodeOperationCancelled,
		desktopCodeOperationFailed,
		desktopCodeRunInvalid,
		desktopCodeRunNotRunnable,
		desktopCodeRunNotActive,
		desktopCodeQuickTaskCredential,
		desktopCodePlanProtocolMismatch,
		desktopCodeCatalogInvalid,
		desktopCodeCatalogConflict,
		desktopCodeCatalogNotFound,
		desktopCodeCatalogSavedRefreshFailed,
		desktopCodeComparisonInvalid,
		desktopCodeComparisonNotReady,
		desktopCodeComparisonTargetUnmapped,
		desktopCodeComparisonChannelNotReady,
		desktopCodeRemoteNodesMissing,
		DesktopCodeRemoteNodeInvalid,
		desktopCodeRemoteNodeConflict,
		DesktopCodeRemoteNodeNotFound,
		desktopCodeRemoteNodeRePair,
		DesktopCodeRemoteNodeReferenced,
		DesktopCodeRemoteNodeUsageUnknown,
		desktopCodeRemoteNodeCredential,
		desktopCodeRemoteNodeBusy,
		desktopCodeRemoteNodeUnconfirmed,
		desktopCodeRemoteNodeUpgrade,
		desktopCodeRemoteNodeIdentity:
		return true
	default:
		return false
	}
}
