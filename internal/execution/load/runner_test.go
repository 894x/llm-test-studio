package load

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func fixedProfile(requests uint64, concurrency uint32) domain.LoadProfile {
	return domain.LoadProfile{
		Mode:             domain.LoadFixedConcurrency,
		Concurrency:      concurrency,
		RequestCount:     requests,
		RequestTimeoutMS: 1_000,
	}
}

func waitForCount(t *testing.T, count *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for count.Load() < want {
		select {
		case <-deadline:
			t.Fatalf("started requests = %d, want at least %d", count.Load(), want)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestNormalizeObservedProgressDurationsKeepsCompletedRunsMeasurable(t *testing.T) {
	progress := normalizeObservedProgressDurations(Progress{Offered: 1, Completed: 1})
	if progress.SendDuration != time.Nanosecond || progress.TotalDuration != time.Nanosecond || progress.DrainDuration != 0 {
		t.Fatalf("normalized zero-duration progress = %#v", progress)
	}

	progress = normalizeObservedProgressDurations(Progress{
		Offered: 1, Completed: 1,
		TotalDuration: 10 * time.Nanosecond, DrainDuration: 10 * time.Nanosecond,
	})
	if progress.SendDuration != time.Nanosecond || progress.TotalDuration != 10*time.Nanosecond || progress.DrainDuration != 9*time.Nanosecond {
		t.Fatalf("normalized positive-duration progress = %#v", progress)
	}

	empty := Progress{}
	if got := normalizeObservedProgressDurations(empty); got != empty {
		t.Fatalf("empty progress changed: %#v", got)
	}
}

func TestRunFixedConcurrencyCapsInFlightAndDrains(t *testing.T) {
	var started atomic.Int64
	release := make(chan struct{})
	done := make(chan struct {
		outcome Outcome
		err     error
	}, 1)

	go func() {
		outcome, err := Run(context.Background(), fixedProfile(5, 2), func(_ context.Context, request Request) Observation {
			started.Add(1)
			<-release
			return Observation{Index: request.Index, Success: true, HTTPStatus: 200, StreamComplete: true}
		}, Options{})
		done <- struct {
			outcome Outcome
			err     error
		}{outcome, err}
	}()

	waitForCount(t, &started, 2)
	time.Sleep(20 * time.Millisecond)
	if got := started.Load(); got != 2 {
		t.Fatalf("fixed concurrency launched %d blocked requests, want 2", got)
	}
	close(release)

	result := <-done
	if result.err != nil {
		t.Fatalf("Run() error = %v", result.err)
	}
	if result.outcome.Progress.Phase != PhaseCompleted || result.outcome.Progress.PeakInFlight != 2 {
		t.Fatalf("progress = %#v", result.outcome.Progress)
	}
	if len(result.outcome.Results) != 5 || result.outcome.Progress.Succeeded != 5 {
		t.Fatalf("outcome = %#v", result.outcome)
	}
	for index, observation := range result.outcome.Results {
		if observation.Index != uint64(index) {
			t.Fatalf("results are not request ordered: %#v", result.outcome.Results)
		}
	}
}

func TestDurationOnlyFixedConcurrencyHonorsScheduledRequestCap(t *testing.T) {
	profile := fixedProfile(0, 4)
	profile.DurationMS = 50
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		time.Sleep(2 * time.Millisecond)
		return Observation{Index: request.Index, Success: true}
	}, Options{MaxScheduledRequests: 3})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Progress.Planned != 3 || outcome.Progress.Offered != 3 || len(outcome.Results) != 3 {
		t.Fatalf("fixed duration cap outcome = %#v", outcome)
	}
	if !outcome.Progress.Capped {
		t.Fatalf("fixed duration cap was exhausted without Capped: %#v", outcome.Progress)
	}
	if outcome.Progress.SendDuration >= 50*time.Millisecond {
		t.Fatalf("fixed duration cap did not end the send phase early: %#v", outcome.Progress)
	}
}

func TestRunFixedRampIncreasesConcurrencyAndDrainsOnce(t *testing.T) {
	profile := fixedProfile(1_000, 4)
	profile.DurationMS = 100

	type progressEvent struct {
		at       time.Duration
		progress Progress
	}
	var eventsMu sync.Mutex
	events := make([]progressEvent, 0, 32)
	var runStarted time.Time
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		time.Sleep(45 * time.Millisecond)
		return Observation{Index: request.Index, Success: true}
	}, Options{Ramp: true, OnProgress: func(progress Progress) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		if runStarted.IsZero() && progress.Phase == PhaseSending {
			runStarted = time.Now()
		}
		events = append(events, progressEvent{at: time.Since(runStarted), progress: progress})
	}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Progress.Capped {
		t.Fatalf("duration-bounded fixed ramp was marked capped: %#v", outcome.Progress)
	}
	if outcome.Progress.PeakInFlight != 4 {
		t.Fatalf("fixed ramp peak in flight = %d, want final target 4", outcome.Progress.PeakInFlight)
	}
	if outcome.Progress.SendDuration < 90*time.Millisecond {
		t.Fatalf("fixed ramp send duration = %v, want final interval to run", outcome.Progress.SendDuration)
	}

	phases := make([]Phase, 0, 3)
	sawFinalTarget := false
	for _, event := range events {
		if len(phases) == 0 || phases[len(phases)-1] != event.progress.Phase {
			phases = append(phases, event.progress.Phase)
		}
		if event.progress.Phase != PhaseSending {
			continue
		}
		var limit uint64
		switch {
		case event.at < 20*time.Millisecond:
			limit = 1
		case event.at < 45*time.Millisecond:
			limit = 2
		case event.at < 70*time.Millisecond:
			limit = 3
		default:
			limit = 4
		}
		if event.progress.InFlight > limit {
			t.Fatalf("fixed ramp in-flight = %d at %v, want <= %d", event.progress.InFlight, event.at, limit)
		}
		if event.at >= 70*time.Millisecond && event.progress.InFlight == 4 {
			sawFinalTarget = true
		}
	}
	if !sawFinalTarget {
		t.Fatalf("fixed ramp never ran target concurrency during final interval: %#v", events)
	}
	wantPhases := []Phase{PhaseSending, PhaseDraining, PhaseCompleted}
	if !reflect.DeepEqual(phases, wantPhases) {
		t.Fatalf("phase transitions = %#v, want one drain %#v", phases, wantPhases)
	}
}

