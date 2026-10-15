package jobcontract

import (
	"encoding/json"
	"math"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxEventBytes     = MaxSubmissionBytes + (64 << 10)
	MaxEventPageBytes = MaxEventBytes + (4 << 10)
)

type Receipt struct {
	Contract           int              `json:"contract"`
	JobID              string           `json:"job_id"`
	Kind               Kind             `json:"kind"`
	AcceptedAt         time.Time        `json:"accepted_at"`
	NodeInstallationID string           `json:"node_installation_id"`
	EngineBuildID      string           `json:"engine_build_id"`
	Status             domain.RunStatus `json:"status"`
	Revision           uint64           `json:"revision"`
	RunIDs             []string         `json:"run_ids"`
	ReportIDs          []string         `json:"report_ids"`
	ComparisonID       string           `json:"comparison_id,omitempty"`
}

type Snapshot struct {
	Receipt        Receipt            `json:"receipt"`
	LatestSequence uint64             `json:"latest_sequence"`
	UpdatedAt      time.Time          `json:"updated_at"`
	Failure        *domain.RunFailure `json:"failure,omitempty"`
}

type EventKind string

const (
	EventAccepted   EventKind = "accepted"
	EventState      EventKind = "state"
	EventRun        EventKind = "run"
	EventResults    EventKind = "results"
	EventEvidence   EventKind = "evidence"
	EventReport     EventKind = "report"
	EventComparison EventKind = "comparison"
	EventProgress   EventKind = "progress"
)

