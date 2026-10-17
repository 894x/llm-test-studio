package quicktest

import (
	"context"
	"encoding/json"
	"errors"
)

const MaxPerformanceCheckpointBytes = 32 << 20

var ErrPerformanceCheckpoint = errors.New("quick performance checkpoint could not be saved")

type PerformanceStage string

const (
	PerformanceStagePreparing PerformanceStage = "preparing"
	PerformanceStageWarmup    PerformanceStage = "warmup"
	PerformanceStageRamp      PerformanceStage = "ramp"
	PerformanceStageMeasured  PerformanceStage = "measured"
	PerformanceStageCapacity  PerformanceStage = "capacity"
)

type PerformanceBoundary string

const (
	PerformanceBoundaryEntered   PerformanceBoundary = "entered"
	PerformanceBoundaryCompleted PerformanceBoundary = "completed"
	PerformanceBoundaryReturned  PerformanceBoundary = "returned"
)

// A checkpoint describes the execution boundary, independently of the selected
// measured projection in Report. An entered stage may have unobserved traffic
// after a process interruption, even when its previous report has zero samples.
type PerformanceCheckpoint struct {
	Stage     PerformanceStage    `json:"stage"`
	Boundary  PerformanceBoundary `json:"boundary"`
	RungIndex *uint32             `json:"rung_index,omitempty"`
	Report    PerformanceReport   `json:"report"`
}

// ValidateEnvelope checks the boundary and current prepared identity. Storage
// additionally validates the recorded traffic and measurements before writing.
func (checkpoint PerformanceCheckpoint) ValidateEnvelope() error {
	report := checkpoint.Report
	if report.ExecutionOrigin != nil && report.ExecutionOrigin.Validate() != nil {
		return ErrPerformanceCheckpoint
	}
	if report.SchemaVersion != PerformanceSchemaVersion || preparedPerformance(report, report.ReportID).Validate() != nil {
		return ErrPerformanceCheckpoint
	}
	switch checkpoint.Boundary {
	case PerformanceBoundaryEntered, PerformanceBoundaryCompleted, PerformanceBoundaryReturned:
	default:
		return ErrPerformanceCheckpoint
	}
	if checkpoint.Stage != PerformanceStageCapacity && checkpoint.RungIndex != nil {
		return ErrPerformanceCheckpoint
	}
	switch checkpoint.Stage {
	case PerformanceStagePreparing:
	case PerformanceStageWarmup:
		if report.Profile.WarmupRequests == 0 {
			return ErrPerformanceCheckpoint
		}
	case PerformanceStageRamp:
		if report.Profile.RampDurationMS == 0 {
			return ErrPerformanceCheckpoint
		}
	case PerformanceStageMeasured:
		if report.Profile.CapacityEnabled {
			return ErrPerformanceCheckpoint
		}
	case PerformanceStageCapacity:
		targets, err := buildPerformanceCapacityTargets(report.Profile)
		if err != nil || !report.Profile.CapacityEnabled || checkpoint.RungIndex == nil {
			return ErrPerformanceCheckpoint
		}
		if uint64(*checkpoint.RungIndex) >= uint64(len(targets)) {
			return ErrPerformanceCheckpoint
		}
	default:
		return ErrPerformanceCheckpoint
	}
	return nil
}

type performanceCheckpointObserver struct {
	callback func(context.Context, PerformanceCheckpoint) error
	stage    PerformanceStage
	rung     *uint32
}

func (observer *performanceCheckpointObserver) emit(
	ctx context.Context,
	checkpoint PerformanceCheckpoint,
) error {
	observer.stage = checkpoint.Stage
	observer.rung = nil
	if checkpoint.RungIndex != nil {
		rung := *checkpoint.RungIndex
		observer.rung = &rung
	}
	if observer.callback == nil {
		return nil
	}
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	document, err := json.Marshal(checkpoint)
	if err != nil || len(document) > MaxPerformanceCheckpointBytes {
		return ErrPerformanceCheckpoint
	}
	// The observer may retain or mutate its value. Later phases must continue
	// with their own samples, evidence, profiles and capacity projections.
	var frozen PerformanceCheckpoint
	if err := json.Unmarshal(document, &frozen); err != nil {
		return ErrPerformanceCheckpoint
	}
	storeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), performanceArchiveTimeout)
	defer cancel()
	if err := observer.callback(storeContext, frozen); err != nil {
		return ErrPerformanceCheckpoint
	}
	return nil
}
