package quicktest

import (
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
)

// ValidateArchivedPerformanceReport protects every persistence and reporting
// adapter from malformed or unsafe quick-report documents.
func ValidateArchivedPerformanceReport(report PerformanceReport) (time.Time, error) {
	if report.SchemaVersion != PerformanceSchemaVersion || !domain.IsUUID(report.ReportID) {
		return time.Time{}, errors.New("quick performance report identity is invalid")
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, report.GeneratedAt)
	if err != nil || generatedAt.IsZero() || generatedAt.Location() != time.UTC || generatedAt.Format(time.RFC3339Nano) != report.GeneratedAt {
		return time.Time{}, errors.New("quick performance report timestamp is invalid")
	}
	if !report.Archived || report.ArchiveStatus != PerformanceArchiveArchived {
		return time.Time{}, errors.New("quick performance report is not archived")
	}
	if strings.TrimSpace(report.ModelID) == "" || report.ModelID != strings.TrimSpace(report.ModelID) || strings.TrimSpace(report.BaseURL) == "" {
		return time.Time{}, errors.New("quick performance report connection summary is invalid")
	}
	if !validArchivedBaseURL(report.BaseURL) || strings.TrimRight(report.BaseURL, "/")+"/chat/completions" != report.Endpoint {
		return time.Time{}, errors.New("quick performance report address is invalid")
	}
	if len(report.Samples) > int(MaxPerformanceRequests) {
		return time.Time{}, errors.New("quick performance report exceeds the sample limit")
	}
	if report.Progress.Launched == 0 || report.Progress.Completed != uint64(len(report.Samples)) || report.Metrics.Completed != report.Progress.Completed ||
		report.Progress.Succeeded+report.Progress.Failed != report.Progress.Completed || report.Metrics.Succeeded != report.Progress.Succeeded || report.Metrics.Failed != report.Progress.Failed {
		return time.Time{}, errors.New("quick performance report counts are inconsistent")
	}
	if report.Success != (report.Progress.Phase == "completed" && report.Metrics.Completed > 0 && report.Metrics.Failed == 0) {
		return time.Time{}, errors.New("quick performance report success is inconsistent")
	}
	if !finiteNonNegative(report.Progress.TotalDurationMS) || !finiteNonNegative(report.Metrics.SuccessRatePercent) || report.Metrics.SuccessRatePercent > 100 {
		return time.Time{}, errors.New("quick performance report aggregate is invalid")
	}
	if report.ErrorCode != "" && !safePerformanceErrorCode(report.ErrorCode) {
		return time.Time{}, errors.New("quick performance report error code is unsafe")
	}
	seen := make(map[uint64]struct{}, len(report.Samples))
	var succeeded, failed, timedOut, promptTokens, completionTokens, cachedTokens uint64
	failureCounts := make(map[domain.ErrorCode]uint64)
	for _, sample := range report.Samples {
		if _, duplicate := seen[sample.RequestIndex]; duplicate {
			return time.Time{}, errors.New("quick performance report sample index is duplicated")
		}
		seen[sample.RequestIndex] = struct{}{}
		if sample.HTTPStatus < 0 || sample.HTTPStatus > 999 || !finiteNonNegative(sample.ScheduledOffsetMS) || !finiteNonNegative(sample.StartedOffsetMS) ||
			!finiteNonNegative(sample.FinishedOffsetMS) || !finiteNonNegative(sample.ScheduleLagMS) || !finiteNonNegative(sample.E2EMS) ||
			!finiteNonNegative(sample.TTFTMS) || !finiteNonNegative(sample.TPOTMS) || sample.FinishedOffsetMS < sample.StartedOffsetMS {
			return time.Time{}, errors.New("quick performance report sample measurement is invalid")
		}
		if sample.Success {
			if sample.ErrorCode != "" {
				return time.Time{}, errors.New("successful quick performance sample has an error code")
			}
			succeeded++
			promptTokens += sample.PromptTokens
			completionTokens += sample.CompletionTokens
			cachedTokens += sample.CachedTokens
		} else {
			if strings.TrimSpace(string(sample.ErrorCode)) == "" || !safePerformanceErrorCode(sample.ErrorCode) {
				return time.Time{}, errors.New("failed quick performance sample lacks an error code")
			}
			failed++
			failureCounts[sample.ErrorCode]++
		}
		if sample.TimedOut {
			timedOut++
		}
	}
	if succeeded != report.Metrics.Succeeded || failed != report.Metrics.Failed || timedOut != report.Metrics.TimedOut ||
		promptTokens != report.Metrics.PromptTokens || completionTokens != report.Metrics.CompletionTokens || cachedTokens != report.Metrics.CachedTokens {
		return time.Time{}, errors.New("quick performance report sample totals are inconsistent")
	}
	if !sort.SliceIsSorted(report.Failures, func(left, right int) bool { return report.Failures[left].ErrorCode < report.Failures[right].ErrorCode }) {
		return time.Time{}, errors.New("quick performance report failures are not stable")
	}
	for _, failure := range report.Failures {
		if failure.Count == 0 || !safePerformanceErrorCode(failure.ErrorCode) || failureCounts[failure.ErrorCode] != failure.Count {
			return time.Time{}, errors.New("quick performance report failures are inconsistent")
		}
		delete(failureCounts, failure.ErrorCode)
	}
	if len(failureCounts) != 0 {
		return time.Time{}, errors.New("quick performance report failures are incomplete")
	}
	return generatedAt, nil
}