type Event struct {
	Contract  int             `json:"contract"`
	JobID     string          `json:"job_id"`
	Sequence  uint64          `json:"sequence"`
	Kind      EventKind       `json:"kind"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

type AcceptedEvent struct {
	Spec     JobSpec  `json:"spec"`
	Snapshot Snapshot `json:"snapshot"`
}

type ReportReference struct {
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	RunID  string `json:"run_id,omitempty"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (spec JobSpec) PreparedRuns() []runs.PreparedRun {
	inputs := []runs.PreparedRun{}
	switch spec.Kind {
	case KindRun:
		if spec.Spec.Run != nil {
			inputs = append(inputs, *spec.Spec.Run)
		}
	case KindComparison:
		if spec.Spec.Comparison != nil {
			inputs = append(inputs, spec.Spec.Comparison.Runs...)
		}
	}
	return inputs
}

func (spec JobSpec) InitialSnapshot(at time.Time) (Snapshot, error) {
	if err := spec.Validate(); err != nil {
		return Snapshot{}, err
	}
	_, offset := at.Zone()
	if at.IsZero() || offset != 0 {
		return Snapshot{}, invalid("accepted_at")
	}
	receipt := Receipt{
		Contract: CurrentContract, JobID: spec.JobID, Kind: spec.Kind, AcceptedAt: at,
		NodeInstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID,
		Status: domain.RunQueued, Revision: 1, RunIDs: []string{}, ReportIDs: []string{},
	}
	for _, input := range spec.PreparedRuns() {
		receipt.RunIDs = append(receipt.RunIDs, input.Meta.ID)
	}
	if spec.Kind == KindPerformance {
		receipt.ReportIDs = append(receipt.ReportIDs, spec.Spec.Performance.ReportID)
	}
	if spec.Kind == KindComparison {
		receipt.ComparisonID = spec.Spec.Comparison.Meta.ID
	}
	return Snapshot{Receipt: receipt, LatestSequence: 1, UpdatedAt: at}, nil
}

func (snapshot Snapshot) Validate(spec JobSpec) error {
	if err := snapshot.ValidateState(); err != nil {
		return err
	}
	expected, err := spec.InitialSnapshot(snapshot.Receipt.AcceptedAt)
	if err != nil {
		return err
	}
	got, want := snapshot.Receipt, expected.Receipt
	sameIdentity := got.Contract == want.Contract && got.JobID == want.JobID && got.Kind == want.Kind
	sameEngine := got.NodeInstallationID == want.NodeInstallationID && got.EngineBuildID == want.EngineBuildID
	sameChildren := slices.Equal(got.RunIDs, want.RunIDs) && slices.Equal(got.ReportIDs, want.ReportIDs)
	if !sameIdentity || !sameEngine || !sameChildren {
		return invalid("receipt.identity")
	}
	if got.ComparisonID != want.ComparisonID {
		return invalid("receipt.revision")
	}
	return nil
}

func (snapshot Snapshot) ValidateState() error {
	receipt := snapshot.Receipt
	validIdentity := receipt.Contract == CurrentContract && domain.IsUUID(receipt.JobID)
	validEngine := domain.IsUUID(receipt.NodeInstallationID) && IsSHA256(receipt.EngineBuildID)
	validRevision := receipt.Revision > 0 && receipt.Revision <= math.MaxInt64
	validSequence := snapshot.LatestSequence >= receipt.Revision && snapshot.LatestSequence <= math.MaxInt64
	if !validIdentity || !validEngine || !validRevision || !validSequence {
		return invalid("receipt")
	}
	initial := receipt.Status == domain.RunQueued && receipt.Revision == 1
	advanced := receipt.Status != domain.RunQueued && receipt.Revision >= 2
	if !initial && !advanced {
		return invalid("receipt.revision")
	}
	_, acceptedOffset := receipt.AcceptedAt.Zone()
	_, updatedOffset := snapshot.UpdatedAt.Zone()
	validTimes := !receipt.AcceptedAt.IsZero() && !snapshot.UpdatedAt.Before(receipt.AcceptedAt)
	if !validTimes || acceptedOffset != 0 || updatedOffset != 0 {
		return invalid("receipt.time")
	}
	if receipt.RunIDs == nil || receipt.ReportIDs == nil {
		return invalid("receipt.children")
	}
	seen := map[string]struct{}{}
	for _, id := range append(slices.Clone(receipt.RunIDs), receipt.ReportIDs...) {
		if !domain.IsUUID(id) {
			return invalid("receipt.children")
		}
		if _, duplicate := seen[id]; duplicate {
			return invalid("receipt.children")
		}
		seen[id] = struct{}{}
	}
	switch receipt.Kind {
	case KindRun:
		if len(receipt.RunIDs) != 1 || len(receipt.ReportIDs) != 0 || receipt.ComparisonID != "" {
			return invalid("receipt.children")
		}
	case KindPerformance:
		if len(receipt.RunIDs) != 0 || len(receipt.ReportIDs) != 1 || receipt.ComparisonID != "" {
			return invalid("receipt.children")
		}
	case KindComparison:
		validCount := len(receipt.RunIDs) >= 2 && len(receipt.RunIDs) <= MaxSecrets
		if !validCount || len(receipt.ReportIDs) != 0 || !domain.IsUUID(receipt.ComparisonID) {
			return invalid("receipt.children")
		}
	default:
		return invalid("receipt.kind")
	}
	switch receipt.Status {
	case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining, domain.RunCompleted, domain.RunCancelled:
		if snapshot.Failure != nil {
			return invalid("failure")
		}
	case domain.RunFailed:
		if snapshot.Failure == nil || snapshot.Failure.Validate() != nil {
			return invalid("failure")
		}
	default:
		return invalid("status")
	}
	return nil
}

func Terminal(status domain.RunStatus) bool {
	return status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCancelled
}

func (event Event) Validate() error {
	validIdentity := event.Contract == CurrentContract && domain.IsUUID(event.JobID)
	validSequence := event.Sequence > 0 && event.Sequence <= math.MaxInt64
	validPayload := len(event.Payload) > 0 && len(event.Payload) <= MaxEventBytes && json.Valid(event.Payload)
	_, offset := event.CreatedAt.Zone()
	validTime := !event.CreatedAt.IsZero() && offset == 0
	if !validIdentity || !validSequence || !validPayload || !validTime {
		return invalid("event")
	}
	switch event.Kind {
	case EventAccepted, EventState, EventRun, EventResults, EventEvidence, EventReport, EventComparison, EventProgress:
	default:
		return invalid("event.kind")
	}
	document, err := json.Marshal(event)
	if err != nil || len(document) > MaxEventBytes {
		return invalid("event.bytes")
	}
	return nil
}
