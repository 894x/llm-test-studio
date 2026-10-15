package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

// This repository deliberately has no catalog methods. A remote host must only
// need frozen execution input and its own operational storage.
type executionOnlyRepository struct{ delegate *fakeRepository }

func (repository executionOnlyRepository) CreateRun(ctx context.Context, run domain.Run) error {
	return repository.delegate.CreateRun(ctx, run)
}

func (repository executionOnlyRepository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return repository.delegate.GetRun(ctx, id)
}

func (repository executionOnlyRepository) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	return repository.delegate.UpdateRun(ctx, revision, run)
}

func (repository executionOnlyRepository) AppendResults(ctx context.Context, results ...domain.Result) error {
	return repository.delegate.AppendResults(ctx, results...)
}

func preparedFixture(t *testing.T, fixture runFixture) runs.PreparedRun {
	t.Helper()
	meta, err := domain.NewEntityMeta(fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	return runs.PreparedRun{Meta: meta, PlanID: fixture.plan.ID, Snapshot: fixture.snapshot()}
}

func TestHostExecutesTransportedSnapshotWithoutCatalogOrKeyring(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})}
	host, err := runs.NewHost(runs.HostDependencies{
		Repository: executionOnlyRepository{delegate: repository}, Executor: executor,
		Clock: &stepClock{next: fixture.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	encoded, err := json.Marshal(preparedFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	var transported runs.PreparedRun
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatal(err)
	}
	lease, err := credentials.NewTemporaryLease([]byte("node-only-secret"))
	if err != nil {
		t.Fatal(err)
	}
	prepareContext, cancelPreparation := context.WithCancel(context.Background())
	id, err := host.AcceptPrepared(prepareContext, transported, lease)
	if err != nil {
		t.Fatal(err)
	}
	// The accepted input must not retain references to the transport's mutable
	// values, and its activation must not inherit the submission context.
	transported.Snapshot.Entries[0].CaseDefinitions[0].Name = "changed after acceptance"
	transported.Snapshot.Channel.BaseURL = "http://changed.example.test"
	cancelPreparation()
	if err := host.ActivateRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	request := <-executor.entered
	if request.Run.Channel().BaseURL != fixture.channel.BaseURL {
		t.Fatal("accepted run retained the mutable channel input")
	}
	if request.Cases[0].Name != fixture.testCase.Name {
		t.Fatal("accepted run retained the mutable Case input")
	}
	secret, err := request.Credential.Bytes()
	if err != nil || string(secret) != "node-only-secret" {
		t.Fatalf("execution lease = %q, %v", secret, err)
	}
	clear(secret)
	close(executor.release)
	waitForStatus(t, repository, domain.RunCompleted)
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Bytes(); err == nil {
		t.Fatal("host retained a completed task's credential")
	}
}

func TestHostRejectsInvalidOrClosedAcceptanceWithoutPersisting(t *testing.T) {
	for _, scenario := range []string{"invalid_snapshot", "closed_host", "cancelled_context"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newRunFixture(t)
			repository := &fakeRepository{fixture: fixture}
			host, err := runs.NewHost(runs.HostDependencies{
				Repository: executionOnlyRepository{delegate: repository},
				Executor:   &recordingExecutor{}, Clock: &stepClock{next: fixture.now},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close() })
			prepared := preparedFixture(t, fixture)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "invalid_snapshot":
				prepared.Snapshot.SchemaVersion = 0
			case "closed_host":
				_ = host.Close()
			case "cancelled_context":
				cancel()
			}
			lease, err := credentials.NewTemporaryLease([]byte("rejected-secret"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := host.AcceptPrepared(ctx, prepared, lease); err == nil {
				t.Fatal("invalid acceptance succeeded")
			}
			if repository.run.Meta().ID != "" {
				t.Fatal("rejected acceptance persisted a Run")
			}
			if _, err := lease.Bytes(); err == nil {
				t.Fatal("rejected acceptance retained its lease")
			}
		})
	}
}

func TestHostRejectsActiveIdentityReuseAndClosesOnlyNewLease(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	host, err := runs.NewHost(runs.HostDependencies{
		Repository: executionOnlyRepository{delegate: repository},
		Executor:   &recordingExecutor{}, Clock: &stepClock{next: fixture.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	prepared := preparedFixture(t, fixture)
	original, _ := credentials.NewTemporaryLease([]byte("original"))
	if _, err := host.AcceptPrepared(context.Background(), prepared, original); err != nil {
		t.Fatal(err)
	}
	replacement, _ := credentials.NewTemporaryLease([]byte("replacement"))
	_, err = host.AcceptPrepared(context.Background(), prepared, replacement)
	if !errors.Is(err, runs.ErrAlreadyAccepted) {
		t.Fatalf("active identity replacement error = %v", err)
	}
	if _, err := replacement.Bytes(); err == nil {
		t.Fatal("rejected replacement lease remains readable")
	}
	secret, err := original.Bytes()
	clear(secret)
	if err != nil {
		t.Fatal("duplicate acceptance closed the active task's original lease")
	}
}