func TestEstimateLinearRampRequestCapUsesPiecewiseIntensityAndRejectsOverflow(t *testing.T) {
	tests := []struct {
		name    string
		arrival ArrivalPattern
		want    uint64
	}{
		{name: "constant", arrival: ArrivalConstant, want: 55},
		{name: "default constant", arrival: "", want: 55},
		{name: "poisson headroom", arrival: ArrivalPoisson, want: 111},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EstimateLinearRampRequestCap(100, time.Second, test.arrival)
			if err != nil {
				t.Fatalf("EstimateLinearRampRequestCap() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("EstimateLinearRampRequestCap() = %d, want %d", got, test.want)
			}
		})
	}

	invalid := []struct {
		rate     float64
		duration time.Duration
		arrival  ArrivalPattern
	}{
		{rate: math.NaN(), duration: time.Second, arrival: ArrivalConstant},
		{rate: math.Inf(1), duration: time.Second, arrival: ArrivalConstant},
		{rate: math.MaxFloat64, duration: time.Duration(math.MaxInt64), arrival: ArrivalPoisson},
		{rate: 1, duration: 0, arrival: ArrivalConstant},
		{rate: 1, duration: time.Second, arrival: "bursty"},
	}
	for _, test := range invalid {
		if got, err := EstimateLinearRampRequestCap(test.rate, test.duration, test.arrival); err == nil {
			t.Fatalf("EstimateLinearRampRequestCap(%v, %v, %q) = %d, nil error", test.rate, test.duration, test.arrival, got)
		}
	}
}

