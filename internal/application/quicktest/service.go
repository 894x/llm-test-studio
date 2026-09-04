package quicktest

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
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
)

const (
	quickTestChannelID        = "123e4567-e89b-42d3-a456-426614174090"
	quickTestCredentialID     = "123e4567-e89b-42d3-a456-426614174091"
	performanceArchiveTimeout = 5 * time.Second
)

type Service struct {
	transport          http.RoundTripper
	allowLoopbackHTTP  bool
	channelConnections ChannelConnectionResolver
	archive            PerformanceArchive
	clock              PerformanceClock
	idFactory          PerformanceReportIDFactory
}

func New(dependencies Dependencies) *Service {
	clock := dependencies.Clock
	if clock == nil {
		clock = systemPerformanceClock{}
	}
	idFactory := dependencies.IDFactory
	if idFactory == nil {
		idFactory = func(now time.Time) (string, error) {
			meta, err := domain.NewEntityMeta(now)
			return meta.ID, err
		}
	}
	return &Service{
		transport:          dependencies.Transport,
		allowLoopbackHTTP:  dependencies.AllowLoopbackHTTPForTesting,
		channelConnections: dependencies.ChannelConnections,
		archive:            dependencies.Archive,
		clock:              clock,
		idFactory:          idFactory,
	}
}

type systemPerformanceClock struct{}

func (systemPerformanceClock) Now() time.Time { return time.Now() }

func (service *Service) Run(ctx context.Context, command Command) (Result, error) {
	if service == nil {
		return Result{}, ErrServiceUnavailable
	}
	result := Result{SchemaVersion: SchemaVersion, AddressMode: command.AddressMode}
	if ctx == nil {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}
	if code := service.applySelectedChannel(ctx, command.ChannelID, &command.AddressMode, &command.URL, &command.APIKey); code != "" {
		result.ErrorCode = code
		return result, nil
	}
	result.AddressMode = command.AddressMode

	address, code := normalizeAddress(command.AddressMode, command.URL, service.allowLoopbackHTTP)
	result.BaseURL, result.Endpoint = address.baseURL, address.endpoint
	if code != "" {
		result.ErrorCode = code
		return result, nil
	}
	if strings.TrimSpace(command.APIKey) == "" {
		result.ErrorCode = ErrorCredentialRequired
		return result, nil
	}
	if command.APIKey != strings.TrimSpace(command.APIKey) || strings.TrimSpace(command.ModelID) == "" || command.ModelID != strings.TrimSpace(command.ModelID) {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}
	timeoutMS := command.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = DefaultTimeoutMS
	}
	if timeoutMS < 1 || timeoutMS > MaxTimeoutMS {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}
	prompt := command.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultPrompt
	}

	runContext, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	if err := runContext.Err(); err != nil {
		result.ErrorCode = classifyContext(err)
		return result, nil
	}

	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, quickTestCredentialID)
	if err != nil {
		result.ErrorCode = load.ErrorRequestFailed
		return result, nil
	}
	store := credentials.NewMemoryStore()
	secret := []byte(command.APIKey)
	command.APIKey = ""
	if err := store.Set(runContext, storeRef, secret); err != nil {
		clear(secret)
		result.ErrorCode = classifyContext(runContext.Err())
		return result, nil
	}
	clear(secret)
	defer func() { _ = store.Delete(context.Background(), storeRef) }()

	lease, err := store.Get(runContext, storeRef)
	if err != nil {
		result.ErrorCode = classifyContext(runContext.Err())
		return result, nil
	}
	options := make([]openai.Option, 0, 1)
	if service.allowLoopbackHTTP {
		options = append(options, openai.WithLoopbackHTTPForTesting())
	}
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: quickTestChannelID, Revision: 1},
		Name:              "quick-test",
		BaseURL:           address.baseURL,
		Protocol:          domain.ProtocolOpenAIChat,
		UpstreamModelName: command.ModelID,
	}
	client, err := openai.NewClient(channel, lease, service.transport, options...)
	_ = lease.Close()
	if err != nil {
		result.ErrorCode = classifyConstruction(err)
		return result, nil
	}
	defer client.Close()

	body, err := json.Marshal(map[string]any{
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
	})
	if err != nil {
		result.ErrorCode = load.ErrorRequestFailed
		return result, nil
	}
	executor, err := client.Executor(domain.TestRequest{
		Method:  domain.RequestPOST,
		Path:    "/chat/completions",
		Headers: map[string]string{},
		Body:    body,
	})
	if err != nil {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}

	observation := executor(runContext, load.Request{})
	result.Success = observation.Success
	result.HTTPStatus = observation.HTTPStatus
	result.E2EMS = float64(observation.E2E) / float64(time.Millisecond)
	result.PromptTokens = observation.PromptTokens
	result.CompletionTokens = observation.CompletionTokens
	result.CachedTokens = observation.CachedTokens
	result.ErrorCode = observation.ErrorCode
	if (result.HTTPStatus == http.StatusUnauthorized || result.HTTPStatus == http.StatusForbidden) && result.ErrorCode == load.ErrorHTTP {
		result.ErrorCode = ErrorAuthenticationFailed
	}
	if !result.Success && result.ErrorCode == "" {
		result.ErrorCode = load.ErrorUnclassified
	}
	return result, nil
}

