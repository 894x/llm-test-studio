package jobhost_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type blockedProgressRepository struct {
	*sqlite.Repository
	entered  chan struct{}
	release  chan struct{}
	returned chan struct{}
	failed   bool
	once     sync.Once
	writes   atomic.Int64
}

func (repository *blockedProgressRepository) RecordJobProgress(
	ctx context.Context,
	id string,
	progress quicktest.PerformanceProgress,
	at time.Time,
) error {
	repository.writes.Add(1)
	repository.once.Do(func() { close(repository.entered) })
	select {
	case <-repository.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if repository.failed {
		return errors.New("injected delayed progress storage failure")
	}
	return repository.Repository.RecordJobProgress(
		ctx,
		id,
		progress,
		at,
	)
}

func (repository *blockedProgressRepository) RecordPerformanceObservation(
	ctx context.Context,
	input jobhost.PerformanceObservationWrite,
) (jobcontract.PerformanceObservation, error) {
	observation, err := repository.Repository.RecordPerformanceObservation(ctx, input)
	if err == nil && input.Checkpoint.Boundary == quicktest.PerformanceBoundaryReturned {
		close(repository.returned)
	}
	return observation, err
}

func TestSlowProgressStorageDoesNotBlockMeasuredTrafficAndIsJoinedBeforeSettlement(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			var requests atomic.Int64
			allRequests := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writePerformanceCoreResponse(writer)
				if requests.Add(1) == 3 {
					close(allRequests)
				}
			}))
			defer server.Close()
			repository := openHostRepository(t)
			blocked := &blockedProgressRepository{
				Repository: repository, entered: make(chan struct{}), release: make(chan struct{}),
				returned: make(chan struct{}), failed: failed,
			}
			release := sync.OnceFunc(func() { close(blocked.release) })
			defer release()
			core := newPerformanceCore(t, blocked)
			dependencies := hostDependencies(blocked, core, &countedGuard{})
			dependencies.Policy.ExecutionTimeout = 30 * time.Second
			host := newTestHost(t, dependencies)
			submission := performanceCoreSubmission(t, server.URL, func(command *quicktest.PerformanceCommand) {
				command.LoadMode, command.Concurrency = domain.LoadOpenLoop, 0
				command.RequestCount, command.RatePerSecond, command.MaxInFlight = 3, 100, 2
			})
			spec := submission.Job
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			for _, event := range []<-chan struct{}{blocked.entered, allRequests, blocked.returned} {
				select {
				case <-event:
				case <-time.After(3 * time.Second):
					t.Fatal("progress storage blocked the measured scheduler or its final execution observation")
				}
			}
			current, err := repository.GetJob(t.Context(), spec.JobID)
			if err != nil || jobcontract.Terminal(current.Receipt.Status) || !host.Status().Ready {
				t.Fatal("owner settled before joining progress persistence or isolated before a known failure")
			}
			release()
			status := domain.RunCompleted
			if failed {
				status = domain.RunFailed
			}
			terminal := waitPerformanceJob(
				t,
				repository,
				spec.JobID,
				status,
			)
			waitSettled(t, repository)
			if failed && (terminal.Failure == nil || terminal.Failure.ErrorCode != "job_storage_unavailable") {
				t.Fatal("delayed progress storage error lost its owner failure reason")
			}
			if host.Status().Ready == failed || requests.Load() != 3 {
				t.Fatal("progress storage failure was ignored or caused repeated paid traffic")
			}
			if failed && blocked.writes.Load() != 1 {
				t.Fatal("known failed storage received more display projection writes")
			}
		})
	}
}
