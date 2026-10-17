package quicktest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func checkpointLabel(checkpoint PerformanceCheckpoint) string {
	label := string(checkpoint.Stage) + "/" + string(checkpoint.Boundary)
	if checkpoint.RungIndex != nil {
		label += fmt.Sprintf("/%d", *checkpoint.RungIndex)
	}
	return label
}

func checkpointTestCommand() PerformanceCommand {
	command := hostTestCommand("https://example.test")
	command.WarmupRequests = 1
	command.RampDurationMS, command.RampRequestCap = 20, 100
	return command
}

func checkpointTestHost(requests *atomic.Int64) *Host {
	return NewHost(HostDependencies{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return successfulStreamResponse(), nil
	})})
}

func TestPerformanceCheckpointsFreezeAllPhaseBoundaries(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), checkpointTestCommand())
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	var requests atomic.Int64
	checkpoints := []PerformanceCheckpoint{}
	labels := []string{}
	report, err := checkpointTestHost(&requests).ExecutePrepared(
		context.Background(),
		prepared,
		lease,
		ExecutionOptions{OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
			if err := checkpoint.ValidateEnvelope(); err != nil {
				t.Fatalf("invalid emitted checkpoint: %s", checkpointLabel(checkpoint))
			}
			checkpoints = append(checkpoints, checkpoint)
			labels = append(labels, checkpointLabel(checkpoint))
			return nil
		}},
	)
	if err != nil || !report.Success {
		t.Fatalf(
			"execute: success=%v code=%s error=%v",
			report.Success,
			report.ErrorCode,
			err,
		)
	}
	want := []string{
		"preparing/entered", "preparing/completed", "warmup/entered", "warmup/completed",
		"ramp/entered", "ramp/completed", "measured/entered", "measured/completed", "measured/returned",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("phase order: %v", labels)
	}
	if checkpoints[2].Report.Warmup != nil || checkpoints[3].Report.Warmup.Completed != 1 {
		t.Fatal("later phases changed the entered warmup snapshot")
	}
	if checkpoints[4].Report.Ramp != nil || checkpoints[5].Report.Ramp.Traffic.Completed == 0 {
		t.Fatal("ramp observation is absent or changed a prior checkpoint")
	}
	if len(checkpoints[6].Report.Samples) != 0 || len(checkpoints[7].Report.Samples) != 2 {
		t.Fatal("measured observation crossed its recorded execution boundary")
	}
	if !reflect.DeepEqual(checkpoints[8].Report, report) {
		t.Fatal("returned checkpoint differs from the actual report")
	}
	if requests.Load() != int64(1+report.Ramp.Traffic.Launched+2) {
		t.Fatal("observations do not account for the actual HTTP requests")
	}
}

func TestPerformanceCheckpointFailureStopsBeforeFurtherTraffic(t *testing.T) {
	labels := []string{
		"preparing/entered", "preparing/completed", "warmup/entered", "warmup/completed",
		"ramp/entered", "ramp/completed", "measured/entered", "measured/completed", "measured/returned",
	}
	for _, failingLabel := range labels {
		t.Run(failingLabel, func(t *testing.T) {
			prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), checkpointTestCommand())
			if code != "" {
				t.Fatalf("prepare: %s", code)
			}
			var requests atomic.Int64
			var failed bool
			var requestsAtFailure int64
			_, err := checkpointTestHost(&requests).ExecutePrepared(
				context.Background(),
				prepared,
				lease,
				ExecutionOptions{OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
					if failed {
						t.Fatal("execution retried a failed checkpoint or advanced to a later boundary")
					}
					if checkpointLabel(checkpoint) == failingLabel {
						failed = true
						requestsAtFailure = requests.Load()
						return errors.New("storage error containing prepared-node-secret")
					}
					return nil
				}},
			)
			if !failed || !errors.Is(err, ErrPerformanceCheckpoint) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("checkpoint failure was lost or exposed its diagnostic: %v", err)
			}
			if requests.Load() != requestsAtFailure {
				t.Fatal("checkpoint failure caused later paid traffic")
			}
			if _, err := lease.Bytes(); err == nil {
				t.Fatal("checkpoint failure retained the execution credential")
			}
		})
	}
}

