package load

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	errExecutorRequired = errors.New("load executor is required")
	errRequestLimit     = errors.New("load request limit exceeded")
)

const maxLinearRampSteps uint64 = 10

type runState struct {
	progress      Progress
	results       []Observation
	startedAt     time.Time
	sendEndedAt   time.Time
	lastIndex     uint64
	requestCap    uint64
	countLimited  bool
	interval      time.Duration
	schedule      []time.Duration
	arrival       ArrivalPattern
	sendWindow    time.Duration
	timeout       time.Duration
	sendingDone   bool
	cancelled     bool
	capacityErr   error
	openLoopLimit uint64
	ramp          bool
	rampSteps     uint64
}

// LinearRampStepCount reports the staircase shape used by Run for a ramped
// profile. Unsupported modes do not have a ramp shape.
func LinearRampStepCount(profile domain.LoadProfile) uint32 {
	switch profile.Mode {
	case domain.LoadFixedConcurrency:
		return min(profile.Concurrency, uint32(maxLinearRampSteps))
	case domain.LoadOpenLoop:
		return uint32(maxLinearRampSteps)
	default:
		return 0
	}
}

// EstimateLinearRampRequestCap returns the deterministic schedule size for a
// constant-arrival ramp and bounded headroom for a seeded Poisson ramp.
func EstimateLinearRampRequestCap(rate float64, duration time.Duration, arrival ArrivalPattern) (uint64, error) {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0, errors.New("linear ramp rate must be finite and positive")
	}
	if duration <= 0 {
		return 0, errors.New("linear ramp duration must be positive")
	}
	if arrival == "" {
		arrival = ArrivalConstant
	}
	if arrival != ArrivalConstant && arrival != ArrivalPoisson {
		return 0, fmt.Errorf("unsupported arrival pattern %q", arrival)
	}

	intensity := linearRampIntensity(rate, duration, maxLinearRampSteps)
	if math.IsNaN(intensity) || math.IsInf(intensity, 0) || intensity < 0 {
		return 0, errors.New("linear ramp request estimate overflow")
	}
	estimate := intensity
	if arrival == ArrivalPoisson {
		estimate *= 2
	}
	estimate = math.Ceil(estimate)
	if arrival == ArrivalPoisson {
		estimate++
	}
	if math.IsNaN(estimate) || math.IsInf(estimate, 0) || estimate >= math.Ldexp(1, 64) {
		return 0, errors.New("linear ramp request estimate overflow")
	}
	return uint64(estimate), nil
}

// EstimateOpenLoopRampSchedule returns the exact seeded schedule size Run
// would materialize for a duration-only open-loop ramp and reports whether
// maxScheduled truncates that schedule.
func EstimateOpenLoopRampSchedule(
	rate float64,
	duration time.Duration,
	arrival ArrivalPattern,
	seed uint32,
	maxScheduled uint64,
) (uint64, bool, error) {
	if maxScheduled == 0 || maxScheduled > MaxRequests {
		return 0, false, fmt.Errorf("open-loop ramp schedule limit must be between 1 and %d", MaxRequests)
	}
	if arrival == "" {
		arrival = ArrivalConstant
	}
	if arrival != ArrivalConstant && arrival != ArrivalPoisson {
		return 0, false, fmt.Errorf("unsupported arrival pattern %q", arrival)
	}
	if _, err := openLoopInterval(rate); err != nil {
		return 0, false, err
	}
	schedule, capped, err := buildLinearRampSchedule(rate, seed, arrival, duration, maxScheduled)
	if err != nil {
		return 0, false, err
	}
	return uint64(len(schedule)), capped, nil
}

// EstimateOpenLoopDurationSchedule returns the number of requests Run plans
// for a duration-only open-loop profile and whether maxScheduled truncates the
// schedule. Constant arrivals use the scheduler's interval-first ceiling;
// Poisson arrivals use the supplied seed's exact schedule.
func EstimateOpenLoopDurationSchedule(
	rate float64,
	duration time.Duration,
	arrival ArrivalPattern,
	seed uint32,
	maxScheduled uint64,
) (uint64, bool, error) {
	plan, err := buildOpenLoopDurationPlan(rate, duration, arrival, seed, maxScheduled)
	if err != nil {
		return 0, false, err
	}
	return plan.planned, plan.capped, nil
}

