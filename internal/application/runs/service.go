// Package runs owns the durable execution lifecycle shared by desktop and CLI
// adapters. It resolves immutable catalog revisions before any network work and
// keeps credential leases outside domain and presentation values.
package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

var (
	ErrInvalid     = errors.New("runs: invalid input")
	ErrNotActive   = errors.New("runs: run is not active")
	ErrNotRunnable = errors.New("runs: plan target is not runnable")
	ErrClosed      = errors.New("runs: service is closed")
)

type Repository interface {
	GetPlan(context.Context, string) (domain.Plan, error)
	ResolvePlanTargetSelection(context.Context, domain.Plan, string, string) (domain.Model, domain.Channel, domain.ChannelModel, error)
	ListTestCases(context.Context) ([]domain.TestCase, error)
	ListSuites(context.Context) ([]domain.Suite, error)
	CreateRun(context.Context, domain.Run) error
	GetRun(context.Context, string) (domain.Run, error)
	UpdateRun(context.Context, uint64, domain.Run) error
	AppendResult(context.Context, domain.Result) error
}

type StartCommand struct {
	PlanID          string `json:"plan_id"`
	ModelID         string `json:"model_id"`
	ChannelID       string `json:"channel_id"`
	CaseConcurrency uint32 `json:"case_concurrency,omitempty"`
}

type CredentialStore interface {
	Get(context.Context, credentials.StoreRef) (*credentials.Lease, error)
}

type Clock interface {
	Now() time.Time
}

type MetaFactory func(time.Time) (domain.EntityMeta, error)

type ExecutionRequest struct {
	Run         domain.Run
	Entry       domain.RunEntrySnapshot
	Cases       []domain.TestCase
	Credential  *credentials.Lease
	StopSending <-chan struct{}
}

// LoadProfile schedules complete Case executions using the entry's policy.
func (request ExecutionRequest) LoadProfile() domain.LoadProfile {
	profile := request.Entry.Load
	if profile.Mode == domain.LoadSingle {
		profile.Mode = domain.LoadFixedConcurrency
		profile.Concurrency = min(profile.Concurrency, uint32(len(request.Cases)))
		profile.RequestCount = uint64(len(request.Cases))
	}
	return profile
}

type ResultDraft struct {
	CaseID          string
	RequestID       string
	ExecutionStatus domain.ExecutionStatus
	Verification    testspec.Verdict
	Observation     *testspec.Observation
	Failure         domain.FailureKind
	ErrorCode       domain.ErrorCode
	Detail          *domain.ProviderDetail
	Dimensions      map[string]string
	Metrics         map[string]float64
	EvidenceIDs     []string
}

type Executor interface {
	Execute(context.Context, ExecutionRequest, func(ResultDraft) error) error
}

type ReportGenerator interface {
	Generate(context.Context, string) error
}

type EnvironmentProvider func() domain.EnvironmentSnapshot

type Diagnostic struct {
	RunID        string
	RequestID    string
	Operation    string
	ErrorCode    string
	Duration     time.Duration
	DroppedCount uint64
	Err          error
}

type startPhaseTiming struct {
	operation string
	duration  time.Duration
}

type Dependencies struct {
	Repository           Repository
	QuickTasks           QuickTaskCatalog
	CaseTypes            *casetypes.Registry
	Credentials          CredentialStore
	QuickTaskCredentials credentials.Store
	Executor             Executor
	Clock                Clock
	MetaFactory          MetaFactory
	Environment          EnvironmentProvider
	Reporter             ReportGenerator
	ReportError          func(error)
	ReportDiagnostic     func(Diagnostic)
}

type Service struct {
	repository           Repository
	quickTasks           QuickTaskCatalog
	caseTypes            *casetypes.Registry
	credentials          CredentialStore
	quickTaskCredentials credentials.Store
	executor             Executor
	clock                Clock
	metaFactory          MetaFactory
	environment          EnvironmentProvider
	reporter             ReportGenerator
	reportError          func(error)
	diagnostics          *diagnosticDispatcher
	recoveryContext      context.Context
	stopRecovery         context.CancelFunc

	mu     sync.Mutex
	active map[string]*runControl
	closed bool
	wg     sync.WaitGroup
}

