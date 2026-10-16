package jobs

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const MaxProgressViewBytes = 64 << 10

// Query reads the fixed submission and its durable execution projection.
// Implementations cannot connect to a node, submit, reconcile or send commands.
type Query interface {
	SubmittedJob(context.Context, string) (JobView, error)
	SubmittedPerformanceResult(context.Context, string) (PerformanceResultView, error)
}

type ExecutorView struct {
	NodeID         string `json:"node_id"`
	InstallationID string `json:"installation_id"`
	Version        string `json:"version"`
	BuildID        string `json:"build_id"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
}

type ExecutionView struct {
	Status         domain.RunStatus   `json:"status"`
	Revision       uint64             `json:"revision"`
	LatestSequence uint64             `json:"latest_sequence"`
	AcceptedAt     string             `json:"accepted_at"`
	UpdatedAt      string             `json:"updated_at"`
	Failure        *domain.RunFailure `json:"failure"`
}

// ConfirmationView contains only facts present in the acknowledged receipt.
// A receipt does not provide a journal cursor, updated time or failure details.
type ConfirmationView struct {
	Status     domain.RunStatus `json:"status"`
	Revision   uint64           `json:"revision"`
	AcceptedAt string           `json:"accepted_at"`
}

type ProgressView struct {
	Sequence   uint64                        `json:"sequence"`
	RecordedAt string                        `json:"recorded_at"`
	Progress   quicktest.PerformanceProgress `json:"progress"`
}

// JobView keeps delivery and execution as separate facts. Missing execution
// means that no matching state has been observed locally, never a queued job.
type JobView struct {
	SchemaVersion          int               `json:"schema_version"`
	JobID                  string            `json:"job_id"`
	Kind                   jobcontract.Kind  `json:"kind"`
	Location               Location          `json:"location"`
	Executor               ExecutorView      `json:"executor"`
	DeliveryRevision       uint64            `json:"delivery_revision"`
	Delivery               SubmissionState   `json:"delivery"`
	Observation            Observation       `json:"observation"`
	PreparedAt             string            `json:"prepared_at"`
	DeliveryUpdatedAt      string            `json:"delivery_updated_at"`
	RunIDs                 []string          `json:"run_ids"`
	ReportIDs              []string          `json:"report_ids"`
	ComparisonID           string            `json:"comparison_id"`
	Confirmation           *ConfirmationView `json:"confirmation"`
	Execution              *ExecutionView    `json:"execution"`
	Progress               *ProgressView     `json:"progress"`
	PerformanceResultReady bool              `json:"performance_result_ready"`
}

type PerformanceObservationView struct {
	Revision   uint64                        `json:"revision"`
	RecordedAt string                        `json:"recorded_at"`
	Stage      quicktest.PerformanceStage    `json:"stage"`
	Boundary   quicktest.PerformanceBoundary `json:"boundary"`
	RungIndex  *uint32                       `json:"rung_index"`
	Report     quicktest.PerformanceReport   `json:"report"`
}

// PerformanceResultView is the complete sealed execution outcome. Its report
// may describe warmup or an interrupted phase and is not an archive substitute.
type PerformanceResultView struct {
	SchemaVersion    int                                     `json:"schema_version"`
	JobID            string                                  `json:"job_id"`
	ReportID         string                                  `json:"report_id"`
	Location         Location                                `json:"location"`
	Executor         ExecutorView                            `json:"executor"`
	Terminal         ExecutionView                           `json:"terminal"`
	SealedAt         string                                  `json:"sealed_at"`
	ExecutionStarted bool                                    `json:"execution_started"`
	ObservationState jobcontract.PerformanceObservationState `json:"observation_state"`
	Observation      *PerformanceObservationView             `json:"observation"`
}

// ProjectSubmittedJob receives records from one storage snapshot. The caller
// must also compare the stored execution spec to record.SpecSHA256.
func ProjectSubmittedJob(
	record SubmissionRecord,
	execution *jobcontract.Snapshot,
	progress *ProgressView,
	resultReady bool,
) (JobView, error) {
	if record.Validate() != nil {
		return JobView{}, ErrInvalid
	}
	spec := record.Prepared.Spec
	view := JobView{SchemaVersion: 1, JobID: spec.JobID, Kind: spec.Kind,
		Location: recordLocation(record), Executor: executorView(record.Prepared),
		DeliveryRevision: record.Revision, Delivery: record.State, Observation: record.Observation,
		PreparedAt: record.CreatedAt.Format(time.RFC3339Nano), DeliveryUpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano),
		RunIDs: []string{}, ReportIDs: []string{}}
	if receipt := record.Receipt; receipt != nil {
		view.Confirmation = &ConfirmationView{Status: receipt.Status, Revision: receipt.Revision,
			AcceptedAt: receipt.AcceptedAt.Format(time.RFC3339Nano)}
	}
	for _, input := range spec.PreparedRuns() {
		view.RunIDs = append(view.RunIDs, input.Meta.ID)
	}
	if spec.Kind == jobcontract.KindPerformance {
		view.ReportIDs = append(view.ReportIDs, spec.Spec.Performance.ReportID)
	}
	if spec.Kind == jobcontract.KindComparison {
		view.ComparisonID = spec.Spec.Comparison.Meta.ID
	}
	if execution == nil {
		if progress != nil || resultReady {
			return JobView{}, ErrInvalid
		}
		return view, nil
	}
	if record.State == SubmissionPrepared || execution.Validate(spec) != nil {
		return JobView{}, ErrConflict
	}
	if confirmed := record.Receipt; confirmed != nil {
		behind := execution.Receipt.Revision < confirmed.Revision
		if behind && spec.Target.Kind == jobcontract.TargetRemote {
			if !confirmedExecutionMatches(execution.Receipt, *confirmed) {
				return JobView{}, ErrConflict
			}
			// GET confirmation and journal synchronization advance independently.
			// Keep the newer confirmation visible until this mirror catches up.
			return view, nil
		}
		if !confirmedExecutionMatches(*confirmed, execution.Receipt) {
			return JobView{}, ErrConflict
		}
	}
	view.Execution = executionView(*execution)
	invalidResult := spec.Kind != jobcontract.KindPerformance || !jobcontract.Terminal(execution.Receipt.Status)
	if resultReady && invalidResult {
		return JobView{}, ErrConflict
	}
	view.PerformanceResultReady = resultReady
	if progress != nil {
		if spec.Kind != jobcontract.KindPerformance || progress.Validate(*execution) != nil {
			return JobView{}, ErrInvalid
		}
		copy := *progress
		view.Progress = &copy
	}
	return view, nil
}

func (progress ProgressView) Validate(snapshot jobcontract.Snapshot) error {
	at, err := time.Parse(time.RFC3339Nano, progress.RecordedAt)
	validSequence := progress.Sequence > 1 && progress.Sequence <= snapshot.LatestSequence
	validTime := err == nil && progress.RecordedAt == at.UTC().Format(time.RFC3339Nano) &&
		!at.Before(snapshot.Receipt.AcceptedAt) && !at.After(snapshot.UpdatedAt)
	if !validSequence || !validTime {
		return ErrInvalid
	}
	p := progress.Progress
	switch p.Phase {
	case load.PhaseSending, load.PhaseDraining, load.PhaseCompleted, load.PhaseCancelled,
		quicktest.PerformancePhaseNotStarted, quicktest.PerformancePhaseWarmingUp, quicktest.PerformancePhaseRamping:
	default:
		return ErrInvalid
	}
	for _, count := range []uint64{p.Planned, p.Offered, p.Launched, p.Completed, p.InFlight,
		p.PeakInFlight, p.Succeeded, p.Failed, p.Rejected} {
		if count > (1<<53)-1 {
			return ErrInvalid
		}
	}
	for _, value := range []float64{p.SendDurationMS, p.DrainDurationMS, p.TotalDurationMS, p.CapacityTarget} {
		finite := !math.IsNaN(value) && !math.IsInf(value, 0)
		if !finite || value < 0 {
			return ErrInvalid
		}
	}
	return nil
}

func ProjectSubmittedPerformanceResult(
	record SubmissionRecord,
	result jobcontract.PerformanceResult,
) (PerformanceResultView, error) {
	if record.Validate() != nil || record.State == SubmissionPrepared {
		return PerformanceResultView{}, ErrInvalid
	}
	spec := record.Prepared.Spec
	validOwner := spec.Kind == jobcontract.KindPerformance && spec.Spec.Performance != nil && result.JobID == spec.JobID
	if !validOwner || result.Validate() != nil || result.Terminal.Validate(spec) != nil {
		return PerformanceResultView{}, ErrConflict
	}
	if result.ReportID != spec.Spec.Performance.ReportID {
		return PerformanceResultView{}, ErrConflict
	}
	if record.Receipt != nil && !confirmedExecutionMatches(*record.Receipt, result.Terminal.Receipt) {
		return PerformanceResultView{}, ErrConflict
	}
	if result.Observation != nil && result.Observation.ValidateFor(spec) != nil {
		return PerformanceResultView{}, ErrConflict
	}
	view := PerformanceResultView{SchemaVersion: 1, JobID: result.JobID, ReportID: result.ReportID,
		Location: recordLocation(record), Executor: executorView(record.Prepared), Terminal: *executionView(result.Terminal),
		SealedAt: result.SealedAt.Format(time.RFC3339Nano), ExecutionStarted: result.ExecutionStarted,
		ObservationState: result.ObservationState}
	if observation := result.Observation; observation != nil {
		checkpoint := observation.Checkpoint
		view.Observation = &PerformanceObservationView{Revision: observation.Revision,
			RecordedAt: observation.RecordedAt.Format(time.RFC3339Nano), Stage: checkpoint.Stage,
			Boundary: checkpoint.Boundary, Report: checkpoint.Report}
		if checkpoint.RungIndex != nil {
			index := *checkpoint.RungIndex
			view.Observation.RungIndex = &index
		}
	}
	return view, nil
}

func executorView(prepared PreparedJob) ExecutorView {
	runner := prepared.Runner
	return ExecutorView{NodeID: runner.NodeID, InstallationID: runner.NodeInstallationID,
		Version: runner.RunnerVersion, BuildID: runner.EngineBuildID, OS: runner.OS, Arch: runner.Arch}
}

func executionView(snapshot jobcontract.Snapshot) *ExecutionView {
	view := &ExecutionView{Status: snapshot.Receipt.Status, Revision: snapshot.Receipt.Revision,
		LatestSequence: snapshot.LatestSequence, AcceptedAt: snapshot.Receipt.AcceptedAt.Format(time.RFC3339Nano),
		UpdatedAt: snapshot.UpdatedAt.Format(time.RFC3339Nano)}
	if snapshot.Failure != nil {
		failure := *snapshot.Failure
		view.Failure = &failure
	}
	return view
}

func confirmedExecutionMatches(first, next jobcontract.Receipt) bool {
	sameIdentity := first.JobID == next.JobID && first.Kind == next.Kind && first.Contract == next.Contract
	sameExecutor := first.NodeInstallationID == next.NodeInstallationID && first.EngineBuildID == next.EngineBuildID
	sameChildren := slices.Equal(first.RunIDs, next.RunIDs) && slices.Equal(first.ReportIDs, next.ReportIDs) &&
		first.ComparisonID == next.ComparisonID
	matchingOwner := sameIdentity && sameExecutor && sameChildren && first.AcceptedAt.Equal(next.AcceptedAt)
	if !matchingOwner {
		return false
	}
	if next.Revision < first.Revision || next.Revision == first.Revision && next.Status != first.Status {
		return false
	}
	if jobcontract.Terminal(first.Status) {
		return first.Status == next.Status && first.Revision == next.Revision
	}
	switch first.Status {
	case domain.RunStarting:
		return next.Status != domain.RunQueued
	case domain.RunRunning:
		return next.Status != domain.RunQueued && next.Status != domain.RunStarting
	case domain.RunDraining:
		return next.Status == domain.RunDraining || jobcontract.Terminal(next.Status)
	default:
		return true
	}
}
