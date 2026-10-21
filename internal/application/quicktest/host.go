package quicktest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/execution/openai"
	"github.com/894x/llm-test-studio/internal/protocol"
)

type HostDependencies struct {
	Transport http.RoundTripper
	Archive   PerformanceArchive
	Clock     PerformanceClock
}

// Host executes prepared performance input without desktop connection resolvers.
type Host struct {
	transport http.RoundTripper
	archive   PerformanceArchive
	clock     PerformanceClock
}

type ExecutionOptions struct {
	OnProgress  func(PerformanceProgress)
	StopSending <-chan struct{}
	// OnCheckpoint is synchronous, owns its snapshot and must honor its storage
	// context. Failure stops execution before another phase can send requests.
	OnCheckpoint func(context.Context, PerformanceCheckpoint) error
}

type performanceControl struct {
	reportID    string
	stop        <-chan struct{}
	onProgress  func(PerformanceProgress)
	checkpoints *performanceCheckpointObserver
}

func NewHost(dependencies HostDependencies) *Host {
	clock := dependencies.Clock
	if clock == nil {
		clock = systemPerformanceClock{}
	}
	return &Host{
		transport: dependencies.Transport, archive: dependencies.Archive,
		clock: clock,
	}
}

func (service *Host) ExecutePrepared(
	ctx context.Context,
	prepared PreparedPerformance,
	lease *credentials.Lease,
	options ExecutionOptions,
) (report PerformanceReport, executionErr error) {
	defer lease.Close()
	if service == nil {
		return PerformanceReport{}, ErrServiceUnavailable
	}
	invalid := ctx == nil || lease == nil
	if invalid || prepared.Validate() != nil {
		return PerformanceReport{}, ErrInvalidPreparedPerformance
	}
	report = prepared.report()
	checkpoints := &performanceCheckpointObserver{
		callback: options.OnCheckpoint, stage: PerformanceStagePreparing,
	}
	defer func() {
		if executionErr == nil {
			executionErr = checkpoints.emit(ctx, PerformanceCheckpoint{
				Stage: checkpoints.stage, Boundary: PerformanceBoundaryReturned,
				RungIndex: checkpoints.rung, Report: report,
			})
		}
	}()
	if err := checkpoints.emit(ctx, PerformanceCheckpoint{
		Stage: PerformanceStagePreparing, Boundary: PerformanceBoundaryEntered, Report: report,
	}); err != nil {
		return report, err
	}
	address := normalizedAddress{baseURL: prepared.BaseURL, endpoint: prepared.Endpoint}
	onProgress := options.OnProgress
	control := performanceControl{
		reportID: prepared.ReportID, stop: options.StopSending, onProgress: onProgress, checkpoints: checkpoints,
	}
	requestBudget, measuredCap, budgetErr := preparePerformanceProfile(report.Profile)
	if budgetErr != nil {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}
	report.RequestBudget = requestBudget
	perRunCap := measuredCap
	if report.Profile.CapacityEnabled {
		perRunCap = report.Profile.RequestCount
	}
	profile := performanceLoadProfile(report.Profile, perRunCap)
	if err := profile.Validate(); err != nil {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}
	var (
		body     json.RawMessage
		workload *performanceWorkload
		err      error
	)
	var affinityKey string
	if prepared.Profile.AffinityEnabled {
		affinityKey = rand.Text()
	}
	if prepared.Profile.WorkloadMode == PerformanceWorkloadNormal || prepared.Profile.RandomInput {
		workload, err = newPerformanceWorkload(report.Profile)
		if err != nil {
			report.ErrorCode = ErrorInvalidRequest
			return report, nil
		}
		workload.affinityKey = affinityKey
	} else {
		prompt, promptErr := newFixedPerformancePrompt(prepared.Profile.InputTokens)
		if promptErr != nil {
			report.ErrorCode = load.ErrorRequestFailed
			return report, nil
		}
		body, err = performanceBody(
			prepared.Protocol,
			prompt,
			prepared.Profile.OutputTokens,
			affinityKey,
		)
		if err != nil {
			report.ErrorCode = load.ErrorRequestFailed
			return report, nil
		}
	}

	evidenceRecorder := newPerformanceEvidenceRecorder()
	executor, cleanup, code := service.performanceExecutor(ctx, performanceExecutionConfig{
		Protocol: prepared.Protocol, Address: address, Credential: lease, Model: prepared.ModelID,
		Body: body, Workload: workload, AffinityKey: affinityKey, OnFailure: evidenceRecorder.record,
	})
	if code != "" {
		report.ErrorCode = code
		return report, nil
	}
	defer cleanup()
	executor = streamingPerformanceExecutor(executor)
	if err := checkpoints.emit(ctx, PerformanceCheckpoint{
		Stage: PerformanceStagePreparing, Boundary: PerformanceBoundaryCompleted, Report: report,
	}); err != nil {
		return report, err
	}

	if prepared.Profile.WarmupRequests > 0 {
		if err := checkpoints.emit(ctx, PerformanceCheckpoint{
			Stage: PerformanceStageWarmup, Boundary: PerformanceBoundaryEntered, Report: report,
		}); err != nil {
			return report, err
		}
		warmupProfile := performanceWarmupProfile(report.Profile)
		warmupOutcome, warmupErr := load.Run(
			ctx,
			warmupProfile,
			namespacedPerformanceExecutor(executor, performanceWarmupRequestIndexBase),
			load.Options{
				MaxScheduledRequests: prepared.Profile.WarmupRequests,
				StopSending:          options.StopSending,
				OnProgress:           performancePhaseProgressCallback(onProgress, PerformancePhaseWarmingUp),
			},
		)
		report.Warmup = performanceTrafficSummary(prepared.Profile.WarmupRequests, warmupOutcome)
		finished := control.preparationRunFailed(
			ctx,
			warmupOutcome,
			warmupErr,
			&report,
		)
		if err := checkpoints.emit(ctx, PerformanceCheckpoint{
			Stage: PerformanceStageWarmup, Boundary: PerformanceBoundaryCompleted, Report: report,
		}); err != nil {
			return report, err
		}
		if finished || control.preparationRunFailed(
			ctx,
			warmupOutcome,
			warmupErr,
			&report,
		) {
			return report, nil
		}
	}

	if prepared.Profile.RampDurationMS > 0 {
		if err := checkpoints.emit(ctx, PerformanceCheckpoint{
			Stage: PerformanceStageRamp, Boundary: PerformanceBoundaryEntered, Report: report,
		}); err != nil {
			return report, err
		}
		rampProfile := performanceRampProfile(report.Profile, requestBudget.RampCap)
		rampOptions := load.Options{
			Ramp:                 true,
			StopSending:          options.StopSending,
			MaxScheduledRequests: requestBudget.RampCap,
			ArrivalPattern:       prepared.Profile.ArrivalPattern,
			RandomSeed:           prepared.Profile.RandomSeed,
			OnProgress:           performancePhaseProgressCallback(onProgress, PerformancePhaseRamping),
		}
		if prepared.Profile.LoadMode == domain.LoadOpenLoop {
			rampOptions.MaxOpenLoopInFlight = uint64(prepared.Profile.MaxInFlight)
		}
		rampOutcome, rampErr := load.Run(
			ctx,
			rampProfile,
			namespacedPerformanceExecutor(executor, performanceRampRequestIndexBase),
			rampOptions,
		)
		rampTraffic := performanceTrafficSummary(requestBudget.RampCap, rampOutcome)
		report.Ramp = &PerformanceRampSummary{
			Shape:           "linear_staircase",
			DurationMS:      prepared.Profile.RampDurationMS,
			Steps:           load.LinearRampStepCount(rampProfile),
			CompletedWindow: rampOutcome.Progress.Phase == load.PhaseCompleted && !rampOutcome.Progress.Stopped && !rampOutcome.Progress.Capped,
			Traffic:         *rampTraffic,
		}
		if prepared.Profile.LoadMode == domain.LoadFixedConcurrency {
			report.Ramp.TargetConcurrency = prepared.Profile.Concurrency
		} else {
			report.Ramp.TargetRatePerSecond = prepared.Profile.RatePerSecond
		}
		finished := control.preparationRunFailed(
			ctx,
			rampOutcome,
			rampErr,
			&report,
		)
		if err := checkpoints.emit(ctx, PerformanceCheckpoint{
			Stage: PerformanceStageRamp, Boundary: PerformanceBoundaryCompleted, Report: report,
		}); err != nil {
			return report, err
		}
		if finished || control.preparationRunFailed(
			ctx,
			rampOutcome,
			rampErr,
			&report,
		) {
			return report, nil
		}
	}

	if report.Profile.CapacityEnabled {
		return service.runPerformanceCapacity(
			ctx,
			report,
			executor,
			evidenceRecorder,
			workload,
			control,
		)
	}

	if err := checkpoints.emit(ctx, PerformanceCheckpoint{
		Stage: PerformanceStageMeasured, Boundary: PerformanceBoundaryEntered, Report: report,
	}); err != nil {
		return report, err
	}
	evidenceRecorder.enableFresh()
	loadOptions := performanceLoadOptions(report.Profile, measuredCap, onProgress)
	loadOptions.StopSending = options.StopSending
	outcome, runErr := load.Run(
		ctx,
		profile,
		executor,
		loadOptions,
	)
	outcome.Progress = performanceMeasuredProgress(report.Profile, measuredCap, outcome.Progress)
	report.Progress = performanceProgress(outcome.Progress)
	report.Metrics = outcome.Metrics
	report.Failures = performanceFailures(outcome.Results)
	report.Samples = performanceSamples(outcome.Results, evidenceRecorder.snapshot(), workload)
	report.SLOAssessment = buildPerformanceSLOAssessment(report.Profile, report.Samples, report.Progress)
	if prepared.Profile.SliceDurationMS > 0 {
		report.TimeSlices = buildPerformanceTimeSlices(
			outcome.Results,
			outcome.Progress.TotalDuration,
			time.Duration(prepared.Profile.SliceDurationMS)*time.Millisecond,
		)
	}
	report.Success = outcome.Progress.Phase == load.PhaseCompleted && outcome.Metrics.Completed > 0 && outcome.Metrics.Failed == 0
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			report.ErrorCode = classifyContext(ctxErr)
		} else {
			report.ErrorCode = load.ErrorRequestFailed
		}
	}
	if err := checkpoints.emit(ctx, PerformanceCheckpoint{
		Stage: PerformanceStageMeasured, Boundary: PerformanceBoundaryCompleted, Report: report,
	}); err != nil {
		return report, err
	}
	if outcome.Progress.Launched > 0 {
		service.archivePerformanceReport(ctx, &report, prepared.ReportID)
	}
	return report, nil
}

