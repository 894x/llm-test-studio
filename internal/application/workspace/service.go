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

// RunProjection is a storage-neutral aggregate. PinnedPlan is intentionally a
// domain value so the service can verify the run's historical ownership and
// snapshot contract without issuing per-run lookups.
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
	for _, plan := range plans {
		planSummaries = append(planSummaries, PlanSummary{
			ID: plan.ID, Revision: plan.Revision, Name: plan.Name,
			CaseCount: len(plan.Cases), RunCount: runCounts[plan.ID],
			LoadMode: plan.Load.Mode, Concurrency: plan.Load.Concurrency,
			RequestCount: plan.Load.RequestCount, RatePerSecond: plan.Load.RatePerSecond,
			DurationMS: plan.Load.DurationMS, RequestTimeout: plan.Load.RequestTimeoutMS,
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
	if err := projection.PinnedPlan.Validate(); err != nil {
		return err
	}
	if err := projection.Conclusion.Validate(); err != nil {
		return err
	}
	run := projection.Run
	snapshot := run.Snapshot()
	plan := projection.PinnedPlan
	if plan.ID != run.PlanID() || plan.ID != snapshot.Plan.ID || plan.Revision != snapshot.Plan.Revision {
		return errors.New("run does not match pinned plan")
	}
	if snapshot.SchemaVersion == domain.CurrentRunSnapshotSchemaVersion {
		if snapshot.PlanDocument == nil || !reflect.DeepEqual(plan, *snapshot.PlanDocument) {
			return errors.New("run snapshot differs from pinned plan")
		}
	} else if !contains(plan.ModelIDs, snapshot.Model.ID) || !contains(plan.ChannelIDs, snapshot.Channel.ID) ||
		!reflect.DeepEqual(plan.Cases, snapshot.Cases) || !reflect.DeepEqual(plan.Load, snapshot.Load) ||
		!reflect.DeepEqual(plan.SLA, snapshot.SLA) {
		return errors.New("run snapshot differs from pinned plan")
	}
	if projection.Completed != projection.Passed+projection.Failed {
		return errors.New("run result counts are inconsistent")
	}
	if snapshot.Load.RequestCount > 0 && projection.Completed > snapshot.Load.RequestCount {
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

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func summarizeRun(projection RunProjection) RunSummary {
	run := projection.Run
	meta := run.Meta()
	snapshot := run.Snapshot()
	summary := RunSummary{
		ID: meta.ID, Revision: meta.Revision,
		PlanID: run.PlanID(), PlanRevision: snapshot.Plan.Revision, PlanName: projection.PinnedPlan.Name,
		Status: run.Status(), Conclusion: projection.Conclusion,
		ModelID: snapshot.Model.ID, ModelRevision: snapshot.Model.Revision, ModelName: snapshot.Model.Name,
		ChannelID: snapshot.Channel.ID, ChannelRevision: snapshot.Channel.Revision, ChannelName: snapshot.Channel.Name,
		LoadMode: snapshot.Load.Mode, Concurrency: snapshot.Load.Concurrency,
		RatePerSecond: snapshot.Load.RatePerSecond, Planned: snapshot.Load.RequestCount,
		DurationMS: snapshot.Load.DurationMS,
		Completed:  projection.Completed, Passed: projection.Passed, Failed: projection.Failed,
		ArtifactCount: projection.ArtifactCount,
		StartedAt:     meta.CreatedAt, UpdatedAt: meta.UpdatedAt,
	}
	if failure := run.Failure(); failure != nil {
		summary.FailurePhase = failure.Phase
		summary.ErrorCode = failure.ErrorCode
	}
	return summary
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
