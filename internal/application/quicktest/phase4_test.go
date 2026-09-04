package quicktest

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestBuildPerformanceSLOAssessmentCountsEveryCompletedRequest(t *testing.T) {
	profile := PerformanceProfile{
		SLOTTFTMS: 100, SLOTPOTMS: 10, SLOE2EMS: 500, SLOTargetPercent: 50,
	}
	samples := []PerformanceSample{
		{Success: true, TTFTMS: 100, TPOTMS: 10, E2EMS: 500},
		{Success: true, TTFTMS: 0, TPOTMS: 5, E2EMS: 400},
		{Success: true, TTFTMS: 50, TPOTMS: 11, E2EMS: 501},
		{Success: false, ErrorCode: load.ErrorHTTP},
	}
	progress := PerformanceProgress{Phase: load.PhaseCompleted, Completed: 4, TotalDurationMS: 2_000}

	assessment := buildPerformanceSLOAssessment(profile, samples, progress)
	if assessment == nil {
		t.Fatal("buildPerformanceSLOAssessment() = nil")
	}
	if assessment.Status != PerformanceSLOFailed || assessment.TotalRequests != 4 || assessment.GoodRequests != 1 ||
		assessment.BadRequests != 3 || assessment.GoodRequestPercent != 25 || assessment.GoodputQPS != 0.5 {
		t.Fatalf("assessment = %#v", assessment)
	}
	if assessment.Violations.Transport != 1 || assessment.Violations.TTFT != 1 ||
		assessment.Violations.TPOT != 1 || assessment.Violations.E2E != 1 {
		t.Fatalf("violations = %#v", assessment.Violations)
	}
}

func TestRunPerformanceCapacityStopsAtFirstFailureAndProjectsHighestPassingRung(t *testing.T) {
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call == 3 {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"non-selected-secret"}}`)),
			}, nil
		}
		return successfulStreamResponse(), nil
	})
	var progress []PerformanceProgress
	report, err := New(Dependencies{Transport: transport}).RunPerformanceWithProgress(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 3, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		WarmupRequests: 1, SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	}, func(next PerformanceProgress) {
		progress = append(progress, next)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || report.Warmup == nil || report.Warmup.Completed != 1 || report.RequestBudget == nil ||
		report.RequestBudget.MeasuredCap != 3 || report.RequestBudget.TotalCap != 4 {
		t.Fatalf("calls = %d, warmup = %#v, budget = %#v", calls.Load(), report.Warmup, report.RequestBudget)
	}
	capacity := report.CapacityResult
	if capacity == nil || capacity.Status != PerformanceSLOFailed || len(capacity.Rungs) != 2 ||
		capacity.SelectedRungIndex == nil || *capacity.SelectedRungIndex != 0 ||
		capacity.HighestPassingRungIndex == nil || *capacity.HighestPassingRungIndex != 0 {
		t.Fatalf("capacity = %#v", capacity)
	}
	if capacity.Rungs[0].Target != 1 || capacity.Rungs[0].SLOAssessment.Status != PerformanceSLOPassed ||
		capacity.Rungs[1].Target != 2 || capacity.Rungs[1].SLOAssessment.Status != PerformanceSLOFailed || capacity.Rungs[1].Success {
		t.Fatalf("rungs = %#v", capacity.Rungs)
	}
	if !report.Success || report.Profile.Concurrency != 3 || report.Progress.CapacityRungNumber != 1 ||
		report.Progress.CapacityRungCount != 3 || report.Progress.CapacityTarget != 1 || len(report.Samples) != 1 ||
		report.SLOAssessment == nil || report.SLOAssessment.Status != PerformanceSLOPassed {
		t.Fatalf("selected projection = %#v", report)
	}
	if report.Progress != capacity.Rungs[0].Progress || report.Metrics != capacity.Rungs[0].Metrics ||
		!reflect.DeepEqual(report.Failures, capacity.Rungs[0].Failures) || !reflect.DeepEqual(report.SLOAssessment, &capacity.Rungs[0].SLOAssessment) {
		t.Fatalf("selected projection disagrees with rung: report = %#v, rung = %#v", report, capacity.Rungs[0])
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "non-selected-secret") {
		t.Fatalf("non-selected evidence leaked: %s", encoded)
	}
	seenRungs := make(map[uint32]bool)
	for _, update := range progress {
		if update.CapacityRungNumber > 0 {
			if update.CapacityRungCount != 3 || update.CapacityTarget <= 0 {
				t.Fatalf("capacity progress = %#v", update)
			}
			seenRungs[update.CapacityRungNumber] = true
		}
	}
	if !seenRungs[1] || !seenRungs[2] || seenRungs[3] {
		t.Fatalf("capacity progress rungs = %#v", seenRungs)
	}
}

func TestRunPerformanceCapacityKeepsOnlySelectedRungSamplesAndEvidence(t *testing.T) {
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call == 1 {
			return successfulStreamResponse(), nil
		}
		body := "discarded-rung-evidence"
		if call == 2 {
			body = "selected-rung-evidence"
		}
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"` + body + `"}}`)),
		}, nil
	})
	report, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 2, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 10_000, SLOTargetPercent: 50,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 || report.CapacityResult == nil || report.CapacityResult.Status != PerformanceSLOFailed ||
		report.CapacityResult.SelectedRungIndex == nil || *report.CapacityResult.SelectedRungIndex != 0 ||
		len(report.Samples) != 2 || report.SLOAssessment == nil || report.SLOAssessment.Status != PerformanceSLOPassed || report.Success {
		t.Fatalf("report = %#v, calls = %d", report, calls.Load())
	}
	encoded, _ := json.Marshal(report)
	if !strings.Contains(string(encoded), "selected-rung-evidence") || strings.Contains(string(encoded), "discarded-rung-evidence") {
		t.Fatalf("selected evidence projection = %s", encoded)
	}
}

