package jobhost_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type maintenanceMemory struct {
	mu       sync.Mutex
	record   *jobcontract.MaintenanceRecord
	fail     bool
	lost     bool
	syncFail bool
	writes   int
}

func (store *maintenanceMemory) Sync(context.Context) error {
	if store.syncFail {
		return errors.New("injected_directory_sync_failure")
	}
	return nil
}

func (store *maintenanceMemory) Read(context.Context) (*jobcontract.MaintenanceRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.record == nil {
		return nil, nil
	}
	copy := *store.record
	return &copy, nil
}

func (store *maintenanceMemory) Write(_ context.Context, record jobcontract.MaintenanceRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.writes++
	if !store.fail {
		store.record = &record
	}
	if store.fail || store.lost {
		return errors.New("injected_control_write_failure")
	}
	return nil
}

func pauseCommand() jobcontract.MaintenanceCommand {
	return jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		Action: jobcontract.MaintenancePause,
	}
}

func TestMaintenanceOwnerAndLostConfirmationDoNotRepeatWrites(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	store := &maintenanceMemory{lost: true}
	control, err := jobhost.NewMaintenance(t.Context(), host, store)
	if err != nil {
		t.Fatal(err)
	}
	command := pauseCommand()
	first, err := control.Apply(t.Context(), command)
	if err != nil || first.Status.Ready || !first.Status.Healthy {
		t.Fatalf("lost pause acknowledgment was not confirmed: %v", err)
	}
	retry, err := control.Apply(t.Context(), command)
	if err != nil || !retry.Record.ChangedAt.Equal(first.Record.ChangedAt) || store.writes != 1 {
		t.Fatal("repeated pause changed the durable operation")
	}
	foreign := command
	foreign.OperationID = "66666666-6666-4666-8666-666666666666"
	for _, action := range []jobcontract.MaintenanceAction{jobcontract.MaintenancePause, jobcontract.MaintenanceResume} {
		foreign.Action = action
		if _, err := control.Apply(t.Context(), foreign); !errors.Is(err, jobhost.ErrConflict) {
			t.Fatal("another operation changed the maintenance gate")
		}
	}
	command.Action = jobcontract.MaintenanceResume
	resumed, err := control.Apply(t.Context(), command)
	if err != nil || !resumed.Status.Ready || store.writes != 2 {
		t.Fatal("lost resume acknowledgment was not confirmed")
	}
	if _, err := control.Apply(t.Context(), command); err != nil || store.writes != 2 {
		t.Fatal("resume retry repeated its write")
	}
	command.Action = jobcontract.MaintenancePause
	if _, err := control.Apply(t.Context(), command); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatal("a completed operation silently paused the runner again")
	}
}

func TestUnconfirmedMaintenanceWriteIsolatesIntake(t *testing.T) {
	for _, action := range []jobcontract.MaintenanceAction{jobcontract.MaintenancePause, jobcontract.MaintenanceResume} {
		t.Run(string(action), func(t *testing.T) {
			repository, core := openHostRepository(t), newControlledCore()
			host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
			store := &maintenanceMemory{}
			control, err := jobhost.NewMaintenance(t.Context(), host, store)
			if err != nil {
				t.Fatal(err)
			}
			command := pauseCommand()
			if action == jobcontract.MaintenanceResume {
				if _, err := control.Apply(t.Context(), command); err != nil {
					t.Fatal(err)
				}
			}
			store.fail = true
			command.Action = action
			if _, err := control.Apply(t.Context(), command); !errors.Is(err, jobhost.ErrUnavailable) {
				t.Fatalf("unconfirmed write returned %v", err)
			}
			if host.Status().Healthy || host.Status().Ready {
				t.Fatal("unconfirmed maintenance write left intake healthy")
			}
			submission := testSubmission(t)
			if _, _, err := host.Submit(t.Context(), &submission); !errors.Is(err, jobhost.ErrUnavailable) {
				t.Fatal("isolated host accepted new work")
			}
		})
	}
}

