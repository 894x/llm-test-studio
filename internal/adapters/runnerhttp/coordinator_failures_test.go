package runnerhttp_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type failingSendIntentRepository struct {
	*sqlite.Repository
	commitThenFail bool
}

func (repository failingSendIntentRepository) UpdateSubmission(
	ctx context.Context,
	next jobs.SubmissionRecord,
	expected uint64,
) error {
	if next.State == jobs.SubmissionPending && expected == 1 {
		if repository.commitThenFail {
			if err := repository.Repository.UpdateSubmission(ctx, next, expected); err != nil {
				return err
			}
		}
		return errors.New("injected send intent failure")
	}
	return repository.Repository.UpdateSubmission(ctx, next, expected)
}

func TestCoordinatorRejectsMissingPerformanceArchiveCapabilityAndUnavailableRemote(t *testing.T) {
	caps := capabilities()
	caps.Features = slices.DeleteFunc(caps.Features, func(feature string) bool {
		return feature == jobcontract.FeaturePerformanceArchives
	})
	fixture := newHTTPFixture(t, false, caps)
	var posts, closed, localOpened atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	repository, _ := coordinatorRepository(t)
	unavailable := false
	unsupported := false
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(_ context.Context, location jobs.Location, access jobs.Access) (jobs.Backend, error) {
			if location.Kind == jobcontract.TargetLocal {
				localOpened.Add(1)
			}
			if unsupported {
				return nil, jobcontract.ErrUnsupported
			}
			if unavailable {
				return nil, errors.New("untrusted-connection-diagnostic")
			}
			return &coordinatorConnection{Client: client, closed: &closed}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	performance := fixture.submission(t, nil)
	command := jobs.PrepareCommand{Location: coordinatorLocation, JobID: performance.Job.JobID,
		Kind: performance.Job.Kind, Payload: performance.Job.Spec, Assets: performance.Job.Assets}
	if _, err := coordinator.Prepare(t.Context(), command); !errors.Is(err, jobs.ErrUpgradeRequired) {
		t.Fatal("missing report-transfer capability was accepted")
	}
	if _, err := repository.GetSubmission(t.Context(), command.JobID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("unsupported task was persisted for submission")
	}
	// A missing Performance feature does not prevent a supported ordinary Run.
	run := fixture.runSubmission(t, false)
	_ = prepareCoordinatedJob(t, coordinator, run)
	unsupported = true
	if _, err := coordinator.Prepare(t.Context(), command); !errors.Is(err, jobs.ErrUpgradeRequired) {
		t.Fatal("unsupported current protocol did not request an explicit upgrade")
	}
	unsupported = false
	unavailable = true
	if _, err := coordinator.Prepare(t.Context(), command); !errors.Is(err, jobs.ErrUnavailable) ||
		strings.Contains(err.Error(), "untrusted-connection-diagnostic") {
		t.Fatal("unavailable node leaked diagnostics or accepted work")
	}
	if posts.Load() != 0 || localOpened.Load() != 0 {
		t.Fatal("preparation submitted work or fell back to local")
	}
}

func TestCoordinatorDoesNotSendOnChangedExecutorOrUnconfirmedOutboxCommit(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	var posts, closed atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	repository, _ := coordinatorRepository(t)
	var change func(*jobcontract.RunnerInfo)
	var queryError error
	dependencies := jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed, getError: queryError, probe: func(info *jobcontract.RunnerInfo) {
				if change != nil {
					change(info)
				}
			}}, nil
		}}
	coordinator, err := jobs.NewCoordinator(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*jobcontract.RunnerInfo){
		func(info *jobcontract.RunnerInfo) { info.EngineBuildID = strings.Repeat("c", 64) },
		func(info *jobcontract.RunnerInfo) { info.NodeInstallationID = coordinatorLocation.ConfigurationID },
		func(info *jobcontract.RunnerInfo) { info.NodeID = coordinatorLocation.ConfigurationID },
	} {
		submission := fixture.runSubmission(t, false)
		_ = prepareCoordinatedJob(t, coordinator, submission)
		change = mutate
		leases := coordinatorLeases(t, submission)
		if _, err := coordinator.Submit(t.Context(), submission.Job.JobID, leases); !errors.Is(err, jobs.ErrTargetChanged) {
			t.Fatal("changed probed executor accepted a new submission")
		}
		for _, lease := range leases {
			if _, err := lease.Bytes(); !errors.Is(err, credentials.ErrClosed) {
				t.Fatal("preflight rejection retained a credential")
			}
		}
		stored, err := repository.GetSubmission(t.Context(), submission.Job.JobID)
		if err != nil || stored.State != jobs.SubmissionPrepared {
			t.Fatal("preflight rejection advanced send intent")
		}
		change = nil
	}
	for _, committed := range []bool{false, true} {
		dependencies.Repository = failingSendIntentRepository{Repository: repository, commitThenFail: committed}
		coordinator, err := jobs.NewCoordinator(dependencies)
		if err != nil {
			t.Fatal(err)
		}
		submission := fixture.runSubmission(t, false)
		_ = prepareCoordinatedJob(t, coordinator, submission)
		if _, err := coordinator.Submit(t.Context(), submission.Job.JobID, coordinatorLeases(t, submission)); err == nil {
			t.Fatal("failed send-intent acknowledgement authorized a POST")
		}
		stored, err := repository.GetSubmission(t.Context(), submission.Job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		expected := jobs.SubmissionPrepared
		if committed {
			expected = jobs.SubmissionPending
		}
		if stored.State != expected {
			t.Fatal("send-intent failure was represented as an execution outcome")
		}
		if committed {
			observed, err := coordinator.Reconcile(t.Context(), submission.Job.JobID)
			if err != nil || observed.State != jobs.SubmissionPending || observed.Observation != jobs.ObservationNotFound {
				t.Fatal("authoritative 404 did not remain pending")
			}
			if _, err := coordinator.Submit(t.Context(), submission.Job.JobID, coordinatorLeases(t, submission)); !errors.Is(err, jobs.ErrPending) {
				t.Fatal("pending intent became automatically resubmittable")
			}
			change = func(info *jobcontract.RunnerInfo) { info.Contract++ }
			observed, err = coordinator.Reconcile(t.Context(), submission.Job.JobID)
			if !errors.Is(err, jobs.ErrUpgradeRequired) || observed.State != jobs.SubmissionPending {
				t.Fatal("unsupported protocol during reconciliation did not require explicit upgrade")
			}
			change = nil
			for _, failure := range []error{jobcontract.ErrUnsupported, jobs.ErrUpgradeRequired} {
				queryError = failure
				observed, err = coordinator.Reconcile(t.Context(), submission.Job.JobID)
				if !errors.Is(err, jobs.ErrUpgradeRequired) || observed.State != jobs.SubmissionPending {
					t.Fatal("unsupported task response after a successful probe was hidden as unreachable")
				}
			}
			queryError = nil
			change = func(info *jobcontract.RunnerInfo) { info.NodeInstallationID = coordinatorLocation.ConfigurationID }
			observed, err = coordinator.Reconcile(t.Context(), submission.Job.JobID)
			if err != nil || observed.State != jobs.SubmissionPending ||
				observed.Observation != jobs.ObservationIdentityChanged {
				t.Fatal("changed installation was queried or replaced the fixed destination")
			}
			change = nil
		}
	}
	if posts.Load() != 0 || fixture.requests.Load() != 0 {
		t.Fatal("preflight or storage failure sent paid work")
	}
}

