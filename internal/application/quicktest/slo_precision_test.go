package quicktest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestPerformanceSLOPrecisionAndObservationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name             string
		value, limit     float64
		observed, failed bool
	}{
		{name: "zero", limit: 50, observed: true},
		{name: "missing", limit: 50, failed: true},
		{name: "equal", value: 50, limit: 50, observed: true},
		{name: "round down", value: 50.004, limit: 50, observed: true},
		{name: "round up", value: 50.005, limit: 50, observed: true, failed: true},
		{name: "round both", value: 50.006, limit: 50.005, observed: true},
		{name: "decimal tie", value: 1.005, limit: 1.01, observed: true},
		{name: "tiny enabled threshold", value: 0.004, limit: 0.000001, observed: true},
		{name: "disabled", value: 50, limit: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := latencySLOViolated(test.value, test.limit, test.observed, domain.SLALatencyTwoDecimalsV1)
			if got != test.failed {
				t.Fatalf("violation = %v, want %v", got, test.failed)
			}
		})
	}
	progress := PerformanceProgress{Phase: load.PhaseCompleted, Completed: 1, TotalDurationMS: 1000}
	profile := PerformanceProfile{SLOTTFTMS: 50, SLOTPOTMS: 50, SLOE2EMS: 50, SLOTargetPercent: 100}
	samples := []PerformanceSample{{Success: true, TTFTObserved: true, CompletionTokens: 2}}
	assessment := buildPerformanceSLOAssessment(profile, samples, progress)
	if assessment.Status != PerformanceSLOPassed || assessment.GoodRequests != 1 {
		t.Fatalf("observed zeros must pass: %+v", assessment)
	}
	samples[0].CompletionTokens = 1
	assessment = buildPerformanceSLOAssessment(profile, samples, progress)
	if assessment.Violations.TPOT != 1 || assessment.Violations.TTFT != 0 || assessment.Violations.E2E != 0 {
		t.Fatalf("missing TPOT must not pass: %+v", assessment)
	}
	samples[0].TTFTObserved = false
	assessment = buildPerformanceSLOAssessment(profile, samples, progress)
	if assessment.Violations.TTFT != 1 {
		t.Fatal("missing first token must not pass")
	}
}

func readPrecisionReport(t *testing.T, name string) PerformanceReport {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var report PerformanceReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestHistoricalPerformanceSLOPrecisionPreservesReports(t *testing.T) {
	for _, name := range []string{"boundary", "zero"} {
		report := readPrecisionReport(t, "legacy-slo-"+name+"-220a2185")
		before, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateArchivedPerformanceReport(report); err != nil {
			t.Fatal(err)
		}
		if report.SLOAssessment.LatencyPrecision != "" || report.SLOAssessment.Status != PerformanceSLOFailed {
			t.Fatal("legacy precision or conclusion changed")
		}
		RecomputePerformanceDerivedFields(&report)
		after, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("historical report was reinterpreted")
		}
		report.SLOAssessment.LatencyPrecision = "future_precision"
		if _, err := ValidateArchivedPerformanceReport(report); err == nil {
			t.Fatal("unknown comparison semantics accepted")
		}
	}
}

func currentPrecisionReport(t *testing.T, zero bool) PerformanceReport {
	t.Helper()
	report := readPrecisionReport(t, "legacy-slo-boundary-220a2185")
	report.Samples[0].TTFTObserved = true
	if zero {
		report.Samples = []PerformanceSample{{Success: true, HTTPStatus: 200,
			TTFTObserved: true, SemanticChunkCount: 2, CompletionTokens: 2}}
		report.Profile.SLOTTFTMS, report.Profile.SLOTPOTMS = 1, 1
	}
	for index := range report.Samples {
		sample := &report.Samples[index]
		sample.TPOTMS = performanceTPOTMilliseconds(
			sample.TTFTMS, sample.E2EMS, sample.CompletionTokens, sample.TTFTObserved,
		)
	}
	report.Profile.SliceDurationMS = 1000
	budget, _, err := preparePerformanceProfile(report.Profile)
	if err != nil {
		t.Fatal(err)
	}
	report.RequestBudget = budget
	RecomputePerformanceDerivedFields(&report)
	report.SLOAssessment = buildPerformanceSLOAssessment(report.Profile, report.Samples, report.Progress)
	return report
}

