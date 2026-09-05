// Package runs owns the durable execution lifecycle shared by desktop and CLI
// adapters. It resolves immutable catalog revisions before any network work and
// keeps credential leases outside domain and presentation values.
package runs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrInvalid                  = errors.New("runs: invalid input")
	ErrNotActive                = errors.New("runs: run is not active")
	ErrNotRunnable              = errors.New("runs: plan target is not runnable")
	ErrPaidConfirmationRequired = errors.New("runs: paid video confirmation is required")
	ErrClosed                   = errors.New("runs: service is closed")
)

type Repository interface {
	GetPlan(context.Context, string) (domain.Plan, error)
	ResolvePlanTargetSelection(context.Context, domain.Plan, string, string) (domain.Model, domain.Channel, domain.ChannelModel, error)
	GetTestCaseRevision(context.Context, string, uint64) (domain.TestCase, error)
	CreateRun(context.Context, domain.Run) error
	GetRun(context.Context, string) (domain.Run, error)
	UpdateRun(context.Context, uint64, domain.Run) error
	AppendResult(context.Context, domain.Result) error
}

type StartCommand struct {
	PlanID           string `json:"plan_id"`
	ModelID          string `json:"model_id"`
	ChannelID        string `json:"channel_id"`
	ConfirmPaidVideo bool   `json:"confirm_paid_video"`
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
	Cases       []domain.TestCase
	Credential  *credentials.Lease
	StopSending <-chan struct{}
}

type ResultDraft struct {
	CaseID      string
	RequestID   string
	Success     domain.SuccessDimensions
	Failure     domain.FailureKind
	ErrorCode   domain.ErrorCode
	Detail      *domain.ProviderDetail
	Dimensions  map[string]string
	Metrics     map[string]float64
	EvidenceIDs []string
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
	DroppedCount uint64
	Err          error
}

type Dependencies struct {
	Repository       Repository
	Credentials      CredentialStore
	Executor         Executor
	Clock            Clock
	MetaFactory      MetaFactory
	Environment      EnvironmentProvider
	Reporter         ReportGenerator
	ReportError      func(error)
	ReportDiagnostic func(Diagnostic)
	// AllowInsecureLoopback is restricted to explicit test harnesses. Desktop
	// production construction deliberately leaves it false.
	AllowInsecureLoopback bool
}

type Service struct {
	repository            Repository
	credentials           CredentialStore
	executor              Executor
	clock                 Clock
	metaFactory           MetaFactory
	environment           EnvironmentProvider
	reporter              ReportGenerator
	reportError           func(error)
	diagnostics           *diagnosticDispatcher
	allowInsecureLoopback bool

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
	stopRequested bool
	cancel        context.CancelFunc
	drafts        map[string][]ResultDraft
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
	return &Service{
		repository: dependencies.Repository, credentials: dependencies.Credentials,
		executor: dependencies.Executor, clock: dependencies.Clock,
		metaFactory: factory, environment: dependencies.Environment,
		reporter: dependencies.Reporter, reportError: dependencies.ReportError,
		diagnostics:           newDiagnosticDispatcher(dependencies.ReportDiagnostic),
		allowInsecureLoopback: dependencies.AllowInsecureLoopback,
		active:                make(map[string]*runControl),
	}, nil
}

func (service *Service) StartRun(ctx context.Context, planID string) error {
	_, err := service.StartTarget(ctx, StartCommand{PlanID: planID})
	return err
}