func streamingPerformanceExecutor(executor load.Executor) load.Executor {
	return func(ctx context.Context, request load.Request) load.Observation {
		observation := executor(ctx, request)
		observation.Streaming = true
		observation.TTFTObserved = observation.TTFTObserved || observation.SemanticChunkCount > 0 || observation.TTFT > 0 || observation.TTFTAny > 0
		if observation.TTFTAny <= 0 {
			observation.TTFTAny = observation.TTFT
		}
		observation.TTFT = observation.TTFTAny
		if observation.TTFTAny > 0 && observation.SemanticChunkCount == 0 {
			observation.SemanticChunkCount = 1
		}
		return observation
	}
}

type performanceCapacityProjection struct {
	success       bool
	progress      PerformanceProgress
	metrics       load.Metrics
	failures      []PerformanceFailure
	samples       []PerformanceSample
	timeSlices    []PerformanceTimeSlice
	sloAssessment *PerformanceSLOAssessment
	runErr        error
}

func (service *Host) runPerformanceCapacity(
	ctx context.Context,
	report PerformanceReport,
	executor load.Executor,
	evidenceRecorder *performanceEvidenceRecorder,
	workload *performanceWorkload,
	control performanceControl,
) (PerformanceReport, error) {
	onProgress := control.onProgress
	targets, err := buildPerformanceCapacityTargets(report.Profile)
	if err != nil {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}
	capacity := &PerformanceCapacityResult{
		Status: PerformanceSLONotEvaluated,
		Rungs:  make([]PerformanceCapacityRung, 0, len(targets)),
	}
	report.CapacityResult = capacity
	var selected *performanceCapacityProjection
	var selectedIndex uint32

	for targetIndex, target := range targets {
		rungIndex := uint32(targetIndex)
		if err := control.checkpoints.emit(ctx, PerformanceCheckpoint{
			Stage: PerformanceStageCapacity, Boundary: PerformanceBoundaryEntered,
			RungIndex: &rungIndex, Report: report,
		}); err != nil {
			return report, err
		}
		effectiveProfile := performanceCapacityProfile(report.Profile, target)
		evidenceRecorder.enableFresh()
		progressCallback := capacityProgressCallback(
			onProgress,
			rungIndex,
			uint32(len(targets)),
			target,
		)
		options := performanceLoadOptions(effectiveProfile, effectiveProfile.RequestCount, progressCallback)
		options.StopSending = control.stop
		if workload != nil && workload.profile.RandomInput {
			workload.rungIndex = rungIndex + 1
		}
		outcome, runErr := load.Run(
			ctx,
			performanceLoadProfile(effectiveProfile, effectiveProfile.RequestCount),
			executor,
			options,
		)
		outcome.Progress = performanceMeasuredProgress(effectiveProfile, effectiveProfile.RequestCount, outcome.Progress)
		progress := performanceProgress(outcome.Progress)
		decorateCapacityProgress(
			&progress,
			rungIndex,
			uint32(len(targets)),
			target,
		)
		failures := performanceFailures(outcome.Results)
		samples := performanceSamples(outcome.Results, evidenceRecorder.snapshot(), workload)
		assessment := buildPerformanceSLOAssessment(report.Profile, samples, progress)
		if assessment == nil {
			report.ErrorCode = ErrorInvalidRequest
			return report, nil
		}
		if runErr != nil {
			assessment.Status = PerformanceSLONotEvaluated
		}
		timeSlices := []PerformanceTimeSlice(nil)
		if report.Profile.SliceDurationMS > 0 {
			timeSlices = buildPerformanceTimeSlices(
				outcome.Results,
				outcome.Progress.TotalDuration,
				time.Duration(report.Profile.SliceDurationMS)*time.Millisecond,
			)
		}
		transportSuccess := outcome.Progress.Phase == load.PhaseCompleted && outcome.Metrics.Completed > 0 && outcome.Metrics.Failed == 0
		capacity.Rungs = append(capacity.Rungs, PerformanceCapacityRung{
			Index:         rungIndex,
			Target:        target,
			Success:       transportSuccess,
			Progress:      progress,
			Metrics:       outcome.Metrics,
			Failures:      failures,
			SLOAssessment: *assessment,
		})
		projection := &performanceCapacityProjection{
			success: transportSuccess, progress: progress, metrics: outcome.Metrics,
			failures: failures, samples: samples, timeSlices: timeSlices,
			sloAssessment: assessment, runErr: runErr,
		}

		switch assessment.Status {
		case PerformanceSLOPassed:
			selected = projection
			selectedIndex = rungIndex
			highest := rungIndex
			capacity.HighestPassingRungIndex = &highest
			if targetIndex == len(targets)-1 {
				capacity.Status = PerformanceSLOPassed
			}
		case PerformanceSLOFailed:
			capacity.Status = PerformanceSLOFailed
			if selected == nil {
				selected = projection
				selectedIndex = rungIndex
			}
		case PerformanceSLONotEvaluated:
			capacity.Status = PerformanceSLONotEvaluated
			selected = projection
			selectedIndex = rungIndex
		}
		if ctx.Err() != nil {
			capacity.Status = PerformanceSLONotEvaluated
		}
		applyPerformanceCapacityProjection(
			ctx,
			&report,
			selected,
			selectedIndex,
		)
		if err := control.checkpoints.emit(ctx, PerformanceCheckpoint{
			Stage: PerformanceStageCapacity, Boundary: PerformanceBoundaryCompleted,
			RungIndex: &rungIndex, Report: report,
		}); err != nil {
			return report, err
		}
		if ctx.Err() != nil || assessment.Status != PerformanceSLOPassed || performanceStopRequested(control.stop) {
			break
		}
	}

	if selected == nil {
		report.ErrorCode = load.ErrorRequestFailed
		return report, nil
	}
	if ctx.Err() != nil {
		capacity.Status = PerformanceSLONotEvaluated
	}
	applyPerformanceCapacityProjection(
		ctx,
		&report,
		selected,
		selectedIndex,
	)
	if capacity.Status == PerformanceSLONotEvaluated || ctx.Err() != nil {
		return report, nil
	}
	if report.Progress.Launched > 0 {
		service.archivePerformanceReport(ctx, &report, control.reportID)
	}
	return report, nil
}

