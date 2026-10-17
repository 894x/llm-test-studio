package runnerhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

var coordinatorLocation = jobs.Location{Kind: jobcontract.TargetRemote,
	ConfigurationID: "81000000-0000-4000-8000-000000000001"}

type coordinatorConnection struct {
	*runnerclient.Client
	closed   *atomic.Int64
	gets     *atomic.Int64
	probe    func(*jobcontract.RunnerInfo)
	getError error
}

func (connection *coordinatorConnection) Close() error { connection.closed.Add(1); return nil }

func (connection *coordinatorConnection) Probe(ctx context.Context) (jobcontract.RunnerInfo, error) {
	info, err := connection.Client.Probe(ctx)
	if err == nil && connection.probe != nil {
		connection.probe(&info)
	}
	return info, err
}

func (connection *coordinatorConnection) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	if connection.gets != nil {
		connection.gets.Add(1)
	}
	if connection.getError != nil {
		return jobcontract.Snapshot{}, connection.getError
	}
	snapshot, err := connection.Client.GetJob(ctx, id)
	var remoteError *runnerclient.RemoteError
	if errors.As(err, &remoteError) && remoteError.Code == "not_found" {
		return jobcontract.Snapshot{}, jobs.ErrNotFound
	}
	return snapshot, err
}

func coordinatorClient(
	t *testing.T,
	fixture *httpFixture,
	post func(*http.Response),
) *runnerclient.Client {
	t.Helper()
	identity := fixture.host.Identity()
	client, err := runnerclient.New(runnerclient.Identity{NodeID: identity.Target.NodeID,
		InstallationID: identity.InstallationID},
		func(ctx context.Context, input remotessh.Request) (*http.Response, error) {
			request, err := http.NewRequestWithContext(
				ctx,
				input.Method,
				fixture.api.URL+input.Route,
				input.Body,
			)
			if err != nil {
				return nil, err
			}
			request.Header.Set("Authorization", "Bearer "+nodeToken)
			request.Header.Set(jobcontract.ContractHeader, "1")
			request.Header.Set("Accept-Encoding", "identity")
			if input.JSONGzip {
				request.Header.Set("Accept-Encoding", "gzip")
			}
			if input.Body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := fixture.api.Client().Do(request)
			if err == nil && input.Method == http.MethodPost && input.Route == "/jobs" {
				post(response)
			}
			return response, err
		})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func coordinatorRepository(t *testing.T) (*sqlite.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desktop-outbox.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "coordinator-test"}); err != nil {
		t.Fatal(err)
	}
	return reopenCoordinatorRepository(t, path), path
}

