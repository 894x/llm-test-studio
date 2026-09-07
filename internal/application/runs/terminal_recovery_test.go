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

type terminalRecoveryRepository struct {
	*fakeRepository
	gate        sync.Mutex
	unavailable bool
	target      domain.RunStatus
	allTerminal bool
	commitError bool
}

func (r *terminalRecoveryRepository) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	r.gate.Lock()
	blocked := r.unavailable && (run.Status() == r.target || r.allTerminal && run.Status() == domain.RunCancelled)
	ambiguous := r.commitError && run.Status() == r.target
	r.gate.Unlock()
	if blocked {
		return errors.New("injected terminal storage outage")
	}
	if err := r.fakeRepository.UpdateRun(ctx, revision, run); err != nil {
		return err
	}
	if ambiguous {
		return errors.New("injected error after successful commit")
	}
	return nil
}

func TestTerminalRecoveryShutdownLeavesInterruptedRunRecoverable(t *testing.T) {
	service, repository, _, request := startBlockedTerminalRecovery(t)
	repository.gate.Lock()
	repository.allTerminal = true
	repository.gate.Unlock()
	closed := make(chan error, 1)
	go func() { closed <- service.Close() }()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("Close hid the failed cancellation write")
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited indefinitely for terminal recovery")
	}
	stored, _ := repository.GetRun(context.Background(), request.Run.Meta().ID)
	if stored.Status() != domain.RunRunning {
		t.Fatalf("stored status = %s", stored.Status())
	}
	restartRepository := &recoveryRepository{runs: []domain.Run{stored}}
	clock := fixedRecoveryClock{now: stored.Meta().UpdatedAt.Add(time.Minute)}
	if err := runs.RecoverInterrupted(context.Background(), restartRepository, clock); err != nil {
		t.Fatal(err)
	}
	if restartRepository.runs[0].Status() != domain.RunCancelled {
		t.Fatal("restart left an orphaned running state")
	}
	revision := restartRepository.runs[0].Meta().Revision
	if err := runs.RecoverInterrupted(context.Background(), restartRepository, clock); err != nil {
		t.Fatal(err)
	}
	if restartRepository.runs[0].Meta().Revision != revision {
		t.Fatal("restart recovery changed an already terminal run")
	}
}

func TestTerminalRecoveryDoesNotOverwriteCancellation(t *testing.T) {
	service, repository, executor, request := startBlockedTerminalRecovery(t)
	if err := service.CancelRun(context.Background(), request.Run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	repository.gate.Lock()
	repository.unavailable = false
	repository.gate.Unlock()
	// Close joins the retry worker, so the final persisted value is checked after
	// both cancellation and terminal recovery have finished.
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	stored, _ := repository.GetRun(context.Background(), request.Run.Meta().ID)
	if stored.Status() != domain.RunCancelled {
		t.Fatalf("recovery overwrote cancellation with %s", stored.Status())
	}
	select {
	case <-executor.entered:
		t.Fatal("replayed execution")
	default:
	}
}

func startBlockedTerminalRecovery(t *testing.T) (*runs.Service, *terminalRecoveryRepository, *controlledExecutor, runs.ExecutionRequest) {
	t.Helper()
	fixture := newRunFixture(t)
	repository := &terminalRecoveryRepository{fakeRepository: &fakeRepository{fixture: fixture}, unavailable: true, target: domain.RunCompleted}
	store := credentials.NewMemoryStore()
	ref, _ := credentials.StoreRefFromCredential(fixture.credential)
	_ = store.Set(context.Background(), ref, []byte("test-secret"))
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 2), release: make(chan struct{})}
	reported := make(chan struct{}, 1)
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		ReportDiagnostic: func(d runs.Diagnostic) {
			if d.Operation == "transition_completed" {
				reported <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := service.StartRun(context.Background(), fixture.plan.ID); err != nil {
		t.Fatal(err)
	}
	request := <-executor.entered
	close(executor.release)
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("terminal failure was not reported")
	}
	if _, err := request.Credential.Bytes(); err == nil {
		t.Fatal("storage outage retains the credential")
	}
	return service, repository, executor, request
}