type openLoopDurationPlan struct {
	interval time.Duration
	schedule []time.Duration
	planned  uint64
	capped   bool
}

func buildOpenLoopDurationPlan(
	rate float64,
	duration time.Duration,
	arrival ArrivalPattern,
	seed uint32,
	maxScheduled uint64,
) (openLoopDurationPlan, error) {
	if duration <= 0 {
		return openLoopDurationPlan{}, errors.New("open-loop duration schedule requires a positive duration")
	}
	if maxScheduled == 0 || maxScheduled > MaxRequests {
		return openLoopDurationPlan{}, fmt.Errorf("open-loop duration schedule limit must be between 1 and %d", MaxRequests)
	}
	if arrival == "" {
		arrival = ArrivalConstant
	}
	if arrival != ArrivalConstant && arrival != ArrivalPoisson {
		return openLoopDurationPlan{}, fmt.Errorf("unsupported arrival pattern %q", arrival)
	}
	interval, err := openLoopInterval(rate)
	if err != nil {
		return openLoopDurationPlan{}, err
	}

	plan := openLoopDurationPlan{interval: interval}
	if arrival == ArrivalPoisson {
		plan.schedule, plan.capped, err = buildPoissonSchedule(rate, seed, 0, duration, false, maxScheduled)
		if err != nil {
			return openLoopDurationPlan{}, err
		}
		plan.planned = uint64(len(plan.schedule))
		return plan, nil
	}

	intervalFloat := float64(time.Second) / rate
	plannedFloat := math.Ceil(float64(duration) / intervalFloat)
	if math.IsNaN(plannedFloat) || math.IsInf(plannedFloat, 0) || plannedFloat < 1 || plannedFloat >= math.Ldexp(1, 64) {
		return openLoopDurationPlan{}, errors.New("open-loop duration schedule estimate overflow")
	}
	planned := uint64(plannedFloat)
	if planned > maxScheduled {
		plan.planned = maxScheduled
		plan.capped = true
	} else {
		plan.planned = planned
	}
	return plan, nil
}

func openLoopInterval(rate float64) (time.Duration, error) {
	intervalFloat := float64(time.Second) / rate
	if math.IsNaN(intervalFloat) || math.IsInf(intervalFloat, 0) || intervalFloat < 1 || intervalFloat > float64(math.MaxInt64) {
		return 0, errors.New("open-loop rate cannot be represented with nanosecond precision")
	}
	interval := time.Duration(intervalFloat)
	if interval <= 0 {
		return 0, errors.New("open-loop rate cannot be represented with nanosecond precision")
	}
	return interval, nil
}

func linearRampIntensity(rate float64, duration time.Duration, steps uint64) float64 {
	intensity := 0.0
	for step := uint64(0); step < steps; step++ {
		start := linearRampBoundary(duration, steps, step)
		end := linearRampBoundary(duration, steps, step+1)
		stepRate := rate * float64(step+1) / float64(steps)
		intensity += stepRate * float64(end-start) / float64(time.Second)
	}
	return intensity
}

