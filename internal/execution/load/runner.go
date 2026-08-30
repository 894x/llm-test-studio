package load

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/894x/llm-studio/internal/domain"
)

var (
	errExecutorRequired = errors.New("load executor is required")
	errRequestLimit     = errors.New("load request limit exceeded")
)

type runState struct {
	progress      Progress
	results       []Observation
	startedAt     time.Time
	sendEndedAt   time.Time
	lastIndex     uint64
	requestCap    uint64
	interval      time.Duration
	sendWindow    time.Duration
	timeout       time.Duration
	sendingDone   bool
	cancelled     bool
	capacityErr   error
	openLoopLimit uint64
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
		Metrics:  ComputeMetrics(state.results, state.progress.TotalDuration),
	}
	if state.cancelled {
		return outcome, ctx.Err()
	}
	if state.capacityErr != nil {
		return outcome, state.capacityErr
	}
	return outcome, nil
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
	timeout, err := durationFromMilliseconds(profile.RequestTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("invalid request timeout: %w", err)
	}
	sendWindow, err := durationFromMilliseconds(profile.DurationMS)
	if err != nil {
		return nil, fmt.Errorf("invalid load duration: %w", err)
	}
	state := &runState{
		progress:   Progress{Planned: profile.RequestCount},
		requestCap: profile.RequestCount,
		sendWindow: sendWindow,
		timeout:    timeout,
	}
	if profile.Mode == domain.LoadOpenLoop {
		state.openLoopLimit = MaxOpenLoopInFlight
		if options.MaxOpenLoopInFlight > MaxOpenLoopInFlight {
			return nil, fmt.Errorf("open-loop in-flight limit cannot exceed %d", MaxOpenLoopInFlight)
		}
		if options.MaxOpenLoopInFlight > 0 {
			state.openLoopLimit = options.MaxOpenLoopInFlight
		}
		intervalFloat := float64(time.Second) / profile.RatePerSecond
		if math.IsNaN(intervalFloat) || math.IsInf(intervalFloat, 0) || intervalFloat < 1 || intervalFloat > float64(math.MaxInt64) {
			return nil, errors.New("open-loop rate cannot be represented with nanosecond precision")
		}
		state.interval = time.Duration(intervalFloat)
		if state.requestCap == 0 {
			state.requestCap = uint64(math.Ceil(float64(sendWindow) / intervalFloat))
			if state.requestCap == 0 {
				state.requestCap = 1
			}
			if state.requestCap > MaxRequests {
				return nil, fmt.Errorf("open-loop schedule exceeds %d requests", MaxRequests)
			}
			state.progress.Planned = state.requestCap
		}
		if state.requestCap > 1 && uint64(state.interval) > uint64(math.MaxInt64)/(state.requestCap-1) {
			return nil, errors.New("open-loop schedule exceeds time.Duration range")
		}
		worstCaseInFlight := math.Ceil(profile.RatePerSecond * timeout.Seconds())
		if worstCaseInFlight < 1 {
			worstCaseInFlight = 1
		}
		if worstCaseInFlight > float64(state.requestCap) {
			worstCaseInFlight = float64(state.requestCap)
		}
		if worstCaseInFlight > float64(state.openLoopLimit) {
			return nil, fmt.Errorf("open-loop worst-case in-flight requests exceed safety limit %d", state.openLoopLimit)
		}
	}
	return state, nil
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
	limit := uint64(profile.Concurrency)
	for {
		if !state.sendingDone && state.requestCap > 0 && state.lastIndex >= state.requestCap {
			finishSending(false, false)
		}
		if !state.sendingDone && state.sendWindow > 0 && time.Since(state.startedAt) >= state.sendWindow {
			finishSending(false, false)
		}
		checkSignals()
		for !state.sendingDone && state.progress.InFlight < limit {
			if state.requestCap > 0 && state.lastIndex >= state.requestCap {
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
			finishSending(false, false)
		}
		if state.sendingDone && state.progress.InFlight == 0 {
			return
		}

		var deadline <-chan time.Time
		var timer *time.Timer
		if !state.sendingDone && state.sendWindow > 0 {
			remaining := time.Until(state.startedAt.Add(state.sendWindow))
			if remaining <= 0 {
				finishSending(false, false)
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
			finishSending(false, false)
		}
	}
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
			if state.lastIndex >= state.requestCap {
				finishSending(false, false)
			} else {
				scheduled := time.Duration(state.lastIndex) * state.interval
				if state.sendWindow > 0 && scheduled >= state.sendWindow {
					finishSending(false, false)
				}
			}
		}
		checkSignals()
		if !state.sendingDone {
			scheduled := time.Duration(state.lastIndex) * state.interval
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
			offset := time.Duration(state.lastIndex) * state.interval
			wait := time.Until(state.startedAt.Add(offset))
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
	if observation.TTFT < 0 {
		observation.TTFT = 0
	}
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