func TestTerminalRecoveryReconcilesCommittedWriteError(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &terminalRecoveryRepository{fakeRepository: &fakeRepository{fixture: fixture}, target: domain.RunCompleted, commitError: true}
	store := credentials.NewMemoryStore()
	ref, _ := credentials.StoreRefFromCredential(fixture.credential)
	_ = store.Set(context.Background(), ref, []byte("test-secret"))
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 2), release: make(chan struct{})}
	reports := make(chan string, 2)
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		Reporter: terminalRecoveryReporter{reports: reports},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := service.StartRun(context.Background(), fixture.plan.ID); err != nil {
		t.Fatal(err)
	}
	request := <-executor.entered
	close(executor.release)
	select {
	case runID := <-reports:
		if runID != request.Run.Meta().ID {
			t.Fatal("reported wrong run")
		}
	case <-time.After(time.Second):
		t.Fatal("ambiguous commit prevented report generation")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.run.Status() != domain.RunCompleted || repository.run.Meta().Revision != 4 {
		t.Fatalf("reconciled run = %s revision %d", repository.run.Status(), repository.run.Meta().Revision)
	}
	if len(repository.results) != 2 {
		t.Fatalf("results = %d, want 2", len(repository.results))
	}
	select {
	case <-reports:
		t.Fatal("duplicated report")
	default:
	}
}

type terminalRecoveryReporter struct{ reports chan string }

func (r terminalRecoveryReporter) Generate(_ context.Context, runID string) error {
	r.reports <- runID
	return nil
}

func TestTerminalPersistenceRecoversWithoutRepeatingExecution(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "failed"}[failed], func(t *testing.T) {
			fixture := newRunFixture(t)
			target := domain.RunCompleted
			if failed {
				target = domain.RunFailed
			}
			repository := &terminalRecoveryRepository{fakeRepository: &fakeRepository{fixture: fixture}, unavailable: true, target: target}
			store := credentials.NewMemoryStore()
			ref, _ := credentials.StoreRefFromCredential(fixture.credential)
			_ = store.Set(context.Background(), ref, []byte("test-secret"))
			executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 2), release: make(chan struct{})}
			if failed {
				repository.failSummaryAppend = errors.New("injected result persistence failure")
			}
			diagnostics := make(chan runs.Diagnostic, 20)
			service, err := runs.New(runs.Dependencies{
				Repository: repository, Credentials: store, Executor: executor,
				Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
				ReportDiagnostic: func(d runs.Diagnostic) { diagnostics <- d },
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if err := service.StartRun(context.Background(), fixture.plan.ID); err != nil {
				t.Fatal(err)
			}
			request := <-executor.entered
			close(executor.release)
			deadline := time.After(time.Second)
		waitFailure:
			for {
				select {
				case d := <-diagnostics:
					if d.Operation == "transition_"+string(target) {
						break waitFailure
					}
				case <-deadline:
					t.Fatal("terminal write failure was not reported")
				}
			}
			repository.gate.Lock()
			repository.unavailable = false
			repository.gate.Unlock()
			waitForStatus(t, repository.fakeRepository, target)
			select {
			case <-executor.entered:
				t.Fatal("terminal recovery replayed the provider request")
			default:
			}
			if _, err := request.Credential.Bytes(); err == nil {
				t.Fatal("credential lease retained after execution")
			}
			repository.mu.Lock()
			defer repository.mu.Unlock()
			wantResults := 2
			if failed {
				wantResults = 1
			}
			if len(repository.results) != wantResults {
				t.Fatalf("results = %d, want %d without replay", len(repository.results), wantResults)
			}
		})
	}
}