type runControl struct {
	mu            sync.Mutex
	run           domain.Run
	cases         []domain.TestCase
	lease         *credentials.Lease
	stop          chan struct{}
	stopOnce      sync.Once
	reportOnce    sync.Once
	stopRequested bool
	cancel        context.CancelFunc
	drafts        map[string][]ResultDraft
	entries       []preparedRunEntry
}

type preparedRunEntry struct {
	snapshot domain.RunEntrySnapshot
	cases    []domain.TestCase
}

func suiteCaseKey(entryID, caseID string) string {
	return entryID + "\x00" + caseID
}

func New(dependencies Dependencies) (*Service, error) {
	if isNil(dependencies.Repository) || isNil(dependencies.Credentials) || isNil(dependencies.Executor) || isNil(dependencies.Clock) || dependencies.Environment == nil {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	environment := dependencies.Environment()
	if err := environment.Validate(); err != nil {
		return nil, fmt.Errorf("%w: environment: %v", ErrInvalid, err)
	}
	caseTypes := dependencies.CaseTypes
	if caseTypes == nil {
		caseTypes = casetypes.MustBuiltinRegistry()
	}
	recoveryContext, stopRecovery := context.WithCancel(context.Background())
	return &Service{
		repository: dependencies.Repository, credentials: dependencies.Credentials,
		quickTasks: dependencies.QuickTasks, caseTypes: caseTypes,
		quickTaskCredentials: dependencies.QuickTaskCredentials,
		executor:             dependencies.Executor, clock: dependencies.Clock,
		metaFactory: factory, environment: dependencies.Environment,
		reporter: dependencies.Reporter, reportError: dependencies.ReportError,
		diagnostics:     newDiagnosticDispatcher(dependencies.ReportDiagnostic),
		recoveryContext: recoveryContext, stopRecovery: stopRecovery,
		active: make(map[string]*runControl),
	}, nil
}

func (service *Service) StartTarget(ctx context.Context, command StartCommand) (string, error) {
	startedAt := time.Now()
	runID, err := service.PrepareTarget(ctx, command)
	if err != nil {
		return "", err
	}
	activationStartedAt := time.Now()
	runID, err = service.activatePrepared(ctx, runID, nil)
	if err != nil {
		return "", err
	}
	service.reportStartTiming(runID, "start_run_activate", time.Since(activationStartedAt))
	service.reportStartTiming(runID, "start_run_total", time.Since(startedAt))
	return runID, nil
}

func (service *Service) activatePrepared(ctx context.Context, runID string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if err := service.ActivateRun(ctx, runID); err != nil {
		_ = service.CancelRun(context.Background(), runID)
		return "", err
	}
	return runID, nil
}

// PrepareTarget resolves and persists an immutable queued run without starting
// network work. Comparison orchestration uses this to durably link every target
// before any goroutine can complete independently.
func (service *Service) PrepareTarget(ctx context.Context, command StartCommand) (string, error) {
	if service == nil || ctx == nil || !domain.IsUUID(command.PlanID) {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	caseConcurrency, err := resolveCaseConcurrency(command.CaseConcurrency)
	if err != nil {
		return "", err
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return "", ErrClosed
	}
	service.mu.Unlock()

	timings := make([]startPhaseTiming, 0, 5)
	phaseStartedAt := time.Now()
	plan, err := service.repository.GetPlan(ctx, command.PlanID)
	if err != nil {
		return "", fmt.Errorf("load plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return "", fmt.Errorf("invalid plan: %w", err)
	}
	if plan.Performance != nil {
		return "", ErrNotRunnable
	}
	timings = append(timings, startPhaseTiming{operation: "start_run_load_plan", duration: time.Since(phaseStartedAt)})
	if !domain.IsUUID(command.ModelID) || !domain.IsUUID(command.ChannelID) {
		return "", ErrNotRunnable
	}
	phaseStartedAt = time.Now()
	model, channel, mapping, err := service.repository.ResolvePlanTargetSelection(ctx, plan, command.ModelID, command.ChannelID)
	if err != nil {
		return "", fmt.Errorf("resolve run binding: %w", err)
	}
	protocolSupported := model.SupportsProtocol(plan.Protocol) && mapping.SupportsProtocol(plan.Protocol)
	bindingMatches := mapping.ModelID == model.ID && mapping.ChannelID == channel.ID
	if !channel.Enabled || !bindingMatches || !protocolSupported {
		return "", ErrNotRunnable
	}
	timings = append(timings, startPhaseTiming{operation: "start_run_resolve_binding", duration: time.Since(phaseStartedAt)})
	phaseStartedAt = time.Now()
	entries, err := service.resolveEntries(ctx, plan)
	if err != nil {
		return "", err
	}
	// The frozen plan captures run-time overrides without changing the catalog.
	plan.Entries = append([]domain.PlanEntry{}, plan.Entries...)
	for index := range entries {
		if entries[index].Load.Mode == domain.LoadSingle {
			concurrency := min(caseConcurrency, uint32(len(entries[index].Cases)))
			entries[index].Load.Concurrency = concurrency
			plan.Entries[index].Load.Concurrency = concurrency
		}
	}
	timings = append(timings, startPhaseTiming{operation: "start_run_resolve_entries", duration: time.Since(phaseStartedAt)})
	if channel.CredentialID == "" || channel.Validate() != nil {
		return "", ErrNotRunnable
	}
	phaseStartedAt = time.Now()
	storeRef, refErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
	if refErr != nil {
		return "", ErrNotRunnable
	}
	lease, leaseErr := service.credentials.Get(ctx, storeRef)
	if leaseErr != nil {
		return "", fmt.Errorf("lease channel credential: %w", leaseErr)
	}
	timings = append(timings, startPhaseTiming{operation: "start_run_lease_credential", duration: time.Since(phaseStartedAt)})
	phaseStartedAt = time.Now()
	meta, metaErr := service.metaFactory(service.clock.Now())
	if metaErr != nil {
		_ = lease.Close()
		return "", fmt.Errorf("create run identity: %w", metaErr)
	}
	planDocument := plan
	mappingDocument := mapping
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: model.Revision},
			Name:              model.Name, Protocol: plan.Protocol, Capabilities: append([]string{}, model.Capabilities...),
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision},
			Name:              channel.Name, BaseURL: channel.BaseURL, Protocol: plan.Protocol,
			UpstreamModelName: mapping.UpstreamModelName,
		},
		Environment: service.environment(), PlanDocument: &planDocument, Mapping: &mappingDocument, Entries: entries,
	}
	runID, err := service.prepareRun(ctx, meta, plan.ID, snapshot, lease)
	if err != nil {
		return "", err
	}
	timings = append(timings, startPhaseTiming{operation: "start_run_persist_queued", duration: time.Since(phaseStartedAt)})
	for _, timing := range timings {
		service.reportStartTiming(runID, timing.operation, timing.duration)
	}
	return runID, nil
}

