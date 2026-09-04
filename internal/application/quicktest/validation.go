package quicktest

import (
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

// ValidateArchivedPerformanceReport protects every persistence and reporting
// adapter from malformed or unsafe quick-report documents.
func ValidateArchivedPerformanceReport(report PerformanceReport) (time.Time, error) {
	if (report.SchemaVersion != LegacyPerformanceSchemaVersion && report.SchemaVersion != PerformanceSchemaVersion) || !domain.IsUUID(report.ReportID) {
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
	arrival := normalizedArrivalPattern(report.Profile.ArrivalPattern)
	workloadMode := normalizedWorkloadMode(report.Profile.WorkloadMode)
	if report.SchemaVersion == PerformanceSchemaVersion {
		if !validArchivedPerformanceProfile(report.Profile) || report.Progress.Offered != report.Progress.Launched+report.Progress.Rejected ||
			report.Progress.Completed != report.Progress.Offered {
			return time.Time{}, errors.New("quick performance report load profile is invalid")
		}
		if !finiteNonNegative(report.Progress.SendDurationMS) || !finiteNonNegative(report.Progress.DrainDurationMS) ||
			report.Progress.SendDurationMS <= 0 || !approximatelyEqual(report.Progress.TotalDurationMS, report.Progress.SendDurationMS+report.Progress.DrainDurationMS) {
			return time.Time{}, errors.New("quick performance report timing windows are invalid")
		}
		if !finiteNonNegative(report.Metrics.OfferedQPS) || !finiteNonNegative(report.Metrics.LaunchedQPS) ||
			!finiteNonNegative(report.Metrics.CompletedQPS) || !finiteNonNegative(report.Metrics.SuccessfulRequestQPS) ||
			report.Progress.TotalDurationMS <= 0 || report.Metrics.OfferedQPS <= 0 || report.Metrics.LaunchedQPS <= 0 ||
			report.Metrics.CompletedQPS <= 0 || report.Metrics.LaunchedQPS > report.Metrics.OfferedQPS ||
			report.Metrics.SuccessfulRequestQPS > report.Metrics.CompletedQPS {
			return time.Time{}, errors.New("quick performance report throughput is invalid")
		}
		seconds := report.Progress.TotalDurationMS / 1_000
		if !approximatelyEqual(report.Metrics.CompletedQPS, float64(report.Progress.Launched)/seconds) ||
			!approximatelyEqual(report.Metrics.SuccessfulRequestQPS, float64(report.Progress.Succeeded)/seconds) ||
			!approximatelyEqual(report.Metrics.RequestQPS, float64(report.Progress.Completed)/seconds) ||
			!approximatelyEqual(report.Metrics.RPM, report.Metrics.RequestQPS*60) ||
			!approximatelyEqual(report.Metrics.LaunchedQPS/report.Metrics.OfferedQPS, float64(report.Progress.Launched)/float64(report.Progress.Offered)) {
			return time.Time{}, errors.New("quick performance report throughput is inconsistent")
		}
		if report.Profile.LoadMode == domain.LoadFixedConcurrency {
			sendSeconds := report.Progress.SendDurationMS / 1_000
			if !approximatelyEqual(report.Metrics.OfferedQPS, float64(report.Progress.Offered)/sendSeconds) ||
				!approximatelyEqual(report.Metrics.LaunchedQPS, report.Metrics.OfferedQPS) {
				return time.Time{}, errors.New("quick performance report fixed-concurrency throughput is inconsistent")
			}
		} else {
			rateWindowSeconds := report.Progress.SendDurationMS / 1_000
			countLimitReached := !report.Progress.Stopped && report.Profile.RequestCount > 0 && report.Progress.Offered >= report.Profile.RequestCount
			useNominalCountWindow := arrival != load.ArrivalPoisson || report.Progress.Offered == 1
			if countLimitReached && useNominalCountWindow {
				minimumScheduleWindow := float64(report.Progress.Offered) / report.Profile.RatePerSecond
				if rateWindowSeconds < minimumScheduleWindow {
					rateWindowSeconds = minimumScheduleWindow
				}
			}
			if !approximatelyEqual(report.Metrics.OfferedQPS, float64(report.Progress.Offered)/rateWindowSeconds) ||
				!approximatelyEqual(report.Metrics.LaunchedQPS, float64(report.Progress.Launched)/rateWindowSeconds) {
				return time.Time{}, errors.New("quick performance report open-loop throughput is inconsistent")
			}
		}
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
	var workload *performanceWorkload
	if report.SchemaVersion == PerformanceSchemaVersion && workloadMode == PerformanceWorkloadNormal {
		workload, err = newPerformanceWorkload(report.Profile)
		if err != nil {
			return time.Time{}, errors.New("quick performance report workload is invalid")
		}
	}
	var succeeded, failed, timedOut, promptTokens, completionTokens, cachedTokens uint64
	evidenceBytes := 0
	failureCounts := make(map[domain.ErrorCode]uint64)
	for _, sample := range report.Samples {
		if _, duplicate := seen[sample.RequestIndex]; duplicate {
			return time.Time{}, errors.New("quick performance report sample index is duplicated")
		}
		seen[sample.RequestIndex] = struct{}{}
		if workload == nil {
			if sample.TargetInputTokens != 0 || sample.TargetOutputTokens != 0 {
				return time.Time{}, errors.New("fixed quick performance sample has workload targets")
			}
		} else {
			target := workload.target(sample.RequestIndex)
			if sample.TargetInputTokens != target.InputTokens || sample.TargetOutputTokens != target.OutputTokens {
				return time.Time{}, errors.New("quick performance sample workload target is inconsistent")
			}
		}
		if sample.HTTPStatus < 0 || sample.HTTPStatus > 999 || !finiteNonNegative(sample.ScheduledOffsetMS) || !finiteNonNegative(sample.StartedOffsetMS) ||
			!finiteNonNegative(sample.FinishedOffsetMS) || !finiteNonNegative(sample.ScheduleLagMS) || !finiteNonNegative(sample.E2EMS) ||
			!finiteNonNegative(sample.TTFTMS) || !finiteNonNegative(sample.TPOTMS) || sample.FinishedOffsetMS < sample.StartedOffsetMS {
			return time.Time{}, errors.New("quick performance report sample measurement is invalid")
		}
		if sample.Success {
			if sample.ErrorCode != "" || sample.ResponseEvidence != nil {
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
			if sample.ResponseEvidence != nil {
				if err := validatePerformanceResponseEvidence(*sample.ResponseEvidence); err != nil {
					return time.Time{}, err
				}
				evidenceBytes += len(sample.ResponseEvidence.Body)
				if evidenceBytes > MaxPerformanceEvidenceTotalBytes {
					return time.Time{}, errors.New("quick performance response evidence exceeds the total limit")
				}
			}
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

func validArchivedPerformanceProfile(profile PerformanceProfile) bool {
	return validPerformanceProfileValues(profile)
}

func validatePerformanceResponseEvidence(evidence PerformanceResponseEvidence) error {
	if !evidence.Redacted || len(evidence.ContentType) > 128 || len(evidence.RequestID) > 256 ||
		!utf8.ValidString(evidence.Body) || len(evidence.Body) > MaxPerformanceEvidenceBodyBytes {
		return errors.New("quick performance response evidence is unsafe")
	}
	switch evidence.CaptureStatus {
	case PerformanceEvidenceCaptured:
		if strings.TrimSpace(evidence.Body) == "" {
			return errors.New("captured quick performance response evidence is empty")
		}
	case PerformanceEvidenceEmpty:
		if evidence.Body != "" || evidence.Truncated {
			return errors.New("empty quick performance response evidence is inconsistent")
		}
	case PerformanceEvidenceOmitted:
		if evidence.Body != "" || !evidence.Truncated {
			return errors.New("omitted quick performance response evidence is inconsistent")
		}
	default:
		return errors.New("quick performance response evidence status is invalid")
	}
	return nil
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

func approximatelyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= scale*1e-9
}
