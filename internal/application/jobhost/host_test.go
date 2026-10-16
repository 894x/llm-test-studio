package jobhost_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type countedGuard struct {
	mu                 sync.Mutex
	reserved, released int
}

type countedReservation struct{ guard *countedGuard }

func (guard *countedGuard) Reserve(context.Context, jobcontract.JobSpec) (io.Closer, error) {
	guard.mu.Lock()
	guard.reserved++
	guard.mu.Unlock()
	return countedReservation{guard: guard}, nil
}

func (reservation countedReservation) Close() error {
	reservation.guard.mu.Lock()
	defer reservation.guard.mu.Unlock()
	reservation.guard.released++
	return nil
}

type controlledExecution struct {
	request jobhost.Execution
	context context.Context
}

type controlledCore struct {
	entered  chan controlledExecution
	release  chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	calls    int
	settled  map[string]jobhost.Outcome
}

func newControlledCore() *controlledCore {
	return &controlledCore{
		entered: make(chan controlledExecution, 16), release: make(chan struct{}), stopped: make(chan struct{}),
		settled: map[string]jobhost.Outcome{},
	}
}

func (core *controlledCore) Execute(ctx context.Context, execution jobhost.Execution) (jobhost.Outcome, error) {
	core.mu.Lock()
	core.calls++
	core.mu.Unlock()
	core.entered <- controlledExecution{request: execution, context: ctx}
	select {
	case <-ctx.Done():
		return jobhost.Outcome{Status: domain.RunCancelled}, nil
	case <-core.release:
		return jobhost.Outcome{Status: domain.RunCompleted}, nil
	case <-execution.StopSending:
		core.stopOnce.Do(func() { close(core.stopped) })
		select {
		case <-ctx.Done():
			return jobhost.Outcome{Status: domain.RunCancelled}, nil
		case <-core.release:
			return jobhost.Outcome{Status: domain.RunCompleted}, nil
		}
	}
}

func (core *controlledCore) Settle(_ context.Context, spec jobcontract.JobSpec, outcome jobhost.Outcome) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.settled[spec.JobID] = outcome
	return nil
}

func openHostRepository(t *testing.T) *sqlite.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "host.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "host-test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

func testSubmission(t *testing.T) jobcontract.Submission {
	t.Helper()
	preparer := quicktest.New(quicktest.Dependencies{})
	prepared, lease, code := preparer.BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: "https://example.test/v1", APIKey: "host-only-secret", ModelID: "test-model",
		LoadMode: domain.LoadFixedConcurrency, RequestCount: 1, Concurrency: 1,
		TimeoutMS: 1000, InputTokens: 32, OutputTokens: 16,
	})
	if code != "" {
		t.Fatalf("prepare fixture: %s", code)
	}
	secret, err := lease.Bytes()
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return jobcontract.Submission{
		Job: jobcontract.JobSpec{
			Contract: jobcontract.CurrentContract, JobID: meta.ID,
			NodeInstallationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EngineBuildID: strings.Repeat("b", 64),
			Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"},
			Kind:   jobcontract.KindPerformance,
			Spec:   jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
		},
		Secrets: []jobcontract.Secret{{Slot: prepared.ReportID, Value: secret}},
	}
}

func retrySubmission(spec jobcontract.JobSpec, secret string) jobcontract.Submission {
	return jobcontract.Submission{
		Job:     spec,
		Secrets: []jobcontract.Secret{{Slot: spec.Spec.Performance.ReportID, Value: []byte(secret)}},
	}
}

func hostDependencies(repository jobhost.Repository, core jobhost.Executor, guard jobhost.Guard) jobhost.Dependencies {
	policy := jobhost.RemotePolicy()
	policy.ExecutionTimeout = 30 * time.Second
	return jobhost.Dependencies{
		Repository: repository, Executor: core, Guard: guard, Clock: wallClock{}, Policy: policy,
		Target: jobcontract.ExecutionTarget{
			Kind: jobcontract.TargetRemote, NodeID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		},
		InstallationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EngineBuildID: strings.Repeat("b", 64),
		NodeID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", RunnerVersion: "host-test",
		CommitmentKey: bytes.Repeat([]byte{7}, 32),
	}
}

func acceptedOrigin(spec jobcontract.JobSpec) domain.ExecutionOrigin {
	nodeID := localBackendNodeID
	if spec.Target.Kind == jobcontract.TargetRemote {
		nodeID = spec.Target.NodeID
	}
	return domain.ExecutionOrigin{
		Kind: string(spec.Target.Kind), NodeID: nodeID,
		InstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID,
		RunnerVersion: "host-test", OS: "linux", Arch: "amd64",
	}
}

