package runs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type hostExecutorFunc func(context.Context, runs.ExecutionRequest, func(runs.ResultDraft) error) error

func (execute hostExecutorFunc) Execute(
	ctx context.Context,
	request runs.ExecutionRequest,
	emit func(runs.ResultDraft) error,
) error {
	return execute(ctx, request, emit)
}

type hostReporterFunc func(context.Context, string) error

func (generate hostReporterFunc) Generate(ctx context.Context, id string) error {
	return generate(ctx, id)
}

func lifecycleHost(
	t *testing.T,
	executor runs.Executor,
	reporter runs.ReportGenerator,
) (*runs.Host, *fakeRepository, string) {
	t.Helper()
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	host, err := runs.NewHost(runs.HostDependencies{
		Repository: executionOnlyRepository{delegate: repository}, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Reporter: reporter,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	prepared := preparedFixture(t, fixture)
	run, err := domain.NewRun(prepared.Meta, prepared.PlanID, prepared.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	lease, err := credentials.NewTemporaryLease([]byte("owned-execution-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := host.AdoptQueued(t.Context(), prepared.Meta.ID, lease); err != nil {
		t.Fatal(err)
	}
	return host, repository, prepared.Meta.ID
}

func TestWaitRunJoinsCancelledWorkersBeforeReportingCompletion(t *testing.T) {
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	executor := hostExecutorFunc(func(ctx context.Context, _ runs.ExecutionRequest, _ func(runs.ResultDraft) error) error {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	})
	host, repository, id := lifecycleHost(t, executor, nil)
	releaseWorker := sync.OnceFunc(func() { close(release) })
	defer releaseWorker()
	if err := host.ActivateRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := host.CancelRun(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	<-cancelled
	stored, err := repository.GetRun(t.Context(), id)
	if err != nil || stored.Status() != domain.RunCancelled {
		t.Fatalf("durable cancellation = %s, %v", stored.Status(), err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err := host.WaitRun(ctx, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitRun returned before worker exit: %v", err)
	}
	releaseWorker()
	ctx, stopWait := context.WithTimeout(t.Context(), time.Second)
	defer stopWait()
	if run, err := host.WaitRun(ctx, id); err != nil || run.Status() != domain.RunCancelled {
		t.Fatalf("joined Run = %s, %v", run.Status(), err)
	}
}

func TestWaitRunWaitsForReportAndDoesNotCancelExecution(t *testing.T) {
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})}
	reportEntered, reportRelease := make(chan struct{}), make(chan struct{})
	reporter := hostReporterFunc(func(context.Context, string) error {
		close(reportEntered)
		<-reportRelease
		return nil
	})
	host, _, id := lifecycleHost(t, executor, reporter)
	releaseReport := sync.OnceFunc(func() { close(reportRelease) })
	defer releaseReport()
	if err := host.ActivateRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	request := <-executor.entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := host.WaitRun(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled observer = %v", err)
	}
	secret, err := request.Credential.Bytes()
	clear(secret)
	if err != nil {
		t.Fatal("observer cancellation closed the execution lease")
	}
	close(executor.release)
	<-reportEntered
	ctx, stopWait := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer stopWait()
	if _, err := host.WaitRun(ctx, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitRun returned during report generation: %v", err)
	}
	releaseReport()
	ctx, stopJoin := context.WithTimeout(t.Context(), time.Second)
	defer stopJoin()
	if run, err := host.WaitRun(ctx, id); err != nil || run.Status() != domain.RunCompleted {
		t.Fatalf("reported Run = %s, %v", run.Status(), err)
	}
}

func TestFailRunPreservesOwnerReasonAndJoinsExecution(t *testing.T) {
	for _, code := range []domain.ErrorCode{"runner_interrupted", "job_execution_timeout"} {
		t.Run(string(code), func(t *testing.T) {
			entered := make(chan struct{})
			executor := hostExecutorFunc(func(
				ctx context.Context,
				_ runs.ExecutionRequest,
				_ func(runs.ResultDraft) error,
			) error {
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			})
			host, _, id := lifecycleHost(t, executor, nil)
			if err := host.ActivateRun(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			<-entered
			failure := domain.RunFailure{Phase: "runner", ErrorCode: code}
			if err := host.FailRun(t.Context(), id, failure); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			stored, err := host.WaitRun(ctx, id)
			if err != nil || stored.Status() != domain.RunFailed {
				t.Fatalf(
					"owner failure = %s, %+v, %v",
					stored.Status(),
					stored.Failure(),
					err,
				)
			}
			if stored.Failure() == nil || *stored.Failure() != failure {
				t.Fatalf("owner reason = %+v", stored.Failure())
			}
			if err := host.CancelRun(t.Context(), id); !errors.Is(err, runs.ErrNotActive) {
				t.Fatalf("terminal Run accepted a second cancellation: %v", err)
			}
		})
	}
}

func TestFailRunStopsTerminalRecoveryWhenFailureCannotBeWritten(t *testing.T) {
	service, repository, _, request := startBlockedTerminalRecovery(t)
	repository.gate.Lock()
	repository.allTerminal = true
	repository.gate.Unlock()
	failure := domain.RunFailure{Phase: "runner", ErrorCode: "job_storage_unavailable"}
	if err := service.FailRun(t.Context(), request.Run.Meta().ID, failure); err == nil {
		t.Fatal("FailRun hid the storage failure")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := service.WaitRun(ctx, request.Run.Meta().ID)
	if !errors.Is(err, runs.ErrNotActive) {
		t.Fatalf("failure left an indefinite terminal retry: %v", err)
	}
	stored, _ := repository.GetRun(t.Context(), request.Run.Meta().ID)
	if stored.Status() != domain.RunRunning {
		t.Fatalf("failed terminal write was replaced with %s", stored.Status())
	}
}

func TestFailQueuedRunPreventsActivation(t *testing.T) {
	executor := &recordingExecutor{}
	host, _, id := lifecycleHost(t, executor, nil)
	failure := domain.RunFailure{Phase: "runner", ErrorCode: "runner_interrupted"}
	if err := host.FailRun(t.Context(), id, failure); err != nil {
		t.Fatal(err)
	}
	if err := host.ActivateRun(t.Context(), id); !errors.Is(err, runs.ErrNotActive) {
		t.Fatalf("failed queued Run was activated: %v", err)
	}
	stored, err := host.WaitRun(t.Context(), id)
	if err != nil || stored.Status() != domain.RunFailed {
		t.Fatalf(
			"queued failure = %s, %+v, %v",
			stored.Status(),
			stored.Failure(),
			err,
		)
	}
	if stored.Failure() == nil || *stored.Failure() != failure {
		t.Fatalf("queued failure reason = %+v", stored.Failure())
	}
}

func TestConcurrentCloseJoinsQueuedCancellationReport(t *testing.T) {
	reportEntered, reportRelease := make(chan struct{}), make(chan struct{})
	reporter := hostReporterFunc(func(context.Context, string) error {
		close(reportEntered)
		<-reportRelease
		return nil
	})
	host, _, id := lifecycleHost(t, &recordingExecutor{}, reporter)
	releaseReport := sync.OnceFunc(func() { close(reportRelease) })
	defer releaseReport()
	cancelled := make(chan error, 1)
	go func() { cancelled <- host.CancelRun(t.Context(), id) }()
	<-reportEntered
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- host.Close() }()
	go func() { second <- host.Close() }()
	for _, closed := range []<-chan error{first, second} {
		select {
		case err := <-closed:
			t.Fatalf("Close returned during queued report generation: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	releaseReport()
	for _, closed := range []<-chan error{cancelled, first, second} {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("close or cancellation did not join the finished report")
		}
	}
}