func (service *Service) reportStartTiming(runID, operation string, duration time.Duration) {
	service.report(Diagnostic{
		RunID: runID, Operation: operation, ErrorCode: "phase_timing", Duration: duration,
	})
}

// prepareRun takes ownership of the lease on every path. Both authored Plans
// and quick Suite invocations enter the same durable queue and lifecycle.
func (service *Service) prepareRun(ctx context.Context, meta domain.EntityMeta, planID string, snapshot domain.RunSnapshot, lease *credentials.Lease) (string, error) {
	owned := true
	defer func() {
		if owned {
			_ = lease.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if lease == nil {
		return "", ErrNotRunnable
	}
	run, err := domain.NewRun(meta, planID, snapshot)
	if err != nil {
		return "", fmt.Errorf("build run: %w", err)
	}
	if err := service.repository.CreateRun(ctx, run); err != nil {
		return "", fmt.Errorf("persist queued run: %w", err)
	}
	control := &runControl{
		run: run, lease: lease,
		stop: make(chan struct{}), cancel: func() {}, drafts: make(map[string][]ResultDraft),
	}
	if len(snapshot.Entries) > 0 {
		control.entries = make([]preparedRunEntry, len(snapshot.Entries))
		for index, suite := range snapshot.Entries {
			control.entries[index] = preparedRunEntry{snapshot: suite, cases: append([]domain.TestCase(nil), suite.CaseDefinitions...)}
		}
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		if cancelled, transitionErr := run.Transition(domain.RunCancelled, service.clock.Now()); transitionErr == nil {
			_ = service.repository.UpdateRun(context.Background(), run.Meta().Revision, cancelled)
		}
		return "", ErrClosed
	}
	service.active[meta.ID] = control
	service.mu.Unlock()
	owned = false
	return meta.ID, nil
}

func (service *Service) ActivateRun(ctx context.Context, runID string) error {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrInvalid
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return ErrClosed
	}
	control := service.active[runID]
	if control == nil {
		service.mu.Unlock()
		return ErrNotActive
	}
	// Reserve the WaitGroup slot while Close is excluded by service.mu. This
	// prevents Add racing with shutdown's Wait without holding service.mu while
	// acquiring the per-run lock.
	service.wg.Add(1)
	service.mu.Unlock()
	control.mu.Lock()
	if control.run.Status() != domain.RunQueued {
		control.mu.Unlock()
		service.wg.Done()
		return ErrNotActive
	}
	if err := service.transition(ctx, control, domain.RunStarting); err != nil {
		control.mu.Unlock()
		service.wg.Done()
		return err
	}
	executionContext, cancel := context.WithCancel(ctx)
	previousCancel := control.cancel
	control.cancel = func() {
		cancel()
		previousCancel()
	}
	control.mu.Unlock()
	go service.execute(executionContext, control)
	return nil
}

func (service *Service) StopSending(ctx context.Context, runID string) error {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrInvalid
	}
	control, err := service.activeControl(runID)
	if err != nil {
		return err
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	control.stopRequested = true
	control.stopOnce.Do(func() { close(control.stop) })
	if control.run.Status() == domain.RunRunning {
		return service.transition(ctx, control, domain.RunDraining)
	}
	if control.run.Status() != domain.RunStarting && control.run.Status() != domain.RunDraining {
		return ErrNotActive
	}
	return nil
}

func (service *Service) CancelRun(ctx context.Context, runID string) error {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrInvalid
	}
	control, err := service.activeControl(runID)
	if err != nil {
		return err
	}
	control.mu.Lock()
	status := control.run.Status()
	if status != domain.RunQueued && status != domain.RunStarting && status != domain.RunRunning && status != domain.RunDraining {
		control.mu.Unlock()
		return ErrNotActive
	}
	if err := service.transition(ctx, control, domain.RunCancelled); err != nil {
		control.mu.Unlock()
		return err
	}
	control.cancel()
	control.mu.Unlock()
	if status == domain.RunQueued {
		_ = control.lease.Close()
		service.mu.Lock()
		delete(service.active, runID)
		service.mu.Unlock()
		service.generateTerminalReport(control)
	}
	return nil
}