func applyPerformanceCapacityProjection(
	ctx context.Context,
	report *PerformanceReport,
	selected *performanceCapacityProjection,
	selectedIndex uint32,
) {
	if selected == nil {
		return
	}
	report.CapacityResult.SelectedRungIndex = &selectedIndex
	report.Success = selected.success
	report.Progress = selected.progress
	report.Metrics = selected.metrics
	report.Failures = selected.failures
	report.Samples = selected.samples
	report.TimeSlices = selected.timeSlices
	report.SLOAssessment = selected.sloAssessment
	if ctxErr := ctx.Err(); ctxErr != nil {
		report.ErrorCode = classifyContext(ctxErr)
		return
	}
	if selected.runErr != nil {
		report.ErrorCode = load.ErrorRequestFailed
	}
}

func capacityProgressCallback(
	onProgress func(PerformanceProgress),
	rungIndex, rungCount uint32,
	target float64,
) func(PerformanceProgress) {
	if onProgress == nil {
		return nil
	}
	return func(progress PerformanceProgress) {
		decorateCapacityProgress(
			&progress,
			rungIndex,
			rungCount,
			target,
		)
		onProgress(progress)
	}
}

func decorateCapacityProgress(progress *PerformanceProgress, rungIndex, rungCount uint32, target float64) {
	progress.CapacityRungNumber = rungIndex + 1
	progress.CapacityRungCount = rungCount
	progress.CapacityTarget = target
}

