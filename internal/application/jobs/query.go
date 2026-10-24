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

// Query reads local delivery and actual execution facts, including shared jobs.
// Implementations cannot connect to a node, submit, reconcile or send commands.
type Query interface {
	Job(context.Context, string) (JobView, error)
	Jobs(context.Context, string, uint32) (JobPage, error)
	PerformanceResult(context.Context, string) (PerformanceResultView, error)
	// JobControls lists this client's durable deliveries, not other clients' intents.
	JobControls(context.Context, string, string, uint32) (ControlPageView, error)
}

type JobPage struct {
	SchemaVersion int       `json:"schema_version"`
	Jobs          []JobView `json:"jobs"`
	NextCursor    string    `json:"next_cursor"`
	HasMore       bool      `json:"has_more"`
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

// SubmissionView contains only this client's durable job delivery facts.
type SubmissionView struct {
	DeliveryRevision  uint64            `json:"delivery_revision"`
	Delivery          SubmissionState   `json:"delivery"`
	Observation       Observation       `json:"observation"`
	PreparedAt        string            `json:"prepared_at"`
	DeliveryUpdatedAt string            `json:"delivery_updated_at"`
	Confirmation      *ConfirmationView `json:"confirmation"`
}

// JobView keeps delivery and execution as separate facts. Missing execution
// means that no matching state has been observed locally, never a queued job.
type JobView struct {
	SchemaVersion          int              `json:"schema_version"`
	JobID                  string           `json:"job_id"`
	Kind                   jobcontract.Kind `json:"kind"`
	Location               Location         `json:"location"`
	Executor               ExecutorView     `json:"executor"`
	Submission             *SubmissionView  `json:"submission"`
	RunIDs                 []string         `json:"run_ids"`
	ReportIDs              []string         `json:"report_ids"`
	ComparisonID           string           `json:"comparison_id"`
	Execution              *ExecutionView   `json:"execution"`
	Progress               *ProgressView    `json:"progress"`
	PerformanceResultReady bool             `json:"performance_result_ready"`
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

// ProjectJob projects one consistent set of delivery and actual executor facts.
func ProjectJob(
	read JobRead,
	progress *ProgressView,
	resultReady bool,
) (JobView, error) {
	if err := read.Validate(); err != nil {
		return JobView{}, err
	}
	spec, execution := read.Spec, read.Execution
	view := JobView{SchemaVersion: 1, JobID: spec.JobID, Kind: spec.Kind,
		Location: read.Location,
		RunIDs:   []string{}, ReportIDs: []string{}}
	if record := read.Submission; record != nil {
		view.Executor = executorView(record.Prepared)
		view.Submission = &SubmissionView{DeliveryRevision: record.Revision, Delivery: record.State,
			Observation: record.Observation, PreparedAt: record.CreatedAt.Format(time.RFC3339Nano),
			DeliveryUpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano)}
		if receipt := record.Receipt; receipt != nil {
			view.Executor = originExecutorView(receipt.Origin)
			view.Submission.Confirmation = &ConfirmationView{Status: receipt.Status, Revision: receipt.Revision,
				AcceptedAt: receipt.AcceptedAt.Format(time.RFC3339Nano)}
		}
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
	if record := read.Submission; record != nil && record.Receipt != nil {
		confirmed := record.Receipt
		behind := execution.Receipt.Revision < confirmed.Revision
		if behind && spec.Target.Kind == jobcontract.TargetRemote {
			// GET confirmation and journal synchronization advance independently.
			// Keep the newer confirmation visible until this mirror catches up.
			return view, nil
		}
	}
	view.Executor = originExecutorView(execution.Receipt.Origin)
	view.Execution = executionView(*execution)
	invalidResult := spec.Kind != jobcontract.KindPerformance || !jobcontract.Terminal(execution.Receipt.Status)
	if resultReady && invalidResult {
		return JobView{}, ErrConflict
	}
	view.PerformanceResultReady = resultReady
	if progress != nil {
		progressSupported := spec.Kind == jobcontract.KindPerformance || spec.FunctionalProgress
		if !progressSupported || progress.Validate(*execution) != nil {
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
	if snapshot.Receipt.Kind == jobcontract.KindRun {
		validPhase := p.Phase == load.PhaseSending || p.Phase == load.PhaseDraining ||
			p.Phase == load.PhaseCompleted || p.Phase == load.PhaseCancelled
		validCounts := p.Completed == p.Succeeded+p.Failed && p.Rejected <= p.Completed &&
			p.InFlight <= p.Launched && p.Launched == p.Completed-p.Rejected+p.InFlight
		if !validPhase || !validCounts {
			return ErrInvalid
		}
		if (p.Phase == load.PhaseCompleted || p.Phase == load.PhaseCancelled) && p.InFlight != 0 {
			return ErrInvalid
		}
	}
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

func ProjectPerformanceResult(
	read JobRead,
	result jobcontract.PerformanceResult,
) (PerformanceResultView, error) {
	if read.Validate() != nil || read.Submission != nil && read.Submission.State == SubmissionPrepared {
		return PerformanceResultView{}, ErrInvalid
	}
	spec := read.Spec
	validOwner := spec.Kind == jobcontract.KindPerformance && spec.Spec.Performance != nil && result.JobID == spec.JobID
	if !validOwner || result.Validate() != nil || result.Terminal.Validate(spec) != nil {
		return PerformanceResultView{}, ErrConflict
	}
	if result.ReportID != spec.Spec.Performance.ReportID {
		return PerformanceResultView{}, ErrConflict
	}
	if receipt := read.acceptanceReceipt(); receipt != nil && !confirmedExecutionMatches(*receipt, result.Terminal.Receipt) {
		return PerformanceResultView{}, ErrConflict
	}
	if result.Observation != nil && result.Observation.ValidateFor(spec) != nil {
		return PerformanceResultView{}, ErrConflict
	}
	view := PerformanceResultView{SchemaVersion: 1, JobID: result.JobID, ReportID: result.ReportID,
		Location: read.Location, Executor: originExecutorView(result.Terminal.Receipt.Origin),
		Terminal: *executionView(result.Terminal),
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

func originExecutorView(origin domain.ExecutionOrigin) ExecutorView {
	return ExecutorView{NodeID: origin.NodeID, InstallationID: origin.InstallationID,
		Version: origin.RunnerVersion, BuildID: origin.EngineBuildID, OS: origin.OS, Arch: origin.Arch}
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
	sameExecutor := first.Origin == next.Origin
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