func TestEstimateOpenLoopRampScheduleUsesExactSeededPlan(t *testing.T) {
	tests := []struct {
		name         string
		arrival      ArrivalPattern
		seed         uint32
		maxScheduled uint64
		wantPlanned  uint64
		wantCapped   bool
	}{
		{name: "constant", arrival: ArrivalConstant, seed: 77, maxScheduled: 200, wantPlanned: 55},
		{name: "default constant", seed: 77, maxScheduled: 200, wantPlanned: 55},
		{name: "constant cap", arrival: ArrivalConstant, seed: 77, maxScheduled: 5, wantPlanned: 5, wantCapped: true},
		{name: "poisson seed 77", arrival: ArrivalPoisson, seed: 77, maxScheduled: 200, wantPlanned: 57},
		{name: "poisson seed 78", arrival: ArrivalPoisson, seed: 78, maxScheduled: 200, wantPlanned: 51},
		{name: "poisson cap", arrival: ArrivalPoisson, seed: 77, maxScheduled: 5, wantPlanned: 5, wantCapped: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planned, capped, err := EstimateOpenLoopRampSchedule(
				2_500,
				40*time.Millisecond,
				test.arrival,
				test.seed,
				test.maxScheduled,
			)
			if err != nil {
				t.Fatalf("EstimateOpenLoopRampSchedule() error = %v", err)
			}
			if planned != test.wantPlanned || capped != test.wantCapped {
				t.Fatalf("EstimateOpenLoopRampSchedule() = (%d, %t), want (%d, %t)", planned, capped, test.wantPlanned, test.wantCapped)
			}
		})
	}
}

func TestLinearRampStepCountMatchesLoadModeAndTarget(t *testing.T) {
	tests := []struct {
		profile domain.LoadProfile
		want    uint32
	}{
		{profile: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1}, want: 1},
		{profile: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 4}, want: 4},
		{profile: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 20}, want: 10},
		{profile: domain.LoadProfile{Mode: domain.LoadOpenLoop, Concurrency: 1}, want: 10},
		{profile: domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1}, want: 0},
	}
	for _, test := range tests {
		if got := LinearRampStepCount(test.profile); got != test.want {
			t.Fatalf("LinearRampStepCount(%#v) = %d, want %d", test.profile, got, test.want)
		}
	}
}

func TestRunOpenLoopRampUsesOneSeededIncreasingSchedule(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 40,
		RatePerSecond: 2_500, RequestTimeoutMS: 1_000,
	}
	run := func(arrival ArrivalPattern, seed uint32) (Outcome, []time.Duration) {
		t.Helper()
		outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
			return Observation{Index: request.Index, Success: true}
		}, Options{
			Ramp: true, ArrivalPattern: arrival, RandomSeed: seed,
			MaxScheduledRequests: 200,
		})
		if err != nil {
			t.Fatalf("Run(%q) error = %v", arrival, err)
		}
		offsets := make([]time.Duration, len(outcome.Results))
		for index, observation := range outcome.Results {
			offsets[index] = observation.ScheduledOffset
		}
		return outcome, offsets
	}

	for _, arrival := range []ArrivalPattern{ArrivalConstant, ArrivalPoisson} {
		t.Run(string(arrival), func(t *testing.T) {
			outcome, first := run(arrival, 77)
			_, second := run(arrival, 77)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("same-seed ramp schedules differ: %v != %v", first, second)
			}
			if arrival == ArrivalPoisson {
				if _, different := run(arrival, 78); reflect.DeepEqual(first, different) {
					t.Fatalf("different-seed ramp schedules are identical: %v", first)
				}
			}
			if len(first) == 0 || first[0] != 0 {
				t.Fatalf("ramp schedule must start at zero: %v", first)
			}
			firstHalf, secondHalf := 0, 0
			for index, offset := range first {
				if offset < 0 || offset >= 40*time.Millisecond {
					t.Fatalf("scheduled offset %v outside [0, 40ms): %v", offset, first)
				}
				if index > 0 && offset <= first[index-1] {
					t.Fatalf("ramp schedule is not strictly increasing: %v", first)
				}
				if offset < 20*time.Millisecond {
					firstHalf++
				} else {
					secondHalf++
				}
			}
			if secondHalf <= firstHalf {
				t.Fatalf("ramp density did not increase: first half=%d second half=%d schedule=%v", firstHalf, secondHalf, first)
			}
			if outcome.Progress.Capped || outcome.Progress.Planned != uint64(len(first)) {
				t.Fatalf("ramp progress = %#v, results = %d", outcome.Progress, len(first))
			}
			if arrival == ArrivalConstant {
				want, err := EstimateLinearRampRequestCap(profile.RatePerSecond, 40*time.Millisecond, arrival)
				if err != nil || uint64(len(first)) != want {
					t.Fatalf("constant ramp results = %d, estimate = %d, error = %v", len(first), want, err)
				}
				wantPrefix := []time.Duration{0, 4 * time.Millisecond, 6 * time.Millisecond, 8 * time.Millisecond}
				if len(first) < len(wantPrefix) || !reflect.DeepEqual(first[:len(wantPrefix)], wantPrefix) {
					t.Fatalf("constant ramp prefix = %v, want cumulative-intensity mapping %v", first, wantPrefix)
				}
			}
		})
	}
}

