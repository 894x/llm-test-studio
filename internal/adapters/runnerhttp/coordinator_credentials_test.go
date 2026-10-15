package runnerhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestCoordinatorRejectsSecretSlotErrorsAndClosesEveryLeaseBeforeConnecting(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	repository, _ := coordinatorRepository(t)
	var opened, closed, posts atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			opened.Add(1)
			return &coordinatorConnection{Client: client, closed: &closed}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	submission := fixture.runSubmission(t, false)
	_ = prepareCoordinatedJob(t, coordinator, submission)
	slot := submission.Job.SecretSlots()[0]
	for _, mutate := range []func(map[string]*credentials.Lease){
		func(leases map[string]*credentials.Lease) { _ = leases[slot].Close() },
		func(leases map[string]*credentials.Lease) {
			leases[coordinatorLocation.ConfigurationID] = leases[slot]
			delete(leases, slot)
		},
		func(leases map[string]*credentials.Lease) { leases[coordinatorLocation.ConfigurationID] = leases[slot] },
	} {
		leases := coordinatorLeases(t, submission)
		mutate(leases)
		if _, err := coordinator.Submit(t.Context(), submission.Job.JobID, leases); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal("invalid secret slot set reached a node")
		}
		for _, lease := range leases {
			if _, err := lease.Bytes(); !errors.Is(err, credentials.ErrClosed) {
				t.Fatal("unused credential was retained")
			}
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	leases := coordinatorLeases(t, submission)
	if _, err := coordinator.Submit(cancelled, submission.Job.JobID, leases); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled caller proceeded to submission")
	}
	if _, err := leases[slot].Bytes(); !errors.Is(err, credentials.ErrClosed) {
		t.Fatal("cancelled caller retained credentials")
	}
	if posts.Load() != 0 || opened.Load() != 1 || closed.Load() != 1 || fixture.requests.Load() != 0 {
		t.Fatal("invalid credentials or cancellation opened another connection")
	}
}

func TestCoordinatorQueriesOriginalAcceptanceAcrossBuildAndMaintenanceChanges(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	repository, _ := coordinatorRepository(t)
	var posts, closed, gets atomic.Int64
	client := coordinatorClient(t, fixture, func(response *http.Response) {
		posts.Add(1)
		_ = response.Body.Close()
		response.Body = io.NopCloser(strings.NewReader("{"))
	})
	changed := false
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed, gets: &gets,
				probe: func(info *jobcontract.RunnerInfo) {
					if changed {
						info.EngineBuildID, info.RunnerVersion = strings.Repeat("c", 64), "other-compatible-release"
						info.Ready, info.MaintenanceOperationID = false, coordinatorLocation.ConfigurationID
					}
				}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	submission := fixture.runSubmission(t, false)
	prepared := prepareCoordinatedJob(t, coordinator, submission)
	if _, err := coordinator.Submit(t.Context(), submission.Job.JobID, coordinatorLeases(t, submission)); !errors.Is(err, jobs.ErrPending) {
		t.Fatal(err)
	}
	changed = true
	confirmed, err := coordinator.Reconcile(t.Context(), submission.Job.JobID)
	if err != nil || confirmed.State != jobs.SubmissionAccepted || gets.Load() != 1 || posts.Load() != 1 {
		t.Fatal("query incorrectly required the prepared executor build or ready state")
	}
	if confirmed.Prepared.Runner.EngineBuildID != prepared.Prepared.Runner.EngineBuildID ||
		confirmed.Receipt.EngineBuildID != prepared.Prepared.Runner.EngineBuildID {
		t.Fatal("query replaced the original actual executor facts")
	}
}

type lateSubmitError struct {
	*coordinatorConnection
	confirmed func()
}

func (backend lateSubmitError) Submit(ctx context.Context, submission *jobcontract.Submission) (jobcontract.Receipt, error) {
	_, err := backend.Client.Submit(ctx, submission)
	if err != nil {
		return jobcontract.Receipt{}, err
	}
	backend.confirmed()
	return jobcontract.Receipt{}, jobs.ErrUnavailable
}

func TestCoordinatorPreservesConcurrentConfirmationWhenSubmitReturnsLateError(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	repository, _ := coordinatorRepository(t)
	var posts, closed atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	var coordinator *jobs.Coordinator
	submission := fixture.runSubmission(t, false)
	var err error
	coordinator, err = jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return lateSubmitError{coordinatorConnection: &coordinatorConnection{Client: client, closed: &closed},
				confirmed: func() {
					if _, err := coordinator.Reconcile(t.Context(), submission.Job.JobID); err != nil {
						t.Error(err)
					}
				}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	_ = prepareCoordinatedJob(t, coordinator, submission)
	confirmed, err := coordinator.Submit(t.Context(), submission.Job.JobID, coordinatorLeases(t, submission))
	if err != nil || confirmed.State != jobs.SubmissionAccepted || posts.Load() != 1 {
		t.Fatal("late connection error obscured a durable concurrent confirmation")
	}
}
