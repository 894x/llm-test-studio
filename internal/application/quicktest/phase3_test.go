package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func phase3FixedProfile() PerformanceProfile {
	return PerformanceProfile{
		LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: load.ArrivalConstant, WorkloadMode: PerformanceWorkloadFixed,
		RequestCount: 8_000, Concurrency: 4, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 3,
		WarmupRequests: 1_000, RampDurationMS: 1_000, RampRequestCap: 1_000,
	}
}

func TestPerformanceRequestBudgetAcceptsExactLimitAndRejectsOverflow(t *testing.T) {
	profile := phase3FixedProfile()
	budget, measuredCap, err := buildPerformanceRequestBudget(profile)
	if err != nil {
		t.Fatalf("buildPerformanceRequestBudget(exact) error = %v", err)
	}
	if budget == nil || measuredCap != 8_000 || *budget != (PerformanceRequestBudget{
		Limit: 10_000, WarmupCap: 1_000, RampCap: 1_000, MeasuredCap: 8_000, TotalCap: 10_000,
	}) {
		t.Fatalf("exact budget = %#v, measured cap = %d", budget, measuredCap)
	}

	profile.RampRequestCap++
	if _, _, err := buildPerformanceRequestBudget(profile); err == nil {
		t.Fatal("buildPerformanceRequestBudget(10,001) error = nil")
	}
}

func TestPerformanceRequestBudgetUsesRemainingCapacityForFixedDuration(t *testing.T) {
	profile := phase3FixedProfile()
	profile.RequestCount = 0
	profile.DurationMS = 5_000
	profile.WarmupRequests = 100
	profile.RampRequestCap = 200
	budget, measuredCap, err := buildPerformanceRequestBudget(profile)
	if err != nil {
		t.Fatal(err)
	}
	if measuredCap != 9_700 || budget.MeasuredCap != 9_700 || budget.TotalCap != MaxPerformanceRequests {
		t.Fatalf("duration budget = %#v, measured cap = %d", budget, measuredCap)
	}
}

func TestPerformanceRequestBudgetUsesExecutionRampEstimator(t *testing.T) {
	for _, test := range []struct {
		arrival load.ArrivalPattern
		seed    uint32
		want    uint64
	}{
		{arrival: load.ArrivalConstant, want: 55},
		{arrival: load.ArrivalPoisson, seed: 7, want: 111},
	} {
		profile := PerformanceProfile{
			LoadMode: domain.LoadOpenLoop, ArrivalPattern: test.arrival, WorkloadMode: PerformanceWorkloadFixed,
			RandomSeed: test.seed, RequestCount: 1, RatePerSecond: 100, MaxInFlight: 4,
			TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2, RampDurationMS: 1_000,
		}
		budget, _, err := buildPerformanceRequestBudget(profile)
		if err != nil {
			t.Fatalf("buildPerformanceRequestBudget(%q) error = %v", test.arrival, err)
		}
		if budget == nil || budget.RampCap != test.want {
			t.Fatalf("ramp budget %q = %#v, want cap %d", test.arrival, budget, test.want)
		}
	}
}

func TestPerformanceRequestBudgetUsesExecutionOpenLoopSchedulerPrecision(t *testing.T) {
	profile := PerformanceProfile{
		LoadMode: domain.LoadOpenLoop, ArrivalPattern: load.ArrivalConstant, WorkloadMode: PerformanceWorkloadFixed,
		DurationMS: 1, RatePerSecond: 29_000, MaxInFlight: 8,
		TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2, SliceDurationMS: 1,
	}
	budget, measuredCap, err := buildPerformanceRequestBudget(profile)
	if err != nil {
		t.Fatalf("buildPerformanceRequestBudget() error = %v", err)
	}
	if measuredCap != 30 || budget == nil || budget.MeasuredCap != 30 || budget.TotalCap != 30 {
		t.Fatalf("scheduler-aligned budget = %#v, measured cap = %d", budget, measuredCap)
	}
}