func (service *Service) GetRun(ctx context.Context, runID string) (domain.Run, error) {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return domain.Run{}, ErrInvalid
	}
	return service.repository.GetRun(ctx, runID)
}

func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	service.stopRecovery()
	controls := make([]*runControl, 0, len(service.active))
	for _, control := range service.active {
		controls = append(controls, control)
	}
	service.mu.Unlock()
	var closeErrors []error
	for _, control := range controls {
		control.mu.Lock()
		wasQueued := control.run.Status() == domain.RunQueued
		switch control.run.Status() {
		case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining:
			if err := service.transition(context.Background(), control, domain.RunCancelled); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		control.cancel()
		control.mu.Unlock()
		if wasQueued {
			_ = control.lease.Close()
			service.generateTerminalReport(control)
		}
	}
	service.wg.Wait()
	if err := service.diagnostics.close(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	return errors.Join(closeErrors...)
}

func (service *Service) execute(ctx context.Context, control *runControl) {
	defer service.wg.Done()
	defer control.cancel()
	defer control.lease.Close()
	defer service.generateTerminalReport(control)
	defer func() {
		service.mu.Lock()
		delete(service.active, control.run.Meta().ID)
		service.mu.Unlock()
	}()

	control.mu.Lock()
	if err := service.transition(context.Background(), control, domain.RunRunning); err != nil {
		runID := control.run.Meta().ID
		control.mu.Unlock()
		service.report(Diagnostic{
			RunID: runID, Operation: "transition_running",
			ErrorCode: "run_state_transition_failed", Err: err,
		})
		service.finishRun(control, domain.RunFailed, domain.RunFailure{
			Phase: "transition_running", ErrorCode: "run_state_transition_failed",
		})
		return
	}
	if control.stopRequested {
		if err := service.transition(context.Background(), control, domain.RunDraining); err != nil {
			runID := control.run.Meta().ID
			control.mu.Unlock()
			service.report(Diagnostic{
				RunID: runID, Operation: "transition_draining",
				ErrorCode: "run_state_transition_failed", Err: err,
			})
			service.finishRun(control, domain.RunFailed, domain.RunFailure{
				Phase: "transition_draining", ErrorCode: "run_state_transition_failed",
			})
			return
		}
	}
	control.mu.Unlock()
	service.executeEntries(ctx, control)
}

func (service *Service) executeEntries(ctx context.Context, control *runControl) {
	runID := control.run.Meta().ID
	started := time.Now()
	anyFailed := false
	for _, prepared := range control.entries {
		control.mu.Lock()
		if control.run.Status() == domain.RunCancelled {
			control.mu.Unlock()
			return
		}
		request := ExecutionRequest{
			Run: control.run, Entry: prepared.snapshot, Cases: append([]domain.TestCase(nil), prepared.cases...),
			Credential: control.lease, StopSending: control.stop,
		}
		control.mu.Unlock()
		suiteOffsetMS := milliseconds(time.Since(started))
		var storageErr error
		emit := func(draft ResultDraft) error {
			control.mu.Lock()
			defer control.mu.Unlock()
			if control.run.Status() != domain.RunRunning && control.run.Status() != domain.RunDraining {
				return context.Canceled
			}
			if draft.RequestID == "" || !preparedContainsCase(prepared, draft.CaseID) {
				return errors.New("invalid request observation identity")
			}
			meta, err := service.metaFactory(service.clock.Now())
			if err != nil {
				storageErr = err
				return err
			}
			metrics := cloneMetrics(draft.Metrics)
			for _, key := range []string{"scheduled_offset_ms", "started_offset_ms", "finished_offset_ms"} {
				if value, exists := metrics[key]; exists {
					metrics[key] = value + suiteOffsetMS
				}
			}
			requestID := prepared.snapshot.EntryID + ":" + draft.RequestID
			result := domain.Result{
				EntityMeta: meta, RunID: control.run.Meta().ID, EntryID: prepared.snapshot.EntryID,
				CaseID: draft.CaseID, RequestID: requestID,
				ExecutionStatus: draft.ExecutionStatus, Verification: draft.Verification, Observation: draft.Observation, Failure: draft.Failure, ErrorCode: draft.ErrorCode, Detail: draft.Detail,
				Dimensions: cloneDimensions(draft.Dimensions), Metrics: metrics, EvidenceIDs: append([]string(nil), draft.EvidenceIDs...),
			}
			if err := result.Validate(); err != nil {
				return fmt.Errorf("invalid execution result: %w", err)
			}
			if err := service.repository.AppendResult(ctx, result); err != nil {
				storageErr = err
				return err
			}
			key := suiteCaseKey(prepared.snapshot.EntryID, draft.CaseID)
			// Full evidence is persisted above; summaries retain only verdict and metric inputs.
			summaryDraft := draft
			summaryDraft.Observation = nil
			summaryDraft.Verification.Assertions = nil
			control.drafts[key] = append(control.drafts[key], cloneDraft(summaryDraft))
			if result.Verification.Status == testspec.VerdictFailed || result.Verification.Status == testspec.VerdictIndeterminate {
				service.report(Diagnostic{
					RunID: result.RunID, RequestID: result.RequestID,
					Operation: "execute_request", ErrorCode: string(result.ErrorCode),
				})
			}
			return nil
		}
		executionErr := service.executor.Execute(ctx, request, emit)

		control.mu.Lock()
		if control.run.Status() == domain.RunCancelled {
			control.mu.Unlock()
			return
		}
		if storageErr == nil && ctx.Err() != nil {
			storageErr = ctx.Err()
		}
		if storageErr != nil {
			control.mu.Unlock()
			service.report(Diagnostic{RunID: runID, Operation: "persist_suite_result", ErrorCode: "result_persistence_failed", Err: storageErr})
			service.finishRun(control, domain.RunFailed, domain.RunFailure{Phase: "persist_suite_result", ErrorCode: "result_persistence_failed"})
			return
		}
		if executionErr == nil && prepared.snapshot.Load.Mode == domain.LoadSingle {
			for _, testCase := range prepared.cases {
				if len(control.drafts[suiteCaseKey(prepared.snapshot.EntryID, testCase.ID)]) == 0 {
					executionErr = errors.New("entry did not execute every scheduled case")
					break
				}
			}
		}
		summaryErr := service.persistSuiteCaseSummaries(context.Background(), control, prepared)
		control.mu.Unlock()
		if summaryErr != nil {
			service.report(Diagnostic{RunID: runID, Operation: "persist_case_summaries", ErrorCode: "result_persistence_failed", Err: summaryErr})
			service.finishRun(control, domain.RunFailed, domain.RunFailure{Phase: "persist_case_summaries", ErrorCode: "result_persistence_failed"})
			return
		}

		status := domain.EntryExecutionCompleted
		if executionErr != nil {
			status = domain.EntryExecutionFailed
			anyFailed = true
			service.report(Diagnostic{RunID: runID, Operation: "execute_suite", ErrorCode: "suite_execution_failed", Err: executionErr})
		}
		cancelled, err := service.appendSuiteMarker(context.Background(), control, prepared.snapshot.EntryID, status)
		if cancelled {
			return
		}
		if err != nil {
			service.report(Diagnostic{RunID: runID, Operation: "persist_suite_marker", ErrorCode: "result_persistence_failed", Err: err})
			service.finishRun(control, domain.RunFailed, domain.RunFailure{Phase: "persist_suite_marker", ErrorCode: "result_persistence_failed"})
			return
		}
	}
	if anyFailed {
		service.finishRun(control, domain.RunFailed, domain.RunFailure{Phase: "execute_suites", ErrorCode: "one_or_more_suites_failed"})
		return
	}
	service.finishRun(control, domain.RunCompleted, domain.RunFailure{})
}

func (service *Service) persistSuiteCaseSummaries(ctx context.Context, control *runControl, prepared preparedRunEntry) error {
	for _, testCase := range prepared.cases {
		drafts := control.drafts[suiteCaseKey(prepared.snapshot.EntryID, testCase.ID)]
		if len(drafts) == 0 {
			continue
		}
		meta, err := service.metaFactory(service.clock.Now())
		if err != nil {
			return err
		}
		summary := aggregateCaseResult(meta, control.run.Meta().ID, prepared.snapshot.EntryID, testCase.ID, drafts)
		if err := summary.Validate(); err != nil {
			return err
		}
		if err := service.repository.AppendResult(ctx, summary); err != nil {
			return err
		}
		delete(control.drafts, suiteCaseKey(prepared.snapshot.EntryID, testCase.ID))
	}
	return nil
}

func (service *Service) appendSuiteMarker(ctx context.Context, control *runControl, entryID string, status domain.EntryExecutionStatus) (bool, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.run.Status() == domain.RunCancelled {
		return true, nil
	}
	meta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return false, err
	}
	marker := domain.Result{EntityMeta: meta, RunID: control.run.Meta().ID, EntryID: entryID, EntryStatus: status}
	if err := marker.Validate(); err != nil {
		return false, err
	}
	return false, service.repository.AppendResult(ctx, marker)
}