const (
	performanceRampRequestIndexBase   uint64 = 1 << 62
	performanceWarmupRequestIndexBase uint64 = 1 << 63
)

func performanceLoadProfile(profile PerformanceProfile, measuredCap uint64) domain.LoadProfile {
	result := domain.LoadProfile{
		Mode: profile.LoadMode, Concurrency: profile.Concurrency,
		RequestCount: profile.RequestCount, DurationMS: profile.DurationMS,
		RatePerSecond: profile.RatePerSecond, RequestTimeoutMS: profile.TimeoutMS,
	}
	if result.Mode == domain.LoadOpenLoop {
		// LoadProfile keeps Concurrency mandatory for compatibility, but open-loop
		// admission is controlled independently by MaxInFlight.
		result.Concurrency = 1
	}
	if result.Mode == domain.LoadFixedConcurrency && result.RequestCount == 0 {
		result.RequestCount = measuredCap
	}
	return result
}

func performanceWarmupProfile(profile PerformanceProfile) domain.LoadProfile {
	concurrency := profile.Concurrency
	if profile.LoadMode == domain.LoadOpenLoop {
		concurrency = profile.MaxInFlight
	}
	if uint64(concurrency) > profile.WarmupRequests {
		concurrency = uint32(profile.WarmupRequests)
	}
	return domain.LoadProfile{
		Mode:             domain.LoadFixedConcurrency,
		Concurrency:      concurrency,
		RequestCount:     profile.WarmupRequests,
		RequestTimeoutMS: profile.TimeoutMS,
	}
}

func performanceRampProfile(profile PerformanceProfile, requestCap uint64) domain.LoadProfile {
	result := domain.LoadProfile{
		Mode: profile.LoadMode, Concurrency: profile.Concurrency,
		DurationMS: profile.RampDurationMS, RatePerSecond: profile.RatePerSecond,
		RequestTimeoutMS: profile.TimeoutMS,
	}
	if profile.LoadMode == domain.LoadFixedConcurrency {
		result.RequestCount = requestCap
	} else {
		result.Concurrency = 1
	}
	return result
}