func TestValidPerformanceProfileRejectsInvalidPhaseThreeRelationships(t *testing.T) {
	base := PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: load.ArrivalConstant, WorkloadMode: PerformanceWorkloadFixed,
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
	}
	for _, test := range []struct {
		name   string
		mutate func(*PerformanceCommand)
	}{
		{name: "fixed ramp without cap", mutate: func(command *PerformanceCommand) { command.RampDurationMS = 1_000 }},
		{name: "cap without ramp", mutate: func(command *PerformanceCommand) { command.RampRequestCap = 1 }},
		{name: "open ramp explicit cap", mutate: func(command *PerformanceCommand) {
			command.LoadMode = domain.LoadOpenLoop
			command.Concurrency = 0
			command.RatePerSecond = 10
			command.MaxInFlight = 2
			command.RampDurationMS = 1_000
			command.RampRequestCap = 1
		}},
		{name: "total budget overflow", mutate: func(command *PerformanceCommand) {
			command.RequestCount = MaxPerformanceRequests
			command.WarmupRequests = 1
		}},
		{name: "slice duration overflow", mutate: func(command *PerformanceCommand) { command.SliceDurationMS = MaxPerformanceDurationMS + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := base
			test.mutate(&command)
			if validPerformanceProfile(command) {
				t.Fatalf("invalid phase-three profile accepted: %#v", command)
			}
		})
	}
}

func TestBuildPerformanceTimeSlicesUsesEventCohortsAndHalfOpenWindows(t *testing.T) {
	observations := []load.Observation{
		{Index: 0, ScheduledOffset: 10 * time.Millisecond, StartedOffset: 20 * time.Millisecond, FinishedOffset: 120 * time.Millisecond, Success: true, TTFT: 10 * time.Millisecond, E2E: 100 * time.Millisecond, PromptTokens: 5, CompletionTokens: 3, CachedTokens: 1},
		{Index: 1, ScheduledOffset: 90 * time.Millisecond, StartedOffset: 105 * time.Millisecond, FinishedOffset: 105 * time.Millisecond, ErrorCode: load.ErrorSchedulerOverload},
		{Index: 2, ScheduledOffset: 110 * time.Millisecond, StartedOffset: 130 * time.Millisecond, FinishedOffset: 230 * time.Millisecond, ErrorCode: load.ErrorHTTP},
		{Index: 3, ScheduledOffset: 190 * time.Millisecond, StartedOffset: 199 * time.Millisecond, FinishedOffset: 240 * time.Millisecond, Success: true, TTFT: 20 * time.Millisecond, E2E: 40 * time.Millisecond, PromptTokens: 7, CompletionTokens: 2, CachedTokens: 2},
	}
	slices := buildPerformanceTimeSlices(observations, 250*time.Millisecond, 100*time.Millisecond)
	if len(slices) != 3 {
		t.Fatalf("slices = %#v", slices)
	}
	if got := slices[0]; got.SliceIndex != 0 || got.StartMS != 0 || got.EndMS != 100 || got.Partial || got.Offered != 2 || got.Launched != 1 || got.Completed != 0 || got.TTFT.Count != 1 {
		t.Fatalf("slice 0 = %#v", got)
	}
	if got := slices[1]; got.SliceIndex != 1 || got.StartMS != 100 || got.EndMS != 200 || got.Partial || got.Offered != 2 || got.Launched != 2 || got.Completed != 2 || got.Succeeded != 1 || got.Failed != 1 || got.Rejected != 1 || got.TTFT.Count != 1 {
		t.Fatalf("slice 1 = %#v", got)
	}
	if got := slices[2]; got.SliceIndex != 2 || got.StartMS != 200 || got.EndMS != 250 || !got.Partial || got.Completed != 2 || got.Succeeded != 1 || got.Failed != 1 || got.PromptTokens != 7 || got.CompletionTokens != 2 || got.CachedTokens != 2 {
		t.Fatalf("slice 2 = %#v", got)
	}
	var offered, launched, completed, succeeded, failed, rejected, prompt, completion, cached uint64
	for _, slice := range slices {
		offered += slice.Offered
		launched += slice.Launched
		completed += slice.Completed
		succeeded += slice.Succeeded
		failed += slice.Failed
		rejected += slice.Rejected
		prompt += slice.PromptTokens
		completion += slice.CompletionTokens
		cached += slice.CachedTokens
	}
	if []uint64{offered, launched, completed, succeeded, failed, rejected, prompt, completion, cached}[0] != 4 ||
		!reflect.DeepEqual([]uint64{offered, launched, completed, succeeded, failed, rejected, prompt, completion, cached}, []uint64{4, 3, 4, 2, 2, 1, 12, 5, 3}) {
		t.Fatalf("slice sums = %v", []uint64{offered, launched, completed, succeeded, failed, rejected, prompt, completion, cached})
	}
}