func Run(ctx context.Context, profile domain.LoadProfile, executor Executor, options Options) (Outcome, error) {
	if ctx == nil {
		return Outcome{}, errors.New("load context is required")
	}
	if executor == nil {
		return Outcome{}, errExecutorRequired
	}
	state, err := newRunState(profile, options)
	if err != nil {
		return Outcome{}, err
	}

	runContext, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	completionBuffer := uint64(profile.Concurrency)
	if profile.Mode == domain.LoadOpenLoop {
		completionBuffer = state.openLoopLimit
	}
	completed := make(chan Observation, int(completionBuffer))
	state.startedAt = time.Now()
	state.progress.Phase = PhaseSending
	emitProgress(options.OnProgress, state.progress)

	stop := options.StopSending
	callerDone := ctx.Done()
	launch := func(index uint64, scheduledOffset time.Duration) {
		state.progress.Offered++
		state.progress.Launched++
		state.progress.InFlight++
		if state.progress.InFlight > state.progress.PeakInFlight {
			state.progress.PeakInFlight = state.progress.InFlight
		}
		emitProgress(options.OnProgress, state.progress)
		request := Request{Index: index, ScheduledOffset: scheduledOffset}
		go executeOne(runContext, state.startedAt, state.timeout, executor, request, completed)
	}

	finishSending := func(stopped bool, cancelled bool) {
		if state.sendingDone {
			if cancelled {
				state.cancelled = true
				state.progress.Stopped = true
			}
			return
		}
		state.sendingDone = true
		state.progress.Stopped = stopped
		state.cancelled = cancelled
		state.sendEndedAt = time.Now()
		state.progress.SendDuration = state.sendEndedAt.Sub(state.startedAt)
		state.progress.Phase = PhaseDraining
		emitProgress(options.OnProgress, state.progress)
	}

	checkSignals := func() {
		if callerDone != nil {
			select {
			case <-callerDone:
				finishSending(true, true)
				cancelRequests()
				callerDone = nil
			default:
			}
		}
		if stop != nil {
			select {
			case <-stop:
				finishSending(true, false)
				stop = nil
			default:
			}
		}
	}

	appendResult := func(observation Observation) {
		state.results = append(state.results, observation)
		state.progress.Completed++
		if observation.Success {
			state.progress.Succeeded++
		} else {
			state.progress.Failed++
		}
		emitProgress(options.OnProgress, state.progress)
	}
	record := func(observation Observation) {
		state.progress.InFlight--
		appendResult(observation)
	}
	reject := func(index uint64, scheduledOffset time.Duration) {
		now := time.Now()
		offset := now.Sub(state.startedAt)
		state.progress.Offered++
		state.progress.Rejected++
		appendResult(Observation{
			Index: index, ScheduledOffset: scheduledOffset,
			StartedOffset: offset, FinishedOffset: offset,
			ScheduleLag: offset - scheduledOffset,
			ErrorCode:   ErrorSchedulerOverload,
		})
	}

	switch profile.Mode {
	case domain.LoadSingle, domain.LoadFixedConcurrency:
		runBounded(profile, state, launch, record, finishSending, checkSignals, completed, &stop, &callerDone)
	case domain.LoadOpenLoop:
		runOpenLoop(state, launch, record, reject, finishSending, checkSignals, completed, &stop, &callerDone)
	default:
		return Outcome{}, fmt.Errorf("unsupported load mode %q", profile.Mode)
	}

	finishedAt := time.Now()
	if state.sendEndedAt.IsZero() {
		state.sendEndedAt = finishedAt
		state.progress.SendDuration = state.sendEndedAt.Sub(state.startedAt)
	}
	state.progress.DrainDuration = finishedAt.Sub(state.sendEndedAt)
	state.progress.TotalDuration = finishedAt.Sub(state.startedAt)
	state.progress = normalizeObservedProgressDurations(state.progress)
	if state.cancelled {
		state.progress.Phase = PhaseCancelled
	} else {
		state.progress.Phase = PhaseCompleted
	}
	sort.Slice(state.results, func(left, right int) bool { return state.results[left].Index < state.results[right].Index })
	emitProgress(options.OnProgress, state.progress)
	outcome := Outcome{
		Progress: state.progress,
		Results:  append([]Observation(nil), state.results...),
		Metrics:  ComputeMetricsWithArrival(state.results, state.progress, profile, state.arrival),
	}
	if state.cancelled {
		return outcome, ctx.Err()
	}
	if state.capacityErr != nil {
		return outcome, state.capacityErr
	}
	return outcome, nil
}

func normalizeObservedProgressDurations(progress Progress) Progress {
	if progress.Offered == 0 && progress.Completed == 0 {
		return progress
	}
	if progress.SendDuration <= 0 {
		progress.SendDuration = time.Nanosecond
	}
	if progress.TotalDuration < progress.SendDuration {
		progress.TotalDuration = progress.SendDuration
	}
	progress.DrainDuration = progress.TotalDuration - progress.SendDuration
	return progress
}