func TestRunMarksRampSafetyCapsWithoutMarkingOrdinaryCountCompletion(t *testing.T) {
	fixedRamp := fixedProfile(2, 4)
	fixedRamp.DurationMS = 100
	fixedOutcome, err := Run(context.Background(), fixedRamp, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{Ramp: true})
	if err != nil {
		t.Fatalf("Run(fixed ramp) error = %v", err)
	}
	if !fixedOutcome.Progress.Capped || fixedOutcome.Progress.Offered != 2 {
		t.Fatalf("fixed ramp safety-cap progress = %#v", fixedOutcome.Progress)
	}

	openRamp := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 20,
		RatePerSecond: 10_000, RequestTimeoutMS: 1_000,
	}
	openOutcome, err := Run(context.Background(), openRamp, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{Ramp: true, MaxScheduledRequests: 5})
	if err != nil {
		t.Fatalf("Run(open ramp) error = %v", err)
	}
	if !openOutcome.Progress.Capped || openOutcome.Progress.Planned != 5 || openOutcome.Progress.Offered != 5 {
		t.Fatalf("open ramp hard-cap progress = %#v", openOutcome.Progress)
	}

	ordinaryOutcome, err := Run(context.Background(), fixedProfile(2, 2), func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{})
	if err != nil {
		t.Fatalf("Run(ordinary count) error = %v", err)
	}
	if ordinaryOutcome.Progress.Capped {
		t.Fatalf("ordinary explicit count was marked capped: %#v", ordinaryOutcome.Progress)
	}
	ordinaryOpen := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 2,
		RatePerSecond: 1_000, RequestTimeoutMS: 1_000,
	}
	ordinaryOpenOutcome, err := Run(context.Background(), ordinaryOpen, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{ArrivalPattern: ArrivalPoisson, RandomSeed: 3})
	if err != nil {
		t.Fatalf("Run(ordinary open count) error = %v", err)
	}
	if ordinaryOpenOutcome.Progress.Capped {
		t.Fatalf("ordinary explicit open-loop count was marked capped: %#v", ordinaryOpenOutcome.Progress)
	}
}

func TestRunRejectsRampWithoutSupportedDurationBoundedMode(t *testing.T) {
	tests := []domain.LoadProfile{
		{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000},
		{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000},
		{Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 1, RatePerSecond: 10, RequestTimeoutMS: 1_000},
	}
	for _, profile := range tests {
		if _, err := Run(context.Background(), profile, func(context.Context, Request) Observation {
			return Observation{Success: true}
		}, Options{Ramp: true}); err == nil {
			t.Fatalf("Run(%#v, Ramp=true) error = nil", profile)
		}
	}
}

func TestRunRejectsCountLimitedOpenLoopRamp(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 1, DurationMS: 20,
		RatePerSecond: 100, RequestTimeoutMS: 1_000,
	}
	var called atomic.Bool
	_, err := Run(context.Background(), profile, func(context.Context, Request) Observation {
		called.Store(true)
		return Observation{Success: true}
	}, Options{Ramp: true})
	if err == nil || err.Error() != "open-loop ramp requires a duration-only profile" {
		t.Fatalf("Run(open-loop count ramp) error = %v", err)
	}
	if called.Load() {
		t.Fatal("executor was called for a rejected count-limited open-loop ramp")
	}
}