func TestBuildPerformanceTimeSlicesIsSparseAcrossIdleWindows(t *testing.T) {
	observations := []load.Observation{{
		ScheduledOffset: 10 * time.Millisecond, StartedOffset: 10 * time.Millisecond,
		FinishedOffset: 2_100 * time.Millisecond, Success: true, E2E: 2_090 * time.Millisecond,
	}}
	slices := buildPerformanceTimeSlices(observations, 2_200*time.Millisecond, time.Second)
	if len(slices) != 2 || slices[0].SliceIndex != 0 || slices[1].SliceIndex != 2 || !slices[1].Partial {
		t.Fatalf("sparse slices = %#v", slices)
	}
}

func TestBuildPerformanceTimeSlicesKeepsEmptyFinalPartialWindow(t *testing.T) {
	observations := []load.Observation{{
		ScheduledOffset: 10 * time.Millisecond, StartedOffset: 20 * time.Millisecond,
		FinishedOffset: 30 * time.Millisecond, ErrorCode: load.ErrorHTTP,
	}}
	slices := buildPerformanceTimeSlices(observations, 250*time.Millisecond, 100*time.Millisecond)
	if len(slices) != 2 || slices[0].SliceIndex != 0 || slices[1].SliceIndex != 2 || !slices[1].Partial ||
		slices[1].StartMS != 200 || slices[1].EndMS != 250 || slices[1].Offered != 0 || slices[1].Completed != 0 {
		t.Fatalf("sparse final partial slices = %#v", slices)
	}
}

func TestBuildPerformanceTimeSlicesOmitsUnavailableLatencyValues(t *testing.T) {
	observations := []load.Observation{{
		ScheduledOffset: 5 * time.Millisecond,
		StartedOffset:   5 * time.Millisecond,
		FinishedOffset:  10 * time.Millisecond,
		Success:         true,
	}}
	slices := buildPerformanceTimeSlices(observations, 20*time.Millisecond, 20*time.Millisecond)
	if len(slices) != 1 || slices[0].TTFT.Count != 0 || slices[0].E2E.Count != 0 || slices[0].TPOT.Count != 0 {
		t.Fatalf("unavailable latency cohorts = %#v", slices)
	}
}

func TestBuildPerformanceTimeSlicesFromSamplesDerivesTPOT(t *testing.T) {
	slices := buildPerformanceTimeSlicesFromSamples([]PerformanceSample{{
		ScheduledOffsetMS: 1,
		StartedOffsetMS:   2,
		FinishedOffsetMS:  42,
		Success:           true,
		TTFTMS:            20,
		E2EMS:             40,
		TPOTMS:            999,
		CompletionTokens:  2,
	}}, 50, 50)
	if len(slices) != 1 || slices[0].TPOT.Count != 1 || slices[0].TPOT.P50MS != 20 {
		t.Fatalf("sample-derived TPOT slice = %#v", slices)
	}
}

