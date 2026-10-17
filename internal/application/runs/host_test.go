package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

// This repository deliberately has no catalog methods. A remote host must only
// need frozen execution input and its own operational storage.
type executionOnlyRepository struct{ delegate *fakeRepository }

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
	run, err := domain.NewRun(transported.Meta, transported.PlanID, transported.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(prepareContext, run); err != nil {
		t.Fatal(err)
	}
	id := transported.Meta.ID
	if err := host.AdoptQueued(prepareContext, id, lease); err != nil {
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

func TestHostRejectsNonInitialOrClosedAdoptionWithoutWriting(t *testing.T) {
	for _, scenario := range []string{
		"missing_run", "closed_host", "cancelled_context", "already_started", "terminal", "wrong_identity",
	} {
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
			run, err := domain.NewRun(prepared.Meta, prepared.PlanID, prepared.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			repository.run = run
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "missing_run":
				repository.run = domain.Run{}
			case "closed_host":
				_ = host.Close()
			case "cancelled_context":
				cancel()
			case "already_started":
				repository.run, err = run.Transition(domain.RunStarting, fixture.now.Add(time.Second))
			case "terminal":
				repository.run, err = run.Transition(domain.RunCancelled, fixture.now.Add(time.Second))
			case "wrong_identity":
				prepared.Meta, err = domain.NewEntityMeta(fixture.now)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := repository.run
			lease, err := credentials.NewTemporaryLease([]byte("rejected-secret"))
			if err != nil {
				t.Fatal(err)
			}
			if err := host.AdoptQueued(ctx, prepared.Meta.ID, lease); err == nil {
				t.Fatal("invalid adoption succeeded")
			}
			if !reflect.DeepEqual(before, repository.run) {
				t.Fatal("rejected adoption changed the stored Run")
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
	run, err := domain.NewRun(prepared.Meta, prepared.PlanID, prepared.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	original, _ := credentials.NewTemporaryLease([]byte("original"))
	if err := host.AdoptQueued(context.Background(), prepared.Meta.ID, original); err != nil {
		t.Fatal(err)
	}
	replacement, _ := credentials.NewTemporaryLease([]byte("replacement"))
	err = host.AdoptQueued(context.Background(), prepared.Meta.ID, replacement)
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

type blockingAdmissionRepository struct {
	*fakeRepository
	entered chan struct{}
	release chan struct{}
}

func (repository blockingAdmissionRepository) CreateRun(ctx context.Context, run domain.Run) error {
	close(repository.entered)
	<-repository.release
	return repository.fakeRepository.CreateRun(ctx, run)
}

func TestLocalAdmissionAndCloseDoNotLeaveAnUnownedQueuedRun(t *testing.T) {
	fixture := newRunFixture(t)
	repository := blockingAdmissionRepository{
		fakeRepository: &fakeRepository{fixture: fixture},
		entered:        make(chan struct{}), release: make(chan struct{}),
	}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: credentials.NewMemoryStore(), Executor: &recordingExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	lease, err := credentials.NewTemporaryLease([]byte("local-admission-secret"))
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedFixture(t, fixture)
	accepted := make(chan error, 1)
	go func() {
		_, err := service.AcceptPrepared(t.Context(), prepared, lease)
		accepted <- err
	}()
	<-repository.entered
	closing := make(chan struct{})
	closed := make(chan error, 1)
	go func() {
		close(closing)
		closed <- service.Close()
	}()
	<-closing
	close(repository.release)
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetRun(t.Context(), prepared.Meta.ID)
	if err != nil || stored.Status() != domain.RunCancelled {
		t.Fatalf("shutdown left a queued Run: status=%s err=%v", stored.Status(), err)
	}
	if _, err := lease.Bytes(); err == nil {
		t.Fatal("shutdown retained the admitted Run's lease")
	}
}

func TestLocalAdmissionRejectsInvalidPreparedInputBeforeWriting(t *testing.T) {
	for _, scenario := range []string{"invalid_snapshot", "advanced_revision", "closed", "cancelled_context"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newRunFixture(t)
			repository := &fakeRepository{fixture: fixture}
			service, err := runs.New(runs.Dependencies{
				Repository: repository, Credentials: credentials.NewMemoryStore(), Executor: &recordingExecutor{},
				Clock:       &stepClock{next: fixture.now},
				Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			prepared := preparedFixture(t, fixture)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "invalid_snapshot":
				prepared.Snapshot.SchemaVersion = 0
			case "advanced_revision":
				prepared.Meta.Revision = 2
			case "closed":
				_ = service.Close()
			case "cancelled_context":
				cancel()
			}
			lease, err := credentials.NewTemporaryLease([]byte("rejected-secret"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.AcceptPrepared(ctx, prepared, lease); err == nil {
				t.Fatal("invalid local admission succeeded")
			}
			if repository.run.Meta().ID != "" {
				t.Fatal("invalid local admission created execution facts")
			}
			if _, err := lease.Bytes(); err == nil {
				t.Fatal("invalid local admission retained its lease")
			}
		})
	}
}
