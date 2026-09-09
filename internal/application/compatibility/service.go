package compatibility

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type RunRequest struct {
	Seed         uint64
	Inputs       json.RawMessage
	Suite        string
	CasesRoot    string
	BaseURL      string
	APIKey       string
	Model        string
	CaseIDs      []string
	OutputDir    string
	AllCases     bool
	AllModels    bool
	DryRun       bool
	PollInterval time.Duration
	Timeout      time.Duration
	Concurrency  int
}

type ListRequest struct {
	Suite     string
	CasesRoot string
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type CaseDefinition = apiaudit.CaseDefinition

type Dependencies struct {
	HTTPDoer    HTTPDoer
	Emit        func(Event)
	WriteReport func(outputDir string, report apiaudit.Report) error
	Now         func() time.Time
	NewID       func() (string, error)
}

type Service struct {
	httpDoer    HTTPDoer
	emit        func(Event)
	writeReport func(string, apiaudit.Report) error
	now         func() time.Time
	newID       func() (string, error)
}

func New(dependencies Dependencies) *Service {
	httpDoer := dependencies.HTTPDoer
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	emit := dependencies.Emit
	if emit == nil {
		emit = func(Event) {}
	}
	writeReport := dependencies.WriteReport
	if writeReport == nil {
		writeReport = apiaudit.WriteReport
	}
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	newID := dependencies.NewID
	if newID == nil {
		newID = newUUID
	}
	return &Service{httpDoer: httpDoer, emit: emit, writeReport: writeReport, now: now, newID: newID}
}

type ConfigError struct {
	Err error
}

func (err *ConfigError) Error() string { return err.Err.Error() }
func (err *ConfigError) Unwrap() error { return err.Err }

func IsConfigError(err error) bool {
	var configError *ConfigError
	return errors.As(err, &configError)
}

type MissingCredentialError struct{}

func (*MissingCredentialError) Error() string { return "api key is required for a live run" }

func IsMissingCredentialError(err error) bool {
	var missingCredential *MissingCredentialError
	return errors.As(err, &missingCredential)
}

type ReportError struct {
	Err error
}

func (err *ReportError) Error() string { return err.Err.Error() }
func (err *ReportError) Unwrap() error { return err.Err }

func IsReportError(err error) bool {
	var reportError *ReportError
	return errors.As(err, &reportError)
}

func (service *Service) List(ctx context.Context, request ListRequest) ([]CaseDefinition, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cases, err := apiaudit.LoadSuite(request.CasesRoot, request.Suite)
	if err != nil {
		return nil, &ConfigError{Err: err}
	}
	return cases, nil
}

func (service *Service) Run(ctx context.Context, request RunRequest) (FinalEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return FinalEvent{}, err
	}
	config, err := buildConfig(request)
	if err != nil {
		return FinalEvent{}, &ConfigError{Err: err}
	}
	cases, err := apiaudit.LoadSuite(request.CasesRoot, config.Suite)
	if err != nil {
		return FinalEvent{}, &ConfigError{Err: err}
	}
	selected, err := apiaudit.SelectCases(cases, request.CaseIDs, request.AllCases)
	if err != nil {
		return FinalEvent{}, &ConfigError{Err: err}
	}
	for _, item := range selected {
		if _, err := testspec.ValidateInputs(item.Spec.Inputs, config.Inputs); err != nil {
			return FinalEvent{}, &ConfigError{Err: fmt.Errorf("case %s: %w", item.ID, err)}
		}
	}
	runs, err := apiaudit.ExpandRuns(config, selected)
	if err != nil {
		return FinalEvent{}, &ConfigError{Err: err}
	}

	events, err := newEventStream(service.newID, service.now, len(runs)+2)
	if err != nil {
		return FinalEvent{}, fmt.Errorf("create compatibility event stream: %w", err)
	}
	service.emit(newPlanEvent(events.next(EventTypePlan), runs))
	var results []apiaudit.CaseResult
	if config.DryRun {
		results, err = service.runDry(ctx, config, runs, events)
	} else {
		results, err = service.runLive(ctx, config, runs, request.Concurrency, events)
	}
	if err != nil {
		return FinalEvent{}, err
	}
	if err := ctx.Err(); err != nil {
		return FinalEvent{}, err
	}

	outputDir := request.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join("output", "api-audit", service.now().Format("20060102-150405"))
	}
	displayConfig := config
	if len(config.Models) > 1 {
		displayConfig.Model = strings.Join(config.Models, ", ")
	}
	safeResults := make([]apiaudit.CaseResult, len(results))
	for index, result := range results {
		safeResults[index] = apiaudit.RedactCaseResult(result, config.APIKey)
	}
	displayConfig.APIKey = ""
	report := apiaudit.BuildReport(displayConfig, safeResults)
	if err := service.writeReport(outputDir, report); err != nil {
		return FinalEvent{}, &ReportError{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return FinalEvent{}, err
	}
	final := FinalEvent{
		EventMetadata: events.next(EventTypeFinal),
		Payload: FinalPayload{
			Command: "run", ReportDir: outputDir,
			ReportJSON: filepath.Join(outputDir, "report.json"),
			ReportHTML: filepath.Join(outputDir, "report.html"),
			Overall:    report.Overall, Verdict: report.Verdict, Summary: report.Summary,
		},
	}
	service.emit(final)
	return final, nil
}