func TestRunPerformancePreparationIsExcludedAndEvidenceStartsFresh(t *testing.T) {
	var calls atomic.Uint64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		switch call {
		case 1:
			fmt.Fprint(writer, `{"error":{"message":"warmup-only"}}`)
		case 2:
			fmt.Fprint(writer, `{"error":{"message":"ramp-only"}}`)
		default:
			fmt.Fprint(writer, `{"error":{"message":"measured-only"}}`)
		}
	}))
	defer server.Close()

	var phases []load.Phase
	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformanceWithProgress(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		WarmupRequests: 1, RampDurationMS: 1_000, RampRequestCap: 1, SliceDurationMS: 100,
	}, func(progress PerformanceProgress) {
		if len(phases) == 0 || phases[len(phases)-1] != progress.Phase {
			phases = append(phases, progress.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || report.Warmup == nil || report.Warmup.Completed != 1 || report.Warmup.Failed != 1 ||
		report.Ramp == nil || report.Ramp.Traffic.Completed != 1 || report.Ramp.Traffic.Failed != 1 ||
		report.Metrics.Completed != 1 || len(report.Samples) != 1 {
		t.Fatalf("isolated report = %#v, calls = %d", report, calls.Load())
	}
	evidence := report.Samples[0].ResponseEvidence
	if evidence == nil || !strings.Contains(evidence.Body, "measured-only") || strings.Contains(evidence.Body, "warmup-only") || strings.Contains(evidence.Body, "ramp-only") {
		t.Fatalf("measured evidence = %#v", evidence)
	}
	wantPhases := []load.Phase{PerformancePhaseWarmingUp, PerformancePhaseRamping, load.PhaseSending, load.PhaseDraining, load.PhaseCompleted}
	if !reflect.DeepEqual(phases, wantPhases) {
		t.Fatalf("phase sequence = %v, want %v", phases, wantPhases)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "warmup-only") || strings.Contains(string(encoded), "ramp-only") {
		t.Fatalf("report leaked preparation evidence: %s", encoded)
	}
}

func TestRunPerformanceRandomInputUsesUniquePrefixesAcrossPhases(t *testing.T) {
	var mu sync.Mutex
	prefixes := make(map[string]bool)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Messages) != 1 {
			t.Errorf("invalid request body: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		words := strings.Fields(body.Messages[0].Content)
		if len(words) != 4 || !strings.HasPrefix(words[0], "r") {
			t.Errorf("unexpected prompt: %q", body.Messages[0].Content)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		prefixes[words[0]] = true
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	archive := &capturingPerformanceArchive{}
	report, err := New(Dependencies{Transport: server.Client().Transport, Archive: archive}).RunPerformance(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		RandomInput: true, RequestCount: 2, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 4, OutputTokens: 2,
		WarmupRequests: 1, RampDurationMS: 1_000, RampRequestCap: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !report.Success || !report.Profile.RandomInput || !report.Archived || len(archive.saved) != 1 || len(prefixes) != 4 || len(report.Samples) != 2 {
		t.Fatalf("random-input report = %#v, unique prefixes = %d", report, len(prefixes))
	}
	if _, err := ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("archived random-input report is invalid: %v", err)
	}
	for _, sample := range report.Samples {
		if sample.TargetInputTokens != 0 || sample.TargetOutputTokens != 0 {
			t.Fatalf("fixed random-input sample has workload targets: %#v", sample)
		}
	}
}

func TestRunPerformanceCancellationDuringWarmupDoesNotArchive(t *testing.T) {
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
		report, _ := New(Dependencies{Transport: transport, Archive: archive}).RunPerformance(ctx, PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2, WarmupRequests: 2,
		})
		done <- report
	}()
	<-started
	cancel()
	report := <-done
	if report.Progress.Phase != load.PhaseCancelled || report.ErrorCode != load.ErrorCancelled || len(report.Samples) != 0 || len(archive.saved) != 0 || report.Archived {
		t.Fatalf("cancelled warmup report = %#v, archive = %#v", report, archive.saved)
	}
}

func TestRunPerformanceCancellationDuringRampDoesNotArchive(t *testing.T) {
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
		report, _ := New(Dependencies{Transport: transport, Archive: archive}).RunPerformance(ctx, PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
			RampDurationMS: 60_000, RampRequestCap: 1,
		})
		done <- report
	}()
	<-started
	cancel()
	report := <-done
	if report.Progress.Phase != load.PhaseCancelled || report.ErrorCode != load.ErrorCancelled || report.Ramp == nil ||
		len(report.Samples) != 0 || len(archive.saved) != 0 || report.Archived {
		t.Fatalf("cancelled ramp report = %#v, archive = %#v", report, archive.saved)
	}
}

