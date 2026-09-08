// Package workspace exposes the secret-free read model used by interactive
// adapters such as Wails. It deliberately returns presentation-neutral values
// instead of database rows or complete domain snapshots.
package workspace

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const CurrentSchemaVersion = 1

var (
	ErrUnavailable  = errors.New("workspace service unavailable")
	ErrInconsistent = errors.New("workspace data is inconsistent")
)

// Catalog supplies two bounded workspace reads. Implementations aggregate run
// list data in their storage layer; detail records are deliberately excluded.
type Catalog interface {
	ListPlans(context.Context) ([]domain.Plan, error)
	ListSuites(context.Context) ([]domain.Suite, error)
	GetSuiteRevision(context.Context, string, uint64) (domain.Suite, error)
	ListRunProjections(context.Context) ([]RunProjection, error)
}

// Conclusion is the authoritative, sealed report conclusion for a run.
// ConclusionNone means that no sealed report exists yet; it is not inferred
// from request counts or the run lifecycle status.
type Conclusion string

const (
	ConclusionNone   Conclusion = "none"
	ConclusionPassed Conclusion = "passed"
	ConclusionFailed Conclusion = "failed"
)

func (conclusion Conclusion) Validate() error {
	switch conclusion {
	case ConclusionNone, ConclusionPassed, ConclusionFailed:
		return nil
	default:
		return errors.New("unknown workspace conclusion")
	}
}

// RunProjection is a storage-neutral aggregate. Authored runs retain their
// pinned Plan so the service can verify historical ownership without issuing
// per-run lookups. Quick tasks have no authored Plan and leave PinnedPlan zero.
type RunProjection struct {
	Run           domain.Run
	PinnedPlan    domain.Plan
	Completed     uint64
	Passed        uint64
	Failed        uint64
	ArtifactCount uint64
	Conclusion    Conclusion
}

type Service struct {
	catalog Catalog
}

func New(catalog Catalog) Service {
	return Service{catalog: catalog}
}

type Snapshot struct {
	SchemaVersion int           `json:"schema_version"`
	Plans         []PlanSummary `json:"plans"`
	Runs          []RunSummary  `json:"runs"`
	ActiveRunID   string        `json:"active_run_id,omitempty"`
}

type PlanSummary struct {
	ID             string          `json:"id"`
	Revision       uint64          `json:"revision"`
	Name           string          `json:"name"`
	CaseCount      int             `json:"case_count"`
	RunCount       int             `json:"run_count"`
	LoadMode       domain.LoadMode `json:"load_mode"`
	Concurrency    uint32          `json:"concurrency"`
	RequestCount   uint64          `json:"request_count"`
	RatePerSecond  float64         `json:"rate_per_second"`
	DurationMS     uint64          `json:"duration_ms"`
	RequestTimeout uint64          `json:"request_timeout_ms"`
}

