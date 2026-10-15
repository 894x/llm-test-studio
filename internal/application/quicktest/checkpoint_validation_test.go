package quicktest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestPerformanceCheckpointFactsAcceptStoppedAndCancelledPhaseObservations(t *testing.T) {
	for _, phase := range []PerformanceStage{
		PerformanceStageWarmup, PerformanceStageRamp, PerformanceStageMeasured, PerformanceStageCapacity,
	} {
		for _, abort := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				t.Run(string(phase)+checkpointTestFlags(abort, empty), func(t *testing.T) {
					command := checkpointTestCommand()
					command.SliceDurationMS = 10
					if phase == PerformanceStageCapacity {
						command.RampDurationMS, command.RampRequestCap = 0, 0
						command.CapacityEnabled = true
						command.Concurrency = 3
						command.CapacityStart, command.CapacityStep = 1, 1
						command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					prepared, lease, code := New(Dependencies{}).BuildPerformance(ctx, command)
					if code != "" {
						t.Fatalf("prepare: %s", code)
					}
					stop := make(chan struct{})
					stopped := false
					stopExecution := func() {
						if !stopped {
							stopped = true
							if abort {
								cancel()
							} else {
								close(stop)
							}
						}
					}
					current := PerformanceStagePreparing
					labels := []string{}
					var requests atomic.Int64
					report, err := checkpointTestHost(&requests).ExecutePrepared(
						ctx,
						prepared,
						lease,
						ExecutionOptions{
							StopSending: stop,
							OnProgress: func(progress PerformanceProgress) {
								if !empty && current == phase && progress.Launched > 0 {
									stopExecution()
								}
							},
							OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
								labels = append(labels, checkpointLabel(checkpoint))
								current = checkpoint.Stage
								if empty && current == phase && checkpoint.Boundary == PerformanceBoundaryEntered {
									stopExecution()
								}
								return nil
							},
						},
					)
					if err != nil || !stopped {
						t.Fatalf(
							"phase observation rejected: boundaries=%v progress=%+v error=%v",
							labels,
							report.Progress,
							err,
						)
					}
					last := labels[len(labels)-1]
					want := string(phase) + "/returned"
					if phase == PerformanceStageCapacity {
						want += "/0"
					}
					if last != want || report.Archived {
						t.Fatalf("observation advanced or archived: last=%s archived=%v", last, report.Archived)
					}
				})
			}
		}
	}
}

func TestPerformanceCheckpointFactsAcceptOpenLoopAndArchivedObservations(t *testing.T) {
	command := hostTestCommand("https://example.test")
	command.LoadMode = domain.LoadOpenLoop
	command.Concurrency = 0
	command.RatePerSecond = 100
	command.MaxInFlight = 2
	command.SliceDurationMS = 10
	command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), command)
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	archive := &capturingPerformanceArchive{}
	var returned PerformanceCheckpoint
	host := NewHost(HostDependencies{
		Archive: archive,
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return successfulStreamResponse(), nil
		}),
	})
	report, err := host.ExecutePrepared(
		context.Background(),
		prepared,
		lease,
		ExecutionOptions{
			OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
				if checkpoint.Boundary == PerformanceBoundaryReturned {
					returned = checkpoint
				}
				return nil
			},
		},
	)
	if err != nil || !report.Archived || returned.Validate() != nil || len(archive.saved) != 1 {
		t.Fatalf("open-loop archive observation: archived=%v error=%v", report.Archived, err)
	}
	if _, err := ValidateArchivedPerformanceReport(returned.Report); err != nil {
		t.Fatalf("observation weakened the statistical archive gate: %v", err)
	}
	returned.Report.Samples = []PerformanceSample{}
	if _, err := ValidateArchivedPerformanceReport(returned.Report); err == nil {
		t.Fatal("statistical archive accepted a missing measured sample set")
	}
}

