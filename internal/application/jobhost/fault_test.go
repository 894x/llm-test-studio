package jobhost_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type failJobState struct {
	jobhost.Repository
	status domain.RunStatus
}

func (repository failJobState) ChangeJob(ctx context.Context, change jobhost.Change) (jobcontract.Snapshot, error) {
	if change.Status == repository.status {
		return jobcontract.Snapshot{}, errors.New("injected_storage_failure")
	}
	return repository.Repository.ChangeJob(ctx, change)
}

type blockedSettlement struct {
	*controlledCore
	settlementEntered chan struct{}
	settlementRelease chan struct{}
}

func (core *blockedSettlement) Settle(ctx context.Context, spec jobcontract.JobSpec, outcome jobhost.Outcome) error {
	close(core.settlementEntered)
	select {
	case <-core.settlementRelease:
		return core.controlledCore.Settle(ctx, spec, outcome)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestKnownStorageFailureBlocksAdmissionBeforeSlowSettlement(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunCompleted} {
		t.Run(string(status), func(t *testing.T) {
			repository := openHostRepository(t)
			core := &blockedSettlement{
				controlledCore: newControlledCore(), settlementEntered: make(chan struct{}), settlementRelease: make(chan struct{}),
			}
			dependencies := hostDependencies(failJobState{Repository: repository, status: status}, core, &countedGuard{})
			dependencies.Policy.MaxActiveJobs = 2
			host := newTestHost(t, dependencies)
			submission := testSubmission(t)
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			if status == domain.RunCompleted {
				enteredExecution(t, core.controlledCore)
			}
			close(core.release)
			select {
			case <-core.settlementEntered:
			case <-time.After(time.Second):
				t.Fatal("settlement did not start")
			}
			if host.Status().Ready {
				t.Fatal("storage failure remained ready during settlement")
			}
			next := testSubmission(t)
			if _, _, err := host.Submit(t.Context(), &next); !errors.Is(err, jobhost.ErrUnavailable) {
				t.Fatal(err)
			}
			close(core.settlementRelease)
			if err := host.Close(); !errors.Is(err, jobhost.ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

type failReservation struct{}

func (failReservation) Close() error { return errors.New("reservation_release_failed") }

type failReleaseGuard struct{}

func (failReleaseGuard) Reserve(context.Context, jobcontract.JobSpec) (io.Closer, error) {
	return failReservation{}, nil
}

type rejectedAdmission struct{ jobhost.Repository }

func (repository rejectedAdmission) AcceptJob(context.Context, jobhost.Acceptance) (jobcontract.Snapshot, bool, error) {
	return jobcontract.Snapshot{}, false, jobhost.ErrConflict
}

func TestRejectedAdmissionWithFailedReservationReleaseStopsHost(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(rejectedAdmission{Repository: repository}, core, failReleaseGuard{}))
	submission := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &submission); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatal(err)
	}
	if host.Status().Ready || len(submission.Secrets) != 0 {
		t.Fatal("failed release left a ready host or readable transport")
	}
	if err := host.Close(); !errors.Is(err, jobhost.ErrUnavailable) {
		t.Fatal(err)
	}
}

type failProgress struct{ jobhost.Repository }

func (repository failProgress) RecordJobProgress(
	context.Context,
	string,
	quicktest.PerformanceProgress,
	time.Time,
) error {
	return errors.New("progress_storage_failed")
}

func TestProgressStorageFailureCancelsActiveAndRejectsNewWork(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(failProgress{Repository: repository}, core, &countedGuard{}))
	submission := testSubmission(t)
	id := submission.Job.JobID
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	execution := enteredExecution(t, core)
	execution.request.OnProgress(quicktest.PerformanceProgress{})
	terminal := waitJob(
		t,
		repository,
		id,
		domain.RunFailed,
	)
	if terminal.Failure == nil || terminal.Failure.ErrorCode != "job_storage_unavailable" {
		t.Fatal("storage cause was lost")
	}
	next := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &next); !errors.Is(err, jobhost.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestSharedConcurrencyBudgetQueuesAcrossActiveJobSlots(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	dependencies := hostDependencies(repository, core, &countedGuard{})
	dependencies.Policy.MaxActiveJobs, dependencies.Policy.MaxConcurrency = 2, 2
	host := newTestHost(t, dependencies)
	first := testSubmission(t)
	first.Job.Spec.Performance.Profile.Concurrency = 2
	first.Job.Spec.Performance.Profile.RequestCount = 4
	if _, _, err := host.Submit(t.Context(), &first); err != nil {
		t.Fatal(err)
	}
	enteredExecution(t, core)
	second := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &second); err != nil {
		t.Fatal(err)
	}
	status := host.Status()
	if status.Active != 1 || status.Queued != 1 {
		t.Fatal("aggregate concurrency budget was exceeded")
	}
	close(core.release)
	enteredExecution(t, core)
	waitJob(
		t,
		repository,
		second.Job.JobID,
		domain.RunCompleted,
	)
}

type lostCommandReply struct{ jobhost.Repository }

func (repository lostCommandReply) ApplyJobCommand(
	ctx context.Context,
	command jobcontract.Command,
	at time.Time,
) (jobcontract.CommandConfirmation, bool, error) {
	if _, _, err := repository.Repository.ApplyJobCommand(ctx, command, at); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	return jobcontract.CommandConfirmation{}, false, context.Canceled
}

func TestCancelSignalIsDeliveredAfterLostCommandConfirmation(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(lostCommandReply{Repository: repository}, core, &countedGuard{}))
	submission := testSubmission(t)
	id := submission.Job.JobID
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	execution := enteredExecution(t, core)
	running := waitJob(
		t,
		repository,
		id,
		domain.RunRunning,
	)
	command := jobcontract.Command{
		Contract: jobcontract.CurrentContract, CommandID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", JobID: id,
		Kind: jobcontract.CommandCancel, ExpectedRevision: running.Receipt.Revision,
	}
	if confirmation, _, err := host.ApplyCommand(t.Context(), command); err != nil || confirmation.Command != command {
		t.Fatalf("lost command reply not recovered: %v", err)
	}
	select {
	case <-execution.context.Done():
	case <-time.After(time.Second):
		t.Fatal("accepted cancellation was not delivered")
	}
	waitJob(
		t,
		repository,
		id,
		domain.RunCancelled,
	)
	waitHost(t, func() bool { return host.Status().Active == 0 })
}

func TestTerminalJobBeforeSettlementMarkerIsRecoveredWithoutExecution(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	submission := testSubmission(t)
	initial, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{ContentClaims: []jobcontent.Claim{},
		Spec: submission.Job, Origin: acceptedOrigin(submission.Job),
		SecretCommitment: submission.Job.EngineBuildID, AcceptedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ChangeJob(t.Context(), jobhost.Change{
		JobID: initial.Receipt.JobID, ExpectedRevision: 1, Status: domain.RunCancelled,
		At: initial.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	if !host.Status().Ready {
		t.Fatal("recovered host unavailable")
	}
	core.mu.Lock()
	if core.calls != 0 || len(core.settled) != 1 {
		t.Fatal("terminal recovery replayed execution or skipped settlement")
	}
	core.mu.Unlock()
	remaining, err := repository.RecoveryJobs(t.Context())
	if err != nil || len(remaining) != 0 {
		t.Fatal("successful recovery did not acknowledge settlement")
	}
	submission.Clear()
}
