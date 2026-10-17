package jobhost_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const localBackendNodeID = "84000000-0000-4000-8000-000000000001"

func TestLocalBackendControlsAndReplaysOneCommandWithoutOwningExecution(t *testing.T) {
	for _, kind := range []jobcontract.CommandKind{jobcontract.CommandStopSending, jobcontract.CommandCancel} {
		t.Run(string(kind), func(t *testing.T) {
			repository := openHostRepository(t)
			core := newControlledCore()
			dependencies := hostDependencies(repository, core, &countedGuard{})
			dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
			host := newTestHost(t, dependencies)
			backend, err := jobs.NewLocalBackend(localBackendDependencies(host, repository))
			if err != nil {
				t.Fatal(err)
			}
			submission := testSubmission(t)
			submission.Job.Target = dependencies.Target
			id := submission.Job.JobID
			if _, err := backend.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			_ = enteredExecution(t, core)
			snapshot, err := backend.GetJob(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := domain.NewEntityMeta(time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			command := jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: meta.ID, JobID: id,
				Kind: kind, ExpectedRevision: snapshot.Receipt.Revision}
			confirmation, err := backend.Command(t.Context(), command)
			if err != nil || confirmation.Command != command {
				t.Fatalf("local management handle did not apply the command: %v", err)
			}
			_ = backend.Close()
			want := domain.RunCancelled
			if kind == jobcontract.CommandStopSending {
				select {
				case <-core.stopped:
				case <-time.After(2 * time.Second):
					t.Fatal("local backend did not deliver the drain signal")
				}
				close(core.release)
				want = domain.RunCompleted
			}
			waitPerformanceJob(
				t,
				repository,
				id,
				want,
			)
			fresh, err := jobs.NewLocalBackend(localBackendDependencies(host, repository))
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			replayed, err := fresh.Command(t.Context(), command)
			if err != nil || replayed.Command != command || !replayed.AcceptedAt.Equal(confirmation.AcceptedAt) {
				t.Fatalf("new handle did not recover the stable original command: %v", err)
			}
			if _, err := backend.Command(t.Context(), command); !errors.Is(err, jobs.ErrUnavailable) {
				t.Fatal("closed local handle still accepted commands")
			}
		})
	}
}

func localBackendDependencies(host *jobhost.Host, repository jobs.LocalReadRepository) jobs.LocalBackendDependencies {
	return jobs.LocalBackendDependencies{Host: host, Repository: repository}
}

func TestLocalBackendCoordinatesAllKindsAndConnectionCloseKeepsAcceptedWork(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison, jobcontract.KindPerformance} {
		t.Run(string(kind), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			releaseRequests := sync.OnceFunc(func() { close(release) })
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if requests.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-release:
				case <-request.Context().Done():
					return
				}
				if request.Header.Get("Authorization") == "Bearer host-only-secret" {
					writePerformanceCoreResponse(writer)
					return
				}
				writeCoreResponse(writer)
			}))
			defer server.Close()
			defer releaseRequests()
			repository := openHostRepository(t)
			core, err := jobhost.NewCore(jobhost.CoreDependencies{Repository: repository,
				Runs: runs.NewProtocolExecutor(http.DefaultTransport), Transport: http.DefaultTransport, Clock: wallClock{}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = core.Close() })
			dependencies := hostDependencies(repository, core, &countedGuard{})
			dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
			dependencies.Policy.MaxActiveJobs = 2
			host := newTestHost(t, dependencies)
			var connections atomic.Int64
			coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
				Open: func(_ context.Context, location jobs.Location, _ jobs.Access) (jobs.Backend, error) {
					if location != (jobs.Location{Kind: jobcontract.TargetLocal}) {
						return nil, jobs.ErrInvalid
					}
					connections.Add(1)
					return jobs.NewLocalBackend(localBackendDependencies(host, repository))
				}})
			if err != nil {
				t.Fatal(err)
			}
			original := coreSubmission(t, server.URL, kind == jobcontract.KindComparison)
			if kind == jobcontract.KindPerformance {
				original.Clear()
				original = performanceCoreSubmission(t, server.URL, nil)
			}
			defer original.Clear()
			prepared, err := coordinator.Prepare(t.Context(), jobs.PrepareCommand{
				Location: jobs.Location{Kind: jobcontract.TargetLocal}, JobID: original.Job.JobID,
				Kind: original.Job.Kind, Payload: original.Job.Spec, Assets: original.Job.Assets,
			})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Prepared.Runner.OS != runtime.GOOS || prepared.Prepared.Runner.Arch != runtime.GOARCH ||
				prepared.Prepared.Spec.Target.Kind != jobcontract.TargetLocal {
				t.Fatal("local preparation reported another execution position")
			}
			leases := map[string]*credentials.Lease{}
			for _, secret := range original.Secrets {
				lease, err := credentials.NewTemporaryLease(secret.Value)
				if err != nil {
					t.Fatal(err)
				}
				leases[secret.Slot] = lease
			}
			caller, cancel := context.WithCancel(t.Context())
			accepted, err := coordinator.Submit(caller, original.Job.JobID, leases)
			cancel()
			if err != nil || accepted.State != jobs.SubmissionAccepted {
				t.Fatalf("local acceptance: %v", err)
			}
			for _, lease := range leases {
				if _, err := lease.Bytes(); !errors.Is(err, credentials.ErrClosed) {
					t.Fatal("local submission retained the caller lease")
				}
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("closing the local management handle stopped accepted execution")
			}
			if host.Status().Active != 1 || connections.Load() != 2 {
				t.Fatal("local calls did not share the application-owned host")
			}
			if _, err := coordinator.Submit(t.Context(), original.Job.JobID, nil); err != nil {
				t.Fatal(err)
			}
			if connections.Load() != 2 {
				t.Fatal("accepted local lookup opened or submitted another job")
			}
			releaseRequests()
			waitPerformanceJob(
				t,
				repository,
				original.Job.JobID,
				domain.RunCompleted,
			)
			waitSettled(t, repository)
			if kind == jobcontract.KindPerformance {
				result, err := repository.ReadPerformanceResult(t.Context(), original.Job.JobID)
				if err != nil || result.Result.ObservationState != jobcontract.PerformanceAvailable {
					t.Fatalf("local performance was not sealed: %v", err)
				}
			} else {
				reports, err := repository.ListReportsForRuns(t.Context(), accepted.Receipt.RunIDs)
				if err != nil || len(reports) != len(accepted.Receipt.RunIDs) {
					t.Fatalf("local child reports were not sealed: %v", err)
				}
			}
		})
	}
}

