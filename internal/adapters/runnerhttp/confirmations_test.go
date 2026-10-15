package runnerhttp_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
)

func TestConfirmationWatcherReachesLaterPendingJobsAfterAnUnavailablePage(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	repository, _ := coordinatorRepository(t)
	var posts, closed atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{
		Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	unsent := fixture.runSubmission(t, false)
	unsent.Job.JobID = "00000000-0000-4000-8000-000000000001"
	prepareCoordinatedJob(t, coordinator, unsent)
	ids := []string{}
	for range 7 {
		submission := fixture.runSubmission(t, false)
		pending := prepareCoordinatedJob(t, coordinator, submission)
		pending.State, pending.Revision = jobs.SubmissionPending, 2
		if err := repository.UpdateSubmission(t.Context(), pending, 1); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, submission.Job.JobID)
	}
	slices.Sort(ids)
	var mu sync.Mutex
	observed := map[string]int{}
	watcher, err := jobs.NewConfirmationWatcher(t.Context(), jobs.ConfirmationDependencies{
		Repository: repository,
		Reconcile: func(ctx context.Context, id string) (jobs.SubmissionRecord, error) {
			mu.Lock()
			observed[id]++
			mu.Unlock()
			if id <= ids[2] {
				return jobs.SubmissionRecord{}, jobs.ErrUnavailable
			}
			return coordinator.Reconcile(ctx, id)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- watcher.Run() }()
	t.Cleanup(func() { _ = watcher.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	for {
		last, err := repository.GetSubmission(ctx, ids[len(ids)-1])
		if err != nil {
			t.Fatal(err)
		}
		if last.Observation == jobs.ObservationNotFound {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("an unavailable first page starved later pending jobs")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range ids {
		if observed[id] != 1 {
			t.Fatal("pending page skipped a job or retried before visiting the tail")
		}
	}
	if observed[unsent.Job.JobID] != 0 {
		t.Fatal("prepared work was queried before it had a durable sending intent")
	}
	prepared, err := repository.GetSubmission(t.Context(), unsent.Job.JobID)
	if err != nil || prepared.State != jobs.SubmissionPrepared || prepared.Revision != 1 {
		t.Fatal("background scan changed an unsent preparation")
	}
	if posts.Load() != 0 || fixture.requests.Load() != 0 {
		t.Fatal("confirmation recovery submitted work")
	}
}

func TestConfirmationWatcherBoundsQueriesAndJoinsCanceledCalls(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	repository, _ := coordinatorRepository(t)
	var closed atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { t.Error("unexpected submission") })
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{
		Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 7 {
		pending := prepareCoordinatedJob(t, coordinator, fixture.runSubmission(t, false))
		pending.State, pending.Revision = jobs.SubmissionPending, 2
		if err := repository.UpdateSubmission(t.Context(), pending, 1); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan struct{}, 7)
	var active atomic.Int64
	watcher, err := jobs.NewConfirmationWatcher(t.Context(), jobs.ConfirmationDependencies{
		Repository: repository,
		Reconcile: func(ctx context.Context, _ string) (jobs.SubmissionRecord, error) {
			active.Add(1)
			defer active.Add(-1)
			entered <- struct{}{}
			<-ctx.Done()
			return jobs.SubmissionRecord{}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- watcher.Run() }()
	t.Cleanup(func() { _ = watcher.Close() })
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("bounded confirmation page did not start")
		}
	}
	if err := watcher.Run(); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("second worker loop was allowed")
	}
	select {
	case <-entered:
		t.Fatal("more than four queries entered at once")
	default:
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if active.Load() != 0 || watcher.Snapshot().Running {
		t.Fatal("close returned before all observations stopped")
	}
}
