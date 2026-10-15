package jobhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type RunRepository interface {
	runs.ExecutionRepository
	comparisons.ExecutionRepository
	reporting.ReportRepository
	ListReportsForRuns(context.Context, []string) ([]domain.Report, error)
}

type RunExecutorDependencies struct {
	Repository RunRepository
	Executor   runs.Executor
	Clock      Clock
}

// RunExecutor executes Run and Comparison jobs through the existing Core.
// Performance jobs have their own execution and observation component.
type RunExecutor struct {
	repository RunRepository
	host       *runs.Host
	reports    *reporting.Generator
	groups     *comparisons.Refresher
	clock      Clock
	mu         sync.Mutex
	active     map[string]*runGroup
	closed     bool
}

type runGroup struct {
	inputs         []runs.PreparedRun
	failed         chan struct{}
	done           chan struct{}
	unavailable    atomic.Bool
	failureOnce    sync.Once
	onFailure      func()
	requestContext context.Context
	stopRequests   context.CancelFunc
}

func NewRunExecutor(dependencies RunExecutorDependencies) (*RunExecutor, error) {
	invalid := nilDependency(dependencies.Repository) || nilDependency(dependencies.Executor) ||
		nilDependency(dependencies.Clock)
	if invalid {
		return nil, ErrInvalid
	}
	core := &RunExecutor{
		repository: dependencies.Repository, clock: dependencies.Clock,
		active: map[string]*runGroup{},
	}
	host, err := runs.NewHost(runs.HostDependencies{
		Repository: dependencies.Repository, Executor: dependencies.Executor, Clock: dependencies.Clock,
		PersistenceFailure: core.observeFailure,
	})
	if err != nil {
		return nil, err
	}
	reports, err := reporting.NewGenerator(reporting.GeneratorDependencies{
		Repository: dependencies.Repository, Clock: dependencies.Clock,
	})
	if err != nil {
		_ = host.Close()
		return nil, err
	}
	groups, err := comparisons.NewRefresher(comparisons.RefresherDependencies{
		Repository: dependencies.Repository, Runs: dependencies.Repository, Clock: dependencies.Clock,
	})
	if err != nil {
		_ = host.Close()
		return nil, err
	}
	core.host, core.reports, core.groups = host, reports, groups
	return core, nil
}

func (core *RunExecutor) observeFailure(runID string) {
	core.mu.Lock()
	group := core.active[runID]
	core.mu.Unlock()
	if group == nil {
		return
	}
	group.unavailable.Store(true)
	group.failureOnce.Do(func() {
		if group.onFailure != nil {
			group.onFailure()
		}
		// A storage outage must also stop activations already past the gate.
		// User cancellation and deadlines still persist each owner's reason first.
		group.stopRequests()
	})
	select {
	case group.failed <- struct{}{}:
	default:
	}
}

func runInputs(spec jobcontract.JobSpec) ([]runs.PreparedRun, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	switch spec.Kind {
	case jobcontract.KindRun:
		return []runs.PreparedRun{*spec.Spec.Run}, nil
	case jobcontract.KindComparison:
		return spec.Spec.Comparison.Runs, nil
	default:
		return nil, ErrInvalid
	}
}

func (core *RunExecutor) register(group *runGroup) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.closed {
		return ErrUnavailable
	}
	for _, input := range group.inputs {
		if core.active[input.Meta.ID] != nil {
			return ErrConflict
		}
	}
	for _, input := range group.inputs {
		core.active[input.Meta.ID] = group
	}
	return nil
}

func (core *RunExecutor) unregister(inputs []runs.PreparedRun) {
	core.mu.Lock()
	defer core.mu.Unlock()
	for _, input := range inputs {
		delete(core.active, input.Meta.ID)
	}
}