func (service *Service) finishRun(control *runControl, terminal domain.RunStatus, failure domain.RunFailure) {
	// Startup transitions can fail before the executor owns the credential.
	// Release it before storage recovery, just as after normal execution.
	_ = control.lease.Close()
	control.mu.Lock()
	transitionContext, cancelTransition := context.WithTimeout(context.Background(), 5*time.Second)
	transitionErr := service.saveTerminal(transitionContext, control, terminal, failure)
	cancelTransition()
	runID := control.run.Meta().ID
	control.mu.Unlock()
	if transitionErr != nil {
		service.report(Diagnostic{
			RunID: runID, Operation: "transition_" + string(terminal),
			ErrorCode: "run_state_transition_failed", Err: transitionErr,
		})
		transitionErr = service.recoverTerminal(control, terminal, failure)
	}
	if transitionErr == nil {
		service.generateTerminalReport(control)
	}
}

func (service *Service) generateTerminalReport(control *runControl) {
	if service == nil || control == nil || isNil(service.reporter) {
		return
	}
	control.mu.Lock()
	status := control.run.Status()
	runID := control.run.Meta().ID
	control.mu.Unlock()
	if status != domain.RunCompleted && status != domain.RunFailed && status != domain.RunCancelled {
		return
	}
	control.reportOnce.Do(func() {
		if err := service.reporter.Generate(context.Background(), runID); err != nil {
			if service.reportError != nil {
				service.reportError(err)
			}
			service.report(Diagnostic{
				RunID: runID, Operation: "generate_report",
				ErrorCode: "report_generation_failed", Err: err,
			})
		}
	})
}