func (service *Service) StartTarget(ctx context.Context, command StartCommand) (string, error) {
	runID, err := service.PrepareTarget(ctx, command)
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
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return "", ErrClosed
	}
	service.mu.Unlock()

	plan, err := service.repository.GetPlan(ctx, command.PlanID)
	if err != nil {
		return "", fmt.Errorf("load plan: %w", err)
	}
	modelID, channelID := command.ModelID, command.ChannelID
	if modelID == "" && channelID == "" && len(plan.ModelIDs) == 1 && len(plan.ChannelIDs) == 1 {
		modelID, channelID = plan.ModelIDs[0], plan.ChannelIDs[0]
	}
	if !domain.IsUUID(modelID) || !domain.IsUUID(channelID) {
		return "", ErrNotRunnable
	}
	model, channel, mapping, err := service.repository.ResolvePlanTargetSelection(ctx, plan, modelID, channelID)
	if err != nil {
		return "", fmt.Errorf("resolve plan target: %w", err)
	}
	if !channel.Enabled || model.Protocol != channel.Protocol || mapping.ModelID != model.ID || mapping.ChannelID != channel.ID {
		return "", ErrNotRunnable
	}
	if (model.Protocol == domain.ProtocolWanVideo || model.Protocol == domain.ProtocolMiniMaxVideo) && !command.ConfirmPaidVideo {
		return "", ErrPaidConfirmationRequired
	}
	cases := make([]domain.TestCase, 0, len(plan.Cases))
	applicableRefs := make([]domain.CaseRevisionRef, 0, len(plan.Cases))
	for _, ref := range plan.Cases {
		testCase, caseErr := service.repository.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
		if caseErr != nil {
			return "", fmt.Errorf("load pinned case %s: %w", ref.CaseID, caseErr)
		}
		if testCase.ID != ref.CaseID || testCase.Revision != ref.Revision || !testCase.Enabled ||
			testCase.ExecutionMode != domain.CaseExecutionAutomatic || testCase.Protocol != model.Protocol {
			return "", ErrNotRunnable
		}
		if (testCase.Protocol == domain.ProtocolWanVideo || testCase.Protocol == domain.ProtocolMiniMaxVideo) && len(testCase.ModelTargets) == 0 {
			return "", ErrNotRunnable
		}
		if !testCase.AppliesToModel(mapping.UpstreamModelName) {
			continue
		}
		cases = append(cases, testCase)
		applicableRefs = append(applicableRefs, ref)
	}
	if len(cases) == 0 || channel.CredentialID == "" || !secureCredentialEndpoint(channel.BaseURL, service.allowInsecureLoopback) {
		return "", ErrNotRunnable
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
	if err != nil {
		return "", ErrNotRunnable
	}
	lease, err := service.credentials.Get(ctx, storeRef)
	if err != nil {
		return "", fmt.Errorf("lease channel credential: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			_ = lease.Close()
		}
	}()

	meta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return "", fmt.Errorf("create run identity: %w", err)
	}
	planDocument := plan
	mappingDocument := mapping
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: model.Revision}, Name: model.Name, Protocol: model.Protocol, Capabilities: append([]string(nil), model.Capabilities...)},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision}, Name: channel.Name, BaseURL: channel.BaseURL, Protocol: channel.Protocol, UpstreamModelName: mapping.UpstreamModelName},
		Cases:         applicableRefs, Load: plan.Load, SLA: plan.SLA,
		Environment:  service.environment(),
		PlanDocument: &planDocument, Mapping: &mappingDocument,
		CaseDefinitions: append([]domain.TestCase(nil), cases...),
	}
	run, err := domain.NewRun(meta, plan.ID, snapshot)
	if err != nil {
		return "", fmt.Errorf("build run: %w", err)
	}
	if err := service.repository.CreateRun(ctx, run); err != nil {
		return "", fmt.Errorf("persist queued run: %w", err)
	}
	control := &runControl{
		run: run, cases: append([]domain.TestCase(nil), cases...), lease: lease,
		stop: make(chan struct{}), cancel: func() {}, drafts: make(map[string][]ResultDraft),
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

func secureCredentialEndpoint(rawURL string, allowInsecureLoopback bool) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || !allowInsecureLoopback {
		return false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
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
			return
		}
	}
	request := ExecutionRequest{Run: control.run, Cases: append([]domain.TestCase(nil), control.cases...), Credential: control.lease, StopSending: control.stop}
	control.mu.Unlock()
	runID := request.Run.Meta().ID

	emit := func(draft ResultDraft) error {
		control.mu.Lock()
		if control.run.Status() != domain.RunRunning && control.run.Status() != domain.RunDraining {
			control.mu.Unlock()
			return context.Canceled
		}
		if draft.RequestID == "" || !controlContainsCase(control, draft.CaseID) {
			control.mu.Unlock()
			return fmt.Errorf("invalid request observation identity")
		}
		meta, err := service.metaFactory(service.clock.Now())
		if err != nil {
			control.mu.Unlock()
			return err
		}
		result := domain.Result{
			EntityMeta: meta, RunID: control.run.Meta().ID, RequestID: draft.RequestID,
			Success: draft.Success, Failure: draft.Failure, ErrorCode: draft.ErrorCode, Detail: draft.Detail,
			Dimensions: cloneDimensions(draft.Dimensions), Metrics: cloneMetrics(draft.Metrics), EvidenceIDs: append([]string(nil), draft.EvidenceIDs...),
		}
		if err := result.Validate(); err != nil {
			control.mu.Unlock()
			return fmt.Errorf("invalid execution result: %w", err)
		}
		if err := service.repository.AppendResult(ctx, result); err != nil {
			control.mu.Unlock()
			return err
		}
		control.drafts[draft.CaseID] = append(control.drafts[draft.CaseID], cloneDraft(draft))
		control.mu.Unlock()
		if !result.Success.Overall() {
			service.report(Diagnostic{
				RunID: result.RunID, RequestID: result.RequestID,
				Operation: "execute_request", ErrorCode: string(result.ErrorCode),
			})
		}
		return nil
	}
	executionErr := service.executor.Execute(ctx, request, emit)
	pendingDiagnostics := make([]Diagnostic, 0, 2)
	failure := domain.RunFailure{}
	if executionErr != nil {
		failure = domain.RunFailure{Phase: "execute", ErrorCode: "run_execution_failed"}
		pendingDiagnostics = append(pendingDiagnostics, Diagnostic{
			RunID: runID, Operation: "execute",
			ErrorCode: "run_execution_failed", Err: executionErr,
		})
	}

	control.mu.Lock()
	status := control.run.Status()
	if status == domain.RunCancelled {
		control.mu.Unlock()
		return
	}
	if summaryErr := service.persistCaseSummaries(context.Background(), control); summaryErr != nil {
		if executionErr == nil {
			executionErr = summaryErr
			failure = domain.RunFailure{Phase: "persist_case_summaries", ErrorCode: "result_persistence_failed"}
		}
		pendingDiagnostics = append(pendingDiagnostics, Diagnostic{
			RunID: control.run.Meta().ID, Operation: "persist_case_summaries",
			ErrorCode: "result_persistence_failed", Err: summaryErr,
		})
	}
	coverageComplete := true
	for _, testCase := range control.cases {
		if len(control.drafts[testCase.ID]) == 0 {
			coverageComplete = false
			break
		}
	}
	terminal := domain.RunCompleted
	if executionErr != nil || !coverageComplete {
		terminal = domain.RunFailed
	}
	if terminal == domain.RunFailed && failure.ErrorCode == "" {
		failure = domain.RunFailure{Phase: "execute", ErrorCode: "run_execution_incomplete"}
		pendingDiagnostics = append(pendingDiagnostics, Diagnostic{
			RunID: control.run.Meta().ID, Operation: "execute",
			ErrorCode: "run_execution_incomplete",
			Err:       errors.New("execution completed without results for every case"),
		})
	}
	var transitionErr error
	if terminal == domain.RunFailed {
		transitionErr = service.fail(context.Background(), control, failure)
	} else {
		transitionErr = service.transition(context.Background(), control, terminal)
	}
	runID = control.run.Meta().ID
	control.mu.Unlock()
	for _, diagnostic := range pendingDiagnostics {
		service.report(diagnostic)
	}
	if transitionErr != nil {
		service.report(Diagnostic{
			RunID: runID, Operation: "transition_" + string(terminal),
			ErrorCode: "run_state_transition_failed", Err: transitionErr,
		})
	}
	if transitionErr == nil && !isNil(service.reporter) {
		if err := service.reporter.Generate(context.Background(), runID); err != nil {
			if service.reportError != nil {
				service.reportError(err)
			}
			service.report(Diagnostic{
				RunID: runID, Operation: "generate_report",
				ErrorCode: "report_generation_failed", Err: err,
			})
		}
	}
}