func TestRunPerformanceCapacitySelectsMaximumWhenEveryRungPasses(t *testing.T) {
	var calls atomic.Uint64
	report, err := New(Dependencies{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulStreamResponse(), nil
	})}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 3, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || report.CapacityResult == nil || report.CapacityResult.Status != PerformanceSLOPassed ||
		len(report.CapacityResult.Rungs) != 3 || report.CapacityResult.SelectedRungIndex == nil || *report.CapacityResult.SelectedRungIndex != 2 ||
		report.CapacityResult.HighestPassingRungIndex == nil || *report.CapacityResult.HighestPassingRungIndex != 2 ||
		report.Progress.CapacityRungNumber != 3 || report.Progress.CapacityTarget != 3 {
		t.Fatalf("report = %#v, calls = %d", report, calls.Load())
	}
}

func TestRunPerformanceCapacityFirstFailureIsSelectedWithItsEvidence(t *testing.T) {
	report, err := New(Dependencies{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"first-rung-only"}}`)),
		}, nil
	})}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.CapacityResult == nil || report.CapacityResult.Status != PerformanceSLOFailed ||
		len(report.CapacityResult.Rungs) != 1 || report.CapacityResult.SelectedRungIndex == nil ||
		*report.CapacityResult.SelectedRungIndex != 0 || report.CapacityResult.HighestPassingRungIndex != nil ||
		report.Success || len(report.Samples) != 1 || report.Samples[0].ResponseEvidence == nil ||
		!strings.Contains(report.Samples[0].ResponseEvidence.Body, "first-rung-only") {
		t.Fatalf("report = %#v", report)
	}
}

func TestRunPerformanceCapacityReusesNormalWorkloadIndicesAcrossRungs(t *testing.T) {
	var mu sync.Mutex
	prompts := make(map[string]int)
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		mu.Lock()
		prompts[body.Messages[0].Content]++
		mu.Unlock()
		return successfulStreamResponse(), nil
	})
	report, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 2, Concurrency: 2, TimeoutMS: 2_000,
		InputTokens: 20, OutputTokens: 4, WorkloadMode: PerformanceWorkloadNormal, RandomSeed: 17,
		InputTokensStdDev: 2, OutputTokensStdDev: 1, SharedPrefixTokens: 4,
		SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	})
	if err != nil || report.CapacityResult == nil || len(report.CapacityResult.Rungs) != 2 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	if len(prompts) != 2 {
		t.Fatalf("unique prompts = %d, want 2; prompts = %#v", len(prompts), prompts)
	}
	for prompt, count := range prompts {
		if count != 2 {
			t.Fatalf("prompt %q count = %d, want 2", prompt, count)
		}
	}
}

func TestRunPerformanceCapacityCancellationStopsLadderAndDoesNotArchive(t *testing.T) {
	started := make(chan struct{})
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	archive := &capturingPerformanceArchive{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan PerformanceReport, 1)
	go func() {
		report, _ := New(Dependencies{Transport: transport, Archive: archive}).RunPerformance(ctx, PerformanceCommand{
			AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
			RequestCount: 2, Concurrency: 3, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
			SLOE2EMS: 10_000, SLOTargetPercent: 100,
			CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
		})
		done <- report
	}()
	<-started
	cancel()
	report := <-done
	if report.Archived || len(archive.saved) != 0 || report.ErrorCode != load.ErrorCancelled ||
		report.CapacityResult == nil || report.CapacityResult.Status != PerformanceSLONotEvaluated ||
		len(report.CapacityResult.Rungs) != 1 || report.CapacityResult.SelectedRungIndex == nil ||
		*report.CapacityResult.SelectedRungIndex != 0 || report.Progress.Phase != load.PhaseCancelled ||
		report.Progress.CapacityRungNumber != 1 {
		t.Fatalf("cancelled report = %#v, archive = %#v", report, archive.saved)
	}
}

func TestRunPerformanceCapacityCancellationBetweenRungsDoesNotStartNextRung(t *testing.T) {
	var calls atomic.Uint64
	archive := &capturingPerformanceArchive{}
	ctx, cancel := context.WithCancel(context.Background())
	report, err := New(Dependencies{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return successfulStreamResponse(), nil
		}),
		Archive: archive,
	}).RunPerformanceWithProgress(ctx, PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 3, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	}, func(progress PerformanceProgress) {
		if progress.CapacityRungNumber == 1 && progress.Phase == load.PhaseCompleted {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || report.Archived || len(archive.saved) != 0 || report.ErrorCode != load.ErrorCancelled ||
		report.CapacityResult == nil || report.CapacityResult.Status != PerformanceSLONotEvaluated ||
		len(report.CapacityResult.Rungs) != 1 || report.CapacityResult.SelectedRungIndex == nil ||
		*report.CapacityResult.SelectedRungIndex != 0 || report.SLOAssessment == nil || report.SLOAssessment.Status != PerformanceSLOPassed {
		t.Fatalf("between-rung cancellation report = %#v, calls = %d, archive = %#v", report, calls.Load(), archive.saved)
	}
}

func TestRunPerformanceCapacityCancellationAfterWarmupDoesNotStartLadder(t *testing.T) {
	var calls atomic.Uint64
	archive := &capturingPerformanceArchive{}
	ctx, cancel := context.WithCancel(context.Background())
	report, err := New(Dependencies{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return successfulStreamResponse(), nil
		}),
		Archive: archive,
	}).RunPerformanceWithProgress(ctx, PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		WarmupRequests: 1, SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	}, func(progress PerformanceProgress) {
		if progress.Phase == PerformancePhaseWarmingUp && progress.Completed == 1 && progress.InFlight == 0 {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || report.Archived || len(archive.saved) != 0 || report.ErrorCode != load.ErrorCancelled ||
		report.CapacityResult != nil || report.Progress.Phase != load.PhaseCancelled {
		t.Fatalf("post-warmup cancellation report = %#v, calls = %d, archive = %#v", report, calls.Load(), archive.saved)
	}
}

func TestValidateArchivedPerformanceReportRebuildsSLOFromSelectedSamples(t *testing.T) {
	report := archivedSLOReport(t, false)
	if _, err := ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("ValidateArchivedPerformanceReport() error = %v; report = %#v", err, report)
	}

	report.SLOAssessment.GoodRequests = 0
	report.SLOAssessment.BadRequests = 1
	report.SLOAssessment.GoodRequestPercent = 0
	report.SLOAssessment.GoodputQPS = 0
	report.SLOAssessment.Status = PerformanceSLOFailed
	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("coordinated SLO assessment tamper was accepted")
	}
}

func TestValidateArchivedPerformanceReportRequiresConfiguredSLOAssessment(t *testing.T) {
	report := archivedSLOReport(t, false)
	report.SLOAssessment = nil
	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("missing configured SLO assessment was accepted")
	}

	report = archivedSLOReport(t, false)
	report.Profile.SLOE2EMS = 0
	report.Profile.SLOTargetPercent = 0
	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("unconfigured SLO assessment was accepted")
	}
}

func TestValidateArchivedPerformanceReportRejectsSubNanosecondDurations(t *testing.T) {
	const subNanosecondMS = math.SmallestNonzeroFloat64 * 1024

	t.Run("ordinary report", func(t *testing.T) {
		report := archivedSLOReport(t, false)
		report.Progress.SendDurationMS = subNanosecondMS
		report.Progress.DrainDurationMS = 0
		report.Progress.TotalDurationMS = subNanosecondMS

		if _, err := ValidateArchivedPerformanceReport(report); err == nil {
			t.Fatal("ordinary report with a sub-nanosecond duration was accepted")
		}
	})

	t.Run("capacity rung", func(t *testing.T) {
		report := archivedSLOReport(t, true)
		rung := &report.CapacityResult.Rungs[0]
		rung.Progress.SendDurationMS = subNanosecondMS
		rung.Progress.DrainDurationMS = 0
		rung.Progress.TotalDurationMS = subNanosecondMS

		if _, err := ValidateArchivedPerformanceReport(report); err == nil {
			t.Fatal("capacity rung with a sub-nanosecond duration was accepted")
		}
	})
}

func TestValidateArchivedPerformanceReportRejectsInfiniteDerivedValues(t *testing.T) {
	t.Run("ordinary report", func(t *testing.T) {
		report := archivedSLOReport(t, false)
		report.Metrics.RPM = math.Inf(1)

		if _, err := ValidateArchivedPerformanceReport(report); err == nil {
			t.Fatal("ordinary report with infinite RPM was accepted")
		}
	})

	t.Run("capacity non-selected rung", func(t *testing.T) {
		report := archivedSLOReport(t, true)
		report.CapacityResult.Rungs[0].SLOAssessment.GoodputQPS = math.Inf(1)

		if _, err := ValidateArchivedPerformanceReport(report); err == nil {
			t.Fatal("capacity report with infinite non-selected goodput was accepted")
		}
	})
}

func TestValidateArchivedPerformanceReportRejectsNegativeMetricsFromOverflowingSampleDuration(t *testing.T) {
	report := archivedSLOReport(t, false)
	report.Profile.SLOE2EMS = math.MaxFloat64
	report.SLOAssessment.Thresholds.E2EMS = math.MaxFloat64
	report.Samples[0].E2EMS = math.MaxFloat64
	overflowedMS := durationMilliseconds(time.Duration(math.MinInt64))
	report.Metrics.E2EP50 = overflowedMS
	report.Metrics.E2EP90 = overflowedMS
	report.Metrics.E2EP95 = overflowedMS
	report.Metrics.E2EP99 = overflowedMS
	report.Metrics.E2EAverage = overflowedMS
	report.Metrics.TPOTP50 = 0
	report.Metrics.TPOTP90 = 0
	report.Metrics.TPOTP95 = 0
	report.Metrics.TPOTP99 = 0
	report.Metrics.TPOTAverage = 0

	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("report with coordinated negative metrics from an overflowing sample duration was accepted")
	}
}

func TestValidateArchivedCapacityReportEnforcesPlanSelectionAndProjection(t *testing.T) {
	baseline := archivedSLOReport(t, true)
	if _, err := ValidateArchivedPerformanceReport(baseline); err != nil {
		t.Fatalf("ValidateArchivedPerformanceReport() error = %v; report = %#v", err, baseline)
	}

	for _, test := range []struct {
		name   string
		mutate func(*PerformanceReport)
	}{
		{name: "missing result", mutate: func(report *PerformanceReport) { report.CapacityResult = nil }},
		{name: "empty rungs", mutate: func(report *PerformanceReport) { report.CapacityResult.Rungs = nil }},
		{name: "too many rungs", mutate: func(report *PerformanceReport) {
			report.CapacityResult.Rungs = append(report.CapacityResult.Rungs, report.CapacityResult.Rungs[1])
		}},
		{name: "rung index", mutate: func(report *PerformanceReport) { report.CapacityResult.Rungs[0].Index = 1 }},
		{name: "rung target", mutate: func(report *PerformanceReport) { report.CapacityResult.Rungs[0].Target = 1.5 }},
		{name: "selected index", mutate: func(report *PerformanceReport) { report.CapacityResult.SelectedRungIndex = uint32Pointer(0) }},
		{name: "missing highest passing", mutate: func(report *PerformanceReport) { report.CapacityResult.HighestPassingRungIndex = nil }},
		{name: "capacity status", mutate: func(report *PerformanceReport) { report.CapacityResult.Status = PerformanceSLOFailed }},
		{name: "rung metrics", mutate: func(report *PerformanceReport) { report.CapacityResult.Rungs[0].Metrics.Completed++ }},
		{name: "rung SLO", mutate: func(report *PerformanceReport) { report.CapacityResult.Rungs[0].SLOAssessment.GoodputQPS /= 2 }},
		{name: "rung impossible SLO overlap", mutate: func(report *PerformanceReport) {
			report.CapacityResult.Rungs[0].SLOAssessment.Violations.E2E = 1
		}},
		{name: "top-level projection", mutate: func(report *PerformanceReport) { report.CapacityResult.Rungs[1].Success = false }},
		{name: "rung after first failure", mutate: func(report *PerformanceReport) {
			assessment := &report.CapacityResult.Rungs[0].SLOAssessment
			assessment.Status = PerformanceSLOFailed
			assessment.GoodRequests = 0
			assessment.BadRequests = 1
			assessment.GoodRequestPercent = 0
			assessment.GoodputQPS = 0
			assessment.Violations.E2E = 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := archivedSLOReport(t, true)
			test.mutate(&report)
			if _, err := ValidateArchivedPerformanceReport(report); err == nil {
				t.Fatalf("mutated capacity report was accepted: %#v", report.CapacityResult)
			}
		})
	}
}

func TestValidateArchivedCapacityReportRejectsOutOfRangeSelectedIndex(t *testing.T) {
	report := archivedSLOReport(t, true)
	report.CapacityResult.SelectedRungIndex = uint32Pointer(math.MaxUint32)

	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("out-of-range selected capacity rung was accepted")
	}
}

func TestValidateArchivedCapacityReportRebuildsSelectedMetricDistributions(t *testing.T) {
	baseline := archivedSLOReport(t, true)
	for _, test := range []struct {
		name   string
		mutate func(*load.Metrics)
	}{
		{name: "TTFT", mutate: func(metrics *load.Metrics) {
			metrics.TTFTP50++
			metrics.TTFTP90++
			metrics.TTFTP95++
			metrics.TTFTP99++
			metrics.TTFTAverage++
		}},
		{name: "TPOT", mutate: func(metrics *load.Metrics) {
			metrics.TPOTP50++
			metrics.TPOTP90++
			metrics.TPOTP95++
			metrics.TPOTP99++
			metrics.TPOTAverage++
		}},
		{name: "E2E", mutate: func(metrics *load.Metrics) {
			metrics.E2EP50++
			metrics.E2EP90++
			metrics.E2EP95++
			metrics.E2EP99++
			metrics.E2EAverage++
		}},
		{name: "schedule lag", mutate: func(metrics *load.Metrics) {
			metrics.ScheduleLagP50++
			metrics.ScheduleLagP90++
			metrics.ScheduleLagP95++
			metrics.ScheduleLagP99++
			metrics.ScheduleLagAverage++
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := clonePerformanceReport(t, baseline)
			selected := *report.CapacityResult.SelectedRungIndex
			test.mutate(&report.Metrics)
			test.mutate(&report.CapacityResult.Rungs[selected].Metrics)

			if _, err := ValidateArchivedPerformanceReport(report); err == nil {
				t.Fatal("coordinated selected metric distribution tamper was accepted")
			}
		})
	}
}

func TestValidateArchivedCapacityReportChecksNonSelectedMetricDistributions(t *testing.T) {
	baseline := archivedSLOReport(t, true)
	for _, test := range []struct {
		name   string
		mutate func(*load.Metrics)
	}{
		{name: "percentiles must be monotonic", mutate: func(metrics *load.Metrics) {
			metrics.E2EP50 = metrics.E2EP90 + 1
		}},
		{name: "zero average requires zero percentiles", mutate: func(metrics *load.Metrics) {
			metrics.TPOTP50 = 1
			metrics.TPOTP90 = 1
			metrics.TPOTP95 = 1
			metrics.TPOTP99 = 1
			metrics.TPOTAverage = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := clonePerformanceReport(t, baseline)
			test.mutate(&report.CapacityResult.Rungs[0].Metrics)

			if _, err := ValidateArchivedPerformanceReport(report); err == nil {
				t.Fatal("invalid non-selected metric distribution was accepted")
			}
		})
	}
}

func TestValidateArchivedOpenCapacityUsesSelectedRungRate(t *testing.T) {
	var calls atomic.Uint64
	archive := &capturingPerformanceArchive{}
	report, err := New(Dependencies{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return successfulStreamResponse(), nil
			}
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"stop"}}`)),
			}, nil
		}),
		Archive:   archive,
		Clock:     fixedPerformanceClock{now: time.Date(2026, time.September, 5, 5, 6, 7, 8, time.UTC)},
		IDFactory: func(time.Time) (string, error) { return "77777777-7777-4777-8777-777777777759", nil },
	}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		LoadMode: domain.LoadOpenLoop, ArrivalPattern: load.ArrivalConstant,
		RequestCount: 1, RatePerSecond: 2, MaxInFlight: 1,
		TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 10_000, SLOTargetPercent: 100,
		CapacityEnabled: true, CapacityStart: 1, CapacityStep: 1,
	})
	if err != nil || len(archive.saved) != 1 || report.CapacityResult == nil ||
		report.CapacityResult.SelectedRungIndex == nil || *report.CapacityResult.SelectedRungIndex != 0 ||
		report.Profile.RatePerSecond != 2 || report.Progress.CapacityTarget != 1 || report.Metrics.OfferedQPS != 1 {
		t.Fatalf("report = %#v, archive = %#v, error = %v", report, archive.saved, err)
	}
	if _, err := ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("ValidateArchivedPerformanceReport() error = %v; report = %#v", err, report)
	}
}