func (service *Service) runDry(ctx context.Context, config apiaudit.RunConfig, runs []apiaudit.PlannedRun, events *eventStream) ([]apiaudit.CaseResult, error) {
	results := make([]apiaudit.CaseResult, 0, len(runs))
	for index, planned := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := service.runPlannedCase(ctx, config, planned)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		results = append(results, result)
		service.emitProgress(index+1, len(runs), result, config.APIKey, events)
	}
	return results, nil
}

type indexedResult struct {
	index  int
	result apiaudit.CaseResult
}

func (service *Service) runLive(ctx context.Context, config apiaudit.RunConfig, runs []apiaudit.PlannedRun, concurrency int, events *eventStream) ([]apiaudit.CaseResult, error) {
	workerCount := min(concurrency, len(runs))
	jobs := make(chan int)
	completed := make(chan indexedResult, len(runs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					outcome := indexedResult{index: index, result: service.runPlannedCase(ctx, config, runs[index])}
					select {
					case completed <- outcome:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range runs {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(completed)
	}()

	results := make([]apiaudit.CaseResult, len(runs))
	completedCount := 0
	for completedCount < len(runs) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case outcome, ok := <-completed:
			if !ok {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("compatibility workers stopped before completing the run")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			results[outcome.index] = outcome.result
			completedCount++
			service.emitProgress(completedCount, len(runs), outcome.result, config.APIKey, events)
		}
	}
	return results, nil
}

func (service *Service) runPlannedCase(parent context.Context, config apiaudit.RunConfig, planned apiaudit.PlannedRun) apiaudit.CaseResult {
	caseContext, cancelCase := context.WithTimeout(parent, config.Timeout)
	defer cancelCase()
	result, err := apiaudit.RunCase(caseContext, service.httpDoer, config, planned)
	if err != nil {
		return apiaudit.CaseResult{
			ID: planned.ResultID, Name: planned.Case.Name, Dimension: planned.Case.Dimension,
			Protocol: planned.Case.Protocol, Model: planned.Model, Severity: planned.Case.Severity,
			Status: apiaudit.StatusFail, Evidence: err.Error(),
			Verification: testspec.Verdict{Status: testspec.VerdictIndeterminate, Assertions: []testspec.AssertionResult{}},
		}
	}
	return result
}

func (service *Service) emitProgress(completed, total int, result apiaudit.CaseResult, apiKey string, events *eventStream) {
	service.emit(ProgressEvent{
		EventMetadata: events.next(EventTypeProgress),
		Payload: ProgressPayload{
			Completed: completed, Total: total,
			Result: apiaudit.RedactCaseResult(result, apiKey),
		},
	})
}

func buildConfig(request RunRequest) (apiaudit.RunConfig, error) {
	descriptor, known := protocol.Lookup(request.Suite)
	supported := apiaudit.SupportedProtocols()
	if !known || !slices.Contains(supported, request.Suite) {
		choices := strings.Join(supported, ", ")
		if len(supported) > 1 {
			choices = strings.Join(supported[:len(supported)-1], ", ") + ", or " + supported[len(supported)-1]
		}
		return apiaudit.RunConfig{}, fmt.Errorf("--suite must be %s", choices)
	}
	parsedBase, err := url.Parse(request.BaseURL)
	if err != nil || parsedBase.Scheme != "https" || parsedBase.Host == "" {
		return apiaudit.RunConfig{}, fmt.Errorf("--base-url must be an absolute HTTPS URL")
	}
	if parsedBase.User != nil || parsedBase.RawQuery != "" || parsedBase.Fragment != "" {
		return apiaudit.RunConfig{}, fmt.Errorf("--base-url must not contain credentials, a query, or a fragment")
	}
	if request.AllModels && request.Suite != "seedance" {
		return apiaudit.RunConfig{}, fmt.Errorf("--all-models is only valid for seedance")
	}
	if (request.Suite == "openai-chat" || request.Suite == "wan-video" || request.Suite == "minimax-video") && strings.TrimSpace(request.Model) == "" {
		return apiaudit.RunConfig{}, fmt.Errorf("--model is required for %s", request.Suite)
	}
	if request.Timeout <= 0 {
		return apiaudit.RunConfig{}, fmt.Errorf("--timeout must be positive")
	}
	if request.PollInterval <= 0 {
		return apiaudit.RunConfig{}, fmt.Errorf("--poll-interval must be positive")
	}
	if request.Concurrency < 1 || request.Concurrency > 32 {
		return apiaudit.RunConfig{}, fmt.Errorf("--concurrency must be between 1 and 32")
	}
	if descriptor.Async && request.Concurrency != 1 {
		return apiaudit.RunConfig{}, fmt.Errorf("--concurrency is not supported for %s", request.Suite)
	}
	if !request.DryRun && strings.TrimSpace(request.APIKey) == "" {
		return apiaudit.RunConfig{}, &MissingCredentialError{}
	}

	config := apiaudit.RunConfig{
		Suite: request.Suite, BaseURL: request.BaseURL, APIKey: strings.TrimSpace(request.APIKey), Model: strings.TrimSpace(request.Model),
		DryRun: request.DryRun, Seed: request.Seed,
		PollInterval: request.PollInterval, Timeout: request.Timeout,
	}
	if len(request.Inputs) > 0 {
		if err := json.Unmarshal(request.Inputs, &config.Inputs); err != nil || config.Inputs == nil {
			return apiaudit.RunConfig{}, errors.New("--inputs must be a JSON object of declared case inputs")
		}
	}
	if request.Suite == "seedance" {
		if config.Model == "" {
			config.Model = apiaudit.DefaultSeedanceModel
		}
		if request.AllModels {
			config.Models = append([]string(nil), apiaudit.DefaultSeedanceModels...)
		}
	}
	return config, nil
}

func newPlanEvent(metadata EventMetadata, runs []apiaudit.PlannedRun) PlanEvent {
	plannedRuns := make([]PlannedRun, 0, len(runs))
	for _, planned := range runs {
		plannedRuns = append(plannedRuns, PlannedRun{
			ID: planned.ResultID, CaseID: planned.Case.ID, Name: planned.Case.Name,
			Dimension: planned.Case.Dimension, Protocol: planned.Case.Protocol, Model: planned.Model,
		})
	}
	return PlanEvent{
		EventMetadata: metadata,
		Payload:       PlanPayload{Total: len(runs), Runs: plannedRuns},
	}
}

type eventStream struct {
	runID    string
	eventIDs []string
	sequence uint64
	now      func() time.Time
}

func newEventStream(newID func() (string, error), now func() time.Time, eventCount int) (*eventStream, error) {
	runID, err := newID()
	if err != nil {
		return nil, err
	}
	eventIDs := make([]string, eventCount)
	for index := range eventIDs {
		eventIDs[index], err = newID()
		if err != nil {
			return nil, err
		}
	}
	return &eventStream{runID: runID, eventIDs: eventIDs, now: now}, nil
}

func (events *eventStream) next(eventType EventType) EventMetadata {
	index := int(events.sequence)
	events.sequence++
	return EventMetadata{
		SchemaVersion: EventSchemaVersion,
		EventID:       events.eventIDs[index],
		RunID:         events.runID,
		Sequence:      events.sequence,
		OccurredAt:    events.now().UTC(),
		Type:          eventType,
	}
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
