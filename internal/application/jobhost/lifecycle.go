package jobhost

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (host *Host) dispatchLocked() {
	if host.available() != nil {
		return
	}
	for len(host.queue) > 0 && host.active < host.policy.MaxActiveJobs {
		control := host.queue[0]
		if host.concurrency+control.units > uint64(host.policy.MaxConcurrency) {
			return
		}
		host.queue = host.queue[1:]
		if control.timer != nil {
			control.timer.Stop()
		}
		control.phase, control.reserved = phaseExecuting, true
		host.active++
		host.concurrency += control.units
		deadlineContext, stopDeadline := context.WithTimeoutCause(
			context.Background(), host.policy.ExecutionTimeout, ErrExecutionLimit,
		)
		executionContext, cancel := context.WithCancelCause(deadlineContext)
		control.cancel = cancel
		host.wg.Add(1)
		go func() {
			defer host.wg.Done()
			defer stopDeadline()
			defer cancel(nil)
			host.execute(executionContext, control)
		}()
	}
}

func (host *Host) execute(ctx context.Context, control *jobControl) {
	host.mu.Lock()
	var err error
	mayRun := false
	if ctx.Err() == nil {
		err = host.available()
		if err == nil {
			mayRun, err = host.startLocked(control.spec.JobID)
		}
	}
	if err != nil {
		host.storageFailedLocked()
	}
	host.mu.Unlock()
	if err != nil {
		host.complete(control, failed("job_storage_unavailable"))
		return
	}
	if ctx.Err() != nil {
		host.complete(control, reasonOutcome(context.Cause(ctx)))
		return
	}
	if !mayRun {
		host.complete(control, Outcome{Status: domain.RunCancelled})
		return
	}
	if host.unhealthy.Load() {
		host.complete(control, failed("job_storage_unavailable"))
		return
	}
	observe, finishProgress := host.startProgressObserver(control)
	defer finishProgress()
	outcome, err := host.executor.Execute(ctx, Execution{
		Spec: control.spec, Secrets: control.secrets, StopSending: control.stop,
		OnProgress:           observe,
		OnPersistenceFailure: host.storageFailed,
	})
	finishProgress()
	if err != nil || outcome.Validate() != nil {
		host.storageFailed()
		outcome = failed("job_execution_unavailable")
	}
	if ctx.Err() != nil {
		outcome = reasonOutcome(context.Cause(ctx))
	}
	host.complete(control, outcome)
}