func TestValidateArchivedPerformanceReportRejectsUnconfiguredCapacityResult(t *testing.T) {
	capacity := archivedSLOReport(t, true).CapacityResult
	report := archivedSLOReport(t, false)
	report.CapacityResult = capacity
	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("unconfigured capacity result was accepted")
	}
}

func uint32Pointer(value uint32) *uint32 { return &value }

func clonePerformanceReport(t *testing.T, report PerformanceReport) PerformanceReport {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var clone PerformanceReport
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func archivedSLOReport(t *testing.T, capacity bool) PerformanceReport {
	t.Helper()
	archive := &capturingPerformanceArchive{}
	command := PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 10_000, SLOTargetPercent: 100,
	}
	if capacity {
		command.Concurrency = 2
		command.CapacityEnabled = true
		command.CapacityStart = 1
		command.CapacityStep = 1
	}
	report, err := New(Dependencies{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return successfulStreamResponse(), nil }),
		Archive:   archive,
		Clock:     fixedPerformanceClock{now: time.Date(2026, time.September, 5, 4, 5, 6, 7, time.UTC)},
		IDFactory: func(time.Time) (string, error) { return "77777777-7777-4777-8777-777777777760", nil },
	}).RunPerformance(context.Background(), command)
	if err != nil || len(archive.saved) != 1 || !report.Archived {
		t.Fatalf("RunPerformance() report = %#v, archive = %#v, error = %v", report, archive.saved, err)
	}
	return report
}