func TestRunCancellationDuringFixedRampDrainsOnce(t *testing.T) {
	profile := fixedProfile(100, 4)
	profile.DurationMS = 1_000
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	done := make(chan struct {
		outcome Outcome
		err     error
	}, 1)
	var phaseMu sync.Mutex
	phases := make([]Phase, 0, 3)

	go func() {
		outcome, err := Run(ctx, profile, func(requestContext context.Context, request Request) Observation {
			select {
			case started <- struct{}{}:
			default:
			}
			<-requestContext.Done()
			return Observation{Index: request.Index, ErrorCode: ErrorCancelled}
		}, Options{Ramp: true, OnProgress: func(progress Progress) {
			phaseMu.Lock()
			defer phaseMu.Unlock()
			if len(phases) == 0 || phases[len(phases)-1] != progress.Phase {
				phases = append(phases, progress.Phase)
			}
		}})
		done <- struct {
			outcome Outcome
			err     error
		}{outcome: outcome, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fixed ramp did not launch its first request")
	}
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) || result.outcome.Progress.Phase != PhaseCancelled {
		t.Fatalf("cancelled ramp result = (%#v, %v)", result.outcome.Progress, result.err)
	}
	phaseMu.Lock()
	defer phaseMu.Unlock()
	want := []Phase{PhaseSending, PhaseDraining, PhaseCancelled}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("cancelled ramp phase transitions = %#v, want %#v", phases, want)
	}
}

func TestRunOpenLoopLaunchesOnScheduleWithoutWaitingForInflightRequests(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 3,
		RatePerSecond: 500, RequestTimeoutMS: 1_000,
	}
	var started atomic.Int64
	release := make(chan struct{})
	done := make(chan Outcome, 1)

	go func() {
		outcome, _ := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
			started.Add(1)
			<-release
			return Observation{Index: request.Index, Success: true, HTTPStatus: 200, StreamComplete: true}
		}, Options{})
		done <- outcome
	}()

	waitForCount(t, &started, 3)
	close(release)
	outcome := <-done
	if outcome.Progress.PeakInFlight != 3 {
		t.Fatalf("open-loop peak in flight = %d, want 3", outcome.Progress.PeakInFlight)
	}
	if outcome.Progress.SendDuration <= 0 || outcome.Progress.Phase != PhaseCompleted {
		t.Fatalf("outcome progress = %#v", outcome.Progress)
	}
}

func TestDurationOnlyOpenLoopKeepsTheConfiguredSendWindow(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 50,
		RatePerSecond: 10, RequestTimeoutMS: 1_000,
	}
	started := time.Now()
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond {
		t.Fatalf("duration-only run ended after %v, want the configured send window", elapsed)
	}
	if outcome.Progress.Offered != 1 {
		t.Fatalf("duration-only outcome = %#v", outcome)
	}
}

func TestDurationOnlyOpenLoopDoesNotExceedDerivedRequestCap(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 100,
		RatePerSecond: 30, RequestTimeoutMS: 1_000,
	}
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Progress.Planned != 3 || outcome.Progress.Offered != 3 || len(outcome.Results) != 3 {
		t.Fatalf("derived-cap outcome = %#v", outcome)
	}
}

func TestDurationOnlyConstantOpenLoopRunsAtScheduledRequestCap(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 20,
		RatePerSecond: 10_000, RequestTimeoutMS: 1_000,
	}
	started := time.Now()
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{MaxScheduledRequests: 5})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("capped duration-only run ended after %v, want configured send window", elapsed)
	}
	if outcome.Progress.Planned != 5 || outcome.Progress.Offered != 5 || len(outcome.Results) != 5 {
		t.Fatalf("constant duration cap outcome = %#v", outcome)
	}
	if !outcome.Progress.Capped {
		t.Fatalf("constant duration schedule was truncated without Capped: %#v", outcome.Progress)
	}
}

func TestEstimateOpenLoopDurationScheduleUsesSchedulerPrecision(t *testing.T) {
	tests := []struct {
		name         string
		rate         float64
		duration     time.Duration
		arrival      ArrivalPattern
		seed         uint32
		maxScheduled uint64
		wantPlanned  uint64
		wantCapped   bool
	}{
		{
			name: "constant scheduler ceiling", rate: 29_000, duration: time.Millisecond,
			arrival: ArrivalConstant, maxScheduled: 100, wantPlanned: 30,
		},
		{
			name: "constant exact boundary", rate: 30, duration: 100 * time.Millisecond,
			arrival: ArrivalConstant, maxScheduled: 100, wantPlanned: 3,
		},
		{
			name: "constant cap", rate: 29_000, duration: time.Millisecond,
			arrival: ArrivalConstant, maxScheduled: 29, wantPlanned: 29, wantCapped: true,
		},
		{
			name: "default constant", rate: 29_000, duration: time.Millisecond,
			maxScheduled: 100, wantPlanned: 30,
		},
		{
			name: "seeded poisson", rate: 1_000, duration: 5 * time.Millisecond,
			arrival: ArrivalPoisson, seed: 42, maxScheduled: 100, wantPlanned: 7,
		},
		{
			name: "seeded poisson cap", rate: 1_000, duration: 5 * time.Millisecond,
			arrival: ArrivalPoisson, seed: 42, maxScheduled: 3, wantPlanned: 3, wantCapped: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planned, capped, err := EstimateOpenLoopDurationSchedule(
				test.rate,
				test.duration,
				test.arrival,
				test.seed,
				test.maxScheduled,
			)
			if err != nil {
				t.Fatalf("EstimateOpenLoopDurationSchedule() error = %v", err)
			}
			if planned != test.wantPlanned || capped != test.wantCapped {
				t.Fatalf("EstimateOpenLoopDurationSchedule() = (%d, %t), want (%d, %t)", planned, capped, test.wantPlanned, test.wantCapped)
			}
		})
	}
}