func TestCapacityCheckpointsPreserveCompletedRungsAndIsolateObservers(t *testing.T) {
	command := hostTestCommand("https://example.test")
	command.Concurrency = 3
	command.CapacityEnabled = true
	command.CapacityStart, command.CapacityStep = 1, 1
	command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), command)
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	var requests atomic.Int64
	labels := []string{}
	var firstCompleted PerformanceCheckpoint
	report, err := checkpointTestHost(&requests).ExecutePrepared(
		context.Background(),
		prepared,
		lease,
		ExecutionOptions{OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
			label := checkpointLabel(checkpoint)
			labels = append(labels, label)
			if checkpoint.Stage != PerformanceStageCapacity {
				return nil
			}
			if label == "capacity/completed/0" {
				firstCompleted = checkpoint
			}
			if label == "capacity/entered/1" {
				checkpoint.Report.CapacityResult.Rungs[0].Target = 999
				checkpoint.Report.Samples[0].PromptTokens = 999
				checkpoint.Report.SLOAssessment.Status = PerformanceSLOFailed
				checkpoint.Report.Profile.RequestCount = 999
				*checkpoint.RungIndex = 999
			}
			return nil
		}},
	)
	if err != nil || !report.Success || requests.Load() != 6 {
		t.Fatalf(
			"capacity execution: requests=%d success=%v error=%v",
			requests.Load(),
			report.Success,
			err,
		)
	}
	want := []string{
		"preparing/entered", "preparing/completed", "capacity/entered/0", "capacity/completed/0",
		"capacity/entered/1", "capacity/completed/1", "capacity/entered/2", "capacity/completed/2", "capacity/returned/2",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("capacity boundary order: %v", labels)
	}
	if len(firstCompleted.Report.CapacityResult.Rungs) != 1 || firstCompleted.Report.CapacityResult.Rungs[0].Target != 1 {
		t.Fatal("later capacity observations mutated the first completed rung")
	}
	if report.CapacityResult.Status != PerformanceSLOPassed || *report.CapacityResult.SelectedRungIndex != 2 {
		t.Fatal("observer mutation changed capacity selection")
	}
	if report.Profile.RequestCount != 2 || report.Samples[0].PromptTokens != 0 {
		t.Fatal("observer mutation changed later execution measurements")
	}
}

func TestCapacityCheckpointFailureDoesNotLaunchAnotherRung(t *testing.T) {
	for _, failingLabel := range []string{
		"capacity/entered/0", "capacity/completed/0", "capacity/entered/1", "capacity/completed/1",
		"capacity/entered/2", "capacity/completed/2", "capacity/returned/2",
	} {
		t.Run(failingLabel, func(t *testing.T) {
			command := hostTestCommand("https://example.test")
			command.Concurrency = 3
			command.CapacityEnabled = true
			command.CapacityStart, command.CapacityStep = 1, 1
			command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
			prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), command)
			if code != "" {
				t.Fatalf("prepare: %s", code)
			}
			var requests atomic.Int64
			requestsAtFailure := int64(-1)
			_, err := checkpointTestHost(&requests).ExecutePrepared(
				context.Background(),
				prepared,
				lease,
				ExecutionOptions{OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
					if requestsAtFailure >= 0 {
						t.Fatal("execution advanced after checkpoint failure")
					}
					if checkpointLabel(checkpoint) == failingLabel {
						requestsAtFailure = requests.Load()
						return errors.New("storage unavailable")
					}
					return nil
				}},
			)
			if !errors.Is(err, ErrPerformanceCheckpoint) || requestsAtFailure < 0 || requests.Load() != requestsAtFailure {
				t.Fatalf(
					"capacity advanced after failure: count=%d at failure=%d error=%v",
					requests.Load(),
					requestsAtFailure,
					err,
				)
			}
		})
	}
}