func TestRunPerformanceReportsSLOWithoutChangingTransportSuccess(t *testing.T) {
	report, err := New(Dependencies{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return successfulStreamResponse(), nil
	})}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOTPOTMS: 100, SLOTargetPercent: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.SLOAssessment == nil || report.SLOAssessment.Status != PerformanceSLOFailed ||
		report.SLOAssessment.TotalRequests != 1 || report.SLOAssessment.GoodRequests != 0 ||
		report.SLOAssessment.Violations.TPOT != 1 || report.SLOAssessment.Violations.Transport != 0 {
		t.Fatalf("report = %#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(encoded), `"slo_assessment"`) {
		t.Fatalf("encoded report = %s, error = %v", encoded, err)
	}
}

func TestBuildPerformanceSLOAssessmentSeparatesStatusFromObservedMath(t *testing.T) {
	profile := PerformanceProfile{SLOE2EMS: 50, SLOTargetPercent: 50}
	samples := []PerformanceSample{
		{Success: true, E2EMS: 50},
		{Success: true, E2EMS: 51},
	}

	completed := buildPerformanceSLOAssessment(profile, samples, PerformanceProgress{
		Phase: load.PhaseCompleted, Completed: 2, TotalDurationMS: 1_000,
	})
	if completed.Status != PerformanceSLOPassed || completed.GoodRequests != 1 || completed.GoodputQPS != 1 {
		t.Fatalf("completed assessment = %#v", completed)
	}

	cancelled := buildPerformanceSLOAssessment(profile, samples, PerformanceProgress{
		Phase: load.PhaseCancelled, Completed: 2, Stopped: true, TotalDurationMS: 1_000,
	})
	if cancelled.Status != PerformanceSLONotEvaluated || cancelled.TotalRequests != 2 ||
		cancelled.GoodRequests != 1 || cancelled.GoodRequestPercent != 50 || cancelled.GoodputQPS != 1 {
		t.Fatalf("cancelled assessment = %#v", cancelled)
	}
	stopped := buildPerformanceSLOAssessment(profile, samples, PerformanceProgress{
		Phase: load.PhaseCompleted, Completed: 2, Stopped: true, TotalDurationMS: 1_000,
	})
	if stopped.Status != PerformanceSLONotEvaluated || stopped.GoodRequests != 1 {
		t.Fatalf("stopped assessment = %#v", stopped)
	}
}