type unconfirmedLocalBackend struct{ *jobs.LocalBackend }

func TestLocalBackendUsesTheInjectedConcurrentJobPolicy(t *testing.T) {
	repository := openHostRepository(t)
	core := newControlledCore()
	dependencies := hostDependencies(repository, core, &countedGuard{})
	dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
	dependencies.Policy.MaxActiveJobs = 2
	host := newTestHost(t, dependencies)
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return jobs.NewLocalBackend(localBackendDependencies(host, repository))
		}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		submission := testSubmission(t)
		defer submission.Clear()
		_, err := coordinator.Prepare(t.Context(), jobs.PrepareCommand{
			Location: jobs.Location{Kind: jobcontract.TargetLocal}, JobID: submission.Job.JobID,
			Kind: submission.Job.Kind, Payload: submission.Job.Spec, Assets: submission.Job.Assets,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := credentials.NewTemporaryLease(submission.Secrets[0].Value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := coordinator.Submit(t.Context(), submission.Job.JobID,
			map[string]*credentials.Lease{submission.Secrets[0].Slot: lease}); err != nil {
			t.Fatal(err)
		}
		_ = enteredExecution(t, core)
	}
	if host.Status().Active != 2 || host.Status().Queued != 0 {
		t.Fatal("local backend imposed the remote node's single-job policy")
	}
}

func (backend unconfirmedLocalBackend) Submit(
	ctx context.Context,
	submission *jobcontract.Submission,
) (jobcontract.Receipt, error) {
	_, err := backend.LocalBackend.Submit(ctx, submission)
	if err != nil {
		return jobcontract.Receipt{}, err
	}
	return jobcontract.Receipt{}, jobs.ErrUnavailable
}

func TestLocalBackendRecoversOriginalAcceptanceAcrossOwnerBuildChange(t *testing.T) {
	repository := openHostRepository(t)
	core := newControlledCore()
	dependencies := hostDependencies(repository, core, &countedGuard{})
	dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
	host := newTestHost(t, dependencies)
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(_ context.Context, _ jobs.Location, access jobs.Access) (jobs.Backend, error) {
			backend, err := jobs.NewLocalBackend(localBackendDependencies(host, repository))
			if err != nil {
				return nil, err
			}
			if access == jobs.SubmissionAccess {
				return unconfirmedLocalBackend{LocalBackend: backend}, nil
			}
			return backend, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	submission := testSubmission(t)
	defer submission.Clear()
	prepared, err := coordinator.Prepare(t.Context(), jobs.PrepareCommand{
		Location: jobs.Location{Kind: jobcontract.TargetLocal}, JobID: submission.Job.JobID,
		Kind: submission.Job.Kind, Payload: submission.Job.Spec, Assets: submission.Job.Assets,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := credentials.NewTemporaryLease(submission.Secrets[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := coordinator.Submit(t.Context(), submission.Job.JobID,
		map[string]*credentials.Lease{submission.Secrets[0].Slot: lease})
	if !errors.Is(err, jobs.ErrPending) || pending.State != jobs.SubmissionPending {
		t.Fatal("lost local confirmation was reinterpreted")
	}
	_ = enteredExecution(t, core)
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	dependencies.Executor = newControlledCore()
	dependencies.EngineBuildID = strings.Repeat("c", 64)
	host = newTestHost(t, dependencies)
	confirmed, err := coordinator.Reconcile(t.Context(), submission.Job.JobID)
	if err != nil || confirmed.State != jobs.SubmissionAccepted {
		t.Fatalf("local acceptance query across build change: %v", err)
	}
	if confirmed.Receipt.Origin.EngineBuildID != prepared.Prepared.Spec.EngineBuildID {
		t.Fatal("read-only recovery rewrote the original local executor")
	}
	snapshot, err := repository.GetJob(t.Context(), submission.Job.JobID)
	if err != nil || snapshot.Receipt.Status != domain.RunCancelled {
		t.Fatal("management close and application owner close have the same lifetime")
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.calls != 1 {
		t.Fatal("read-only recovery repeated local execution")
	}
}

func TestLocalBackendRejectsForeignTargetsClosedHandlesAndUnsupportedContracts(t *testing.T) {
	repository := openHostRepository(t)
	core := newControlledCore()
	dependencies := hostDependencies(repository, core, &countedGuard{})
	remoteHost := newTestHost(t, dependencies)
	if _, err := jobs.NewLocalBackend(localBackendDependencies(remoteHost, repository)); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatal("remote host was relabeled as local")
	}
	_ = remoteHost.Close()
	dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
	host := newTestHost(t, dependencies)
	backend, err := jobs.NewLocalBackend(localBackendDependencies(host, repository))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if _, err := backend.GetJob(t.Context(), "85000000-0000-4000-8000-000000000001"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("authoritative local miss lost its lookup meaning")
	}
	foreign := testSubmission(t)
	defer foreign.Clear()
	if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{
		ContentClaims: []jobcontent.Claim{},
		Spec:          foreign.Job, Origin: acceptedOrigin(foreign.Job),
		SecretCommitment: strings.Repeat("d", 64), AcceptedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.GetJob(t.Context(), foreign.Job.JobID); !errors.Is(err, jobs.ErrTargetChanged) {
		t.Fatal("local connection read a foreign execution target")
	}
	for _, expected := range []error{jobs.ErrTargetChanged, jobs.ErrUpgradeRequired, jobs.ErrUnavailable} {
		submission := testSubmission(t)
		switch expected {
		case jobs.ErrUpgradeRequired:
			submission.Job.Contract++
		case jobs.ErrUnavailable:
			_ = backend.Close()
		}
		if _, err := backend.Submit(t.Context(), &submission); !errors.Is(err, expected) {
			t.Fatalf("local boundary classification: got %v want %v", err, expected)
		}
		if len(submission.Secrets) != 0 {
			t.Fatal("rejected local submission retained transport secrets")
		}
	}
}