func newTestHost(t *testing.T, dependencies jobhost.Dependencies) *jobhost.Host {
	t.Helper()
	host, err := jobhost.New(t.Context(), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func enteredExecution(t *testing.T, core *controlledCore) controlledExecution {
	t.Helper()
	select {
	case execution := <-core.entered:
		return execution
	case <-time.After(3 * time.Second):
		t.Fatal("execution did not start")
		return controlledExecution{}
	}
}

func waitHost(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("host condition did not become true")
}

func waitJob(t *testing.T, repository jobhost.Repository, id string, status domain.RunStatus) jobcontract.Snapshot {
	t.Helper()
	var snapshot jobcontract.Snapshot
	waitHost(t, func() bool {
		var err error
		snapshot, err = repository.GetJob(t.Context(), id)
		return err == nil && snapshot.Receipt.Status == status
	})
	return snapshot
}

func TestHostOwnsAcceptedContextSecretsAndFrozenInput(t *testing.T) {
	repository, core, guard := openHostRepository(t), newControlledCore(), &countedGuard{}
	host := newTestHost(t, hostDependencies(repository, core, guard))
	submission := testSubmission(t)
	spec := submission.Job
	secretBytes := submission.Secrets[0].Value
	ctx, cancel := context.WithCancel(t.Context())
	snapshot, created, err := host.Submit(ctx, &submission)
	if err != nil || !created {
		t.Fatalf("submit: created=%v err=%v", created, err)
	}
	cancel()
	secretCleared := len(submission.Secrets) == 0 && bytes.Equal(secretBytes, make([]byte, len(secretBytes)))
	if snapshot.Receipt.JobID != spec.JobID || !secretCleared {
		t.Fatal("transport secret was retained or identity changed")
	}
	submission.Job.Spec.Performance.ModelID = "mutated-after-submit"
	execution := enteredExecution(t, core)
	if execution.context.Err() != nil || execution.request.Spec.Spec.Performance.ModelID != "test-model" {
		t.Fatal("execution inherited client cancellation or mutable input")
	}
	lease := execution.request.Secrets[spec.Spec.Performance.ReportID]
	secret, err := lease.Bytes()
	if err != nil || string(secret) != "host-only-secret" {
		t.Fatal("host lost its owned lease")
	}
	clear(secret)
	close(core.release)
	waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunCompleted,
	)
	waitHost(t, func() bool { return host.Status().Active == 0 })
	if _, err := lease.Bytes(); err == nil {
		t.Fatal("completed job retained its lease")
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.reserved != 1 || guard.released != 1 {
		t.Fatal("job did not release exactly its reservation")
	}
}

type lostAcceptReply struct{ jobhost.Repository }

func (repository lostAcceptReply) AcceptJob(
	ctx context.Context,
	input jobhost.Acceptance,
) (jobcontract.Snapshot, bool, error) {
	if _, _, err := repository.Repository.AcceptJob(ctx, input); err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	return jobcontract.Snapshot{}, false, errors.New("lost_storage_confirmation")
}

func TestHostResolvesLostAdmissionConfirmationWithoutReexecution(t *testing.T) {
	repository, core, guard := openHostRepository(t), newControlledCore(), &countedGuard{}
	host := newTestHost(t, hostDependencies(lostAcceptReply{Repository: repository}, core, guard))
	submission := testSubmission(t)
	spec, err := submission.Job.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := host.Submit(t.Context(), &submission); err != nil || !created {
		t.Fatalf("lost commit acknowledgement not resolved: created=%v err=%v", created, err)
	}
	enteredExecution(t, core)
	retry := retrySubmission(spec, "host-only-secret")
	if _, created, err := host.Submit(t.Context(), &retry); err != nil || created {
		t.Fatalf("same submission not idempotent: created=%v err=%v", created, err)
	}
	changed := retrySubmission(spec, "different-secret")
	if _, _, err := host.Submit(t.Context(), &changed); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatal(err)
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.calls != 1 {
		t.Fatalf("duplicate execution count=%d", core.calls)
	}
}

func TestHostQueueCapacityExpiryAndRetryDoNotSendExtraRequests(t *testing.T) {
	repository, core, guard := openHostRepository(t), newControlledCore(), &countedGuard{}
	dependencies := hostDependencies(repository, core, guard)
	dependencies.Policy.MaxQueuedJobs, dependencies.Policy.QueueTimeout = 1, 60*time.Millisecond
	host := newTestHost(t, dependencies)
	first := testSubmission(t)
	firstSpec, _ := first.Job.Freeze()
	if _, _, err := host.Submit(t.Context(), &first); err != nil {
		t.Fatal(err)
	}
	enteredExecution(t, core)
	second := testSubmission(t)
	secondID := second.Job.JobID
	if _, _, err := host.Submit(t.Context(), &second); err != nil {
		t.Fatal(err)
	}
	third := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &third); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatal(err)
	}
	retry := retrySubmission(firstSpec, "host-only-secret")
	if _, created, err := host.Submit(t.Context(), &retry); err != nil || created {
		t.Fatalf("full-queue retry=%v", err)
	}
	expired := waitJob(
		t,
		repository,
		secondID,
		domain.RunFailed,
	)
	if expired.Failure == nil || expired.Failure.ErrorCode != "job_queue_timeout" {
		t.Fatal("queued expiry reason differs")
	}
	waitHost(t, func() bool { return host.Status().Queued == 0 })
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.calls != 1 {
		t.Fatal("expired queued job sent traffic")
	}
}