func TestCurrentPerformancePrecisionSurvivesArchiveAndCheckpointValidation(t *testing.T) {
	for _, zero := range []bool{false, true} {
		report := currentPrecisionReport(t, zero)
		if _, err := ValidateArchivedPerformanceReport(report); err != nil {
			t.Fatal(err)
		}
		if report.SLOAssessment.Status != PerformanceSLOPassed {
			t.Fatalf("new precision failed: %+v", report.SLOAssessment)
		}
		checkpoint := PerformanceCheckpoint{
			Stage: PerformanceStageMeasured, Boundary: PerformanceBoundaryReturned, Report: report,
		}
		if err := checkpoint.Validate(); err != nil {
			t.Fatal(err)
		}
		if zero && (report.Metrics.TTFTSamples != 1 || report.TimeSlices[0].TTFT.Count != 1) {
			t.Fatal("true zero was removed from distributions")
		}
		name := "current-slo-boundary"
		if zero {
			name = "current-slo-zero"
		}
		fixture := readPrecisionReport(t, name)
		got, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("shared frontend/SQLite fixture drifted from the Go producer")
		}
	}
}

func TestCapacityUsesRoundedLatencyForSelectionAndStop(t *testing.T) {
	profile := currentPrecisionReport(t, false).Profile
	profile.Concurrency, profile.CapacityEnabled = 3, true
	profile.CapacityStart, profile.CapacityStep = 1, 1
	profile.SLOE2EMS, profile.SLOTTFTMS, profile.SLOTPOTMS = 1, 1, 1
	base := readPrecisionReport(t, "legacy-slo-boundary-220a2185")
	base.Profile = profile
	report := preparedPerformance(base, base.ReportID).report()
	budget, _, err := preparePerformanceProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	report.RequestBudget = budget
	archive := &capturingPerformanceArchive{}
	host := NewHost(HostDependencies{Archive: archive})
	requests := 0
	executor := func(context.Context, load.Request) load.Observation {
		// The controlled interval is shorter than the actual request window,
		// so checkpoint timing algebra remains valid without clock-dependent SLA boundaries.
		started := time.Now()
		for time.Since(started) < 2*time.Millisecond {
			time.Sleep(time.Millisecond)
		}
		requests++
		e2e := 1004 * time.Microsecond
		if requests == 2 {
			e2e = 1005 * time.Microsecond
		}
		return load.Observation{Success: true, Streaming: true, StreamComplete: true,
			TTFTObserved: true, SemanticChunkCount: 2, CompletionTokens: 2, E2E: e2e}
	}
	control := performanceControl{reportID: report.ReportID, checkpoints: &performanceCheckpointObserver{
		callback: func(_ context.Context, checkpoint PerformanceCheckpoint) error { return checkpoint.Validate() },
	}}
	result, err := host.runPerformanceCapacity(
		t.Context(), report, executor, newPerformanceEvidenceRecorder(), nil, control,
	)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(result.CapacityResult.Rungs) != 2 || *result.CapacityResult.SelectedRungIndex != 0 {
		t.Fatalf("wrong capacity selection: requests=%d, result=%+v", requests, result.CapacityResult)
	}
	if result.CapacityResult.Rungs[0].SLOAssessment.Status != PerformanceSLOPassed ||
		result.CapacityResult.Rungs[1].SLOAssessment.Status != PerformanceSLOFailed {
		t.Fatal("capacity failed to use rounded latency")
	}
	if _, err := ValidateArchivedPerformanceReport(result); err != nil {
		t.Fatal(err)
	}
	result.CapacityResult.Rungs[1].SLOAssessment.LatencyPrecision = ""
	if _, err := ValidateArchivedPerformanceReport(result); err == nil {
		t.Fatal("mixed capacity precision accepted")
	}
}

func TestNativePerformanceMarksObservedZeroFirstToken(t *testing.T) {
	status, complete := 200, true
	observation := nativePerformanceObservation(0, testspec.Observation{
		HTTPStatus: &status, StreamCompleted: &complete,
		Metrics: map[string]float64{"ttft_ms": 0, "semantic_chunk_count": 2, "completion_tokens": 2},
	})
	executor := streamingPerformanceExecutor(func(context.Context, load.Request) load.Observation { return observation })
	measured := executor(t.Context(), load.Request{})
	if !measured.Success || !measured.TTFTObserved {
		t.Fatal("semantic events did not mark the real zero")
	}
	samples := performanceSamples([]load.Observation{measured}, map[uint64]*PerformanceResponseEvidence{}, nil)
	if !samples[0].TTFTObserved || samples[0].TTFTMS != 0 || samples[0].TPOTMS != 0 {
		t.Fatal("the reporting boundary changed the zero observation")
	}
}