func newRunState(profile domain.LoadProfile, options Options) (*runState, error) {
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("invalid load profile: %w", err)
	}
	if profile.Concurrency > MaxConcurrency {
		return nil, fmt.Errorf("load concurrency exceeds %d", MaxConcurrency)
	}
	if profile.RequestCount > MaxRequests {
		return nil, fmt.Errorf("load request count exceeds %d", MaxRequests)
	}
	if profile.Mode == domain.LoadSingle && (profile.RequestCount != 1 || profile.DurationMS != 0 || profile.Concurrency != 1) {
		return nil, errors.New("single load requires exactly one request, concurrency one, and no send duration")
	}
	if options.Ramp {
		if profile.Mode != domain.LoadFixedConcurrency && profile.Mode != domain.LoadOpenLoop {
			return nil, errors.New("ramp requires fixed-concurrency or open-loop load")
		}
		if profile.DurationMS == 0 {
			return nil, errors.New("ramp requires a positive load duration")
		}
		if profile.Mode == domain.LoadOpenLoop && profile.RequestCount > 0 {
			return nil, errors.New("open-loop ramp requires a duration-only profile")
		}
	}
	arrival := options.ArrivalPattern
	if arrival == "" {
		arrival = ArrivalConstant
	}
	if arrival != ArrivalConstant && arrival != ArrivalPoisson {
		return nil, fmt.Errorf("unsupported arrival pattern %q", arrival)
	}
	if profile.Mode != domain.LoadOpenLoop && arrival != ArrivalConstant {
		return nil, errors.New("non-constant arrivals require open-loop load")
	}
	maxScheduledRequests := uint64(MaxRequests)
	if options.MaxScheduledRequests > MaxRequests {
		return nil, fmt.Errorf("scheduled request limit cannot exceed %d", MaxRequests)
	}
	if options.MaxScheduledRequests > 0 {
		maxScheduledRequests = options.MaxScheduledRequests
	}
	if profile.RequestCount > maxScheduledRequests {
		return nil, fmt.Errorf("load request count exceeds scheduled limit %d", maxScheduledRequests)
	}
	timeout, err := durationFromMilliseconds(profile.RequestTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("invalid request timeout: %w", err)
	}
	sendWindow, err := durationFromMilliseconds(profile.DurationMS)
	if err != nil {
		return nil, fmt.Errorf("invalid load duration: %w", err)
	}
	state := &runState{
		progress:     Progress{Planned: profile.RequestCount},
		requestCap:   profile.RequestCount,
		countLimited: profile.RequestCount > 0,
		sendWindow:   sendWindow,
		timeout:      timeout,
		arrival:      arrival,
		ramp:         options.Ramp,
	}
	if options.Ramp && profile.Mode == domain.LoadFixedConcurrency {
		state.rampSteps = uint64(LinearRampStepCount(profile))
	}
	if profile.Mode == domain.LoadFixedConcurrency && !state.countLimited {
		state.requestCap = maxScheduledRequests
		state.progress.Planned = state.requestCap
	}
	if profile.Mode == domain.LoadOpenLoop {
		state.openLoopLimit = MaxOpenLoopInFlight
		if options.MaxOpenLoopInFlight > MaxOpenLoopInFlight {
			return nil, fmt.Errorf("open-loop in-flight limit cannot exceed %d", MaxOpenLoopInFlight)
		}
		if options.MaxOpenLoopInFlight > 0 {
			state.openLoopLimit = options.MaxOpenLoopInFlight
		}
		interval, err := openLoopInterval(profile.RatePerSecond)
		if err != nil {
			return nil, err
		}
		state.interval = interval
		if options.Ramp {
			schedule, capped, err := buildLinearRampSchedule(
				profile.RatePerSecond,
				options.RandomSeed,
				arrival,
				sendWindow,
				maxScheduledRequests,
			)
			if err != nil {
				return nil, err
			}
			state.schedule = schedule
			state.requestCap = uint64(len(schedule))
			state.progress.Planned = state.requestCap
			state.progress.Capped = capped
		} else if !state.countLimited {
			plan, err := buildOpenLoopDurationPlan(
				profile.RatePerSecond,
				sendWindow,
				arrival,
				options.RandomSeed,
				maxScheduledRequests,
			)
			if err != nil {
				return nil, err
			}
			state.interval = plan.interval
			state.schedule = plan.schedule
			state.requestCap = plan.planned
			state.progress.Planned = plan.planned
			state.progress.Capped = plan.capped
		} else if arrival == ArrivalPoisson {
			schedule, capped, err := buildPoissonSchedule(profile.RatePerSecond, options.RandomSeed, state.requestCap, sendWindow, state.countLimited, maxScheduledRequests)
			if err != nil {
				return nil, err
			}
			state.schedule = schedule
			state.progress.Capped = capped
			if !state.countLimited {
				state.requestCap = uint64(len(schedule))
				state.progress.Planned = state.requestCap
			}
		} else {
			if state.requestCap > 1 && uint64(state.interval) > uint64(math.MaxInt64)/(state.requestCap-1) {
				return nil, errors.New("open-loop schedule exceeds time.Duration range")
			}
		}
	}
	return state, nil
}