type RunSummary struct {
	Source          string           `json:"source,omitempty"`
	ID              string           `json:"id"`
	Revision        uint64           `json:"revision"`
	PlanID          string           `json:"plan_id"`
	PlanRevision    uint64           `json:"plan_revision"`
	PlanName        string           `json:"plan_name"`
	Status          domain.RunStatus `json:"status"`
	Conclusion      Conclusion       `json:"conclusion"`
	FailurePhase    domain.ErrorCode `json:"failure_phase,omitempty"`
	ErrorCode       domain.ErrorCode `json:"error_code,omitempty"`
	ModelID         string           `json:"model_id"`
	ModelRevision   uint64           `json:"model_revision"`
	ModelName       string           `json:"model_name"`
	ChannelID       string           `json:"channel_id"`
	ChannelRevision uint64           `json:"channel_revision"`
	ChannelName     string           `json:"channel_name"`
	LoadMode        domain.LoadMode  `json:"load_mode"`
	Concurrency     uint32           `json:"concurrency"`
	RatePerSecond   float64          `json:"rate_per_second"`
	Planned         uint64           `json:"planned"`
	DurationMS      uint64           `json:"duration_ms"`
	Completed       uint64           `json:"completed"`
	Passed          uint64           `json:"passed"`
	Failed          uint64           `json:"failed"`
	ArtifactCount   uint64           `json:"artifact_count"`
	StartedAt       time.Time        `json:"started_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

func (service Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if service.catalog == nil {
		return Snapshot{}, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	plans, err := service.catalog.ListPlans(ctx)
	if err != nil {
		return Snapshot{}, safePortError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	planByID := make(map[string]domain.Plan, len(plans))
	for _, plan := range plans {
		if err := plan.Validate(); err != nil {
			return Snapshot{}, ErrInconsistent
		}
		if _, duplicate := planByID[plan.ID]; duplicate {
			return Snapshot{}, ErrInconsistent
		}
		planByID[plan.ID] = plan
	}

	projections, err := service.catalog.ListRunProjections(ctx)
	if err != nil {
		return Snapshot{}, safePortError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	runCounts := make(map[string]int, len(plans))
	runSummaries := make([]RunSummary, 0, len(projections))
	runIDs := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		meta := projection.Run.Meta()
		if _, duplicate := runIDs[meta.ID]; duplicate {
			return Snapshot{}, ErrInconsistent
		}
		runIDs[meta.ID] = struct{}{}
		if err := validateProjection(projection); err != nil {
			return Snapshot{}, ErrInconsistent
		}
		runCounts[projection.Run.PlanID()]++
		runSummaries = append(runSummaries, summarizeRun(projection))
	}

	planSummaries := make([]PlanSummary, 0, len(plans))
	suitesByID := map[string]domain.Suite{}
	if len(plans) > 0 {
		suites, err := service.catalog.ListSuites(ctx)
		if err != nil {
			return Snapshot{}, safePortError(ctx, err)
		}
		for _, suite := range suites {
			suitesByID[suite.ID] = suite
		}
	}
	for _, plan := range plans {
		load := summarizeSuiteLoads(plan.Suites)
		caseCount := 0
		for _, entry := range plan.Suites {
			suite, exists := suitesByID[entry.SuiteID]
			if !exists || suite.Revision != entry.SuiteRevision {
				var err error
				suite, err = service.catalog.GetSuiteRevision(ctx, entry.SuiteID, entry.SuiteRevision)
				if err != nil {
					return Snapshot{}, safePortError(ctx, err)
				}
			}
			caseCount += len(suite.Cases)
		}
		planSummaries = append(planSummaries, PlanSummary{
			ID: plan.ID, Revision: plan.Revision, Name: plan.Name,
			CaseCount: caseCount, RunCount: runCounts[plan.ID],
			LoadMode: load.Mode, Concurrency: load.Concurrency,
			RequestCount: load.RequestCount, RatePerSecond: load.RatePerSecond,
			DurationMS: load.DurationMS, RequestTimeout: load.RequestTimeoutMS,
		})
	}
	sort.Slice(planSummaries, func(left, right int) bool {
		if planSummaries[left].Name == planSummaries[right].Name {
			return planSummaries[left].ID < planSummaries[right].ID
		}
		return planSummaries[left].Name < planSummaries[right].Name
	})
	sort.Slice(runSummaries, func(left, right int) bool {
		if runSummaries[left].StartedAt.Equal(runSummaries[right].StartedAt) {
			return runSummaries[left].ID > runSummaries[right].ID
		}
		return runSummaries[left].StartedAt.After(runSummaries[right].StartedAt)
	})

	return Snapshot{
		SchemaVersion: CurrentSchemaVersion,
		Plans:         planSummaries,
		Runs:          runSummaries,
		ActiveRunID:   newestActiveRun(runSummaries),
	}, nil
}

func safePortError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrUnavailable
}

func validateProjection(projection RunProjection) error {
	if err := projection.Run.Validate(); err != nil {
		return err
	}
	if err := projection.Conclusion.Validate(); err != nil {
		return err
	}
	run := projection.Run
	snapshot := run.Snapshot()
	plan := projection.PinnedPlan
	if snapshot.QuickTask != nil {
		if snapshot.PlanDocument != nil || !reflect.DeepEqual(plan, domain.Plan{}) {
			return errors.New("quick task must not have an authored pinned plan")
		}
	} else {
		if err := plan.Validate(); err != nil {
			return err
		}
		if snapshot.SchemaVersion != domain.CurrentRunSnapshotSchemaVersion || snapshot.PlanDocument == nil ||
			plan.ID != run.PlanID() || plan.ID != snapshot.Plan.ID || plan.Revision != snapshot.Plan.Revision ||
			!reflect.DeepEqual(plan, *snapshot.PlanDocument) {
			return errors.New("run snapshot differs from pinned plan")
		}
	}
	if projection.Completed != projection.Passed+projection.Failed {
		return errors.New("run result counts are inconsistent")
	}
	_, load := summarizeSnapshotLoads(snapshot)
	if snapshot.QuickTask == nil && load.RequestCount > 0 && projection.Completed > load.RequestCount {
		return errors.New("run completed more requests than planned")
	}
	switch projection.Conclusion {
	case ConclusionPassed:
		if run.Status() != domain.RunCompleted {
			return errors.New("passing conclusion requires a completed run")
		}
	case ConclusionFailed:
		switch run.Status() {
		case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
		default:
			return errors.New("failed conclusion requires a terminal run")
		}
	}
	return nil
}

func summarizeRun(projection RunProjection) RunSummary {
	run := projection.Run
	meta := run.Meta()
	snapshot := run.Snapshot()
	_, load := summarizeSnapshotLoads(snapshot)
	planName := projection.PinnedPlan.Name
	if snapshot.QuickTask != nil {
		planName = snapshot.QuickTask.Suite.Name
	}
	summary := RunSummary{
		ID: meta.ID, Revision: meta.Revision,
		PlanID: run.PlanID(), PlanRevision: snapshot.Plan.Revision, PlanName: planName,
		Status: run.Status(), Conclusion: projection.Conclusion,
		ModelID: snapshot.Model.ID, ModelRevision: snapshot.Model.Revision, ModelName: snapshot.Model.Name,
		ChannelID: snapshot.Channel.ID, ChannelRevision: snapshot.Channel.Revision, ChannelName: snapshot.Channel.Name,
		LoadMode: load.Mode, Concurrency: load.Concurrency,
		RatePerSecond: load.RatePerSecond, Planned: load.RequestCount,
		DurationMS: load.DurationMS,
		Completed:  projection.Completed, Passed: projection.Passed, Failed: projection.Failed,
		ArtifactCount: projection.ArtifactCount,
		StartedAt:     meta.CreatedAt, UpdatedAt: meta.UpdatedAt,
	}
	if failure := run.Failure(); failure != nil {
		summary.FailurePhase = failure.Phase
		summary.ErrorCode = failure.ErrorCode
	}
	if snapshot.QuickTask != nil {
		// A task executes each member once; a member can own a variable number
		// of observations. Its Case count is not a request budget.
		summary.Source, summary.Planned = "quick_task", 0
	}
	return summary
}

func summarizeSuiteLoads(suites []domain.PlanSuiteEntry) domain.LoadProfile {
	loads := make([]domain.LoadProfile, len(suites))
	for index, suite := range suites {
		loads[index] = suite.Load
	}
	return aggregateSequentialLoads(loads)
}

func summarizeSnapshotLoads(snapshot domain.RunSnapshot) (int, domain.LoadProfile) {
	if snapshot.QuickTask != nil {
		return len(snapshot.Cases), snapshot.Load
	}
	loads := make([]domain.LoadProfile, len(snapshot.Suites))
	caseCount := 0
	for index, suite := range snapshot.Suites {
		caseCount += len(suite.Cases)
		loads[index] = suite.Load
	}
	return caseCount, aggregateSequentialLoads(loads)
}

func aggregateSequentialLoads(loads []domain.LoadProfile) domain.LoadProfile {
	if len(loads) == 0 {
		return domain.LoadProfile{}
	}
	aggregate := loads[0]
	for _, load := range loads[1:] {
		aggregate.RequestCount += load.RequestCount
		aggregate.DurationMS += load.DurationMS
		if load.Concurrency > aggregate.Concurrency {
			aggregate.Concurrency = load.Concurrency
		}
		if load.RatePerSecond > aggregate.RatePerSecond {
			aggregate.RatePerSecond = load.RatePerSecond
		}
		if load.RequestTimeoutMS > aggregate.RequestTimeoutMS {
			aggregate.RequestTimeoutMS = load.RequestTimeoutMS
		}
	}
	return aggregate
}

func newestActiveRun(runs []RunSummary) string {
	for _, run := range runs {
		switch run.Status {
		case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining:
			return run.ID
		}
	}
	return ""
}
