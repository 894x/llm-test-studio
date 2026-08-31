package quicktest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
	"github.com/894x/llm-studio/internal/execution/openai"
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
	report := PerformanceReport{
		SchemaVersion: PerformanceSchemaVersion,
		AddressMode:   command.AddressMode,
		ModelID:       command.ModelID,
		ArchiveStatus: PerformanceArchiveNotAttempted,
		Profile: PerformanceProfile{
			RequestCount: command.RequestCount,
			DurationMS:   command.DurationMS,
			Concurrency:  command.Concurrency,
			TimeoutMS:    command.TimeoutMS,
			InputTokens:  command.InputTokens,
			OutputTokens: command.OutputTokens,
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

	profile := domain.LoadProfile{
		Mode: domain.LoadFixedConcurrency, Concurrency: command.Concurrency,
		RequestCount: command.RequestCount, DurationMS: command.DurationMS,
		RequestTimeoutMS: command.TimeoutMS,
	}
	if profile.RequestCount == 0 {
		profile.RequestCount = MaxPerformanceRequests
	}
	if err := profile.Validate(); err != nil {
		report.ErrorCode = ErrorInvalidRequest
		return report, nil
	}
	prompt := strings.TrimSpace(strings.Repeat("test ", int(command.InputTokens)))
	body, err := json.Marshal(map[string]any{
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": command.OutputTokens,
		"stream":     true,
	})
	if err != nil {
		report.ErrorCode = load.ErrorRequestFailed
		return report, nil
	}

	executor, cleanup, code := service.performanceExecutor(ctx, address, command.APIKey, command.ModelID, body)
	command.APIKey = ""
	if code != "" {
		report.ErrorCode = code
		return report, nil
	}
	defer cleanup()

	options := load.Options{}
	if onProgress != nil {
		options.OnProgress = func(progress load.Progress) {
			onProgress(performanceProgress(progress))
		}
	}
	outcome, runErr := load.Run(ctx, profile, executor, options)
	report.Progress = performanceProgress(outcome.Progress)
	report.Metrics = outcome.Metrics
	report.Failures = performanceFailures(outcome.Results)
	report.Samples = performanceSamples(outcome.Results)
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
	return (command.RequestCount > 0 || command.DurationMS > 0) &&
		command.RequestCount <= MaxPerformanceRequests &&
		command.Concurrency > 0 && command.Concurrency <= MaxPerformanceConcurrency &&
		command.DurationMS <= MaxPerformanceDurationMS &&
		command.TimeoutMS > 0 && command.TimeoutMS <= MaxPerformanceTimeoutMS &&
		command.InputTokens > 0 && command.InputTokens <= MaxPerformanceInputTokens &&
		command.OutputTokens > 0 && command.OutputTokens <= MaxPerformanceOutputTokens
}

func (service *Service) performanceExecutor(ctx context.Context, address normalizedAddress, apiKey, modelID string, body json.RawMessage) (load.Executor, func(), domain.ErrorCode) {
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
	options := make([]openai.Option, 0, 1)
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
	executor, err := client.Executor(domain.TestRequest{
		Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: body,
	})
	if err != nil {
		client.Close()
		_ = store.Delete(context.Background(), storeRef)
		return nil, func() {}, ErrorInvalidRequest
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
		Completed: progress.Completed, InFlight: progress.InFlight, PeakInFlight: progress.PeakInFlight,
		Succeeded: progress.Succeeded, Failed: progress.Failed, Rejected: progress.Rejected,
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

func performanceSamples(observations []load.Observation) []PerformanceSample {
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
		tpot := 0.0
		if observation.TTFT > 0 && observation.E2E > observation.TTFT && observation.CompletionTokens > 1 {
			tpot = durationMilliseconds(observation.E2E-observation.TTFT) / float64(observation.CompletionTokens-1)
		}
		samples = append(samples, PerformanceSample{
			RequestIndex:      observation.Index,
			ScheduledOffsetMS: durationMilliseconds(observation.ScheduledOffset),
			StartedOffsetMS:   durationMilliseconds(observation.StartedOffset),
			FinishedOffsetMS:  durationMilliseconds(observation.FinishedOffset),
			ScheduleLagMS:     durationMilliseconds(observation.ScheduleLag),
			E2EMS:             durationMilliseconds(observation.E2E), TTFTMS: durationMilliseconds(observation.TTFT), TPOTMS: tpot,
			HTTPStatus: observation.HTTPStatus, Success: observation.Success, TimedOut: observation.TimedOut,
			PromptTokens: observation.PromptTokens, CompletionTokens: observation.CompletionTokens, CachedTokens: observation.CachedTokens,
			ErrorCode: code,
		})
	}
	return samples
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