func reopenCoordinatorRepository(t *testing.T, path string) *sqlite.Repository {
	t.Helper()
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

func coordinatorLeases(t *testing.T, submission jobcontract.Submission) map[string]*credentials.Lease {
	t.Helper()
	leases := map[string]*credentials.Lease{}
	for _, secret := range submission.Secrets {
		lease, err := credentials.NewTemporaryLease(secret.Value)
		if err != nil {
			t.Fatal(err)
		}
		leases[secret.Slot] = lease
		t.Cleanup(func() { _ = lease.Close() })
	}
	return leases
}

func prepareCoordinatedJob(t *testing.T, coordinator *jobs.Coordinator, submission jobcontract.Submission) jobs.SubmissionRecord {
	t.Helper()
	record, err := coordinator.Prepare(t.Context(), jobs.PrepareCommand{Location: coordinatorLocation,
		JobID: submission.Job.JobID, Kind: submission.Job.Kind, Payload: submission.Job.Spec, Assets: submission.Job.Assets})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestCoordinatorAutomaticallyReconcilesLostHTTPAcceptanceAfterReopenAndImportsOriginalReports(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison, jobcontract.KindPerformance} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newHTTPFixture(t, true, capabilities())
			submission := fixture.runSubmission(t, kind == jobcontract.KindComparison)
			if kind == jobcontract.KindPerformance {
				submission = fixture.submission(t, nil)
			}
			var posts, opened, closed, submissionConnections, observationConnections atomic.Int64
			intakeDisabled := false
			client := coordinatorClient(t, fixture, func(response *http.Response) {
				posts.Add(1)
				if response.StatusCode != http.StatusAccepted {
					t.Error("acceptance was not committed before truncation")
				}
				_ = response.Body.Close()
				response.Body = io.NopCloser(strings.NewReader("{"))
			})
			repository, path := coordinatorRepository(t)
			dependencies := jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
				Open: func(_ context.Context, location jobs.Location, access jobs.Access) (jobs.Backend, error) {
					if location != coordinatorLocation {
						return nil, jobs.ErrTargetChanged
					}
					switch access {
					case jobs.SubmissionAccess:
						if intakeDisabled {
							return nil, jobs.ErrUnavailable
						}
						submissionConnections.Add(1)
					case jobs.ObservationAccess:
						observationConnections.Add(1)
					default:
						return nil, jobs.ErrInvalid
					}
					opened.Add(1)
					return &coordinatorConnection{Client: client, closed: &closed}, nil
				}}
			coordinator, err := jobs.NewCoordinator(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			prepared := prepareCoordinatedJob(t, coordinator, submission)
			view, err := repository.Job(t.Context(), submission.Job.JobID)
			if err != nil || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionPrepared || view.Execution != nil {
				t.Fatal("prepared query fabricated a remote execution")
			}
			if prepared.Prepared.Runner.RunnerVersion != "runner-test-1" ||
				prepared.Prepared.Spec.EngineBuildID != fixture.host.Identity().EngineBuildID {
				t.Fatal("prepared job did not use the actual runner")
			}
			leases := coordinatorLeases(t, submission)
			pending, err := coordinator.Submit(t.Context(), submission.Job.JobID, leases)
			if !errors.Is(err, jobs.ErrPending) || pending.State != jobs.SubmissionPending || pending.Receipt != nil {
				t.Fatalf("lost response was treated as rejection or confirmation: %v", err)
			}
			view, err = repository.Job(t.Context(), submission.Job.JobID)
			if err != nil || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionPending || view.Execution != nil {
				t.Fatal("lost acknowledgement was inferred from missing local execution")
			}
			for _, lease := range leases {
				if _, err := lease.Bytes(); !errors.Is(err, credentials.ErrClosed) {
					t.Fatal("coordinator retained a caller credential")
				}
			}
			if err := repository.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.release()
			waitHTTPJob(
				t,
				fixture,
				submission.Job.JobID,
				domain.RunCompleted,
			)
			requests := fixture.requests.Load()
			repository = reopenCoordinatorRepository(t, path)
			dependencies.Repository = repository
			intakeDisabled = true
			coordinator, err = jobs.NewCoordinator(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			confirmations, err := jobs.NewConfirmationWatcher(t.Context(), jobs.ConfirmationDependencies{
				Repository: repository, Reconcile: coordinator.Reconcile,
			})
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- confirmations.Run() }()
			t.Cleanup(func() { _ = confirmations.Close() })
			deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			for {
				confirmed, err := repository.GetSubmission(deadline, submission.Job.JobID)
				if err != nil {
					t.Fatal(err)
				}
				if confirmed.State == jobs.SubmissionAccepted {
					if confirmed.Receipt.JobID != submission.Job.JobID {
						t.Fatal("automatic recovery changed the original task")
					}
					break
				}
				select {
				case <-deadline.Done():
					t.Fatal("background query did not restore original acceptance")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err := confirmations.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-finished; !errors.Is(err, context.Canceled) {
				t.Fatalf("confirmation worker shutdown: %v", err)
			}
			if posts.Load() != 1 || fixture.requests.Load() != requests || requests == 0 {
				t.Fatal("read-only recovery submitted or repeated provider work")
			}
			if submissionConnections.Load() != 2 || observationConnections.Load() != 1 {
				t.Fatal("recovery did not use observation access independently of disabled intake")
			}
			if _, err := coordinator.Submit(t.Context(), submission.Job.JobID, nil); err != nil {
				t.Fatal("repeated lookup of an accepted submission required another credential or connection")
			}
			view, err = repository.Job(t.Context(), submission.Job.JobID)
			if err != nil || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionAccepted || view.Execution != nil {
				t.Fatal("accepted but unsynchronized task was relabeled as queued")
			}
			cache, _ := syncDocuments(t)
			synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: repository, Documents: cache})
			if err != nil {
				t.Fatal(err)
			}
			origin := remoteresults.Origin{ConfigurationID: coordinatorLocation.ConfigurationID,
				NodeID: prepared.Prepared.Runner.NodeID, InstallationID: prepared.Prepared.Runner.NodeInstallationID}
			deadline, cancel = context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			expectedDocuments := 1
			if kind == jobcontract.KindComparison || kind == jobcontract.KindPerformance {
				expectedDocuments = 2
			}
			for {
				request := remotesync.Request{Origin: origin, JobID: submission.Job.JobID, Remote: client}
				if _, err := synchronizer.Observe(deadline, request); err != nil {
					t.Fatal(err)
				}
				documents, err := repository.ListRemoteDocuments(deadline, submission.Job.JobID)
				if err != nil {
					t.Fatal(err)
				}
				for _, kind := range []jobcontract.EventKind{jobcontract.EventPerformanceResult, jobcontract.EventReport} {
					for _, document := range documents {
						if document.Kind != kind {
							continue
						}
						if _, err := synchronizer.ImportDocument(deadline, remotesync.DocumentRequest{
							Request: request, Document: document,
						}); err != nil {
							t.Fatal(err)
						}
					}
				}
				documents, err = repository.ListRemoteDocuments(deadline, submission.Job.JobID)
				if err != nil {
					t.Fatal(err)
				}
				info, err := client.Probe(deadline)
				if err != nil {
					t.Fatal(err)
				}
				if info.Pending == 0 && len(documents) == expectedDocuments {
					for _, document := range documents {
						if !document.Imported {
							t.Fatal("original report was not imported")
						}
					}
					break
				}
				select {
				case <-deadline.Done():
					t.Fatal(deadline.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if opened.Load() != closed.Load() || posts.Load() != 1 || fixture.requests.Load() != requests {
				t.Fatal("coordinator retained a connection or replayed paid work")
			}
			fixture.api.Close()
			view, err = repository.Job(t.Context(), submission.Job.JobID)
			if err != nil || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionAccepted ||
				view.Execution == nil || view.Execution.Status != domain.RunCompleted ||
				view.Executor.Version != prepared.Prepared.Runner.RunnerVersion {
				t.Fatalf("offline submitted execution query lost its original state or runner: %v", err)
			}
			if view.Location != coordinatorLocation || view.Executor.NodeID != origin.NodeID {
				t.Fatal("offline submitted query changed the fixed origin")
			}
			if kind == jobcontract.KindPerformance {
				result, err := repository.PerformanceResult(t.Context(), submission.Job.JobID)
				if err != nil || !view.PerformanceResultReady || view.Progress == nil || result.Observation == nil ||
					result.ObservationState != jobcontract.PerformanceAvailable {
					t.Fatalf("offline submitted query did not expose the full imported result: %v", err)
				}
				if result.ReportID != submission.Job.Spec.Performance.ReportID || result.Executor != view.Executor {
					t.Fatal("offline performance result lost its frozen input or executor")
				}
			} else if view.PerformanceResultReady {
				t.Fatal("run or comparison was projected as a performance result")
			}
			if fixture.requests.Load() != requests || posts.Load() != 1 {
				t.Fatal("offline presentation query accessed execution")
			}
		})
	}
}