func (service *Service) report(diagnostic Diagnostic) {
	if service == nil {
		return
	}
	service.diagnostics.submit(diagnostic)
}

func aggregateCaseResult(meta domain.EntityMeta, runID, entryID, caseID string, drafts []ResultDraft) domain.Result {
	measured := make([]ResultDraft, 0, len(drafts))
	for _, draft := range drafts {
		if draft.Dimensions["phase"] != "warmup" {
			measured = append(measured, draft)
		}
	}
	drafts = measured
	result := domain.Result{EntityMeta: meta, RunID: runID, EntryID: entryID, CaseID: caseID,
		ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictNotApplicable, Assertions: []testspec.AssertionResult{}},
		Metrics: map[string]float64{}, EvidenceIDs: []string{}}
	metricCounts := map[string]int{}
	observations := []domain.Result{}
	evidence := map[string]bool{}
	for _, draft := range drafts {
		observations = append(observations, domain.Result{Verification: draft.Verification})
		if draft.ExecutionStatus != domain.ExecutionCompleted {
			result.ExecutionStatus = draft.ExecutionStatus
			result.Failure = draft.Failure
			result.ErrorCode = draft.ErrorCode
		}
		for name, value := range draft.Metrics {
			result.Metrics[name] += value
			metricCounts[name]++
		}
		for _, id := range draft.EvidenceIDs {
			if !evidence[id] {
				evidence[id] = true
				result.EvidenceIDs = append(result.EvidenceIDs, id)
			}
		}
	}
	for name, count := range metricCounts {
		result.Metrics[name] /= float64(count)
	}
	summary := domain.SummarizeVerification(observations)
	result.Verification.Status = summary.Status
	result.Metrics["execution_count"] = float64(len(drafts))
	result.Metrics["verification_passed"] = float64(summary.Passed)
	result.Metrics["verification_failed"] = float64(summary.Failed)
	result.Metrics["verification_observed"] = float64(summary.Observed)
	result.Metrics["verification_indeterminate"] = float64(summary.Indeterminate)
	return result
}