func TestRunPerformanceRampIsSummarizedAndMeasuredRunStartsFresh(t *testing.T) {
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulStreamResponse(), nil
	})
	var phases []load.Phase
	report, err := New(Dependencies{Transport: transport}).RunPerformanceWithProgress(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		WarmupRequests: 1, RampDurationMS: 100, RampRequestCap: 2,
	}, func(progress PerformanceProgress) {
		if len(phases) == 0 || phases[len(phases)-1] != progress.Phase {
			phases = append(phases, progress.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 || report.Warmup == nil || report.Ramp == nil || report.Ramp.Shape != "linear_staircase" || report.Ramp.Steps != 2 ||
		report.Ramp.TargetConcurrency != 2 || report.Ramp.TargetRatePerSecond != 0 || report.Ramp.CompletedWindow ||
		!report.Ramp.Traffic.Capped || report.Ramp.Traffic.RequestCap != 2 || report.Ramp.Traffic.Completed != 2 ||
		report.Progress.Completed != 1 || len(report.Samples) != 1 {
		t.Fatalf("ramp report = %#v, calls = %d", report, calls.Load())
	}
	wantPhases := []load.Phase{PerformancePhaseWarmingUp, PerformancePhaseRamping, load.PhaseSending, load.PhaseDraining, load.PhaseCompleted}
	if !reflect.DeepEqual(phases, wantPhases) {
		t.Fatalf("phase sequence = %v, want %v", phases, wantPhases)
	}
}

func TestRunPerformanceFixedDurationMarksMeasuredBudgetExhaustionAsCapped(t *testing.T) {
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulStreamResponse(), nil
	})
	var latestProgress PerformanceProgress
	report, err := New(Dependencies{Transport: transport}).RunPerformanceWithProgress(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		DurationMS: 60_000, Concurrency: MaxPerformanceConcurrency, TimeoutMS: 2_000, InputTokens: 1, OutputTokens: 1,
		WarmupRequests: MaxPerformanceRequests - 1,
	}, func(progress PerformanceProgress) {
		latestProgress = progress
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != MaxPerformanceRequests || report.RequestBudget == nil || report.RequestBudget.MeasuredCap != 1 ||
		report.Progress.Offered != 1 || !report.Progress.Capped || !latestProgress.Capped ||
		report.Progress.SendDurationMS >= float64(report.Profile.DurationMS) {
		t.Fatalf("fixed-duration capped report = %#v, latest progress = %#v, calls = %d", report, latestProgress, calls.Load())
	}
}

func TestRunPerformanceOpenRampUsesDerivedCapAndCompletesWindow(t *testing.T) {
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulStreamResponse(), nil
	})
	archive := &capturingPerformanceArchive{}
	report, err := New(Dependencies{
		Transport: transport,
		Archive:   archive,
		Clock:     fixedPerformanceClock{now: time.Date(2026, time.September, 5, 2, 3, 4, 5, time.UTC)},
		IDFactory: func(time.Time) (string, error) { return "77777777-7777-4777-8777-777777777767", nil },
	}).RunPerformance(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		LoadMode: domain.LoadOpenLoop, ArrivalPattern: load.ArrivalConstant,
		RequestCount: 1, RatePerSecond: 100, MaxInFlight: 2,
		TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2, RampDurationMS: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 7 || report.RequestBudget == nil || report.RequestBudget.RampCap != 6 || report.Ramp == nil ||
		report.Ramp.Steps != 10 || report.Ramp.TargetConcurrency != 0 || report.Ramp.TargetRatePerSecond != 100 ||
		!report.Ramp.CompletedWindow || report.Ramp.Traffic.Capped || report.Ramp.Traffic.SendDurationMS < 100 || report.Progress.Completed != 1 {
		t.Fatalf("open ramp report = %#v, calls = %d", report, calls.Load())
	}
	if _, err := ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("ValidateArchivedPerformanceReport() error = %v", err)
	}
	report.Ramp.Traffic.Capped = true
	report.Ramp.CompletedWindow = false
	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("coordinated open-ramp cap tamper was accepted")
	}
}