func TestIndependentCoordinatorsSendOnlyAfterWinningDurableIntent(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	var posts, closed atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	first, path := coordinatorRepository(t)
	second := reopenCoordinatorRepository(t, path)
	barrier := make(chan struct{})
	release := sync.OnceFunc(func() { close(barrier) })
	t.Cleanup(release)
	entered := make(chan struct{}, 2)
	block := false
	dependencies := jobs.CoordinatorDependencies{Repository: first, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed, probe: func(*jobcontract.RunnerInfo) {
				if block {
					entered <- struct{}{}
					<-barrier
				}
			}}, nil
		}}
	coordinator, err := jobs.NewCoordinator(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	submission := fixture.runSubmission(t, false)
	_ = prepareCoordinatedJob(t, coordinator, submission)
	dependencies.Repository = second
	other, err := jobs.NewCoordinator(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	block = true
	done := make(chan error, 2)
	for _, launcher := range []*jobs.Coordinator{coordinator, other} {
		leases := coordinatorLeases(t, submission)
		go func() { _, err := launcher.Submit(t.Context(), submission.Job.JobID, leases); done <- err }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("coordinators did not reach the same prepared revision")
		}
	}
	release()
	var accepted, conflicts int
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				accepted++
				continue
			}
			if errors.Is(err, jobs.ErrConflict) {
				conflicts++
				continue
			}
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("coordinator did not release its management connection")
		}
	}
	if accepted != 1 || conflicts != 1 || posts.Load() != 1 {
		t.Fatal("a losing durable intent sent another submission")
	}
	stored, err := first.GetSubmission(t.Context(), submission.Job.JobID)
	if err != nil || stored.State != jobs.SubmissionAccepted {
		t.Fatal("winning submission was not confirmed")
	}
}