func preparedContainsCase(prepared preparedRunEntry, caseID string) bool {
	for _, testCase := range prepared.cases {
		if testCase.ID == caseID {
			return true
		}
	}
	return false
}

func sameCaseRevisionRefs(left, right []domain.CaseRevisionRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneDraft(draft ResultDraft) ResultDraft {
	draft.Dimensions = cloneDimensions(draft.Dimensions)
	draft.Metrics = cloneMetrics(draft.Metrics)
	draft.EvidenceIDs = append([]string(nil), draft.EvidenceIDs...)
	encoded, _ := json.Marshal(struct {
		Verification testspec.Verdict
		Observation  *testspec.Observation
	}{draft.Verification, draft.Observation})
	var copy struct {
		Verification testspec.Verdict
		Observation  *testspec.Observation
	}
	_ = json.Unmarshal(encoded, &copy)
	draft.Verification = copy.Verification
	draft.Observation = copy.Observation
	return draft
}

func cloneDimensions(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func (service *Service) transition(ctx context.Context, control *runControl, status domain.RunStatus) error {
	current := control.run
	next, err := current.Transition(status, service.clock.Now())
	if err != nil {
		return err
	}
	if err := service.repository.UpdateRun(ctx, current.Meta().Revision, next); err != nil {
		return err
	}
	control.run = next
	return nil
}

func (service *Service) fail(ctx context.Context, control *runControl, failure domain.RunFailure) error {
	current := control.run
	next, err := current.Fail(failure, service.clock.Now())
	if err != nil {
		return err
	}
	if err := service.repository.UpdateRun(ctx, current.Meta().Revision, next); err != nil {
		return err
	}
	control.run = next
	return nil
}

func (service *Service) activeControl(runID string) (*runControl, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return nil, ErrClosed
	}
	control := service.active[runID]
	if control == nil {
		return nil, ErrNotActive
	}
	return control, nil
}

func cloneMetrics(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	result := make(map[string]float64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