// ValidatePerformanceArchiveSummary protects the list boundary without
// loading or decoding the potentially large request-sample document.
func ValidatePerformanceArchiveSummary(summary PerformanceArchiveSummary) (time.Time, error) {
	if !domain.IsUUID(summary.ReportID) {
		return time.Time{}, errors.New("quick performance summary identity is invalid")
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, summary.GeneratedAt)
	if err != nil || generatedAt.IsZero() || generatedAt.Location() != time.UTC || generatedAt.Format(time.RFC3339Nano) != summary.GeneratedAt {
		return time.Time{}, errors.New("quick performance summary timestamp is invalid")
	}
	if strings.TrimSpace(summary.ModelID) == "" || summary.ModelID != strings.TrimSpace(summary.ModelID) || !validArchivedBaseURL(summary.BaseURL) {
		return time.Time{}, errors.New("quick performance summary connection is invalid")
	}
	if summary.Completed == 0 || summary.Completed > MaxPerformanceRequests || summary.Failed > summary.Completed {
		return time.Time{}, errors.New("quick performance summary counts are invalid")
	}
	if summary.Phase != load.PhaseCompleted && summary.Phase != load.PhaseCancelled {
		return time.Time{}, errors.New("quick performance summary phase is invalid")
	}
	if summary.Success != (summary.Phase == load.PhaseCompleted && summary.Failed == 0) {
		return time.Time{}, errors.New("quick performance summary success is inconsistent")
	}
	return generatedAt, nil
}

func validArchivedBaseURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == "" && strings.TrimRight(value, "/") == value
}

func safePerformanceErrorCode(code domain.ErrorCode) bool {
	switch code {
	case ErrorInvalidRequest, ErrorInsecureEndpoint, ErrorCredentialRequired, ErrorAuthenticationFailed,
		load.ErrorNetwork, load.ErrorTimeout, load.ErrorCancelled, load.ErrorHTTP, load.ErrorRateLimited,
		load.ErrorProtocol, load.ErrorIncompleteStream, load.ErrorSemanticEmpty, load.ErrorResponseTooLarge,
		load.ErrorClientClosed, load.ErrorSchedulerOverload, load.ErrorExecutorPanic, load.ErrorRequestFailed,
		load.ErrorUnclassified:
		return true
	default:
		return false
	}
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