func TestRunOpenLoopPoissonScheduleIsSeededAndNonConstant(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 5,
		RatePerSecond: 1_000, RequestTimeoutMS: 1_000,
	}
	run := func(seed uint32) []time.Duration {
		outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
			return Observation{Index: request.Index, Success: true}
		}, Options{ArrivalPattern: ArrivalPoisson, RandomSeed: seed})
		if err != nil {
			t.Fatalf("Run(seed=%d) error = %v", seed, err)
		}
		offsets := make([]time.Duration, len(outcome.Results))
		for index, observation := range outcome.Results {
			offsets[index] = observation.ScheduledOffset
		}
		return offsets
	}

	first := run(42)
	if second := run(42); !reflect.DeepEqual(second, first) {
		t.Fatalf("same-seed schedules differ: %v != %v", first, second)
	}
	if different := run(43); reflect.DeepEqual(different, first) {
		t.Fatalf("different-seed schedules are identical: %v", first)
	}
	if first[0] != 0 {
		t.Fatalf("first scheduled offset = %v, want zero", first[0])
	}
	gaps := make(map[time.Duration]struct{}, len(first)-1)
	for index := 1; index < len(first); index++ {
		if first[index] <= first[index-1] {
			t.Fatalf("schedule is not strictly increasing: %v", first)
		}
		gaps[first[index]-first[index-1]] = struct{}{}
	}
	if len(gaps) == 1 {
		t.Fatalf("poisson inter-arrivals are constant: %v", first)
	}
}

func TestDurationOnlyPoissonWaitsWindowAndHonorsCallerScheduleCap(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 30,
		RatePerSecond: 10_000, RequestTimeoutMS: 1_000,
	}
	started := time.Now()
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{ArrivalPattern: ArrivalPoisson, RandomSeed: 7, MaxScheduledRequests: 10})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("duration-only poisson run ended after %v, want configured send window", elapsed)
	}
	if outcome.Progress.Offered == 0 || outcome.Progress.Offered > 10 || len(outcome.Results) > 10 {
		t.Fatalf("bounded poisson outcome = %#v", outcome)
	}
	if !outcome.Progress.Capped {
		t.Fatalf("truncated duration-only poisson schedule was not marked capped: %#v", outcome.Progress)
	}
}

func TestRunRejectsPoissonOutsideOpenLoop(t *testing.T) {
	if _, err := Run(context.Background(), fixedProfile(1, 1), func(context.Context, Request) Observation {
		return Observation{Success: true}
	}, Options{ArrivalPattern: ArrivalPoisson}); err == nil {
		t.Fatal("Run(fixed + poisson) error = nil")
	}
}

func TestRunStopSendingDrainsOnlyLaunchedWork(t *testing.T) {
	stop := make(chan struct{})
	release := make(chan struct{})
	var started atomic.Int64
	done := make(chan Outcome, 1)

	go func() {
		outcome, _ := Run(context.Background(), fixedProfile(20, 2), func(_ context.Context, request Request) Observation {
			started.Add(1)
			<-release
			return Observation{Index: request.Index, Success: true}
		}, Options{StopSending: stop})
		done <- outcome
	}()

	waitForCount(t, &started, 2)
	close(stop)
	close(release)
	outcome := <-done
	if !outcome.Progress.Stopped || outcome.Progress.Launched != 2 || outcome.Progress.Completed != 2 {
		t.Fatalf("stop/drain progress = %#v", outcome.Progress)
	}
	if outcome.Progress.Phase != PhaseCompleted {
		t.Fatalf("phase = %q, want completed drain", outcome.Progress.Phase)
	}
}