func (state *runState) scheduledOffset(index uint64) time.Duration {
	if state.schedule != nil {
		if index < uint64(len(state.schedule)) {
			return state.schedule[index]
		}
		return state.sendWindow
	}
	return time.Duration(index) * state.interval
}

func buildLinearRampSchedule(
	rate float64,
	seed uint32,
	arrival ArrivalPattern,
	sendWindow time.Duration,
	maxScheduled uint64,
) ([]time.Duration, bool, error) {
	if sendWindow <= 0 {
		return nil, false, errors.New("linear ramp schedule requires a positive duration")
	}
	if maxScheduled == 0 {
		return nil, false, errors.New("linear ramp schedule requires a positive request limit")
	}
	steps := maxLinearRampSteps
	totalIntensity := linearRampIntensity(rate, sendWindow, steps)
	if math.IsNaN(totalIntensity) || math.IsInf(totalIntensity, 0) || totalIntensity <= 0 {
		return nil, false, errors.New("linear ramp intensity must be finite and positive")
	}

	schedule := make([]time.Duration, 0, min(maxScheduled, 1_024))
	hazard := 0.0
	randomState := uint64(seed) ^ 0xe7037ed1a0b428db
	for {
		offset, inWindow, err := linearRampOffsetForHazard(rate, sendWindow, steps, totalIntensity, hazard)
		if err != nil {
			return nil, false, err
		}
		if !inWindow {
			return schedule, false, nil
		}
		if uint64(len(schedule)) >= maxScheduled {
			return schedule, true, nil
		}
		if len(schedule) > 0 && offset <= schedule[len(schedule)-1] {
			offset = schedule[len(schedule)-1] + 1
			if offset >= sendWindow {
				return schedule, false, nil
			}
		}
		schedule = append(schedule, offset)

		switch arrival {
		case ArrivalConstant:
			hazard++
		case ArrivalPoisson:
			randomState += 0x9e3779b97f4a7c15
			mixed := randomState
			mixed = (mixed ^ (mixed >> 30)) * 0xbf58476d1ce4e5b9
			mixed = (mixed ^ (mixed >> 27)) * 0x94d049bb133111eb
			mixed ^= mixed >> 31
			uniform := (float64(mixed>>11) + 0.5) / (1 << 53)
			hazard += -math.Log1p(-uniform)
		default:
			return nil, false, fmt.Errorf("unsupported arrival pattern %q", arrival)
		}
	}
}

func linearRampOffsetForHazard(
	rate float64,
	duration time.Duration,
	steps uint64,
	totalIntensity float64,
	hazard float64,
) (time.Duration, bool, error) {
	if math.IsNaN(hazard) || math.IsInf(hazard, 0) || hazard < 0 {
		return 0, false, errors.New("linear ramp hazard must be finite and non-negative")
	}
	if hazard >= totalIntensity {
		return 0, false, nil
	}
	cumulative := 0.0
	for step := uint64(0); step < steps; step++ {
		start := linearRampBoundary(duration, steps, step)
		end := linearRampBoundary(duration, steps, step+1)
		stepRate := rate * float64(step+1) / float64(steps)
		stepIntensity := stepRate * float64(end-start) / float64(time.Second)
		nextCumulative := cumulative + stepIntensity
		if hazard < nextCumulative {
			offsetFloat := float64(start) + (hazard-cumulative)*float64(time.Second)/stepRate
			if math.IsNaN(offsetFloat) || math.IsInf(offsetFloat, 0) || offsetFloat < 0 || offsetFloat > float64(math.MaxInt64) {
				return 0, false, errors.New("linear ramp schedule exceeds time.Duration range")
			}
			offset := time.Duration(math.Round(offsetFloat))
			return offset, offset < duration, nil
		}
		cumulative = nextCumulative
	}
	return 0, false, nil
}