func (host *Host) startLocked(id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	current, err := host.repository.GetJob(ctx, id)
	if err != nil {
		return false, err
	}
	if jobcontract.Terminal(current.Receipt.Status) {
		return false, nil
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		current, err = host.repository.ChangeJob(ctx, Change{
			JobID: id, ExpectedRevision: current.Receipt.Revision, Status: status, At: host.after(current.UpdatedAt),
		})
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func (host *Host) after(previous time.Time) time.Time {
	at := host.clock.Now().UTC()
	if at.Before(previous) {
		return previous
	}
	return at
}

func (host *Host) recordOutcomeLocked(id string, outcome Outcome) (jobcontract.Snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	defer cancel()
	current, err := host.repository.GetJob(ctx, id)
	if err != nil || jobcontract.Terminal(current.Receipt.Status) {
		return current, err
	}
	next, err := host.repository.ChangeJob(ctx, Change{
		JobID: id, ExpectedRevision: current.Receipt.Revision, Status: outcome.Status,
		Failure: outcome.Failure, At: host.after(current.UpdatedAt),
	})
	if !errors.Is(err, ErrConflict) {
		return next, err
	}
	winner, readErr := host.repository.GetJob(ctx, id)
	if readErr == nil && jobcontract.Terminal(winner.Receipt.Status) {
		return winner, nil
	}
	return jobcontract.Snapshot{}, err
}

func outcomeOf(snapshot jobcontract.Snapshot) Outcome {
	return Outcome{Status: snapshot.Receipt.Status, Failure: snapshot.Failure}
}

func (host *Host) complete(control *jobControl, outcome Outcome) {
	host.mu.Lock()
	control.phase = phaseSettling
	snapshot, stateErr := host.recordOutcomeLocked(control.spec.JobID, outcome)
	if stateErr != nil {
		host.storageFailedLocked()
	}
	host.mu.Unlock()
	if stateErr == nil {
		outcome = outcomeOf(snapshot)
	}
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	settleErr := host.executor.Settle(ctx, control.spec, outcome)
	cancel()
	if settleErr != nil {
		host.storageFailed()
	}
	releaseErr := releaseControl(control)
	if stateErr != nil || settleErr != nil || releaseErr != nil {
		host.storageFailed()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
		markErr := host.repository.MarkJobSettled(ctx, control.spec.JobID)
		cancel()
		if markErr != nil {
			host.storageFailed()
		}
	}
	host.mu.Lock()
	delete(host.controls, control.spec.JobID)
	if control.reserved {
		host.active--
		host.concurrency -= control.units
	}
	host.dispatchLocked()
	host.mu.Unlock()
}

func (host *Host) removeQueuedLocked(control *jobControl) {
	if control.timer != nil {
		control.timer.Stop()
	}
	host.queue = slices.DeleteFunc(host.queue, func(candidate *jobControl) bool { return candidate == control })
	control.phase = phaseSettling
}

func (host *Host) settleQueuedLocked(control *jobControl, outcome Outcome) {
	host.removeQueuedLocked(control)
	host.wg.Add(1)
	go func() {
		defer host.wg.Done()
		host.complete(control, outcome)
	}()
}

func (host *Host) expire(control *jobControl) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.closed || control.phase != phaseQueued {
		return
	}
	host.settleQueuedLocked(control, failed("job_queue_timeout"))
}

func (host *Host) storageFailed() {
	host.unhealthy.Store(true)
	host.mu.Lock()
	defer host.mu.Unlock()
	host.storageFailedLocked()
}

func (host *Host) storageFailedLocked() {
	host.unhealthy.Store(true)
	if host.failureHandled {
		return
	}
	host.failureHandled = true
	for _, control := range host.controls {
		if control.phase == phaseQueued {
			host.settleQueuedLocked(control, failed("job_storage_unavailable"))
			continue
		}
		if control.cancel != nil {
			control.cancel(ErrUnavailable)
		}
	}
}

func (host *Host) shutdownReason() error {
	if host.target.Kind == jobcontract.TargetLocal {
		return ErrLocalShutdown
	}
	return ErrInterrupted
}

func (host *Host) recover(ctx context.Context) error {
	jobs, err := host.repository.RecoveryJobs(ctx)
	if err != nil {
		return err
	}
	for _, snapshot := range jobs {
		if snapshot.Receipt.Origin.InstallationID != host.installationID {
			continue
		}
		spec, err := host.repository.GetJobSpec(ctx, snapshot.Receipt.JobID)
		if err != nil {
			return err
		}
		if spec.Target != host.target {
			continue
		}
		outcome := outcomeOf(snapshot)
		if !jobcontract.Terminal(snapshot.Receipt.Status) {
			outcome = reasonOutcome(host.shutdownReason())
			snapshot, err = host.recordOutcomeLocked(spec.JobID, outcome)
			if err != nil {
				return err
			}
			outcome = outcomeOf(snapshot)
		}
		if err := host.executor.Settle(ctx, spec, outcome); err != nil {
			return err
		}
		if err := host.repository.MarkJobSettled(ctx, spec.JobID); err != nil {
			return err
		}
	}
	return nil
}

func (host *Host) ApplyCommand(
	ctx context.Context,
	command jobcontract.Command,
) (jobcontract.CommandConfirmation, bool, error) {
	if host == nil || ctx == nil {
		return jobcontract.CommandConfirmation{}, false, ErrInvalid
	}
	host.mu.Lock()
	fatalStorage := false
	defer func() {
		if fatalStorage {
			host.storageFailedLocked()
		}
		host.mu.Unlock()
	}()
	if err := host.available(); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	if err := command.Validate(); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	ownedSpec, err := host.repository.GetJobSpec(ctx, command.JobID)
	if err != nil {
		fatalStorage = storageError(err)
		return jobcontract.CommandConfirmation{}, false, err
	}
	if ownedSpec.NodeInstallationID != host.installationID || ownedSpec.Target != host.target {
		return jobcontract.CommandConfirmation{}, false, ErrIdentity
	}
	before, err := host.repository.GetJob(ctx, command.JobID)
	if err != nil {
		fatalStorage = storageError(err)
		return jobcontract.CommandConfirmation{}, false, err
	}
	confirmation, created, err := host.repository.ApplyJobCommand(ctx, command, host.after(before.UpdatedAt))
	if err != nil {
		lookupContext, cancel := context.WithTimeout(context.Background(), storageTimeout)
		confirmed, lookupErr := host.repository.GetJobCommand(lookupContext, command.CommandID)
		cancel()
		if lookupErr != nil {
			fatalStorage = storageError(err) || !errors.Is(lookupErr, ErrNotFound)
			return jobcontract.CommandConfirmation{}, false, err
		}
		if confirmed.Command != command {
			return jobcontract.CommandConfirmation{}, false, ErrConflict
		}
		confirmation, created = confirmed, false
	}
	control := host.controls[command.JobID]
	if control == nil {
		return confirmation, created, nil
	}
	readContext, cancel := context.WithTimeout(context.Background(), storageTimeout)
	current, err := host.repository.GetJob(readContext, command.JobID)
	cancel()
	if err != nil {
		fatalStorage = true
		return jobcontract.CommandConfirmation{}, false, err
	}
	switch current.Receipt.Status {
	case domain.RunDraining:
		control.stopOnce.Do(func() { close(control.stop) })
	case domain.RunCancelled:
		if control.phase == phaseQueued {
			host.settleQueuedLocked(control, Outcome{Status: domain.RunCancelled})
		} else if control.cancel != nil {
			control.cancel(ErrCancelled)
		}
	}
	return confirmation, created, nil
}

func (host *Host) Close() error {
	if host == nil {
		return nil
	}
	host.mu.Lock()
	if host.closed {
		done := host.closeDone
		host.mu.Unlock()
		<-done
		host.mu.Lock()
		defer host.mu.Unlock()
		if host.unhealthy.Load() {
			return ErrUnavailable
		}
		return nil
	}
	host.closed = true
	cause := host.shutdownReason()
	for _, control := range host.controls {
		if control.phase == phaseQueued {
			host.settleQueuedLocked(control, reasonOutcome(cause))
			continue
		}
		if control.phase == phaseExecuting {
			if _, err := host.recordOutcomeLocked(control.spec.JobID, reasonOutcome(cause)); err != nil {
				host.storageFailedLocked()
			}
			control.cancel(cause)
		}
	}
	host.mu.Unlock()
	host.wg.Wait()
	host.mu.Lock()
	defer host.mu.Unlock()
	clear(host.commitmentKey)
	close(host.closeDone)
	if host.unhealthy.Load() {
		return ErrUnavailable
	}
	return nil
}