func TestRunNaturalSendWindowEmitsDrainWithoutClaimingManualStop(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 100,
		RatePerSecond: 1_000, DurationMS: 2, RequestTimeoutMS: 1_000,
	}
	phases := make([]Phase, 0, 8)
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		return Observation{Index: request.Index, Success: true}
	}, Options{OnProgress: func(progress Progress) {
		if len(phases) == 0 || phases[len(phases)-1] != progress.Phase {
			phases = append(phases, progress.Phase)
		}
	}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Progress.Stopped {
		t.Fatalf("natural send-window completion was marked stopped: %#v", outcome.Progress)
	}
	if len(outcome.Results) < 1 || len(outcome.Results) >= 100 {
		t.Fatalf("send-window results = %d, want a bounded prefix", len(outcome.Results))
	}
	want := []Phase{PhaseSending, PhaseDraining, PhaseCompleted}
	if len(phases) != len(want) {
		t.Fatalf("phase sequence = %#v, want %#v", phases, want)
	}
	for index := range want {
		if phases[index] != want[index] {
			t.Fatalf("phase sequence = %#v, want %#v", phases, want)
		}
	}
}

func TestNaturalCompletionWinsStopSignalClosedByFinalLaunchProgress(t *testing.T) {
	profiles := []domain.LoadProfile{
		fixedProfile(1, 1),
		{Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 1, RatePerSecond: 10, RequestTimeoutMS: 1_000},
	}
	for _, profile := range profiles {
		t.Run(string(profile.Mode), func(t *testing.T) {
			stop := make(chan struct{})
			var closed atomic.Bool
			outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
				return Observation{Index: request.Index, Success: true}
			}, Options{StopSending: stop, OnProgress: func(progress Progress) {
				if progress.Launched == progress.Planned && closed.CompareAndSwap(false, true) {
					close(stop)
				}
			}})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Progress.Stopped || outcome.Progress.Launched != 1 {
				t.Fatalf("final-launch stop race = %#v", outcome.Progress)
			}
		})
	}
}

func TestRunCancellationReachesInflightExecutor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 2)
	done := make(chan error, 1)

	go func() {
		_, err := Run(ctx, fixedProfile(2, 2), func(requestContext context.Context, request Request) Observation {
			started <- struct{}{}
			<-requestContext.Done()
			return Observation{Index: request.Index, ErrorCode: "cancelled"}
		}, Options{})
		done <- err
	}()

	<-started
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestRunCancellationDuringDrainRemainsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	draining := make(chan struct{})
	done := make(chan error, 1)
	var announced atomic.Bool

	go func() {
		_, err := Run(ctx, fixedProfile(1, 1), func(requestContext context.Context, request Request) Observation {
			<-requestContext.Done()
			return Observation{Index: request.Index}
		}, Options{OnProgress: func(progress Progress) {
			if progress.Phase == PhaseDraining && announced.CompareAndSwap(false, true) {
				close(draining)
			}
		}})
		done <- err
	}()

	<-draining
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled after send phase", err)
	}
}

func TestRunAppliesPerRequestTimeout(t *testing.T) {
	profile := fixedProfile(1, 1)
	profile.RequestTimeoutMS = 10
	outcome, err := Run(context.Background(), profile, func(requestContext context.Context, request Request) Observation {
		<-requestContext.Done()
		return Observation{Index: request.Index}
	}, Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(outcome.Results) != 1 || !outcome.Results[0].TimedOut || outcome.Results[0].ErrorCode != "timeout" {
		t.Fatalf("timeout result = %#v", outcome.Results)
	}
}

func TestRunContainsExecutorPanicsAndNormalizesUnsafeErrorCodes(t *testing.T) {
	outcome, err := Run(context.Background(), fixedProfile(3, 1), func(_ context.Context, request Request) Observation {
		if request.Index == 0 {
			panic("sk-do-not-return-this")
		}
		if request.Index == 1 {
			return Observation{Index: request.Index, ErrorCode: "sk-do-not-return-this"}
		}
		return Observation{Index: request.Index, ErrorCode: "secret_live_token_1234"}
	}, Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Results[0].ErrorCode != "executor_panic" ||
		outcome.Results[1].ErrorCode != "unclassified_error" ||
		outcome.Results[2].ErrorCode != "unclassified_error" {
		t.Fatalf("normalized errors = %#v", outcome.Results)
	}
}

func TestOpenLoopUsesRuntimeAdmissionInsteadOfWorstCasePreflight(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1,
		RequestCount: 3, RatePerSecond: 10, RequestTimeoutMS: 1_000,
	}
	var called atomic.Int64
	outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
		called.Add(1)
		return Observation{Index: request.Index, Success: true}
	}, Options{MaxOpenLoopInFlight: 1})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called.Load() != 3 || outcome.Progress.Rejected != 0 || outcome.Progress.Offered != 3 {
		t.Fatalf("runtime admission outcome = %#v, calls = %d", outcome.Progress, called.Load())
	}
}