// buildPoissonSchedule uses SplitMix64 and inverse-transform sampling rather
// than math/rand, keeping the seed-to-schedule mapping stable across Go
// releases. The first request starts at zero; each later gap is exponentially
// distributed with the configured mean rate and rounded to at least 1ns.
func buildPoissonSchedule(rate float64, seed uint32, requestCap uint64, sendWindow time.Duration, countLimited bool, maxScheduled uint64) ([]time.Duration, bool, error) {
	limit := requestCap
	if !countLimited {
		limit = maxScheduled
	}
	if limit == 0 {
		return nil, false, errors.New("poisson schedule requires a positive request limit")
	}
	schedule := make([]time.Duration, 1, min(limit, 1_024))
	schedule[0] = 0
	state := uint64(seed) ^ 0xa0761d6478bd642f
	for uint64(len(schedule)) < limit {
		previous := schedule[len(schedule)-1]
		next, err := nextPoissonOffset(previous, rate, &state)
		if err != nil {
			return nil, false, err
		}
		if sendWindow > 0 && next >= sendWindow {
			if countLimited {
				schedule = append(schedule, next)
			}
			break
		}
		schedule = append(schedule, next)
	}
	if !countLimited && uint64(len(schedule)) == limit {
		next, err := nextPoissonOffset(schedule[len(schedule)-1], rate, &state)
		if err != nil {
			return nil, false, err
		}
		return schedule, sendWindow <= 0 || next < sendWindow, nil
	}
	return schedule, false, nil
}

func nextPoissonOffset(previous time.Duration, rate float64, state *uint64) (time.Duration, error) {
	*state += 0x9e3779b97f4a7c15
	mixed := *state
	mixed = (mixed ^ (mixed >> 30)) * 0xbf58476d1ce4e5b9
	mixed = (mixed ^ (mixed >> 27)) * 0x94d049bb133111eb
	mixed ^= mixed >> 31
	uniform := (float64(mixed>>11) + 0.5) / (1 << 53)
	gapFloat := -math.Log1p(-uniform) * float64(time.Second) / rate
	if math.IsNaN(gapFloat) || math.IsInf(gapFloat, 0) || gapFloat > float64(math.MaxInt64) {
		return 0, errors.New("poisson schedule exceeds time.Duration range")
	}
	gap := time.Duration(math.Round(gapFloat))
	if gap < 1 {
		gap = 1
	}
	if gap > time.Duration(math.MaxInt64)-previous {
		return 0, errors.New("poisson schedule exceeds time.Duration range")
	}
	return previous + gap, nil
}