func performanceLoadOptions(profile PerformanceProfile, requestCap uint64, onProgress func(PerformanceProgress)) load.Options {
	options := load.Options{
		MaxScheduledRequests: requestCap,
		ArrivalPattern:       normalizedArrivalPattern(profile.ArrivalPattern),
		RandomSeed:           profile.RandomSeed,
	}
	if profile.LoadMode == domain.LoadOpenLoop {
		options.MaxOpenLoopInFlight = uint64(profile.MaxInFlight)
	}
	if onProgress != nil {
		options.OnProgress = func(progress load.Progress) {
			onProgress(performanceProgress(performanceMeasuredProgress(profile, requestCap, progress)))
		}
	}
	return options
}

func performanceMeasuredProgress(profile PerformanceProfile, requestCap uint64, progress load.Progress) load.Progress {
	if !progress.Capped && performanceMeasuredBudgetCapped(
		profile,
		requestCap,
		progress.Offered,
		durationMilliseconds(progress.SendDuration),
	) {
		progress.Capped = true
	}
	return progress
}

func performanceMeasuredBudgetCapped(profile PerformanceProfile, requestCap, offered uint64, sendDurationMS float64) bool {
	return profile.LoadMode == domain.LoadFixedConcurrency && profile.RequestCount == 0 && profile.DurationMS > 0 &&
		requestCap > 0 && offered >= requestCap && sendDurationMS < float64(profile.DurationMS)
}

func performancePhaseProgressCallback(onProgress func(PerformanceProgress), phase load.Phase) func(load.Progress) {
	if onProgress == nil {
		return nil
	}
	return func(progress load.Progress) {
		converted := performanceProgress(progress)
		if progress.Phase != load.PhaseCancelled {
			converted.Phase = phase
		}
		onProgress(converted)
	}
}

func namespacedPerformanceExecutor(executor load.Executor, base uint64) load.Executor {
	return func(ctx context.Context, request load.Request) load.Observation {
		request.Index += base
		return executor(ctx, request)
	}
}

func (control performanceControl) preparationRunFailed(
	ctx context.Context,
	outcome load.Outcome,
	runErr error,
	report *PerformanceReport,
) bool {
	if ctxErr := ctx.Err(); ctxErr != nil {
		report.Progress = PerformanceProgress{Phase: load.PhaseCancelled, Stopped: true}
		report.ErrorCode = classifyContext(ctxErr)
		return true
	}
	if outcome.Progress.Stopped || performanceStopRequested(control.stop) {
		report.Progress = performanceProgress(outcome.Progress)
		report.Progress.Stopped = true
		return true
	}
	if runErr == nil && outcome.Progress.Phase != load.PhaseCancelled {
		return false
	}
	report.Progress = PerformanceProgress{Phase: outcome.Progress.Phase, Stopped: outcome.Progress.Stopped}
	report.ErrorCode = load.ErrorRequestFailed
	return true
}

func performanceStopRequested(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func performanceTrafficSummary(requestCap uint64, outcome load.Outcome) *PerformanceTrafficSummary {
	return &PerformanceTrafficSummary{
		RequestCap:       requestCap,
		Offered:          outcome.Progress.Offered,
		Launched:         outcome.Progress.Launched,
		Completed:        outcome.Progress.Completed,
		Succeeded:        outcome.Progress.Succeeded,
		Failed:           outcome.Progress.Failed,
		TimedOut:         outcome.Metrics.TimedOut,
		Rejected:         outcome.Progress.Rejected,
		PeakInFlight:     outcome.Progress.PeakInFlight,
		PromptTokens:     outcome.Metrics.PromptTokens,
		CompletionTokens: outcome.Metrics.CompletionTokens,
		CachedTokens:     outcome.Metrics.CachedTokens,
		SendDurationMS:   durationMilliseconds(outcome.Progress.SendDuration),
		DrainDurationMS:  durationMilliseconds(outcome.Progress.DrainDuration),
		TotalDurationMS:  durationMilliseconds(outcome.Progress.TotalDuration),
		Failures:         performanceFailures(outcome.Results),
		Stopped:          outcome.Progress.Stopped,
		Capped:           outcome.Progress.Capped,
	}
}

func (service *Host) archivePerformanceReport(ctx context.Context, report *PerformanceReport, reportID string) {
	if service.archive == nil {
		return
	}
	report.ArchiveStatus = PerformanceArchiveFailed
	generatedAt := service.clock.Now().UTC()
	if !domain.IsUUID(reportID) || generatedAt.IsZero() {
		return
	}
	report.ReportID = reportID
	report.GeneratedAt = generatedAt.Format(time.RFC3339Nano)
	report.Archived = true
	report.ArchiveStatus = PerformanceArchiveArchived
	archiveContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), performanceArchiveTimeout)
	defer cancel()
	if err := service.archive.SaveQuickPerformanceReport(archiveContext, *report); err != nil {
		report.Archived = false
		report.ArchiveStatus = PerformanceArchiveFailed
	}
}