func TestVisibleMaintenanceWriteWithoutDurabilityIsNotAcknowledged(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	store := &maintenanceMemory{lost: true, syncFail: true}
	control, err := jobhost.NewMaintenance(t.Context(), host, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Apply(t.Context(), pauseCommand()); !errors.Is(err, jobhost.ErrUnavailable) {
		t.Fatal("visible but not durable maintenance state was acknowledged")
	}
	if store.record == nil || !store.record.Paused {
		t.Fatal("test did not expose the renamed document")
	}
	if host.Status().Healthy || host.Status().Ready {
		t.Fatal("unconfirmed durability left intake available")
	}
}

type settleOneJob struct {
	*controlledCore
	id      string
	entered chan struct{}
	release chan struct{}
}

func (core settleOneJob) Settle(ctx context.Context, spec jobcontract.JobSpec, outcome jobhost.Outcome) error {
	if spec.JobID == core.id {
		close(core.entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-core.release:
		}
	}
	return core.controlledCore.Settle(ctx, spec, outcome)
}

func TestMaintenancePendingIncludesCancelledQueueSettlement(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	queued := testSubmission(t)
	settlement := settleOneJob{
		controlledCore: core, id: queued.Job.JobID, entered: make(chan struct{}), release: make(chan struct{}),
	}
	releaseSettlement := sync.OnceFunc(func() { close(settlement.release) })
	host := newTestHost(t, hostDependencies(repository, settlement, &countedGuard{}))
	// This cleanup must precede Host.Close when an assertion fails.
	t.Cleanup(releaseSettlement)
	control, err := jobhost.NewMaintenance(t.Context(), host, &maintenanceMemory{})
	if err != nil {
		t.Fatal(err)
	}
	active := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &active); err != nil {
		t.Fatal(err)
	}
	_ = enteredExecution(t, core)
	if _, _, err := host.Submit(t.Context(), &queued); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Apply(t.Context(), pauseCommand()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.ApplyCommand(t.Context(), jobcontract.Command{
		Contract: jobcontract.CurrentContract, CommandID: "77777777-7777-4777-8777-777777777777",
		JobID: queued.Job.JobID, Kind: jobcontract.CommandCancel, ExpectedRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-settlement.entered:
	case <-time.After(time.Second):
		t.Fatal("cancelled queue item did not enter settlement")
	}
	close(core.release)
	waitHost(t, func() bool { return host.Status().Active == 0 })
	status := host.Status()
	valid := status.Active == 0 && status.Queued == 0 && status.Pending == 1 && status.Healthy && !status.Ready
	if !valid {
		t.Fatal("upgrade could mistake unfinished settlement for an idle node")
	}
	releaseSettlement()
	waitHost(t, func() bool { return host.Status().Pending == 0 })
}

type blockedAdmissionGuard struct {
	countedGuard
	entered chan struct{}
	release chan struct{}
}

func (guard *blockedAdmissionGuard) Reserve(ctx context.Context, spec jobcontract.JobSpec) (io.Closer, error) {
	close(guard.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-guard.release:
		return guard.countedGuard.Reserve(ctx, spec)
	}
}

func TestMaintenancePauseIncludesAnAdmissionAlreadyHoldingTheHostGate(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	guard := &blockedAdmissionGuard{entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(guard.release) })
	host := newTestHost(t, hostDependencies(repository, core, guard))
	t.Cleanup(release)
	control, err := jobhost.NewMaintenance(t.Context(), host, &maintenanceMemory{})
	if err != nil {
		t.Fatal(err)
	}
	submission := testSubmission(t)
	accepted := make(chan error, 1)
	go func() {
		_, _, err := host.Submit(t.Context(), &submission)
		accepted <- err
	}()
	select {
	case <-guard.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("admission did not enter the resource gate")
	}
	paused := make(chan jobhost.MaintenanceResult, 1)
	pauseError := make(chan error, 1)
	go func() {
		result, err := control.Apply(t.Context(), pauseCommand())
		paused <- result
		pauseError <- err
	}()
	release()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admission did not finish")
	}
	select {
	case result := <-paused:
		if err := <-pauseError; err != nil {
			t.Fatal(err)
		}
		if result.Status.Ready || result.Status.Pending != 1 {
			t.Fatal("pause acknowledgment omitted the concurrent admission")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pause did not finish")
	}
	newSubmission := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &newSubmission); !errors.Is(err, jobhost.ErrMaintenance) {
		t.Fatal("a new admission bypassed the confirmed maintenance gate")
	}
	_ = enteredExecution(t, core)
	close(core.release)
	waitHost(t, func() bool { return host.Status().Pending == 0 })
}