func TestOpenLoopRuntimeAdmissionRejectsBacklogWithoutExceedingLimit(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 8,
		RatePerSecond: 1_000, RequestTimeoutMS: 1,
	}
	release := make(chan struct{})
	scheduled := make(chan struct{})
	var scheduledOnce atomic.Bool
	var active atomic.Int64
	var actualPeak atomic.Int64
	done := make(chan struct {
		outcome Outcome
		err     error
	}, 1)
	go func() {
		outcome, err := Run(context.Background(), profile, func(_ context.Context, request Request) Observation {
			current := active.Add(1)
			for peak := actualPeak.Load(); current > peak && !actualPeak.CompareAndSwap(peak, current); peak = actualPeak.Load() {
			}
			<-release
			active.Add(-1)
			return Observation{Index: request.Index, Success: true}
		}, Options{MaxOpenLoopInFlight: 2, OnProgress: func(progress Progress) {
			if progress.Launched+progress.Rejected == progress.Planned && scheduledOnce.CompareAndSwap(false, true) {
				close(scheduled)
			}
		}})
		done <- struct {
			outcome Outcome
			err     error
		}{outcome, err}
	}()

	select {
	case <-scheduled:
	case <-time.After(2 * time.Second):
		t.Fatal("open-loop schedule did not finish")
	}
	if peak := actualPeak.Load(); peak > 2 {
		t.Fatalf("actual executor peak = %d, want at most 2", peak)
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatalf("Run() error = %v", result.err)
	}
	if result.outcome.Progress.PeakInFlight > 2 || result.outcome.Progress.Rejected == 0 {
		t.Fatalf("runtime admission = %#v", result.outcome.Progress)
	}
	if got, want := result.outcome.Progress.Launched+result.outcome.Progress.Rejected, result.outcome.Progress.Planned; got != want {
		t.Fatalf("launched + rejected = %d, want %d", got, want)
	}
	if result.outcome.Progress.Offered != result.outcome.Progress.Launched+result.outcome.Progress.Rejected {
		t.Fatalf("offered invariant = %#v", result.outcome.Progress)
	}
	if got, want := result.outcome.Progress.Completed, result.outcome.Progress.Planned; got != want {
		t.Fatalf("completed = %d, want %d", got, want)
	}
	if got, want := uint64(len(result.outcome.Results)), result.outcome.Progress.Planned; got != want {
		t.Fatalf("results = %d, want %d", got, want)
	}
	if result.outcome.Progress.InFlight != 0 || active.Load() != 0 {
		t.Fatalf("final in-flight: reported=%d actual=%d", result.outcome.Progress.InFlight, active.Load())
	}
}

func TestRunRejectsInvalidSingleAndUnrepresentableOpenLoopProfiles(t *testing.T) {
	tests := []domain.LoadProfile{
		{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 2, RequestTimeoutMS: 1_000},
		{Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 2, RatePerSecond: 2e9, RequestTimeoutMS: 1_000},
		{Mode: domain.LoadOpenLoop, Concurrency: 1, RequestCount: 2, RatePerSecond: 1e-20, RequestTimeoutMS: 1_000},
	}
	for _, profile := range tests {
		if _, err := Run(context.Background(), profile, func(context.Context, Request) Observation { return Observation{} }, Options{}); err == nil {
			t.Fatalf("invalid profile validated: %#v", profile)
		}
	}
}