func (core *RunExecutor) Execute(ctx context.Context, execution Execution) (Outcome, error) {
	if core == nil || ctx == nil {
		return Outcome{}, ErrInvalid
	}
	inputs, err := runInputs(execution.Spec)
	if err != nil || len(execution.Secrets) != len(inputs) {
		return Outcome{}, ErrInvalid
	}
	for _, input := range inputs {
		if execution.Secrets[input.Meta.ID] == nil {
			return Outcome{}, ErrInvalid
		}
	}
	if ctx.Err() != nil {
		return reasonOutcome(context.Cause(ctx)), nil
	}
	requestContext, stopRequests := context.WithCancel(context.Background())
	defer stopRequests()
	group := &runGroup{
		inputs: inputs, failed: make(chan struct{}, 1), done: make(chan struct{}),
		onFailure: execution.OnPersistenceFailure, requestContext: requestContext, stopRequests: stopRequests,
	}
	if err := core.register(group); err != nil {
		return Outcome{}, err
	}
	defer core.unregister(inputs)
	adopted := make([]runs.PreparedRun, 0, len(inputs))
	for _, input := range inputs {
		if ctx.Err() != nil {
			outcome := reasonOutcome(context.Cause(ctx))
			stopErr := core.terminate(adopted, outcome)
			_, joinErr := core.join(adopted)
			return outcome, errors.Join(stopErr, joinErr)
		}
		storeContext, cancel := context.WithTimeout(context.Background(), storageTimeout)
		err := core.host.AdoptQueued(storeContext, input.Meta.ID, execution.Secrets[input.Meta.ID])
		cancel()
		if err != nil {
			_ = core.terminate(adopted, failed("job_storage_unavailable"))
			_, joinErr := core.join(adopted)
			return Outcome{}, errors.Join(err, joinErr)
		}
		adopted = append(adopted, input)
	}
	stop := execution.StopSending
	select {
	case <-stop:
		if err := core.stop(inputs); err != nil {
			_ = core.terminate(inputs, failed("job_storage_unavailable"))
			_, joinErr := core.join(inputs)
			return Outcome{}, errors.Join(err, joinErr)
		}
		stop = nil
	default:
	}
	watched := make(chan error, 1)
	go func() { watched <- core.watch(ctx, stop, group) }()
	var startErr error
	for _, input := range inputs {
		if ctx.Err() != nil || group.unavailable.Load() {
			break
		}
		// The owner commits a cancellation/failure before cancelling requests.
		// Passing ctx here could let Core record a different failure first.
		err := core.host.ActivateRun(group.requestContext, input.Meta.ID)
		if err != nil && !errors.Is(err, runs.ErrNotActive) {
			outcome := failed("job_storage_unavailable")
			if ctx.Err() != nil {
				outcome = reasonOutcome(context.Cause(ctx))
			}
			startErr = errors.Join(err, core.terminate(inputs, outcome))
			break
		}
	}
	children, joinErr := core.join(inputs)
	close(group.done)
	watchErr := <-watched
	if group.unavailable.Load() {
		watchErr = errors.Join(watchErr, ErrUnavailable)
	}
	if err := errors.Join(startErr, joinErr, watchErr); err != nil {
		return Outcome{}, err
	}
	if ctx.Err() != nil {
		return reasonOutcome(context.Cause(ctx)), nil
	}
	for _, child := range children {
		failure := child.Failure()
		if failure == nil {
			continue
		}
		storageFailure := failure.ErrorCode == "result_persistence_failed" ||
			failure.ErrorCode == "run_state_transition_failed"
		if storageFailure {
			return Outcome{}, ErrUnavailable
		}
	}
	if execution.Spec.Kind == jobcontract.KindRun {
		return runOutcome(children[0]), nil
	}
	storeContext, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	comparison, err := core.groups.Refresh(storeContext, execution.Spec.Spec.Comparison.Meta.ID)
	if err != nil {
		return Outcome{}, err
	}
	switch comparison.Status() {
	case domain.ComparisonCompleted:
		return Outcome{Status: domain.RunCompleted}, nil
	case domain.ComparisonCancelled:
		return Outcome{Status: domain.RunCancelled}, nil
	case domain.ComparisonFailed:
		for _, child := range children {
			if child.Status() == domain.RunFailed {
				return runOutcome(child), nil
			}
		}
	}
	return Outcome{}, ErrUnavailable
}

func runOutcome(run domain.Run) Outcome {
	return Outcome{Status: run.Status(), Failure: run.Failure()}
}

func (core *RunExecutor) watch(
	ctx context.Context,
	stop <-chan struct{},
	group *runGroup,
) error {
	for {
		select {
		case <-group.done:
			return nil
		case <-ctx.Done():
			return core.terminate(group.inputs, reasonOutcome(context.Cause(ctx)))
		case <-group.failed:
			return errors.Join(ErrUnavailable, core.terminate(group.inputs, failed("job_storage_unavailable")))
		case <-stop:
			stop = nil
			if err := core.stop(group.inputs); err != nil {
				return errors.Join(err, core.terminate(group.inputs, failed("job_storage_unavailable")))
			}
		}
	}
}

func (core *RunExecutor) stop(inputs []runs.PreparedRun) error {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	for _, input := range inputs {
		err := core.host.StopSending(ctx, input.Meta.ID)
		if err != nil && !errors.Is(err, runs.ErrNotActive) {
			return err
		}
	}
	return nil
}

func (core *RunExecutor) terminate(inputs []runs.PreparedRun, outcome Outcome) error {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	errorsFound := []error{}
	for _, input := range inputs {
		var err error
		if outcome.Status == domain.RunFailed {
			err = core.host.FailRun(ctx, input.Meta.ID, *outcome.Failure)
		} else {
			err = core.host.CancelRun(ctx, input.Meta.ID)
		}
		if err != nil && !errors.Is(err, runs.ErrNotActive) {
			errorsFound = append(errorsFound, err)
		}
	}
	return errors.Join(errorsFound...)
}