func TestBuildPerformanceSLOAssessmentTreatsMissingEnabledLatencyAsViolation(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile PerformanceProfile
		count   func(PerformanceSLOViolations) uint64
	}{
		{name: "TTFT", profile: PerformanceProfile{SLOTTFTMS: 10, SLOTargetPercent: 100}, count: func(value PerformanceSLOViolations) uint64 { return value.TTFT }},
		{name: "TPOT", profile: PerformanceProfile{SLOTPOTMS: 10, SLOTargetPercent: 100}, count: func(value PerformanceSLOViolations) uint64 { return value.TPOT }},
		{name: "E2E", profile: PerformanceProfile{SLOE2EMS: 10, SLOTargetPercent: 100}, count: func(value PerformanceSLOViolations) uint64 { return value.E2E }},
	} {
		t.Run(test.name, func(t *testing.T) {
			assessment := buildPerformanceSLOAssessment(test.profile, []PerformanceSample{{Success: true}}, PerformanceProgress{
				Phase: load.PhaseCompleted, Completed: 1, TotalDurationMS: 1_000,
			})
			if assessment.Status != PerformanceSLOFailed || assessment.GoodRequests != 0 || test.count(assessment.Violations) != 1 {
				t.Fatalf("assessment = %#v", assessment)
			}
		})
	}
}