func (service *Service) RunPerformance(ctx context.Context, command PerformanceCommand) (PerformanceReport, error) {
	return service.runPerformance(ctx, command, nil)
}

func (service *Service) RunPerformanceWithProgress(ctx context.Context, command PerformanceCommand, onProgress func(PerformanceProgress)) (PerformanceReport, error) {
	return service.runPerformance(ctx, command, onProgress)
}

func (service *Service) runPerformance(ctx context.Context, command PerformanceCommand, onProgress func(PerformanceProgress)) (PerformanceReport, error) {
	if service == nil {
		return PerformanceReport{}, ErrServiceUnavailable
	}
	if command.LoadMode == "" {
		command.LoadMode = domain.LoadFixedConcurrency
	}
	command.ArrivalPattern = normalizedArrivalPattern(command.ArrivalPattern)
	command.WorkloadMode = normalizedWorkloadMode(command.WorkloadMode)
	report := PerformanceReport{
		SchemaVersion: PerformanceSchemaVersion,
		AddressMode:   command.AddressMode,
		ModelID:       command.ModelID,
		ArchiveStatus: PerformanceArchiveNotAttempted,
		Profile: PerformanceProfile{
			LoadMode:           command.LoadMode,
			ArrivalPattern:     command.ArrivalPattern,
			WorkloadMode:       command.WorkloadMode,
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
		},
		Failures: []PerformanceFailure{},
		Samples:  []PerformanceSample{},
		Progress: PerformanceProgress{Phase: PerformancePhaseNotStarted},
	}
	if ctx == nil {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}
	if code := service.applySelectedChannel(ctx, command.ChannelID, &command.AddressMode, &command.URL, &command.APIKey); code != "" {
		report.ErrorCode = code
		return report, nil
	}
	report.AddressMode = command.AddressMode
	address, code := normalizeAddress(command.AddressMode, command.URL, service.allowLoopbackHTTP)
	report.BaseURL, report.Endpoint = address.baseURL, address.endpoint
	if code != "" {
		report.ErrorCode = code
		return report, nil
	}
	if strings.TrimSpace(command.APIKey) == "" {
		report.ErrorCode = ErrorCredentialRequired
		return report, nil
	}
	if command.APIKey != strings.TrimSpace(command.APIKey) || strings.TrimSpace(command.ModelID) == "" || command.ModelID != strings.TrimSpace(command.ModelID) || !validPerformanceProfile(command) {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}

	requestBudget, measuredCap, budgetErr := buildPerformanceRequestBudget(report.Profile)
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
	if command.WorkloadMode == PerformanceWorkloadNormal {
		workload, err = newPerformanceWorkload(report.Profile)
		if err != nil {
			report.ErrorCode = ErrorInvalidRequest
			return report, nil
		}
	} else {
		prompt := strings.TrimSpace(strings.Repeat("test ", int(command.InputTokens)))
		body, err = json.Marshal(map[string]any{
			"messages":   []map[string]string{{"role": "user", "content": prompt}},
			"max_tokens": command.OutputTokens,
			"stream":     true,
		})
		if err != nil {
			report.ErrorCode = load.ErrorRequestFailed
			return report, nil
		}
	}

	evidenceRecorder := newPerformanceEvidenceRecorder()
	executor, cleanup, code := service.performanceExecutor(ctx, address, command.APIKey, command.ModelID, body, workload, evidenceRecorder.record)
	command.APIKey = ""
	if code != "" {
		report.ErrorCode = code
		return report, nil
	}
	defer cleanup()

	if command.WarmupRequests > 0 {
		warmupProfile := performanceWarmupProfile(report.Profile)
		warmupOutcome, warmupErr := load.Run(
			ctx,
			warmupProfile,
			namespacedPerformanceExecutor(executor, performanceWarmupRequestIndexBase),
			load.Options{
				MaxScheduledRequests: command.WarmupRequests,
				OnProgress:           performancePhaseProgressCallback(onProgress, PerformancePhaseWarmingUp),
			},
		)
		report.Warmup = performanceTrafficSummary(command.WarmupRequests, warmupOutcome)
		if preparationRunFailed(ctx, warmupOutcome, warmupErr, &report) {
			return report, nil
		}
	}

	if command.RampDurationMS > 0 {
		rampProfile := performanceRampProfile(report.Profile, requestBudget.RampCap)
		rampOptions := load.Options{
			Ramp:                 true,
			MaxScheduledRequests: requestBudget.RampCap,
			ArrivalPattern:       command.ArrivalPattern,
			RandomSeed:           command.RandomSeed,
			OnProgress:           performancePhaseProgressCallback(onProgress, PerformancePhaseRamping),
		}
		if command.LoadMode == domain.LoadOpenLoop {
			rampOptions.MaxOpenLoopInFlight = uint64(command.MaxInFlight)
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
			DurationMS:      command.RampDurationMS,
			Steps:           load.LinearRampStepCount(rampProfile),
			CompletedWindow: rampOutcome.Progress.Phase == load.PhaseCompleted && !rampOutcome.Progress.Stopped && !rampOutcome.Progress.Capped,
			Traffic:         *rampTraffic,
		}
		if command.LoadMode == domain.LoadFixedConcurrency {
			report.Ramp.TargetConcurrency = command.Concurrency
		} else {
			report.Ramp.TargetRatePerSecond = command.RatePerSecond
		}
		if preparationRunFailed(ctx, rampOutcome, rampErr, &report) {
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
			onProgress,
		)
	}

	evidenceRecorder.enableFresh()
	options := performanceLoadOptions(report.Profile, measuredCap, onProgress)
	outcome, runErr := load.Run(ctx, profile, executor, options)
	outcome.Progress = performanceMeasuredProgress(report.Profile, measuredCap, outcome.Progress)
	report.Progress = performanceProgress(outcome.Progress)
	report.Metrics = outcome.Metrics
	report.Failures = performanceFailures(outcome.Results)
	report.Samples = performanceSamples(outcome.Results, evidenceRecorder.snapshot(), workload)
	report.SLOAssessment = buildPerformanceSLOAssessment(report.Profile, report.Samples, report.Progress)
	if command.SliceDurationMS > 0 {
		report.TimeSlices = buildPerformanceTimeSlices(
			outcome.Results,
			outcome.Progress.TotalDuration,
			time.Duration(command.SliceDurationMS)*time.Millisecond,
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
	if outcome.Progress.Launched > 0 {
		service.archivePerformanceReport(ctx, &report)
	}
	return report, nil
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

func (service *Service) runPerformanceCapacity(
	ctx context.Context,
	report PerformanceReport,
	executor load.Executor,
	evidenceRecorder *performanceEvidenceRecorder,
	workload *performanceWorkload,
	onProgress func(PerformanceProgress),
) (PerformanceReport, error) {
	targets, err := buildPerformanceCapacityTargets(report.Profile)
	if err != nil {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}
	capacity := &PerformanceCapacityResult{
		Status: PerformanceSLONotEvaluated,
		Rungs:  make([]PerformanceCapacityRung, 0, len(targets)),
	}
	var selected *performanceCapacityProjection
	var selectedIndex uint32

	for targetIndex, target := range targets {
		rungIndex := uint32(targetIndex)
		effectiveProfile := performanceCapacityProfile(report.Profile, target)
		evidenceRecorder.enableFresh()
		progressCallback := capacityProgressCallback(onProgress, rungIndex, uint32(len(targets)), target)
		options := performanceLoadOptions(effectiveProfile, effectiveProfile.RequestCount, progressCallback)
		outcome, runErr := load.Run(
			ctx,
			performanceLoadProfile(effectiveProfile, effectiveProfile.RequestCount),
			executor,
			options,
		)
		outcome.Progress = performanceMeasuredProgress(effectiveProfile, effectiveProfile.RequestCount, outcome.Progress)
		progress := performanceProgress(outcome.Progress)
		decorateCapacityProgress(&progress, rungIndex, uint32(len(targets)), target)
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
			break
		}
		if assessment.Status != PerformanceSLOPassed {
			break
		}
	}

	if selected == nil {
		report.ErrorCode = load.ErrorRequestFailed
		return report, nil
	}
	selectedCopy := selectedIndex
	capacity.SelectedRungIndex = &selectedCopy
	report.CapacityResult = capacity
	report.Success = selected.success
	report.Progress = selected.progress
	report.Metrics = selected.metrics
	report.Failures = selected.failures
	report.Samples = selected.samples
	report.TimeSlices = selected.timeSlices
	report.SLOAssessment = selected.sloAssessment
	if ctxErr := ctx.Err(); ctxErr != nil {
		report.ErrorCode = classifyContext(ctxErr)
	} else if selected.runErr != nil {
		report.ErrorCode = load.ErrorRequestFailed
	}
	if capacity.Status == PerformanceSLONotEvaluated || ctx.Err() != nil {
		return report, nil
	}
	if report.Progress.Launched > 0 {
		service.archivePerformanceReport(ctx, &report)
	}
	return report, nil
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
		decorateCapacityProgress(&progress, rungIndex, rungCount, target)
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

func preparationRunFailed(ctx context.Context, outcome load.Outcome, runErr error, report *PerformanceReport) bool {
	if runErr == nil && outcome.Progress.Phase != load.PhaseCancelled {
		return false
	}
	report.Progress = PerformanceProgress{Phase: outcome.Progress.Phase, Stopped: outcome.Progress.Stopped}
	if ctxErr := ctx.Err(); ctxErr != nil {
		report.Progress.Phase = load.PhaseCancelled
		report.Progress.Stopped = true
		report.ErrorCode = classifyContext(ctxErr)
	} else {
		report.ErrorCode = load.ErrorRequestFailed
	}
	return true
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

func (service *Service) applySelectedChannel(ctx context.Context, channelID string, addressMode *AddressMode, address, apiKey *string) domain.ErrorCode {
	if channelID == "" {
		return ""
	}
	if !domain.IsUUID(channelID) || strings.TrimSpace(*apiKey) != "" || service.channelConnections == nil {
		return ErrorInvalidRequest
	}
	connection, err := service.channelConnections.Resolve(ctx, channelID)
	if err != nil || strings.TrimSpace(connection.BaseURL) == "" || len(connection.APIKey) == 0 {
		clear(connection.APIKey)
		return ErrorInvalidRequest
	}
	*addressMode = AddressModeBaseURL
	*address = connection.BaseURL
	*apiKey = string(connection.APIKey)
	clear(connection.APIKey)
	return ""
}

func (service *Service) archivePerformanceReport(ctx context.Context, report *PerformanceReport) {
	if service.archive == nil {
		return
	}
	report.ArchiveStatus = PerformanceArchiveFailed
	generatedAt := service.clock.Now().UTC()
	reportID, err := service.idFactory(generatedAt)
	if err != nil || !domain.IsUUID(reportID) || generatedAt.IsZero() {
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

func validPerformanceProfile(command PerformanceCommand) bool {
	return validPerformanceProfileValues(PerformanceProfile{
		LoadMode: command.LoadMode, ArrivalPattern: command.ArrivalPattern, WorkloadMode: command.WorkloadMode, RandomSeed: command.RandomSeed,
		RequestCount: command.RequestCount, DurationMS: command.DurationMS,
		Concurrency: command.Concurrency, RatePerSecond: command.RatePerSecond, MaxInFlight: command.MaxInFlight,
		TimeoutMS: command.TimeoutMS, InputTokens: command.InputTokens, OutputTokens: command.OutputTokens,
		InputTokensStdDev: command.InputTokensStdDev, OutputTokensStdDev: command.OutputTokensStdDev,
		SharedPrefixTokens: command.SharedPrefixTokens, WarmupRequests: command.WarmupRequests,
		RampDurationMS: command.RampDurationMS, RampRequestCap: command.RampRequestCap, SliceDurationMS: command.SliceDurationMS,
		SLOTTFTMS: command.SLOTTFTMS, SLOTPOTMS: command.SLOTPOTMS, SLOE2EMS: command.SLOE2EMS, SLOTargetPercent: command.SLOTargetPercent,
		CapacityEnabled: command.CapacityEnabled, CapacityStart: command.CapacityStart, CapacityStep: command.CapacityStep,
	})
}

func validPerformanceProfileValues(profile PerformanceProfile) bool {
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
		if profile.RequestCount > 0 {
			validLoad = true
			break
		}
		_, err := estimateOpenLoopRequestCap(profile.RatePerSecond, profile.DurationMS, arrival)
		validLoad = err == nil
	default:
		return false
	}
	if !validLoad {
		return false
	}
	if !validPerformanceSLOConfiguration(profile) || !validPerformanceCapacityConfiguration(profile) {
		return false
	}
	_, _, err := buildPerformanceRequestBudget(profile)
	return err == nil
}

func (service *Service) performanceExecutor(ctx context.Context, address normalizedAddress, apiKey, modelID string, body json.RawMessage, workload *performanceWorkload, onFailureEvidence openai.FailureResponseEvidenceSink) (load.Executor, func(), domain.ErrorCode) {
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, quickTestCredentialID)
	if err != nil {
		return nil, func() {}, load.ErrorRequestFailed
	}
	store := credentials.NewMemoryStore()
	secret := []byte(apiKey)
	if err := store.Set(ctx, storeRef, secret); err != nil {
		clear(secret)
		return nil, func() {}, classifyContext(ctx.Err())
	}
	clear(secret)
	lease, err := store.Get(ctx, storeRef)
	if err != nil {
		_ = store.Delete(context.Background(), storeRef)
		return nil, func() {}, classifyContext(ctx.Err())
	}
	options := make([]openai.Option, 0, 2)
	if onFailureEvidence != nil {
		options = append(options, openai.WithFailureResponseEvidence(MaxPerformanceEvidenceBodyBytes, onFailureEvidence))
	}
	if service.allowLoopbackHTTP {
		options = append(options, openai.WithLoopbackHTTPForTesting())
	}
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: quickTestChannelID, Revision: 1},
		Name:              "quick-performance-test", BaseURL: address.baseURL,
		Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: modelID,
	}
	client, err := openai.NewClient(channel, lease, service.transport, options...)
	_ = lease.Close()
	if err != nil {
		_ = store.Delete(context.Background(), storeRef)
		return nil, func() {}, classifyConstruction(err)
	}
	var executor load.Executor
	if workload == nil {
		executor, err = client.Executor(domain.TestRequest{
			Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: body,
		})
		if err != nil {
			client.Close()
			_ = store.Delete(context.Background(), storeRef)
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
		_ = store.Delete(context.Background(), storeRef)
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
		tpot := performanceTPOTMilliseconds(
			durationMilliseconds(observation.TTFT),
			durationMilliseconds(observation.E2E),
			observation.CompletionTokens,
		)
		sample := PerformanceSample{
			RequestIndex:      observation.Index,
			ScheduledOffsetMS: durationMilliseconds(observation.ScheduledOffset),
			StartedOffsetMS:   durationMilliseconds(observation.StartedOffset),
			FinishedOffsetMS:  durationMilliseconds(observation.FinishedOffset),
			ScheduleLagMS:     durationMilliseconds(observation.ScheduleLag),
			E2EMS:             durationMilliseconds(observation.E2E), TTFTMS: durationMilliseconds(observation.TTFT), TPOTMS: tpot,
			HTTPStatus: observation.HTTPStatus, Success: observation.Success, TimedOut: observation.TimedOut,
			PromptTokens: observation.PromptTokens, CompletionTokens: observation.CompletionTokens, CachedTokens: observation.CachedTokens,
			ErrorCode:        code,
			ResponseEvidence: evidence[observation.Index],
		}
		if workload != nil {
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

func normalizeAddress(mode AddressMode, rawURL string, allowLoopbackHTTP bool) (normalizedAddress, domain.ErrorCode) {
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL || strings.Contains(rawURL, "\\") {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.RawPath != "" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if !secureEndpoint(parsed, allowLoopbackHTTP) {
		return normalizedAddress{}, ErrorInsecureEndpoint
	}
	trimmed := strings.TrimRight(rawURL, "/")
	var baseURL string
	switch mode {
	case AddressModeBaseURL:
		baseURL = trimmed
	case AddressModeFullURL:
		if !strings.HasSuffix(trimmed, "/chat/completions") {
			return normalizedAddress{}, ErrorInvalidRequest
		}
		baseURL = strings.TrimSuffix(trimmed, "/chat/completions")
	default:
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if baseURL == "" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	return normalizedAddress{baseURL: baseURL, endpoint: baseURL + "/chat/completions"}, ""
}

func secureEndpoint(parsed *url.URL, allowLoopbackHTTP bool) bool {
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || !allowLoopbackHTTP {
		return false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
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
	case errors.Is(err, openai.ErrInsecureEndpoint):
		return ErrorInsecureEndpoint
	case errors.Is(err, openai.ErrCredentialRequired), errors.Is(err, openai.ErrCredentialUnavailable):
		return ErrorCredentialRequired
	case errors.Is(err, openai.ErrClientClosed):
		return load.ErrorClientClosed
	default:
		return ErrorInvalidRequest
	}
}
