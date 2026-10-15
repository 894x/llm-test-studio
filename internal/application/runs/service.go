// Package runs prepares frozen runs and owns their durable execution lifecycle.
// Hosts execute prepared snapshots without a catalog or credential store.
package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

var (
	ErrInvalid         = errors.New("runs: invalid input")
	ErrNotActive       = errors.New("runs: run is not active")
	ErrNotRunnable     = errors.New("runs: plan target is not runnable")
	ErrClosed          = errors.New("runs: service is closed")
	ErrAlreadyAccepted = errors.New("runs: run is already accepted")
)

type Repository interface {
	ExecutionRepository
	GetPlan(context.Context, string) (domain.Plan, error)
	ResolvePlanTargetSelection(
		context.Context, domain.Plan, string, string,
	) (domain.Model, domain.Channel, domain.ChannelModel, error)
	ListTestCases(context.Context) ([]domain.TestCase, error)
	ListSuites(context.Context) ([]domain.Suite, error)
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
	*Host
	repository           Repository
	quickTasks           QuickTaskCatalog
	caseTypes            *casetypes.Registry
	credentials          CredentialStore
	quickTaskCredentials credentials.Store
	clock                Clock
	metaFactory          MetaFactory
	environment          EnvironmentProvider
}

func New(dependencies Dependencies) (*Service, error) {
	invalidPreparation := isNil(dependencies.Credentials) || dependencies.Environment == nil
	if invalidPreparation || isNil(dependencies.Repository) {
		return nil, ErrInvalid
	}
	if err := dependencies.Environment().Validate(); err != nil {
		return nil, fmt.Errorf("%w: environment: %v", ErrInvalid, err)
	}
	host, err := NewHost(HostDependencies{
		Repository: dependencies.Repository, Executor: dependencies.Executor,
		Clock: dependencies.Clock, MetaFactory: dependencies.MetaFactory,
		Reporter: dependencies.Reporter, ReportError: dependencies.ReportError,
		ReportDiagnostic: dependencies.ReportDiagnostic,
	})
	if err != nil {
		return nil, err
	}
	caseTypes := dependencies.CaseTypes
	if caseTypes == nil {
		caseTypes = casetypes.MustBuiltinRegistry()
	}
	return &Service{
		Host: host, repository: dependencies.Repository, quickTasks: dependencies.QuickTasks,
		caseTypes: caseTypes, credentials: dependencies.Credentials,
		quickTaskCredentials: dependencies.QuickTaskCredentials,
		clock:                dependencies.Clock, metaFactory: host.metaFactory, environment: dependencies.Environment,
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
	model, channel, mapping, err := service.repository.ResolvePlanTargetSelection(
		ctx,
		plan,
		command.ModelID,
		command.ChannelID,
	)
	if err != nil {
		return "", fmt.Errorf("resolve run binding: %w", err)
	}
	protocolSupported := model.SupportsProtocol(plan.Protocol) && mapping.SupportsProtocol(plan.Protocol)
	bindingMatches := mapping.ModelID == model.ID && mapping.ChannelID == channel.ID
	if !channel.Enabled || !bindingMatches || !protocolSupported {
		return "", ErrNotRunnable
	}
	timings = append(timings, startPhaseTiming{
		operation: "start_run_resolve_binding", duration: time.Since(phaseStartedAt),
	})
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
	timings = append(timings, startPhaseTiming{
		operation: "start_run_resolve_entries", duration: time.Since(phaseStartedAt),
	})
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
	timings = append(timings, startPhaseTiming{
		operation: "start_run_lease_credential", duration: time.Since(phaseStartedAt),
	})
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
	runID, err := service.AcceptPrepared(ctx, PreparedRun{Meta: meta, PlanID: plan.ID, Snapshot: snapshot}, lease)
	if err != nil {
		return "", err
	}
	timings = append(timings, startPhaseTiming{
		operation: "start_run_persist_queued", duration: time.Since(phaseStartedAt),
	})
	for _, timing := range timings {
		service.reportStartTiming(runID, timing.operation, timing.duration)
	}
	return runID, nil
}