func runBounded(
	profile domain.LoadProfile,
	state *runState,
	launch func(uint64, time.Duration),
	record func(Observation),
	finishSending func(bool, bool),
	checkSignals func(),
	completed <-chan Observation,
	stop *<-chan struct{},
	callerDone *<-chan struct{},
) {
	for {
		if !state.sendingDone && state.requestCap > 0 && state.lastIndex >= state.requestCap {
			if (state.ramp || !state.countLimited) && state.sendWindow > 0 && time.Since(state.startedAt) < state.sendWindow {
				state.progress.Capped = true
			}
			finishSending(false, false)
		}
		if !state.sendingDone && state.sendWindow > 0 && time.Since(state.startedAt) >= state.sendWindow {
			finishSending(false, false)
		}
		checkSignals()
		limit := uint64(profile.Concurrency)
		if state.ramp {
			limit, _ = fixedRampLimit(profile.Concurrency, state.rampSteps, state.sendWindow, time.Since(state.startedAt))
		}
		for !state.sendingDone && state.progress.InFlight < limit {
			if state.requestCap > 0 && state.lastIndex >= state.requestCap {
				if (state.ramp || !state.countLimited) && state.sendWindow > 0 && time.Since(state.startedAt) < state.sendWindow {
					state.progress.Capped = true
				}
				finishSending(false, false)
				break
			}
			if state.sendWindow > 0 && time.Since(state.startedAt) >= state.sendWindow {
				finishSending(false, false)
				break
			}
			checkSignals()
			if state.sendingDone {
				break
			}
			if state.lastIndex >= MaxRequests {
				state.capacityErr = errRequestLimit
				finishSending(true, false)
				break
			}
			index := state.lastIndex
			state.lastIndex++
			launch(index, 0)
		}
		if !state.sendingDone && state.requestCap > 0 && state.lastIndex >= state.requestCap {
			if (state.ramp || !state.countLimited) && state.sendWindow > 0 && time.Since(state.startedAt) < state.sendWindow {
				state.progress.Capped = true
			}
			finishSending(false, false)
		}
		if state.sendingDone && state.progress.InFlight == 0 {
			return
		}

		var deadline <-chan time.Time
		var timer *time.Timer
		if !state.sendingDone && state.sendWindow > 0 {
			wakeOffset := state.sendWindow
			if state.ramp {
				_, wakeOffset = fixedRampLimit(profile.Concurrency, state.rampSteps, state.sendWindow, time.Since(state.startedAt))
			}
			remaining := time.Until(state.startedAt.Add(wakeOffset))
			if remaining <= 0 {
				continue
			}
			timer = time.NewTimer(remaining)
			deadline = timer.C
		}
		select {
		case observation := <-completed:
			stopTimer(timer)
			record(observation)
		case <-*stop:
			stopTimer(timer)
			finishSending(true, false)
			*stop = nil
		case <-*callerDone:
			stopTimer(timer)
			finishSending(true, true)
			*callerDone = nil
		case <-deadline:
			// A ramp boundary is a scheduler wake-up, not a drain boundary. The
			// next loop recomputes the active concurrency level.
		}
	}
}

func fixedRampLimit(target uint32, steps uint64, duration time.Duration, elapsed time.Duration) (uint64, time.Duration) {
	if target <= 1 || steps <= 1 {
		return uint64(target), duration
	}
	step := uint64(0)
	for step+1 < steps && elapsed >= linearRampBoundary(duration, steps, step+1) {
		step++
	}
	level := uint64(1) + (uint64(target)-1)*step/(steps-1)
	nextBoundary := duration
	if step+1 < steps {
		nextBoundary = linearRampBoundary(duration, steps, step+1)
	}
	return level, nextBoundary
}

func linearRampBoundary(duration time.Duration, steps uint64, index uint64) time.Duration {
	if steps == 0 || index >= steps {
		return duration
	}
	quotient := duration / time.Duration(steps)
	remainder := duration % time.Duration(steps)
	extra := time.Duration(index)
	if extra > remainder {
		extra = remainder
	}
	return quotient*time.Duration(index) + extra
}

