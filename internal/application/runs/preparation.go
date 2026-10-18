package runs

import (
	"context"
	"fmt"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

// PreparationRepository is read-only. Preparing a submission cannot start or
// persist a run in the desktop's operational repository.
type PreparationRepository interface {
	GetPlan(context.Context, string) (domain.Plan, error)
	ResolvePlanTargetSelection(
		context.Context, domain.Plan, string, string,
	) (domain.Model, domain.Channel, domain.ChannelModel, error)
	ListTestCases(context.Context) ([]domain.TestCase, error)
	ListSuites(context.Context) ([]domain.Suite, error)
	GetRun(context.Context, string) (domain.Run, error)
}

type PreparationDependencies struct {
	Repository           PreparationRepository
	QuickTasks           QuickTaskCatalog
	CaseTypes            *casetypes.Registry
	Credentials          CredentialStore
	QuickTaskCredentials credentials.Store
	Clock                Clock
	MetaFactory          MetaFactory
	Environment          EnvironmentProvider
}

// Preparer resolves all catalog references before input crosses a host boundary.
type Preparer struct {
	repository           PreparationRepository
	quickTasks           QuickTaskCatalog
	caseTypes            *casetypes.Registry
	credentials          CredentialStore
	quickTaskCredentials credentials.Store
	clock                Clock
	metaFactory          MetaFactory
	environment          EnvironmentProvider
	reportDiagnostic     func(Diagnostic)
	mu                   sync.Mutex
	closed               bool
}

func NewPreparer(dependencies PreparationDependencies) (*Preparer, error) {
	invalid := isNil(dependencies.Repository) || isNil(dependencies.Credentials) || isNil(dependencies.Clock)
	if invalid || dependencies.Environment == nil {
		return nil, ErrInvalid
	}
	if err := dependencies.Environment().Validate(); err != nil {
		return nil, fmt.Errorf("%w: environment: %v", ErrInvalid, err)
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	caseTypes := dependencies.CaseTypes
	if caseTypes == nil {
		caseTypes = casetypes.MustBuiltinRegistry()
	}
	return &Preparer{
		repository: dependencies.Repository, quickTasks: dependencies.QuickTasks,
		caseTypes: caseTypes, credentials: dependencies.Credentials,
		quickTaskCredentials: dependencies.QuickTaskCredentials,
		clock:                dependencies.Clock, metaFactory: factory, environment: dependencies.Environment,
	}, nil
}

func (service *Preparer) Close() {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.closed = true
	service.mu.Unlock()
}

func (service *Preparer) reportPreparationTiming(runID, operation string, duration time.Duration) {
	if service.reportDiagnostic != nil {
		service.reportDiagnostic(Diagnostic{
			RunID: runID, Operation: operation, ErrorCode: "phase_timing", Duration: duration,
		})
	}
}

// BuildTarget freezes a target without creating a queued Run. The caller owns
// the returned lease and must either transfer it to a host or close it.
func (service *Preparer) BuildTarget(
	ctx context.Context,
	command StartCommand,
) (PreparedRun, *credentials.Lease, error) {
	invalid := service == nil || ctx == nil || !domain.IsUUID(command.PlanID)
	if invalid {
		return PreparedRun{}, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return PreparedRun{}, nil, err
	}
	caseConcurrency, err := resolveCaseConcurrency(command.CaseConcurrency)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return PreparedRun{}, nil, ErrClosed
	}
	service.mu.Unlock()

	timings := make([]startPhaseTiming, 0, 5)
	phaseStartedAt := time.Now()
	plan, err := service.repository.GetPlan(ctx, command.PlanID)
	if err != nil {
		return PreparedRun{}, nil, fmt.Errorf("load plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return PreparedRun{}, nil, fmt.Errorf("invalid plan: %w", err)
	}
	timings = append(timings, startPhaseTiming{operation: "start_run_load_plan", duration: time.Since(phaseStartedAt)})
	if !domain.IsUUID(command.ModelID) || !domain.IsUUID(command.ChannelID) {
		return PreparedRun{}, nil, ErrNotRunnable
	}
	phaseStartedAt = time.Now()
	model, channel, mapping, err := service.repository.ResolvePlanTargetSelection(
		ctx,
		plan,
		command.ModelID,
		command.ChannelID,
	)
	if err != nil {
		return PreparedRun{}, nil, fmt.Errorf("resolve run binding: %w", err)
	}
	protocolSupported := model.SupportsProtocol(plan.Protocol) && mapping.SupportsProtocol(plan.Protocol)
	bindingMatches := mapping.ModelID == model.ID && mapping.ChannelID == channel.ID
	if !channel.Enabled || !bindingMatches || !protocolSupported {
		return PreparedRun{}, nil, ErrNotRunnable
	}
	timings = append(timings, startPhaseTiming{
		operation: "start_run_resolve_binding", duration: time.Since(phaseStartedAt),
	})
	phaseStartedAt = time.Now()
	entries, err := service.resolveEntries(ctx, plan)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	for _, entry := range entries {
		if entry.Performance == nil {
			continue
		}
		_, err := quicktest.PreparePlanEntry(entry.EntryID, domain.ChannelSnapshot{
			Protocol: plan.Protocol, BaseURL: channel.BaseURL, UpstreamModelName: mapping.UpstreamModelName,
		}, *entry.Performance)
		if err != nil {
			return PreparedRun{}, nil, fmt.Errorf("invalid performance item %s: %w", entry.EntryID, err)
		}
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
		return PreparedRun{}, nil, ErrNotRunnable
	}
	phaseStartedAt = time.Now()
	storeRef, refErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
	if refErr != nil {
		return PreparedRun{}, nil, ErrNotRunnable
	}
	lease, leaseErr := service.credentials.Get(ctx, storeRef)
	if leaseErr != nil {
		return PreparedRun{}, nil, fmt.Errorf("lease channel credential: %w", leaseErr)
	}
	timings = append(timings, startPhaseTiming{
		operation: "start_run_lease_credential", duration: time.Since(phaseStartedAt),
	})
	meta, metaErr := service.metaFactory(service.clock.Now())
	if metaErr != nil {
		_ = lease.Close()
		return PreparedRun{}, nil, fmt.Errorf("create run identity: %w", metaErr)
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
	prepared, err := freezePreparedRun(meta, plan.ID, snapshot)
	if err != nil {
		_ = lease.Close()
		return PreparedRun{}, nil, err
	}
	for _, timing := range timings {
		service.reportPreparationTiming(meta.ID, timing.operation, timing.duration)
	}
	return prepared, lease, nil
}