func (service *Service) report(diagnostic Diagnostic) {
	if service == nil {
		return
	}
	service.diagnostics.submit(diagnostic)
}

func (service *Service) persistCaseSummaries(ctx context.Context, control *runControl) error {
	for _, testCase := range control.cases {
		drafts := control.drafts[testCase.ID]
		if len(drafts) == 0 {
			continue
		}
		meta, err := service.metaFactory(service.clock.Now())
		if err != nil {
			return err
		}
		summary := aggregateCaseResult(meta, control.run.Meta().ID, testCase.ID, drafts)
		if err := summary.Validate(); err != nil {
			return err
		}
		if err := service.repository.AppendResult(ctx, summary); err != nil {
			return err
		}
	}
	return nil
}

func aggregateCaseResult(meta domain.EntityMeta, runID, caseID string, drafts []ResultDraft) domain.Result {
	result := domain.Result{
		EntityMeta: meta, RunID: runID, CaseID: caseID,
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics: make(map[string]float64), EvidenceIDs: []string{},
	}
	metricCounts := make(map[string]int)
	evidence := make(map[string]struct{})
	for _, draft := range drafts {
		for name, value := range draft.Metrics {
			result.Metrics[name] += value
			metricCounts[name]++
		}
		for _, id := range draft.EvidenceIDs {
			if _, exists := evidence[id]; !exists {
				evidence[id] = struct{}{}
				result.EvidenceIDs = append(result.EvidenceIDs, id)
			}
		}
		if !draft.Success.Overall() && result.Success.Overall() {
			result.Success = draft.Success
			result.Failure = draft.Failure
			result.ErrorCode = draft.ErrorCode
			result.Detail = draft.Detail
		}
	}
	for name, count := range metricCounts {
		result.Metrics[name] /= float64(count)
	}
	return result
}

func controlContainsCase(control *runControl, caseID string) bool {
	for _, testCase := range control.cases {
		if testCase.ID == caseID {
			return true
		}
	}
	return false
}

func cloneDraft(draft ResultDraft) ResultDraft {
	draft.Dimensions = cloneDimensions(draft.Dimensions)
	draft.Metrics = cloneMetrics(draft.Metrics)
	draft.EvidenceIDs = append([]string(nil), draft.EvidenceIDs...)
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