func TestHostExecutionDeadlineAndShutdownReasons(t *testing.T) {
	for _, scenario := range []string{"deadline", "remote_shutdown", "local_shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			repository, core := openHostRepository(t), newControlledCore()
			dependencies := hostDependencies(repository, core, &countedGuard{})
			submission := testSubmission(t)
			status := domain.RunFailed
			if scenario == "deadline" {
				dependencies.Policy.ExecutionTimeout = 2 * time.Second
			}
			if scenario == "local_shutdown" {
				dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
				submission.Job.Target, status = dependencies.Target, domain.RunCancelled
			}
			host := newTestHost(t, dependencies)
			id := submission.Job.JobID
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			execution := enteredExecution(t, core)
			if scenario != "deadline" {
				if err := host.Close(); err != nil {
					t.Fatal(err)
				}
			}
			terminal := waitJob(
				t,
				repository,
				id,
				status,
			)
			if scenario != "local_shutdown" {
				code := domain.ErrorCode("runner_interrupted")
				if scenario == "deadline" {
					code = "job_execution_timeout"
				}
				if terminal.Failure == nil || terminal.Failure.ErrorCode != code {
					t.Fatal("terminal reason differs")
				}
			}
			waitHost(t, func() bool { return host.Status().Active == 0 })
			for _, lease := range execution.request.Secrets {
				if _, err := lease.Bytes(); err == nil {
					t.Fatal("terminal job retained lease")
				}
			}
		})
	}
}

func TestHostStopDrainsThenCompletesAndCancelIsDurable(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
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
		Contract: jobcontract.CurrentContract, JobID: id, CommandID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		Kind: jobcontract.CommandStopSending, ExpectedRevision: running.Receipt.Revision,
	}
	confirmation, _, err := host.ApplyCommand(t.Context(), command)
	if err != nil || confirmation.Snapshot.Receipt.Status != domain.RunDraining {
		t.Fatal(err)
	}
	select {
	case <-core.stopped:
	case <-time.After(time.Second):
		t.Fatal("durable stop signal was not delivered")
	}
	if execution.context.Err() != nil {
		t.Fatal("stop cancelled in-flight execution")
	}
	close(core.release)
	waitJob(
		t,
		repository,
		id,
		domain.RunCompleted,
	)
	if _, created, err := host.ApplyCommand(t.Context(), command); err != nil || created {
		t.Fatal(err)
	}
}

func TestHostRecoverySealsOwnedJobsWithoutSendingAndPreservesForeignJobs(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	owned := testSubmission(t)
	foreign := testSubmission(t)
	foreign.Job.NodeInstallationID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	for _, submission := range []jobcontract.Submission{owned, foreign} {
		_, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{ContentClaims: []jobcontent.Claim{},
			Spec: submission.Job, Origin: acceptedOrigin(submission.Job),
			SecretCommitment: strings.Repeat("f", 64), AcceptedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	terminal := waitJob(
		t,
		repository,
		owned.Job.JobID,
		domain.RunFailed,
	)
	if terminal.Failure == nil || terminal.Failure.ErrorCode != "runner_interrupted" {
		t.Fatal("restart reason differs")
	}
	current, err := repository.GetJob(t.Context(), foreign.Job.JobID)
	if err != nil || current.Receipt.Status != domain.RunQueued {
		t.Fatal("recovery changed foreign owner")
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.calls != 0 || len(core.settled) != 1 {
		t.Fatal("recovery executed or settled a foreign job")
	}
	owned.Clear()
	foreign.Clear()
}

func TestHostProgressIsThrottledAndDoesNotExposeSecrets(t *testing.T) {
	repository, core := openHostRepository(t), newControlledCore()
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := testSubmission(t)
	id := submission.Job.JobID
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	execution := enteredExecution(t, core)
	for count := 0; count < 3; count++ {
		execution.request.OnProgress(quicktest.PerformanceProgress{Phase: load.PhaseSending})
	}
	close(core.release)
	waitJob(
		t,
		repository,
		id,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	page, err := repository.JobEvents(
		t.Context(),
		id,
		0,
		200,
	)
	if err != nil {
		t.Fatal(err)
	}
	progressCount := 0
	for _, event := range page.Events {
		if event.Kind == jobcontract.EventProgress {
			progressCount++
		}
		if bytes.Contains(event.Payload, []byte("host-only-secret")) {
			t.Fatal("secret entered the journal")
		}
	}
	if progressCount != 1 {
		t.Fatalf("unthrottled progress count=%d", progressCount)
	}
}