func performanceProfile(command PerformanceCommand) PerformanceProfile {
	return PerformanceProfile{
		LoadMode:           command.LoadMode,
		ArrivalPattern:     command.ArrivalPattern,
		WorkloadMode:       command.WorkloadMode,
		RandomInput:        command.RandomInput,
		AffinityEnabled:    command.AffinityEnabled,
		RandomSeed:         command.RandomSeed,
		RequestCount:       command.RequestCount,
		DurationMS:         command.DurationMS,
		Concurrency:        command.Concurrency,
		RatePerSecond:      command.RatePerSecond,
		MaxInFlight:        command.MaxInFlight,
		TimeoutMS:          command.TimeoutMS,
		InputTokens:        command.InputTokens,
		OutputTokens:       command.OutputTokens,
		InputTokensStdDev:  command.InputTokensStdDev,
		OutputTokensStdDev: command.OutputTokensStdDev,
		SharedPrefixTokens: command.SharedPrefixTokens,
		WarmupRequests:     command.WarmupRequests,
		RampDurationMS:     command.RampDurationMS,
		RampRequestCap:     command.RampRequestCap,
		SliceDurationMS:    command.SliceDurationMS,
		SLOTTFTMS:          command.SLOTTFTMS,
		SLOTPOTMS:          command.SLOTPOTMS,
		SLOE2EMS:           command.SLOE2EMS,
		SLOTargetPercent:   command.SLOTargetPercent,
		CapacityEnabled:    command.CapacityEnabled,
		CapacityStart:      command.CapacityStart,
		CapacityStep:       command.CapacityStep,
	}
}

func preparePerformanceProfile(profile PerformanceProfile) (*PerformanceRequestBudget, uint64, error) {
	if !validPerformanceProfileFields(profile) {
		return nil, 0, errors.New("invalid performance profile")
	}
	return buildPerformanceRequestBudget(profile)
}

func validPerformanceProfileFields(profile PerformanceProfile) bool {
	arrival := normalizedArrivalPattern(profile.ArrivalPattern)
	workload := normalizedWorkloadMode(profile.WorkloadMode)
	if !((profile.RequestCount > 0 || profile.DurationMS > 0) &&
		profile.RequestCount <= MaxPerformanceRequests &&
		profile.DurationMS <= MaxPerformanceDurationMS &&
		profile.TimeoutMS > 0 && profile.TimeoutMS <= MaxPerformanceTimeoutMS &&
		profile.InputTokens > 0 && profile.InputTokens <= MaxPerformanceInputTokens &&
		profile.OutputTokens > 0 && profile.OutputTokens <= MaxPerformanceOutputTokens &&
		profile.InputTokensStdDev <= MaxPerformanceInputTokens &&
		profile.OutputTokensStdDev <= MaxPerformanceOutputTokens) {
		return false
	}
	if arrival != load.ArrivalConstant && arrival != load.ArrivalPoisson {
		return false
	}
	switch workload {
	case PerformanceWorkloadFixed:
		if profile.InputTokensStdDev != 0 || profile.OutputTokensStdDev != 0 || profile.SharedPrefixTokens != 0 {
			return false
		}
	case PerformanceWorkloadNormal:
		if profile.InputTokensStdDev > profile.InputTokens || profile.OutputTokensStdDev > profile.OutputTokens ||
			profile.SharedPrefixTokens >= profile.InputTokens || profile.SharedPrefixTokens >= MaxPerformanceInputTokens {
			return false
		}
	default:
		return false
	}
	needsSeed := arrival == load.ArrivalPoisson || workload == PerformanceWorkloadNormal
	if needsSeed != (profile.RandomSeed > 0) {
		return false
	}
	validLoad := false
	switch profile.LoadMode {
	case domain.LoadFixedConcurrency:
		validLoad = profile.Concurrency > 0 && profile.Concurrency <= MaxPerformanceConcurrency &&
			profile.RatePerSecond == 0 && profile.MaxInFlight == 0 && arrival == load.ArrivalConstant
	case domain.LoadOpenLoop:
		if profile.Concurrency != 0 || profile.MaxInFlight == 0 || profile.MaxInFlight > MaxPerformanceInFlight ||
			math.IsNaN(profile.RatePerSecond) || math.IsInf(profile.RatePerSecond, 0) ||
			profile.RatePerSecond < MinPerformanceRatePerSecond || profile.RatePerSecond > MaxPerformanceRatePerSecond {
			return false
		}
		validLoad = true
	default:
		return false
	}
	if !validLoad {
		return false
	}
	if !validPerformanceSLOConfiguration(profile) || !validPerformanceCapacityConfiguration(profile) {
		return false
	}
	return true
}

type performanceExecutionConfig struct {
	Protocol    domain.Protocol
	Address     normalizedAddress
	Credential  *credentials.Lease
	Model       string
	Body        json.RawMessage
	Workload    *performanceWorkload
	AffinityKey string
	OnFailure   openai.FailureResponseEvidenceSink
}

