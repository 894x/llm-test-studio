package load

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/domain"
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

func TestOpenLoopRejectsUnsafeWorstCaseInflightBeforeLaunching(t *testing.T) {
	profile := domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 1,
		RequestCount: MaxOpenLoopInFlight + 1, RatePerSecond: float64(MaxOpenLoopInFlight + 1),
		RequestTimeoutMS: 1_000,
	}
	var called atomic.Int64
	_, err := Run(context.Background(), profile, func(context.Context, Request) Observation {
		called.Add(1)
		return Observation{}
	}, Options{})
	if err == nil {
		t.Fatal("unsafe open-loop worst-case in-flight estimate was accepted")
	}
	if called.Load() != 0 {
		t.Fatalf("executor calls = %d, want zero after admission rejection", called.Load())
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
