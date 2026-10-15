package jobcontract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

func performanceObservationFixture(t *testing.T) (JobSpec, PerformanceObservation) {
	t.Helper()
	spec := performanceSpec(t)
	input := spec.Spec.Performance
	observation := PerformanceObservation{
		Contract: CurrentContract, JobID: spec.JobID, Revision: 1, RecordedAt: testMeta(t).CreatedAt,
		Checkpoint: quicktest.PerformanceCheckpoint{
			Stage: quicktest.PerformanceStagePreparing, Boundary: quicktest.PerformanceBoundaryEntered,
			Report: quicktest.PerformanceReport{
				SchemaVersion: quicktest.PerformanceSchemaVersion,
				ReportID:      input.ReportID, Protocol: input.Protocol, AddressMode: input.AddressMode,
				BaseURL: input.BaseURL, Endpoint: input.Endpoint, ModelID: input.ModelID, Profile: input.Profile,
				ArchiveStatus: quicktest.PerformanceArchiveNotAttempted,
				Progress:      quicktest.PerformanceProgress{Phase: quicktest.PerformancePhaseNotStarted},
				Samples:       []quicktest.PerformanceSample{}, Failures: []quicktest.PerformanceFailure{},
			},
		},
	}
	if err := observation.ValidateFor(spec); err != nil {
		t.Fatal(err)
	}
	return spec, observation
}

func TestPerformanceObservationMetadataAndFrozenOwnerBoundaries(t *testing.T) {
	spec, initial := performanceObservationFixture(t)
	for _, test := range []struct {
		name   string
		change func(*PerformanceObservation)
		valid  bool
	}{
		{name: "current", valid: true},
		{name: "zero contract", change: func(value *PerformanceObservation) { value.Contract = 0 }},
		{name: "other contract", change: func(value *PerformanceObservation) { value.Contract++ }},
		{name: "missing owner", change: func(value *PerformanceObservation) { value.JobID = "" }},
		{name: "wrong owner", change: func(value *PerformanceObservation) { value.JobID = testReportID }},
		{name: "zero revision", change: func(value *PerformanceObservation) { value.Revision = 0 }},
		{name: "max revision", change: func(value *PerformanceObservation) { value.Revision = math.MaxInt64 }, valid: true},
		{name: "revision overflow", change: func(value *PerformanceObservation) { value.Revision = math.MaxInt64 + 1 }},
		{name: "missing time", change: func(value *PerformanceObservation) { value.RecordedAt = time.Time{} }},
		{name: "non UTC", change: func(value *PerformanceObservation) {
			value.RecordedAt = value.RecordedAt.In(time.FixedZone("other", 3600))
		}},
		{name: "different model", change: func(value *PerformanceObservation) { value.Checkpoint.Report.ModelID = "other" }},
		{name: "different report", change: func(value *PerformanceObservation) {
			value.Checkpoint.Report.ReportID = spec.JobID
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := initial
			if test.change != nil {
				test.change(&value)
			}
			if (value.ValidateFor(spec) == nil) != test.valid {
				t.Fatal("metadata or immutable input validation differs from the current contract")
			}
		})
	}
}

func TestPerformanceResultObservationStateAndTerminalBoundaries(t *testing.T) {
	spec, observation := performanceObservationFixture(t)
	terminal, err := spec.InitialSnapshot(observation.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	terminal.Receipt.Status, terminal.Receipt.Revision = domain.RunCancelled, 2
	terminal.LatestSequence = 2
	terminal.UpdatedAt = terminal.UpdatedAt.Add(time.Second)
	initial := PerformanceResult{
		Contract: CurrentContract, JobID: spec.JobID, ReportID: testReportID,
		Terminal: terminal, SealedAt: terminal.UpdatedAt, ExecutionStarted: true,
		ObservationState: PerformanceInterrupted, Observation: &observation,
	}
	encoded, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*PerformanceResult)
		valid  bool
	}{
		{name: "partial observation", valid: true},
		{name: "unknown after start", change: func(value *PerformanceResult) { value.Observation = nil }, valid: true},
		{name: "known never started", change: func(value *PerformanceResult) {
			value.Observation, value.ExecutionStarted, value.ObservationState = nil, false, PerformanceNotStarted
		}, valid: true},
		{name: "available final error", change: func(value *PerformanceResult) {
			value.ObservationState = PerformanceAvailable
			value.Observation.Checkpoint.Boundary = quicktest.PerformanceBoundaryReturned
			value.Observation.Checkpoint.Report.ErrorCode = "request_failed"
		}, valid: true},
		{name: "old contract", change: func(value *PerformanceResult) { value.Contract = 0 }},
		{name: "wrong report", change: func(value *PerformanceResult) { value.ReportID = value.JobID }},
		{name: "unknown state", change: func(value *PerformanceResult) { value.ObservationState = "unknown" }},
		{name: "invented availability", change: func(value *PerformanceResult) {
			value.ObservationState = PerformanceAvailable
		}},
		{name: "observation without start", change: func(value *PerformanceResult) { value.ExecutionStarted = false }},
		{name: "claimed never started", change: func(value *PerformanceResult) {
			value.ObservationState = PerformanceNotStarted
		}},
		{name: "completed without return", change: func(value *PerformanceResult) {
			value.Terminal.Receipt.Status = domain.RunCompleted
		}},
		{name: "active result", change: func(value *PerformanceResult) { value.Terminal.Receipt.Status = domain.RunRunning }},
		{name: "missing time", change: func(value *PerformanceResult) { value.SealedAt = time.Time{} }},
		{name: "time before terminal", change: func(value *PerformanceResult) {
			value.SealedAt = value.Terminal.UpdatedAt.Add(-time.Second)
		}},
		{name: "observation before admission", change: func(value *PerformanceResult) {
			value.Observation.RecordedAt = value.Terminal.Receipt.AcceptedAt.Add(-time.Second)
		}},
		{name: "observation after seal", change: func(value *PerformanceResult) {
			value.Observation.RecordedAt = value.SealedAt.Add(time.Second)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value PerformanceResult
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				test.change(&value)
			}
			if (value.Validate() == nil) != test.valid {
				t.Fatal("result validation differs from the actual observation boundary")
			}
		})
	}
}

func TestPerformanceResultReferenceByteAndIdentityBounds(t *testing.T) {
	for _, size := range []int64{0, 1, MaxPerformanceResultBytes, MaxPerformanceResultBytes + 1} {
		reference := PerformanceResultReference{
			JobID: testReportID, ReportID: testReportID, SHA256: strings.Repeat("a", 64), Size: size,
		}
		valid := size > 0 && size <= MaxPerformanceResultBytes
		if (reference.Validate() == nil) != valid {
			t.Fatalf("result size %d: validity differs from %v", size, valid)
		}
		reference.SHA256 = "wrong"
		if reference.Validate() == nil {
			t.Fatal("invalid result digest accepted")
		}
	}
}