func TestPerformanceCheckpointFactsPreserveHighestPassingRungWhenNextRungFails(t *testing.T) {
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
	var returned PerformanceCheckpoint
	host := NewHost(HostDependencies{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		if requests.Add(1) <= 2 {
			return successfulStreamResponse(), nil
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"controlled failure"}}`)),
		}, nil
	})})
	report, err := host.ExecutePrepared(
		context.Background(),
		prepared,
		lease,
		ExecutionOptions{
			OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
				if checkpoint.Boundary == PerformanceBoundaryReturned {
					returned = checkpoint
				}
				return nil
			},
		},
	)
	if err != nil || requests.Load() != 4 || returned.Validate() != nil {
		t.Fatalf("failed rung observation: requests=%d error=%v", requests.Load(), err)
	}
	if report.CapacityResult.Status != PerformanceSLOFailed || *report.CapacityResult.SelectedRungIndex != 0 {
		t.Fatal("failed rung replaced the highest passing measured projection")
	}
	encoded, err := json.Marshal(returned)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PerformanceCheckpoint)
	}{
		{name: "wrong selection", mutate: func(value *PerformanceCheckpoint) { *value.Report.CapacityResult.SelectedRungIndex = 1 }},
		{name: "wrong highest", mutate: func(value *PerformanceCheckpoint) { value.Report.CapacityResult.HighestPassingRungIndex = nil }},
		{name: "wrong rung target", mutate: func(value *PerformanceCheckpoint) { value.Report.CapacityResult.Rungs[1].Target = 3 }},
		{name: "wrong rung metrics", mutate: func(value *PerformanceCheckpoint) { value.Report.CapacityResult.Rungs[1].Metrics.Completed++ }},
		{name: "wrong projection", mutate: func(value *PerformanceCheckpoint) { value.Report.Samples[0].PromptTokens++ }},
		{name: "continued failed rung", mutate: func(value *PerformanceCheckpoint) {
			value.Boundary = PerformanceBoundaryEntered
			*value.RungIndex = 2
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value PerformanceCheckpoint
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(&value)
			if value.Validate() == nil {
				t.Fatal("corrupt capacity observation was accepted")
			}
		})
	}
}

func checkpointTestFlags(abort, empty bool) string {
	label := "/stop"
	if abort {
		label = "/cancel"
	}
	if empty {
		label += "/before-traffic"
	} else {
		label += "/in-flight"
	}
	return label
}

func TestPerformanceCheckpointFactsRejectCorruptMeasuredAndPreparationData(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), checkpointTestCommand())
	if code != "" {
		t.Fatalf("prepare: %s", code)
	}
	var requests atomic.Int64
	var measured PerformanceCheckpoint
	_, err := checkpointTestHost(&requests).ExecutePrepared(
		context.Background(),
		prepared,
		lease,
		ExecutionOptions{
			OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
				if checkpoint.Stage == PerformanceStageMeasured && checkpoint.Boundary == PerformanceBoundaryCompleted {
					measured = checkpoint
				}
				return nil
			},
		},
	)
	if err != nil || measured.Validate() != nil {
		t.Fatalf("fixture execution: %v", err)
	}
	mutations := []struct {
		name   string
		mutate func(*PerformanceCheckpoint)
	}{
		{name: "missing samples", mutate: func(value *PerformanceCheckpoint) { value.Report.Samples = nil }},
		{name: "wrong tokens", mutate: func(value *PerformanceCheckpoint) { value.Report.Metrics.PromptTokens++ }},
		{name: "wrong counts", mutate: func(value *PerformanceCheckpoint) { value.Report.Progress.Completed++ }},
		{name: "wrong plan", mutate: func(value *PerformanceCheckpoint) { value.Report.Progress.Planned++ }},
		{name: "active phase", mutate: func(value *PerformanceCheckpoint) { value.Report.Progress.Phase = load.PhaseSending }},
		{name: "active requests", mutate: func(value *PerformanceCheckpoint) { value.Report.Progress.InFlight++ }},
		{name: "capacity decoration", mutate: func(value *PerformanceCheckpoint) { value.Report.Progress.CapacityTarget = 1 }},
		{name: "missing warmup", mutate: func(value *PerformanceCheckpoint) { value.Report.Warmup = nil }},
		{name: "wrong warmup", mutate: func(value *PerformanceCheckpoint) { value.Report.Warmup.Completed++ }},
		{name: "missing ramp", mutate: func(value *PerformanceCheckpoint) { value.Report.Ramp = nil }},
		{name: "wrong ramp", mutate: func(value *PerformanceCheckpoint) { value.Report.Ramp.Steps++ }},
		{name: "wrong budget", mutate: func(value *PerformanceCheckpoint) { value.Report.RequestBudget.TotalCap++ }},
		{name: "raw error", mutate: func(value *PerformanceCheckpoint) { value.Report.ErrorCode = "secret error text" }},
		{name: "invented archive", mutate: func(value *PerformanceCheckpoint) { value.Report.Archived = true }},
		{name: "invented sample", mutate: func(value *PerformanceCheckpoint) { value.Report.Samples[0].PromptTokens++ }},
		{name: "duplicate sample", mutate: func(value *PerformanceCheckpoint) { value.Report.Samples[1] = value.Report.Samples[0] }},
	}
	encoded, err := json.Marshal(measured)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			var value PerformanceCheckpoint
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(&value)
			if value.Validate() == nil {
				t.Fatal("corrupt current observation was accepted")
			}
		})
	}
}