type confirmationFaultRepository struct {
	*sqlite.Repository
	mode               string
	failure            error
	armed              atomic.Bool
	confirmationWrites atomic.Int64
}

func (repository *confirmationFaultRepository) GetSubmission(
	ctx context.Context,
	id string,
) (jobs.SubmissionRecord, error) {
	if repository.armed.Load() && repository.mode == "read failure" {
		return jobs.SubmissionRecord{}, repository.failure
	}
	record, err := repository.Repository.GetSubmission(ctx, id)
	if err == nil && repository.armed.Load() && repository.mode == "preparation conflict" {
		record.Prepared.Runner.RunnerVersion = "different-prepared-runner"
	}
	return record, err
}

func (repository *confirmationFaultRepository) UpdateSubmission(
	ctx context.Context,
	next jobs.SubmissionRecord,
	expected uint64,
) error {
	if !repository.armed.Load() || next.State != jobs.SubmissionAccepted {
		return repository.Repository.UpdateSubmission(ctx, next, expected)
	}
	repository.confirmationWrites.Add(1)
	switch repository.mode {
	case "write failure":
		return repository.failure
	case "lost acknowledgement":
		if err := repository.Repository.UpdateSubmission(ctx, next, expected); err != nil {
			return err
		}
		return repository.failure
	case "CAS exhaustion":
		return jobs.ErrConflict
	default:
		return repository.Repository.UpdateSubmission(ctx, next, expected)
	}
}