func TestPerformanceSLOProfileValidation(t *testing.T) {
	base := PerformanceProfile{
		LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: load.ArrivalConstant,
		WorkloadMode: PerformanceWorkloadFixed, RequestCount: 1, Concurrency: 1,
		TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
	}
	for _, test := range []struct {
		name  string
		apply func(*PerformanceProfile)
		valid bool
	}{
		{name: "disabled", valid: true},
		{name: "one threshold", apply: func(profile *PerformanceProfile) { profile.SLOTTFTMS = 10; profile.SLOTargetPercent = 99 }, valid: true},
		{name: "all thresholds", apply: func(profile *PerformanceProfile) {
			profile.SLOTTFTMS = 10
			profile.SLOTPOTMS = 2
			profile.SLOE2EMS = 20
			profile.SLOTargetPercent = 100
		}, valid: true},
		{name: "target without threshold", apply: func(profile *PerformanceProfile) { profile.SLOTargetPercent = 99 }},
		{name: "threshold without target", apply: func(profile *PerformanceProfile) { profile.SLOE2EMS = 20 }},
		{name: "target over one hundred", apply: func(profile *PerformanceProfile) { profile.SLOE2EMS = 20; profile.SLOTargetPercent = 100.01 }},
		{name: "negative threshold", apply: func(profile *PerformanceProfile) { profile.SLOE2EMS = -1; profile.SLOTargetPercent = 99 }},
		{name: "nan threshold", apply: func(profile *PerformanceProfile) { profile.SLOE2EMS = math.NaN(); profile.SLOTargetPercent = 99 }},
		{name: "infinite target", apply: func(profile *PerformanceProfile) { profile.SLOE2EMS = 20; profile.SLOTargetPercent = math.Inf(1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := base
			if test.apply != nil {
				test.apply(&profile)
			}
			if got := validPerformanceProfileValues(profile); got != test.valid {
				t.Fatalf("validPerformanceProfileValues() = %v, want %v; profile = %#v", got, test.valid, profile)
			}
		})
	}
}

func TestBuildPerformanceCapacityTargets(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile PerformanceProfile
		want    []float64
		wantErr bool
	}{
		{name: "fixed divides exactly", profile: fixedCapacityProfile(10, 2, 4), want: []float64{2, 6, 10}},
		{name: "fixed appends exact maximum", profile: fixedCapacityProfile(10, 2, 3), want: []float64{2, 5, 8, 10}},
		{name: "fixed starts at maximum", profile: fixedCapacityProfile(10, 10, 3), want: []float64{10}},
		{name: "fixed rejects fractional start", profile: fixedCapacityProfile(10, 1.5, 1), wantErr: true},
		{name: "fixed rejects fractional step", profile: fixedCapacityProfile(10, 1, 1.5), wantErr: true},
		{name: "open preserves decimals", profile: openCapacityProfile(1.1, .1, .3), want: []float64{.1, .4, .7, 1, 1.1}},
		{name: "open preserves a near-maximum rung", profile: openCapacityProfile(1, math.Nextafter(1, 0), .5), want: []float64{math.Nextafter(1, 0), 1}},
		{name: "open starts at maximum", profile: openCapacityProfile(1.1, 1.1, .3), want: []float64{1.1}},
		{name: "twenty rungs", profile: fixedCapacityProfile(20, 1, 1), want: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}},
		{name: "twenty one rungs rejected", profile: fixedCapacityProfile(21, 1, 1), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := buildPerformanceCapacityTargets(test.profile)
			if (err != nil) != test.wantErr {
				t.Fatalf("buildPerformanceCapacityTargets() error = %v, wantErr %v", err, test.wantErr)
			}
			if !equalFloat64Slices(got, test.want) {
				t.Fatalf("targets = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPerformanceCapacityValidationAndGlobalBudget(t *testing.T) {
	valid := fixedCapacityProfile(10, 1, 1)
	valid.RequestCount = 999
	valid.WarmupRequests = 10
	budget, perLadderCap, err := buildPerformanceRequestBudget(valid)
	if err != nil {
		t.Fatalf("buildPerformanceRequestBudget(10,000) error = %v", err)
	}
	if budget == nil || budget.WarmupCap != 10 || budget.MeasuredCap != 9_990 || budget.TotalCap != 10_000 || perLadderCap != 9_990 {
		t.Fatalf("budget = %#v, measured cap = %d", budget, perLadderCap)
	}
	if !validPerformanceProfileValues(valid) {
		t.Fatalf("valid capacity profile rejected: %#v", valid)
	}

	for _, test := range []struct {
		name   string
		mutate func(*PerformanceProfile)
	}{
		{name: "ten thousand and one", mutate: func(profile *PerformanceProfile) { profile.WarmupRequests = 11 }},
		{name: "requires slo", mutate: func(profile *PerformanceProfile) { profile.SLOE2EMS = 0; profile.SLOTargetPercent = 0 }},
		{name: "requires request count", mutate: func(profile *PerformanceProfile) { profile.RequestCount = 0; profile.DurationMS = 1_000 }},
		{name: "rejects ramp duration", mutate: func(profile *PerformanceProfile) { profile.RampDurationMS = 1_000 }},
		{name: "rejects ramp cap", mutate: func(profile *PerformanceProfile) { profile.RampRequestCap = 1 }},
		{name: "disabled start", mutate: func(profile *PerformanceProfile) { profile.CapacityEnabled = false }},
		{name: "start above maximum", mutate: func(profile *PerformanceProfile) { profile.CapacityStart = 11 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := valid
			test.mutate(&profile)
			if validPerformanceProfileValues(profile) {
				t.Fatalf("invalid capacity profile accepted: %#v", profile)
			}
		})
	}
}

func TestPhaseThreeReportKeepsCanonicalShapeWithoutPhaseFourConfiguration(t *testing.T) {
	report := PerformanceReport{SchemaVersion: PerformanceSchemaVersion, Profile: PerformanceProfile{}}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"slo_ttft_ms", "slo_tpot_ms", "slo_e2e_ms", "slo_target_percent",
		"capacity_enabled", "capacity_start", "capacity_step", "slo_assessment", "capacity_result",
		"capacity_rung_number", "capacity_rung_count", "capacity_target",
	} {
		if strings.Contains(string(encoded), `"`+field+`"`) {
			t.Fatalf("phase-three shape contains %q: %s", field, encoded)
		}
	}
}

func fixedCapacityProfile(max uint32, start, step float64) PerformanceProfile {
	return PerformanceProfile{
		LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: load.ArrivalConstant,
		WorkloadMode: PerformanceWorkloadFixed, RequestCount: 1, Concurrency: max,
		TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 100, SLOTargetPercent: 99,
		CapacityEnabled: true, CapacityStart: start, CapacityStep: step,
	}
}

func openCapacityProfile(max, start, step float64) PerformanceProfile {
	return PerformanceProfile{
		LoadMode: domain.LoadOpenLoop, ArrivalPattern: load.ArrivalConstant,
		WorkloadMode: PerformanceWorkloadFixed, RequestCount: 1,
		RatePerSecond: max, MaxInFlight: 4,
		TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		SLOE2EMS: 100, SLOTargetPercent: 99,
		CapacityEnabled: true, CapacityStart: start, CapacityStep: step,
	}
}

func equalFloat64Slices(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !approximatelyEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}