func TestCancelledPerformanceStillCheckpointsItsActualFinalObservation(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "execution"))
	defer cancel()
	prepared, lease, code := New(Dependencies{}).BuildPerformance(ctx, hostTestCommand("https://example.test"))
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	var requests atomic.Int64
	labels := []string{}
	report, err := checkpointTestHost(&requests).ExecutePrepared(
		ctx,
		prepared,
		lease,
		ExecutionOptions{
			OnCheckpoint: func(storeContext context.Context, checkpoint PerformanceCheckpoint) error {
				labels = append(labels, checkpointLabel(checkpoint))
				if checkpoint.Stage == PerformanceStageMeasured && checkpoint.Boundary == PerformanceBoundaryEntered {
					cancel()
				}
				if storeContext.Err() != nil || storeContext.Value(contextKey{}) != "execution" {
					t.Fatal("execution cancellation leaked into the independent storage context")
				}
				deadline, bounded := storeContext.Deadline()
				if !bounded || time.Until(deadline) > performanceArchiveTimeout {
					t.Fatal("checkpoint storage has no bounded deadline")
				}
				return nil
			},
		},
	)
	if err != nil || requests.Load() != 0 || report.ErrorCode != "cancelled" {
		t.Fatalf(
			"cancelled observation: requests=%d errorCode=%s error=%v",
			requests.Load(),
			report.ErrorCode,
			err,
		)
	}
	if labels[len(labels)-1] != "measured/returned" || len(report.Samples) != 0 {
		t.Fatal("cancelled execution lost its final zero-sample observation")
	}
}

func TestPerformanceCheckpointEnvelopeAndByteBoundaries(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), hostTestCommand("https://example.test"))
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	defer lease.Close()
	valid := PerformanceCheckpoint{
		Stage: PerformanceStagePreparing, Boundary: PerformanceBoundaryEntered, Report: prepared.report(),
	}
	mutations := []struct {
		name   string
		mutate func(*PerformanceCheckpoint)
	}{
		{name: "unknown stage", mutate: func(value *PerformanceCheckpoint) { value.Stage = "unknown" }},
		{name: "unknown boundary", mutate: func(value *PerformanceCheckpoint) { value.Boundary = "unknown" }},
		{name: "unconfigured warmup", mutate: func(value *PerformanceCheckpoint) { value.Stage = PerformanceStageWarmup }},
		{name: "unconfigured ramp", mutate: func(value *PerformanceCheckpoint) { value.Stage = PerformanceStageRamp }},
		{name: "unconfigured capacity", mutate: func(value *PerformanceCheckpoint) { value.Stage = PerformanceStageCapacity }},
		{name: "noncapacity rung", mutate: func(value *PerformanceCheckpoint) { value.RungIndex = new(uint32) }},
		{name: "old report", mutate: func(value *PerformanceCheckpoint) { value.Report.SchemaVersion-- }},
		{name: "invalid identity", mutate: func(value *PerformanceCheckpoint) { value.Report.ReportID = "invalid" }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if value.ValidateEnvelope() == nil {
				t.Fatal("invalid checkpoint envelope accepted")
			}
		})
	}
	valid.Report.ModelID = "m"
	document, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	observer := performanceCheckpointObserver{callback: func(context.Context, PerformanceCheckpoint) error {
		calls++
		return nil
	}}
	valid.Report.ModelID = strings.Repeat("m", MaxPerformanceCheckpointBytes-len(document)+1)
	if err := observer.emit(context.Background(), valid); err != nil || calls != 1 {
		t.Fatalf("exact byte limit was rejected: calls=%d error=%v", calls, err)
	}
	valid.Report.ModelID += "m"
	if !errors.Is(observer.emit(context.Background(), valid), ErrPerformanceCheckpoint) || calls != 1 {
		t.Fatal("oversized checkpoint reached storage")
	}
}

func TestPerformanceCapacityCheckpointRungBounds(t *testing.T) {
	command := hostTestCommand("https://example.test")
	command.Concurrency = MaxPerformanceCapacityRungs
	command.CapacityEnabled = true
	command.CapacityStart, command.CapacityStep = 1, 1
	command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), command)
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	defer lease.Close()
	for _, test := range []struct {
		index uint32
		valid bool
	}{
		{index: 0, valid: true},
		{index: MaxPerformanceCapacityRungs - 1, valid: true},
		{index: MaxPerformanceCapacityRungs, valid: false},
		{index: ^uint32(0), valid: false},
	} {
		index := test.index
		checkpoint := PerformanceCheckpoint{
			Stage: PerformanceStageCapacity, Boundary: PerformanceBoundaryEntered,
			RungIndex: &index, Report: prepared.report(),
		}
		if (checkpoint.ValidateEnvelope() == nil) != test.valid {
			t.Fatalf("rung %d validity differs from %v", test.index, test.valid)
		}
	}
	checkpoint := PerformanceCheckpoint{
		Stage: PerformanceStageMeasured, Boundary: PerformanceBoundaryEntered, Report: prepared.report(),
	}
	if checkpoint.ValidateEnvelope() == nil {
		t.Fatal("capacity profile accepted a noncapacity measurement stage")
	}
}
