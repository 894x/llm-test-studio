package jobhost_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type groupActivationRepository struct {
	*sqlite.Repository
	firstID        string
	secondID       string
	secondStarting chan struct{}
	failureWrite   chan struct{}
	releaseFailure chan struct{}
	startOnce      sync.Once
	failureOnce    sync.Once
}

func (repository *groupActivationRepository) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	firstRunning := run.Meta().ID == repository.firstID && run.Status() == domain.RunRunning
	secondStarting := run.Meta().ID == repository.secondID && run.Status() == domain.RunStarting
	firstFailing := run.Meta().ID == repository.firstID && run.Status() == domain.RunFailed
	switch {
	case firstRunning:
		select {
		case <-repository.secondStarting:
			return errors.New("injected first child storage fault")
		case <-ctx.Done():
			return ctx.Err()
		}
	case secondStarting:
		repository.startOnce.Do(func() { close(repository.secondStarting) })
		// This activation has passed the main loop's gate. The independent
		// request context must still be cancelled by the first child's fault.
		<-ctx.Done()
		return ctx.Err()
	case firstFailing:
		repository.failureOnce.Do(func() { close(repository.failureWrite) })
		select {
		case <-repository.releaseFailure:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return repository.Repository.UpdateRun(ctx, revision, run)
}

func TestRunExecutorGroupFaultStopsPendingActivationBeforeSettlement(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	submission := coreSubmission(t, server.URL, true)
	spec := submission.Job
	fault := &groupActivationRepository{
		Repository: repository,
		firstID:    spec.Spec.Comparison.Runs[0].Meta.ID, secondID: spec.Spec.Comparison.Runs[1].Meta.ID,
		secondStarting: make(chan struct{}), failureWrite: make(chan struct{}), releaseFailure: make(chan struct{}),
	}
	releaseFailure := sync.OnceFunc(func() { close(fault.releaseFailure) })
	defer releaseFailure()
	core := newRunCore(t, fault)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fault.failureWrite:
	case <-time.After(3 * time.Second):
		t.Fatal("storage fault did not cancel pending activation and enter settlement")
	}
	if host.Status().Ready || requests.Load() != 0 {
		t.Fatal("faulted group remained available or sent traffic while terminal storage was blocked")
	}
	releaseFailure()
	waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunFailed,
	)
	waitSettled(t, repository)
	if requests.Load() != 0 {
		t.Fatal("pending activation sent requests after the group fault")
	}
}

type blockedStartingRepository struct {
	*sqlite.Repository
	started   chan struct{}
	unbounded atomic.Bool
}

func (repository *blockedStartingRepository) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	if run.Status() == domain.RunStarting {
		if _, bounded := ctx.Deadline(); !bounded {
			repository.unbounded.Store(true)
			close(repository.started)
			return errors.New("Starting write has no storage deadline")
		}
		close(repository.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return repository.Repository.UpdateRun(ctx, revision, run)
}

func TestRunExecutorCancelAndCloseEscapeBlockedStartingWrite(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	blocked := &blockedStartingRepository{Repository: repository, started: make(chan struct{})}
	core := newRunCore(t, blocked)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := coreSubmission(t, server.URL, false)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Starting write did not enter the storage adapter")
	}
	if blocked.unbounded.Load() {
		t.Fatal("Starting storage inherited an unbounded execution context")
	}
	snapshot, err := repository.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.ApplyCommand(t.Context(), jobcontract.Command{
		Contract: jobcontract.CurrentContract, CommandID: coreMeta(t).ID, JobID: spec.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: snapshot.Receipt.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- host.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, jobhost.ErrUnavailable) {
			t.Fatalf("blocked storage shutdown = %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("owner shutdown waited indefinitely for Starting persistence")
	}
	waitSettled(t, repository)
	child, err := repository.GetRun(t.Context(), spec.Spec.Run.Meta.ID)
	if err != nil || child.Status() != domain.RunCancelled || requests.Load() != 0 {
		t.Fatalf(
			"bounded cancellation: child=%s requests=%d err=%v",
			child.Status(),
			requests.Load(),
			err,
		)
	}
}