func TestRunPerformancePreparationDoesNotShiftNormalMeasuredTargets(t *testing.T) {
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return successfulStreamResponse(), nil
	})
	base := PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 5, Concurrency: 1, TimeoutMS: 2_000,
		InputTokens: 50, OutputTokens: 10, WorkloadMode: PerformanceWorkloadNormal, RandomSeed: 77,
		InputTokensStdDev: 10, OutputTokensStdDev: 2, SharedPrefixTokens: 5,
	}
	withoutPreparation, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	base.WarmupRequests = 2
	withPreparation, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutPreparation.Samples) != 5 || len(withPreparation.Samples) != 5 {
		t.Fatalf("sample counts = %d and %d", len(withoutPreparation.Samples), len(withPreparation.Samples))
	}
	for index := range withoutPreparation.Samples {
		left, right := withoutPreparation.Samples[index], withPreparation.Samples[index]
		if left.RequestIndex != right.RequestIndex || left.TargetInputTokens != right.TargetInputTokens || left.TargetOutputTokens != right.TargetOutputTokens {
			t.Fatalf("target %d shifted: %#v vs %#v", index, left, right)
		}
	}
}

func TestRunPerformanceNormalPreparationUsesDisjointPromptNamespace(t *testing.T) {
	var prompts []string
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		prompts = append(prompts, body.Messages[0].Content)
		return successfulStreamResponse(), nil
	})
	report, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000,
		InputTokens: 20, OutputTokens: 4, WorkloadMode: PerformanceWorkloadNormal, RandomSeed: 9,
		InputTokensStdDev: 2, OutputTokensStdDev: 1, SharedPrefixTokens: 4, WarmupRequests: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || len(prompts) != 2 || prompts[0] == prompts[1] {
		t.Fatalf("preparation prompts = %#v, report = %#v", prompts, report)
	}
}

func TestRunPerformanceProducesValidArchivedPhaseThreeReport(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"prompt_tokens_details\":{\"cached_tokens\":1}}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	archive := &capturingPerformanceArchive{}
	generatedAt := time.Date(2026, time.September, 5, 1, 2, 3, 4, time.UTC)
	report, err := New(Dependencies{
		Transport: server.Client().Transport,
		Archive:   archive,
		Clock:     fixedPerformanceClock{now: generatedAt},
		IDFactory: func(time.Time) (string, error) { return "77777777-7777-4777-8777-777777777768", nil },
	}).RunPerformance(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2,
		WarmupRequests: 1, RampDurationMS: 20, RampRequestCap: 1, SliceDurationMS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.saved) != 1 || !report.Archived || report.RequestBudget == nil || report.Warmup == nil || report.Ramp == nil || len(report.TimeSlices) == 0 {
		t.Fatalf("archived phase-three report = %#v, saved = %d", report, len(archive.saved))
	}
	if _, err := ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("ValidateArchivedPerformanceReport() error = %v; report = %#v", err, report)
	}
}

func TestPhaseTwoReportKeepsCanonicalShapeWithoutPhaseThreeConfiguration(t *testing.T) {
	report := PerformanceReport{Protocol: domain.ProtocolOpenAIChat, SchemaVersion: PerformanceSchemaVersion, Profile: PerformanceProfile{}}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"request_budget", "warmup", "ramp", "time_slices", "warmup_requests", "ramp_duration_ms", "ramp_request_cap", "slice_duration_ms"} {
		if strings.Contains(string(encoded), `"`+field+`"`) {
			t.Fatalf("phase-two shape contains %q: %s", field, encoded)
		}
	}
	if strings.Contains(string(encoded), `"capped"`) {
		t.Fatalf("uncapped phase-two shape contains capped: %s", encoded)
	}
	report.Progress.Capped = true
	encoded, err = json.Marshal(report)
	if err != nil || !strings.Contains(string(encoded), `"capped":true`) {
		t.Fatalf("capped progress encoding = %s, error = %v", encoded, err)
	}
}

func successfulStreamResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
				"data: [DONE]\n\n",
		)),
	}
}