func (service *Host) performanceExecutor(ctx context.Context, config performanceExecutionConfig) (load.Executor, func(), domain.ErrorCode) {
	selectedProtocol, address := config.Protocol, config.Address
	lease, modelID := config.Credential, config.Model
	body, workload := config.Body, config.Workload
	onFailureEvidence := config.OnFailure

	if lease == nil {
		return nil, func() {}, ErrorCredentialRequired
	}
	if selectedProtocol != domain.ProtocolOpenAIChat {
		return service.nativePerformanceExecutor(config, lease)
	}
	options := make([]openai.Option, 0, 1)
	if onFailureEvidence != nil {
		options = append(options, openai.WithFailureResponseEvidence(MaxPerformanceEvidenceBodyBytes, onFailureEvidence))
	}
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: quickTestChannelID, Revision: 1},
		Name:              "quick-performance-test", BaseURL: address.baseURL,
		Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: modelID,
	}
	client, err := openai.NewClient(
		channel,
		lease,
		service.transport,
		options...,
	)
	_ = lease.Close()
	if err != nil {
		return nil, func() {}, classifyConstruction(err)
	}
	var executor load.Executor
	if workload == nil {
		executor, err = client.Executor(domain.TestRequest{
			Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: body,
		})
		if err != nil {
			client.Close()
			return nil, func() {}, ErrorInvalidRequest
		}
	} else {
		executor = func(requestContext context.Context, request load.Request) load.Observation {
			target := workload.target(request.Index)
			release, budgetErr := sharedPerformanceInputTokenBudget.acquire(requestContext, target.InputTokens)
			if budgetErr != nil {
				return load.Observation{Index: request.Index, ErrorCode: classifyContext(budgetErr)}
			}
			defer release()
			requestBody, bodyErr := workload.requestBodyForTarget(request.Index, target)
			if bodyErr != nil {
				return load.Observation{Index: request.Index, ErrorCode: load.ErrorRequestFailed}
			}
			requestExecutor, executorErr := client.Executor(domain.TestRequest{
				Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: requestBody,
			})
			if executorErr != nil {
				return load.Observation{Index: request.Index, ErrorCode: load.ErrorRequestFailed}
			}
			return requestExecutor(requestContext, request)
		}
	}
	cleanup := func() {
		client.Close()
	}
	return executor, cleanup, ""
}

func performanceProgress(progress load.Progress) PerformanceProgress {
	return PerformanceProgress{
		Phase: progress.Phase, Planned: progress.Planned, Launched: progress.Launched,
		Offered:   progress.Offered,
		Completed: progress.Completed, InFlight: progress.InFlight, PeakInFlight: progress.PeakInFlight,
		Succeeded: progress.Succeeded, Failed: progress.Failed, Rejected: progress.Rejected, Stopped: progress.Stopped, Capped: progress.Capped,
		SendDurationMS:  float64(progress.SendDuration) / float64(time.Millisecond),
		DrainDurationMS: float64(progress.DrainDuration) / float64(time.Millisecond),
		TotalDurationMS: float64(progress.TotalDuration) / float64(time.Millisecond),
	}
}

func performanceFailures(observations []load.Observation) []PerformanceFailure {
	counts := make(map[domain.ErrorCode]uint64)
	for _, observation := range observations {
		if observation.Success {
			continue
		}
		code := observation.ErrorCode
		if (observation.HTTPStatus == http.StatusUnauthorized || observation.HTTPStatus == http.StatusForbidden) && code == load.ErrorHTTP {
			code = ErrorAuthenticationFailed
		}
		if code == "" {
			code = load.ErrorUnclassified
		}
		counts[code]++
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, string(code))
	}
	sort.Strings(codes)
	failures := make([]PerformanceFailure, 0, len(codes))
	for _, code := range codes {
		typed := domain.ErrorCode(code)
		failures = append(failures, PerformanceFailure{ErrorCode: typed, Count: counts[typed]})
	}
	return failures
}

func performanceSamples(observations []load.Observation, evidence map[uint64]*PerformanceResponseEvidence, workload *performanceWorkload) []PerformanceSample {
	samples := make([]PerformanceSample, 0, len(observations))
	for _, observation := range observations {
		code := observation.ErrorCode
		if !observation.Success {
			if (observation.HTTPStatus == http.StatusUnauthorized || observation.HTTPStatus == http.StatusForbidden) && code == load.ErrorHTTP {
				code = ErrorAuthenticationFailed
			}
			if code == "" {
				code = load.ErrorUnclassified
			}
		} else {
			code = ""
		}
		ttftAnyMS := durationMilliseconds(observation.TTFTAny)
		if ttftAnyMS <= 0 {
			ttftAnyMS = durationMilliseconds(observation.TTFT)
		}
		tpot := performanceTPOTMilliseconds(
			ttftAnyMS,
			durationMilliseconds(observation.E2E),
			observation.CompletionTokens,
			observation.TTFTObserved,
		)
		sample := PerformanceSample{
			TTFTObserved:       observation.TTFTObserved,
			RequestIndex:       observation.Index,
			ScheduledOffsetMS:  durationMilliseconds(observation.ScheduledOffset),
			StartedOffsetMS:    durationMilliseconds(observation.StartedOffset),
			FinishedOffsetMS:   durationMilliseconds(observation.FinishedOffset),
			ScheduleLagMS:      durationMilliseconds(observation.ScheduleLag),
			E2EMS:              durationMilliseconds(observation.E2E),
			TTFBMS:             durationMilliseconds(observation.TTFB),
			TTFTAnyMS:          ttftAnyMS,
			TTFTVisibleMS:      durationMilliseconds(observation.TTFTVisible),
			TTFTMS:             ttftAnyMS,
			TTSTMS:             durationMilliseconds(observation.TTST),
			ObservedICLMS:      durationMilliseconds(observation.ObservedICL),
			SemanticChunkCount: observation.SemanticChunkCount,
			TPOTMS:             tpot,
			HTTPStatus:         observation.HTTPStatus, Success: observation.Success, TimedOut: observation.TimedOut,
			PromptTokens: observation.PromptTokens, CompletionTokens: observation.CompletionTokens, CachedTokens: observation.CachedTokens,
			ErrorCode:        code,
			ResponseEvidence: evidence[observation.Index],
		}
		if workload != nil && workload.profile.WorkloadMode == PerformanceWorkloadNormal {
			target := workload.target(observation.Index)
			sample.TargetInputTokens = target.InputTokens
			sample.TargetOutputTokens = target.OutputTokens
		}
		samples = append(samples, sample)
	}
	return samples
}

