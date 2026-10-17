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
	PreparationRepository
	CreateRun(context.Context, domain.Run) error
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
	*Preparer
	repository Repository
}

func New(dependencies Dependencies) (*Service, error) {
	preparer, err := NewPreparer(PreparationDependencies{
		Repository: dependencies.Repository, QuickTasks: dependencies.QuickTasks,
		CaseTypes: dependencies.CaseTypes, Credentials: dependencies.Credentials,
		QuickTaskCredentials: dependencies.QuickTaskCredentials, Clock: dependencies.Clock,
		MetaFactory: dependencies.MetaFactory, Environment: dependencies.Environment,
	})
	if err != nil {
		return nil, err
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
	preparer.reportDiagnostic = host.report
	return &Service{Host: host, Preparer: preparer, repository: dependencies.Repository}, nil
}

func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.Preparer.Close()
	return service.Host.Close()
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
	if service == nil {
		return "", ErrInvalid
	}
	prepared, lease, err := service.Preparer.BuildTarget(ctx, command)
	if err != nil {
		return "", err
	}
	started := time.Now()
	id, err := service.AcceptPrepared(ctx, prepared, lease)
	if err != nil {
		return "", err
	}
	service.Host.reportStartTiming(id, "start_run_persist_queued", time.Since(started))
	return id, nil
}

// AcceptPrepared is the local admission facade. The shared execution Host only
// adopts existing rows; a Job repository can create a whole group atomically.
func (service *Service) AcceptPrepared(
	ctx context.Context,
	prepared PreparedRun,
	lease *credentials.Lease,
) (string, error) {
	owned := true
	defer func() {
		if owned {
			_ = lease.Close()
		}
	}()
	if service == nil || ctx == nil || lease == nil || prepared.Meta.Revision != 1 {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	run, err := domain.NewRun(prepared.Meta, prepared.PlanID, prepared.Snapshot)
	if err != nil {
		return "", fmt.Errorf("build prepared run: %w", err)
	}
	service.Host.mu.Lock()
	defer service.Host.mu.Unlock()
	if service.Host.closed {
		return "", ErrClosed
	}
	if _, exists := service.Host.active[prepared.Meta.ID]; exists {
		return "", ErrAlreadyAccepted
	}
	if err := service.repository.CreateRun(ctx, run); err != nil {
		return "", fmt.Errorf("persist queued run: %w", err)
	}
	service.Host.registerQueued(run, lease)
	owned = false
	return prepared.Meta.ID, nil
}
