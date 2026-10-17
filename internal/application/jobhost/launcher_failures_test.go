package jobhost_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestLauncherReleasesFailedGroupAndPerformancePreparation(t *testing.T) {
	for _, kind := range []jobs.LaunchKind{jobs.LaunchComparison, jobs.LaunchPerformance} {
		t.Run(string(kind), func(t *testing.T) {
			repository := openHostRepository(t)
			var connections atomic.Int64
			coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
				Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
					connections.Add(1)
					return nil, jobs.ErrUnavailable
				}})
			if err != nil {
				t.Fatal(err)
			}
			input := coreSubmission(t, "http://provider.example.test", true)
			if kind == jobs.LaunchPerformance {
				input.Clear()
				input = performanceCoreSubmission(t, "http://provider.example.test", nil)
			}
			defer input.Clear()
			preparation := &launcherPreparation{input: input, leases: []*credentials.Lease{},
				err: errors.New("group preparation failed"), code: quicktest.ErrorInvalidRequest}
			launcher := launcherFor(t, coordinator, preparation)
			_, err = launcher.Launch(t.Context(), launchCommand(kind))
			if kind == jobs.LaunchComparison {
				if !errors.Is(err, preparation.err) || len(preparation.leases) != len(input.Secrets) {
					t.Fatal("failed group preparation lost its leases or error")
				}
			} else {
				var failure jobs.PreparationError
				if !errors.As(err, &failure) || failure.Code != quicktest.ErrorInvalidRequest {
					t.Fatal("performance preparation lost its safe error code")
				}
				preparation.code = domain.ErrorCode("bad code with private data")
				_, err = launcher.Launch(t.Context(), launchCommand(kind))
				if !errors.As(err, &failure) || failure.Code != quicktest.ErrorInvalidRequest {
					t.Fatal("performance preparation leaked a malformed error code")
				}
			}
			assertLaunchLeasesClosed(t, preparation)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			preparation.after = cancel
			if _, err := launcher.Launch(ctx, launchCommand(kind)); !errors.Is(err, context.Canceled) {
				t.Fatal("preparation completion hid a canceled launch")
			}
			assertLaunchLeasesClosed(t, preparation)
			page, err := repository.ListPendingSubmissions(t.Context(), "", jobs.MaxSubmissionPageSize)
			if err != nil || len(page.Records) != 0 || connections.Load() != 0 {
				t.Fatal("failed preparation connected or persisted a delivery intent")
			}
		})
	}
}

func TestLauncherReturnsTheOriginalPendingIdentityWithoutResubmission(t *testing.T) {
	repository := openHostRepository(t)
	core := newControlledCore()
	dependencies := hostDependencies(repository, core, &countedGuard{})
	dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
	host := newTestHost(t, dependencies)
	var posts atomic.Int64
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(_ context.Context, _ jobs.Location, access jobs.Access) (jobs.Backend, error) {
			backend, err := jobs.NewLocalBackend(localBackendDependencies(host, repository))
			if err != nil {
				return nil, err
			}
			if access == jobs.SubmissionAccess {
				return launcherUnconfirmedBackend{LocalBackend: backend, posts: &posts}, nil
			}
			return backend, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	input := performanceCoreSubmission(t, "http://provider.example.test", nil)
	defer input.Clear()
	preparation := &launcherPreparation{input: input, leases: []*credentials.Lease{}}
	pending, err := launcherFor(t, coordinator, preparation).Launch(t.Context(), launchCommand(jobs.LaunchPerformance))
	if !errors.Is(err, jobs.ErrPending) || pending.State != jobs.SubmissionPending {
		t.Fatal("lost acceptance did not preserve its durable delivery identity")
	}
	assertLaunchLeasesClosed(t, preparation)
	_ = enteredExecution(t, core)
	accepted, err := coordinator.Reconcile(t.Context(), pending.Prepared.Spec.JobID)
	if err != nil || accepted.State != jobs.SubmissionAccepted ||
		accepted.Prepared.Spec.JobID != pending.Prepared.Spec.JobID {
		t.Fatal("launch confirmation did not recover the original accepted job")
	}
	if posts.Load() != 1 {
		t.Fatal("reconciling a launch submitted another execution")
	}
	close(core.release)
	waitPerformanceJob(
		t,
		repository,
		accepted.Prepared.Spec.JobID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
}

type launcherUnconfirmedBackend struct {
	*jobs.LocalBackend
	posts *atomic.Int64
}

func (backend launcherUnconfirmedBackend) Submit(
	ctx context.Context,
	submission *jobcontract.Submission,
) (jobcontract.Receipt, error) {
	backend.posts.Add(1)
	return unconfirmedLocalBackend{LocalBackend: backend.LocalBackend}.Submit(ctx, submission)
}