func TestCoordinatorPreservesDurablePendingIdentityAfterConfirmationStorageFailure(t *testing.T) {
	for _, test := range []struct {
		mode          string
		writes        int64
		pending       bool
		storedConfirm bool
	}{
		{mode: "read failure", pending: true},
		{mode: "write failure", writes: 1, pending: true},
		{mode: "lost acknowledgement", writes: 1, pending: true, storedConfirm: true},
		{mode: "CAS exhaustion", writes: 3, pending: true},
		{mode: "preparation conflict"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			fixture := newHTTPFixture(t, true, capabilities())
			repository, path := coordinatorRepository(t)
			failure := errors.New("injected confirmation storage failure")
			store := &confirmationFaultRepository{Repository: repository, mode: test.mode, failure: failure}
			var posts, opened, closed atomic.Int64
			client := coordinatorClient(t, fixture, func(response *http.Response) {
				posts.Add(1)
				if response.StatusCode != http.StatusAccepted {
					t.Error("confirmation fault was not preceded by committed HTTP acceptance")
				}
				store.armed.Store(true)
			})
			dependencies := jobs.CoordinatorDependencies{Repository: store, Now: time.Now,
				Open: func(_ context.Context, location jobs.Location, _ jobs.Access) (jobs.Backend, error) {
					if location != coordinatorLocation {
						return nil, jobs.ErrTargetChanged
					}
					opened.Add(1)
					return &coordinatorConnection{Client: client, closed: &closed}, nil
				}}
			coordinator, err := jobs.NewCoordinator(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			submission := fixture.runSubmission(t, false)
			prepared := prepareCoordinatedJob(t, coordinator, submission)
			leases := coordinatorLeases(t, submission)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			pending, err := coordinator.Submit(ctx, submission.Job.JobID, leases)
			cause := failure
			if test.mode == "CAS exhaustion" || test.mode == "preparation conflict" {
				cause = jobs.ErrConflict
			}
			if !errors.Is(err, cause) || errors.Is(err, jobs.ErrPending) != test.pending {
				t.Fatalf("confirmation error lost pending delivery or its original cause: %v", err)
			}
			if pending.Validate() != nil || pending.State != jobs.SubmissionPending || pending.Receipt != nil ||
				pending.Prepared.Spec.JobID != prepared.Prepared.Spec.JobID || pending.Revision != 2 {
				t.Fatal("accepted upstream work lost its durable pending identity")
			}
			if test.pending && !pending.SamePreparation(prepared) {
				t.Fatal("confirmation failure changed the original destination or input")
			}
			if posts.Load() != 1 || store.confirmationWrites.Load() != test.writes {
				t.Fatal("confirmation retry posted another job or exceeded its CAS budget")
			}
			for _, lease := range leases {
				if _, err := lease.Bytes(); !errors.Is(err, credentials.ErrClosed) {
					t.Fatal("confirmation failure retained submission credentials")
				}
			}
			stored, err := repository.GetSubmission(ctx, submission.Job.JobID)
			if err != nil {
				t.Fatal(err)
			}
			storedState := jobs.SubmissionPending
			if test.storedConfirm {
				storedState = jobs.SubmissionAccepted
			}
			if stored.State != storedState || !stored.SamePreparation(prepared) {
				t.Fatal("storage acknowledgement failure changed the durable instruction")
			}
			fixture.release()
			waitHTTPJob(
				t,
				fixture,
				submission.Job.JobID,
				domain.RunCompleted,
			)
			requests := fixture.requests.Load()
			if requests == 0 {
				t.Fatal("confirmation fixture did not execute a real Core request")
			}
			if err := repository.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := reopenCoordinatorRepository(t, path)
			dependencies.Repository = reopened
			recovery, err := jobs.NewCoordinator(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := recovery.Submit(ctx, submission.Job.JobID, nil)
			if test.storedConfirm {
				if err != nil || observed.State != jobs.SubmissionAccepted {
					t.Fatalf("lost stored acknowledgement was not recovered by a read: %v", err)
				}
			} else if !errors.Is(err, jobs.ErrPending) || observed.State != jobs.SubmissionPending {
				t.Fatalf("reopened pending intent became resubmittable: %v", err)
			}
			confirmed, err := recovery.Reconcile(ctx, submission.Job.JobID)
			if err != nil || confirmed.State != jobs.SubmissionAccepted || confirmed.Receipt == nil ||
				confirmed.Prepared.Spec.JobID != prepared.Prepared.Spec.JobID {
				t.Fatalf("read-only reconciliation did not recover the original acceptance: %v", err)
			}
			if !test.storedConfirm && confirmed.Receipt.Status != domain.RunCompleted {
				t.Fatal("reconciliation replaced the actual terminal receipt")
			}
			if posts.Load() != 1 || fixture.requests.Load() != requests || opened.Load() != closed.Load() {
				t.Fatal("confirmation recovery resent work or leaked a management connection")
			}
		})
	}
}