func (core *RunExecutor) join(inputs []runs.PreparedRun) ([]domain.Run, error) {
	children := make([]domain.Run, 0, len(inputs))
	errorsFound := []error{}
	for _, input := range inputs {
		// This joins owned workers even when the observing Job context has ended.
		child, err := core.host.WaitRun(context.Background(), input.Meta.ID)
		if err != nil {
			errorsFound = append(errorsFound, err)
			continue
		}
		children = append(children, child)
	}
	return children, errors.Join(errorsFound...)
}

func (core *RunExecutor) Settle(ctx context.Context, spec jobcontract.JobSpec, outcome Outcome) error {
	if core == nil || ctx == nil || outcome.Validate() != nil {
		return ErrInvalid
	}
	inputs, err := runInputs(spec)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		child, err := core.repository.GetRun(ctx, input.Meta.ID)
		if err != nil {
			return err
		}
		if err := matchesInput(child, input); err != nil {
			return err
		}
		if !jobcontract.Terminal(child.Status()) {
			child, err = core.settleChild(ctx, child, outcome)
			if err != nil {
				return err
			}
		}
		if err := core.ensureReport(ctx, child); err != nil {
			return err
		}
	}
	if spec.Kind == jobcontract.KindComparison {
		comparison, err := core.groups.Refresh(ctx, spec.Spec.Comparison.Meta.ID)
		if err != nil {
			return err
		}
		if comparison.Status() == domain.ComparisonRunning {
			return ErrUnavailable
		}
	}
	return nil
}

func matchesInput(child domain.Run, input runs.PreparedRun) error {
	expected, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
	if err != nil {
		return err
	}
	identityMatches := child.Meta().ID == input.Meta.ID && child.PlanID() == input.PlanID
	same, err := sameRunSnapshot(child.Snapshot(), expected.Snapshot())
	if err != nil {
		return err
	}
	if !identityMatches || !same {
		return fmt.Errorf("%w: stored Run does not match its admitted Job", ErrConflict)
	}
	return nil
}

func (core *RunExecutor) settleChild(ctx context.Context, child domain.Run, outcome Outcome) (domain.Run, error) {
	at := core.clock.Now().UTC()
	if at.Before(child.Meta().UpdatedAt) {
		at = child.Meta().UpdatedAt
	}
	var next domain.Run
	var err error
	switch outcome.Status {
	case domain.RunFailed:
		next, err = child.Fail(*outcome.Failure, at)
	case domain.RunCancelled:
		next, err = child.Transition(domain.RunCancelled, at)
	default:
		return domain.Run{}, ErrUnavailable
	}
	if err != nil {
		return domain.Run{}, err
	}
	if err := core.repository.UpdateRun(ctx, child.Meta().Revision, next); err != nil {
		winner, readErr := core.repository.GetRun(ctx, child.Meta().ID)
		if readErr == nil && jobcontract.Terminal(winner.Status()) {
			return winner, nil
		}
		return domain.Run{}, err
	}
	return next, nil
}

func (core *RunExecutor) ensureReport(ctx context.Context, child domain.Run) error {
	exists, err := core.hasReport(ctx, child)
	if err != nil || exists {
		return err
	}
	generationErr := core.reports.Generate(ctx, child.Meta().ID)
	exists, err = core.hasReport(ctx, child)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if generationErr != nil {
		return generationErr
	}
	return ErrUnavailable
}

func (core *RunExecutor) hasReport(ctx context.Context, child domain.Run) (bool, error) {
	reports, err := core.repository.ListReportsForRuns(ctx, []string{child.Meta().ID})
	if err != nil {
		return false, err
	}
	if len(reports) == 0 {
		return false, nil
	}
	if len(reports) != 1 {
		return false, ErrConflict
	}
	report := reports[0]
	if err := report.Validate(); err != nil {
		return false, err
	}
	sameRun := report.RunID == child.Meta().ID && report.RunStatus == child.Status()
	same, err := sameRunSnapshot(report.PlanSnapshot, child.Snapshot())
	if err != nil {
		return false, err
	}
	if !sameRun || !same {
		return false, ErrConflict
	}
	return true, nil
}

func sameRunSnapshot(left, right domain.RunSnapshot) (bool, error) {
	normalized := make([][]byte, 0, 2)
	for _, snapshot := range []domain.RunSnapshot{left, right} {
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return false, err
		}
		// Native request objects may be reordered by canonical storage. UseNumber
		// keeps exact token budgets while comparing their current JSON values.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return false, err
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return false, err
		}
		normalized = append(normalized, canonical)
	}
	return bytes.Equal(normalized[0], normalized[1]), nil
}

// Close follows JobHost.Close so it never reclassifies a remote interruption.
func (core *RunExecutor) Close() error {
	if core == nil {
		return nil
	}
	core.mu.Lock()
	core.closed = true
	core.mu.Unlock()
	return core.host.Close()
}
