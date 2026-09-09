package runs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestBackgroundTransitionFailurePersistsFailedRun(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunRunning, domain.RunDraining} {
		t.Run(string(status), func(t *testing.T) {
			harness := startTransitionFailure(t, status, false)
			waitForStatus(t, harness.repository.fakeRepository, domain.RunFailed)
			if err := harness.service.Close(); err != nil {
				t.Fatal(err)
			}
			stored, err := harness.repository.GetRun(context.Background(), harness.runID)
			if err != nil {
				t.Fatal(err)
			}
			failure := stored.Failure()
			wantFailure := domain.RunFailure{
				Phase: domain.ErrorCode("transition_" + string(status)), ErrorCode: "run_state_transition_failed",
			}
			if failure == nil || *failure != wantFailure {
				t.Fatalf("failure = %#v, want correlated transition failure", failure)
			}
			harness.assertNoExecution(t)
		})
	}
}

func TestBackgroundTransitionFailureRetainsRecoveryUntilTerminal(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		name := "storage_recovers"
		if cancelRun {
			name = "cancellation_wins"
		}
		t.Run(name, func(t *testing.T) {
			harness := startTransitionFailure(t, domain.RunRunning, true)
			select {
			case <-harness.terminalFailure:
			case <-time.After(time.Second):
				t.Fatal("background failure never attempted to persist a terminal state")
			}
			if _, err := harness.credential.lease.Bytes(); err == nil {
				t.Fatal("terminal recovery retains the credential lease")
			}
			want := domain.RunFailed
			if cancelRun {
				if err := harness.service.CancelRun(context.Background(), harness.runID); err != nil {
					t.Fatalf("CancelRun during recovery: %v", err)
				}
				want = domain.RunCancelled
			}
			harness.repository.gate.Lock()
			harness.repository.unavailable = false
			harness.repository.gate.Unlock()
			waitForStatus(t, harness.repository.fakeRepository, want)
			if err := harness.service.Close(); err != nil {
				t.Fatal(err)
			}
			stored, _ := harness.repository.GetRun(context.Background(), harness.runID)
			if stored.Status() != want {
				t.Fatalf("final status = %q, want %q", stored.Status(), want)
			}
			harness.assertNoExecution(t)
		})
	}
}

type transitionFailureHarness struct {
	service         *runs.Service
	repository      *terminalRecoveryRepository
	credential      *transitionFailureCredentialStore
	executor        *recordingExecutor
	runID           string
	terminalFailure chan struct{}
}

func startTransitionFailure(
	t *testing.T,
	status domain.RunStatus,
	terminalUnavailable bool,
) *transitionFailureHarness {
	t.Helper()
	fixture := newRunFixture(t)
	store := credentials.NewMemoryStore()
	ref, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), ref, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	harness := &transitionFailureHarness{
		repository: &terminalRecoveryRepository{
			fakeRepository: &fakeRepository{
				fixture: fixture, failUpdateStatus: status,
				updateErr: errors.New("injected background transition failure"),
			},
			unavailable: terminalUnavailable, target: domain.RunFailed,
		},
		credential:      &transitionFailureCredentialStore{store: store},
		executor:        &recordingExecutor{},
		terminalFailure: make(chan struct{}, 1),
	}
	harness.service, err = runs.New(runs.Dependencies{
		Repository: harness.repository, Credentials: harness.credential, Executor: harness.executor,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		ReportDiagnostic: func(diagnostic runs.Diagnostic) {
			if diagnostic.Operation == "transition_failed" {
				harness.terminalFailure <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = harness.service.Close() })
	command := runs.StartCommand{PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID}
	harness.runID, err = harness.service.PrepareTarget(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if status == domain.RunDraining {
		// Queue the same early stop request exercised by the existing draining
		// transition diagnostic test, before the execution goroutine can start.
		err := harness.service.StopSending(context.Background(), harness.runID)
		if !errors.Is(err, runs.ErrNotActive) {
			t.Fatalf("StopSending(queued) = %v, want ErrNotActive", err)
		}
	}
	if err := harness.service.ActivateRun(context.Background(), harness.runID); err != nil {
		t.Fatal(err)
	}
	return harness
}

func (harness *transitionFailureHarness) assertNoExecution(t *testing.T) {
	t.Helper()
	if harness.executor.calls != 0 {
		t.Fatalf("provider executions = %d, want zero", harness.executor.calls)
	}
	if _, err := harness.credential.lease.Bytes(); err == nil {
		t.Fatal("credential lease remains readable after transition failure")
	}
}

type transitionFailureCredentialStore struct {
	store *credentials.MemoryStore
	lease *credentials.Lease
}

func (store *transitionFailureCredentialStore) Get(
	ctx context.Context,
	ref credentials.StoreRef,
) (*credentials.Lease, error) {
	lease, err := store.store.Get(ctx, ref)
	store.lease = lease
	return lease, err
}