type performanceEvidenceRecorder struct {
	mu        sync.Mutex
	enabled   bool
	remaining int
	byIndex   map[uint64]*PerformanceResponseEvidence
}

func newPerformanceEvidenceRecorder() *performanceEvidenceRecorder {
	return &performanceEvidenceRecorder{
		remaining: MaxPerformanceEvidenceTotalBytes,
		byIndex:   make(map[uint64]*PerformanceResponseEvidence),
	}
}

func (recorder *performanceEvidenceRecorder) record(value openai.FailureResponseEvidence) {
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if !recorder.enabled {
		return
	}
	body := value.Body
	status := PerformanceEvidenceCaptured
	if body == "" {
		status = PerformanceEvidenceEmpty
	} else if recorder.remaining == 0 {
		body = ""
		status = PerformanceEvidenceOmitted
	} else if len(body) > recorder.remaining {
		body = truncateUTF8(body, recorder.remaining)
		value.Truncated = true
		if body == "" {
			status = PerformanceEvidenceOmitted
			recorder.remaining = 0
		}
	}
	recorder.remaining -= len(body)
	recorder.byIndex[value.RequestIndex] = &PerformanceResponseEvidence{
		CaptureStatus: status,
		ContentType:   value.ContentType,
		RequestID:     value.RequestID,
		Body:          body,
		BodyBytes:     value.BodyBytes,
		Truncated:     value.Truncated || status == PerformanceEvidenceOmitted,
		Redacted:      true,
	}
}

func (recorder *performanceEvidenceRecorder) enableFresh() {
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.enabled = true
	recorder.remaining = MaxPerformanceEvidenceTotalBytes
	recorder.byIndex = make(map[uint64]*PerformanceResponseEvidence)
}

func (recorder *performanceEvidenceRecorder) snapshot() map[uint64]*PerformanceResponseEvidence {
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	result := make(map[uint64]*PerformanceResponseEvidence, len(recorder.byIndex))
	for index, evidence := range recorder.byIndex {
		copy := *evidence
		result[index] = &copy
	}
	return result
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func durationMilliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

type normalizedAddress struct {
	baseURL  string
	endpoint string
}

func normalizeAddress(selectedProtocol domain.Protocol, mode AddressMode, rawURL string) (normalizedAddress, domain.ErrorCode) {
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL || strings.Contains(rawURL, "\\") {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.RawPath != "" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if !SupportsPerformance(selectedProtocol) {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	nativePath := performancePath(selectedProtocol)
	suffix := strings.TrimPrefix(nativePath, "/v1")
	trimmed := strings.TrimRight(rawURL, "/")
	var baseURL string
	switch mode {
	case AddressModeBaseURL:
		descriptor, _ := protocol.Lookup(string(selectedProtocol))
		address, err := protocol.ResolveAddress(rawURL, nativePath, descriptor.RequestPaths)
		if err != nil {
			return normalizedAddress{}, ErrorInvalidRequest
		}
		baseURL = strings.TrimSuffix(address.Endpoint, suffix)
	case AddressModeFullURL:
		if !strings.HasSuffix(trimmed, suffix) {
			return normalizedAddress{}, ErrorInvalidRequest
		}
		baseURL = strings.TrimSuffix(trimmed, suffix)
	default:
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if baseURL == "" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	return normalizedAddress{baseURL: baseURL, endpoint: baseURL + suffix}, ""
}

func classifyContext(err error) domain.ErrorCode {
	if errors.Is(err, context.DeadlineExceeded) {
		return load.ErrorTimeout
	}
	if errors.Is(err, context.Canceled) {
		return load.ErrorCancelled
	}
	return load.ErrorRequestFailed
}

func classifyConstruction(err error) domain.ErrorCode {
	switch {
	case errors.Is(err, openai.ErrCredentialRequired), errors.Is(err, openai.ErrCredentialUnavailable):
		return ErrorCredentialRequired
	case errors.Is(err, openai.ErrClientClosed):
		return load.ErrorClientClosed
	default:
		return ErrorInvalidRequest
	}
}