func runOpenLoop(
	state *runState,
	launch func(uint64, time.Duration),
	record func(Observation),
	reject func(uint64, time.Duration),
	finishSending func(bool, bool),
	checkSignals func(),
	completed <-chan Observation,
	stop *<-chan struct{},
	callerDone *<-chan struct{},
) {
	for {
		if !state.sendingDone {
			if state.countLimited && state.lastIndex >= state.requestCap {
				finishSending(false, false)
			} else if state.sendWindow > 0 && time.Since(state.startedAt) >= state.sendWindow {
				// A ramp schedule is materialized for the whole window. If the timer
				// wakes late, offer every already-due offset before draining so the
				// same seed always produces the same schedule.
				if !state.ramp || state.lastIndex >= state.requestCap {
					finishSending(false, false)
				}
			}
		}
		checkSignals()
		if !state.sendingDone && state.lastIndex < state.requestCap {
			scheduled := state.scheduledOffset(state.lastIndex)
			if wait := time.Until(state.startedAt.Add(scheduled)); wait <= 0 {
				for {
					select {
					case observation := <-completed:
						record(observation)
					default:
						goto completionsDrained
					}
				}
			completionsDrained:
				if state.progress.InFlight >= state.openLoopLimit {
					index := state.lastIndex
					state.lastIndex++
					reject(index, scheduled)
					continue
				}
				index := state.lastIndex
				state.lastIndex++
				launch(index, scheduled)
				continue
			}
		}
		if state.sendingDone && state.progress.InFlight == 0 {
			return
		}

		var scheduled <-chan time.Time
		var timer *time.Timer
		if !state.sendingDone {
			wakeAt := state.startedAt.Add(state.sendWindow)
			if state.lastIndex < state.requestCap {
				offset := state.scheduledOffset(state.lastIndex)
				wakeAt = state.startedAt.Add(offset)
				if state.sendWindow > 0 {
					windowEnd := state.startedAt.Add(state.sendWindow)
					if windowEnd.Before(wakeAt) {
						wakeAt = windowEnd
					}
				}
			}
			wait := time.Until(wakeAt)
			if wait < 0 {
				wait = 0
			}
			timer = time.NewTimer(wait)
			scheduled = timer.C
		}
		select {
		case observation := <-completed:
			stopTimer(timer)
			record(observation)
		case <-*stop:
			stopTimer(timer)
			finishSending(true, false)
			*stop = nil
		case <-*callerDone:
			stopTimer(timer)
			finishSending(true, true)
			*callerDone = nil
		case <-scheduled:
		}
	}
}

func executeOne(
	parent context.Context,
	runStarted time.Time,
	timeout time.Duration,
	executor Executor,
	request Request,
	completed chan<- Observation,
) {
	requestContext, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	observation := executeSafely(requestContext, executor, request)
	finished := time.Now()
	observation.Index = request.Index
	observation.ScheduledOffset = request.ScheduledOffset
	observation.StartedOffset = started.Sub(runStarted)
	observation.FinishedOffset = finished.Sub(runStarted)
	observation.ScheduleLag = observation.StartedOffset - request.ScheduledOffset
	if observation.ScheduleLag < 0 {
		observation.ScheduleLag = 0
	}
	if observation.E2E <= 0 {
		observation.E2E = finished.Sub(started)
	}
	observation = normalizeObservationTimings(observation)
	if observation.HTTPStatus < 0 || observation.HTTPStatus > 999 {
		observation.HTTPStatus = 0
	}
	if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
		observation.Success = false
		observation.TimedOut = true
		observation.ErrorCode = ErrorTimeout
	} else if errors.Is(parent.Err(), context.Canceled) && observation.ErrorCode == "" {
		observation.Success = false
		observation.ErrorCode = ErrorCancelled
	}
	if observation.TimedOut {
		observation.Success = false
		observation.ErrorCode = ErrorTimeout
	}
	if observation.Success {
		observation.ErrorCode = ""
	} else {
		observation.ErrorCode = normalizeErrorCode(observation.ErrorCode)
	}
	completed <- observation
}

func executeSafely(ctx context.Context, executor Executor, request Request) (observation Observation) {
	defer func() {
		if recover() != nil {
			observation = Observation{Index: request.Index, ErrorCode: ErrorExecutorPanic}
		}
	}()
	return executor(ctx, request)
}

func emitProgress(callback func(Progress), progress Progress) {
	if callback != nil {
		callback(progress)
	}
}

func normalizeErrorCode(code domain.ErrorCode) domain.ErrorCode {
	switch code {
	case ErrorNetwork, ErrorTimeout, ErrorCancelled, ErrorHTTP, ErrorRateLimited,
		ErrorProtocol, ErrorIncompleteStream, ErrorSemanticEmpty, ErrorResponseTooLarge,
		ErrorClientClosed, ErrorSchedulerOverload, ErrorExecutorPanic, ErrorRequestFailed,
		ErrorUnclassified:
		return code
	default:
		return ErrorUnclassified
	}
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func durationFromMilliseconds(value uint64) (time.Duration, error) {
	if value > uint64(math.MaxInt64)/uint64(time.Millisecond) {
		return 0, errors.New("milliseconds overflow time.Duration")
	}
	return time.Duration(value) * time.Millisecond, nil
}
