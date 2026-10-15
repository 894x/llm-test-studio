package jobcontract

import (
	"encoding/json"
	"math"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxPerformanceObservationBytes = quicktest.MaxPerformanceCheckpointBytes + (4 << 10)
	MaxPerformanceResultBytes      = MaxPerformanceObservationBytes + MaxEventBytes + (4 << 10)
)

type PerformanceObservation struct {
	Contract   int                             `json:"contract"`
	JobID      string                          `json:"job_id"`
	Revision   uint64                          `json:"revision"`
	RecordedAt time.Time                       `json:"recorded_at"`
	Checkpoint quicktest.PerformanceCheckpoint `json:"checkpoint"`
}

type PerformanceObservationState string

const (
	PerformanceNotStarted  PerformanceObservationState = "not_started"
	PerformanceAvailable   PerformanceObservationState = "available"
	PerformanceInterrupted PerformanceObservationState = "interrupted"
)

// PerformanceResult preserves the winning terminal decision and the latest
// observation. It is distinct from a measured statistical report archive.
type PerformanceResult struct {
	Contract         int                         `json:"contract"`
	JobID            string                      `json:"job_id"`
	ReportID         string                      `json:"report_id"`
	Terminal         Snapshot                    `json:"terminal"`
	SealedAt         time.Time                   `json:"sealed_at"`
	ExecutionStarted bool                        `json:"execution_started"`
	ObservationState PerformanceObservationState `json:"observation_state"`
	Observation      *PerformanceObservation     `json:"observation,omitempty"`
}

type PerformanceCheckpointEvent struct {
	ReportID  string                        `json:"report_id"`
	Revision  uint64                        `json:"observation_revision"`
	Stage     quicktest.PerformanceStage    `json:"stage"`
	Boundary  quicktest.PerformanceBoundary `json:"boundary"`
	RungIndex *uint32                       `json:"rung_index,omitempty"`
}

type PerformanceResultReference struct {
	JobID    string `json:"job_id"`
	ReportID string `json:"report_id"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

func validPerformanceTime(at time.Time) bool {
	_, offset := at.Zone()
	return !at.IsZero() && offset == 0
}

func (observation PerformanceObservation) Validate() error {
	identity := observation.Contract == CurrentContract && domain.IsUUID(observation.JobID)
	revision := observation.Revision > 0 && observation.Revision <= math.MaxInt64
	if !identity || !revision || !validPerformanceTime(observation.RecordedAt) {
		return invalid("performance_observation")
	}
	if err := observation.Checkpoint.Validate(); err != nil {
		return invalid("performance_observation.checkpoint")
	}
	document, err := json.Marshal(observation.Checkpoint)
	if err != nil || len(document) > quicktest.MaxPerformanceCheckpointBytes {
		return invalid("performance_observation.checkpoint.bytes")
	}
	return nil
}

func (observation PerformanceObservation) ValidateFor(spec JobSpec) error {
	if err := observation.Validate(); err != nil {
		return err
	}
	if spec.Kind != KindPerformance || spec.Spec.Performance == nil || observation.JobID != spec.JobID {
		return invalid("performance_observation.owner")
	}
	report := observation.Checkpoint.Report
	actual := quicktest.PreparedPerformance{
		ReportID: report.ReportID, Protocol: report.Protocol, AddressMode: report.AddressMode,
		BaseURL: report.BaseURL, Endpoint: report.Endpoint, ModelID: report.ModelID, Profile: report.Profile,
	}
	if !reflect.DeepEqual(actual, *spec.Spec.Performance) {
		return invalid("performance_observation.input")
	}
	return nil
}

func (result PerformanceResult) Validate() error {
	identity := result.Contract == CurrentContract && domain.IsUUID(result.JobID) && domain.IsUUID(result.ReportID)
	receipt := result.Terminal.Receipt
	owner := receipt.JobID == result.JobID && receipt.Kind == KindPerformance &&
		len(receipt.ReportIDs) == 1 && receipt.ReportIDs[0] == result.ReportID
	if !identity || !owner || result.Terminal.ValidateState() != nil {
		return invalid("performance_result.identity")
	}
	validSealTime := validPerformanceTime(result.SealedAt) && !result.SealedAt.Before(result.Terminal.UpdatedAt)
	if !Terminal(receipt.Status) || !validSealTime {
		return invalid("performance_result.terminal")
	}
	if result.Observation != nil {
		observation := result.Observation
		matching := observation.JobID == result.JobID && observation.Checkpoint.Report.ReportID == result.ReportID &&
			!observation.RecordedAt.Before(receipt.AcceptedAt) && !observation.RecordedAt.After(result.SealedAt)
		if !matching || !result.ExecutionStarted || observation.Validate() != nil {
			return invalid("performance_result.observation")
		}
	}
	returned := result.Observation != nil &&
		result.Observation.Checkpoint.Boundary == quicktest.PerformanceBoundaryReturned
	switch result.ObservationState {
	case PerformanceNotStarted:
		if result.ExecutionStarted || result.Observation != nil {
			return invalid("performance_result.not_started")
		}
	case PerformanceAvailable:
		if !returned {
			return invalid("performance_result.available")
		}
	case PerformanceInterrupted:
		if !result.ExecutionStarted || returned {
			return invalid("performance_result.interrupted")
		}
	default:
		return invalid("performance_result.observation_state")
	}
	if receipt.Status == domain.RunCompleted && !returned {
		return invalid("performance_result.completed")
	}
	return nil
}

func (reference PerformanceResultReference) Validate() error {
	identity := domain.IsUUID(reference.JobID) && domain.IsUUID(reference.ReportID) && IsSHA256(reference.SHA256)
	if !identity || reference.Size <= 0 || reference.Size > MaxPerformanceResultBytes {
		return invalid("performance_result.reference")
	}
	return nil
}
