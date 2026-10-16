package jobhost_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// The existing preparers have separate catalog tests. This port fixture selects
// their frozen outputs while the delivery, Core, database and HTTP are real.
type launcherPreparation struct {
	input  jobcontract.Submission
	leases []*credentials.Lease
	called jobs.LaunchKind
	err    error
	code   domain.ErrorCode
	after  func()
}

func (preparation *launcherPreparation) lease(slot string) *credentials.Lease {
	for _, secret := range preparation.input.Secrets {
		if secret.Slot == slot {
			lease, _ := credentials.NewTemporaryLease(secret.Value)
			preparation.leases = append(preparation.leases, lease)
			return lease
		}
	}
	return nil
}

func (preparation *launcherPreparation) BuildTarget(
	context.Context,
	runs.StartCommand,
) (runs.PreparedRun, *credentials.Lease, error) {
	preparation.called = jobs.LaunchPlan
	input := *preparation.input.Job.Spec.Run
	lease := preparation.lease(input.Meta.ID)
	if preparation.after != nil {
		preparation.after()
	}
	return input, lease, preparation.err
}

func (preparation *launcherPreparation) BuildQuickTask(
	ctx context.Context,
	_ runs.QuickTaskCommand,
) (runs.PreparedRun, *credentials.Lease, error) {
	input, lease, err := preparation.BuildTarget(ctx, runs.StartCommand{})
	preparation.called = jobs.LaunchQuickTask
	return input, lease, err
}

func (preparation *launcherPreparation) Build(
	context.Context,
	comparisons.StartCommand,
) (comparisons.PreparedComparison, map[string]*credentials.Lease, error) {
	preparation.called = jobs.LaunchComparison
	input := *preparation.input.Job.Spec.Comparison
	leases := map[string]*credentials.Lease{}
	for _, run := range input.Runs {
		leases[run.Meta.ID] = preparation.lease(run.Meta.ID)
	}
	if preparation.after != nil {
		preparation.after()
	}
	return input, leases, preparation.err
}

func (preparation *launcherPreparation) BuildPerformance(
	context.Context,
	quicktest.PerformanceCommand,
) (quicktest.PreparedPerformance, *credentials.Lease, domain.ErrorCode) {
	preparation.called = jobs.LaunchPerformance
	input := *preparation.input.Job.Spec.Performance
	lease := preparation.lease(input.ReportID)
	if preparation.after != nil {
		preparation.after()
	}
	return input, lease, preparation.code
}

func launchCommand(kind jobs.LaunchKind) jobs.LaunchCommand {
	command := jobs.LaunchCommand{Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: kind}
	switch kind {
	case jobs.LaunchPlan:
		command.Plan = &runs.StartCommand{}
	case jobs.LaunchQuickTask:
		command.QuickTask = &runs.QuickTaskCommand{}
	case jobs.LaunchComparison:
		command.Comparison = &comparisons.StartCommand{}
	case jobs.LaunchPerformance:
		command.Performance = &quicktest.PerformanceCommand{}
	}
	return command
}

func launcherFor(
	t *testing.T,
	coordinator *jobs.Coordinator,
	preparation *launcherPreparation,
) *jobs.Launcher {
	t.Helper()
	launcher, err := jobs.NewLauncher(jobs.LauncherDependencies{Coordinator: coordinator,
		Runs: preparation, Comparisons: preparation, Performance: preparation, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	return launcher
}

func assertLaunchLeasesClosed(t *testing.T, preparation *launcherPreparation) {
	t.Helper()
	for _, lease := range preparation.leases {
		if _, err := lease.Bytes(); !errors.Is(err, credentials.ErrClosed) {
			t.Fatal("launcher retained a preparation credential")
		}
	}
}

func TestLauncherRoutesEveryPreparationThroughOneLocalOwner(t *testing.T) {
	for _, kind := range []jobs.LaunchKind{
		jobs.LaunchPlan, jobs.LaunchQuickTask, jobs.LaunchComparison, jobs.LaunchPerformance,
	} {
		t.Run(string(kind), func(t *testing.T) {
			var requests atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Header.Get("Authorization") == "Bearer host-only-secret" {
					writePerformanceCoreResponse(writer)
					return
				}
				writeCoreResponse(writer)
			}))
			defer provider.Close()
			repository := openHostRepository(t)
			core, err := jobhost.NewCore(jobhost.CoreDependencies{Repository: repository,
				Runs: runs.NewProtocolExecutor(http.DefaultTransport), Transport: http.DefaultTransport, Clock: wallClock{}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = core.Close() })
			dependencies := hostDependencies(repository, core, &countedGuard{})
			dependencies.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
			host := newTestHost(t, dependencies)
			coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
				Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
					return jobs.NewLocalBackend(localBackendDependencies(host, repository))
				}})
			if err != nil {
				t.Fatal(err)
			}
			input := coreSubmission(t, provider.URL, kind == jobs.LaunchComparison)
			if kind == jobs.LaunchPerformance {
				input.Clear()
				input = performanceCoreSubmission(t, provider.URL, nil)
			}
			defer input.Clear()
			preparation := &launcherPreparation{input: input, leases: []*credentials.Lease{}}
			accepted, err := launcherFor(t, coordinator, preparation).Launch(t.Context(), launchCommand(kind))
			if err != nil || accepted.State != jobs.SubmissionAccepted || preparation.called != kind {
				t.Fatalf("launch %s: %v", kind, err)
			}
			if accepted.Prepared.Spec.JobID == input.Job.JobID {
				t.Fatal("launcher reused an execution entity as its delivery identity")
			}
			assertLaunchLeasesClosed(t, preparation)
			waitPerformanceJob(
				t,
				repository,
				accepted.Prepared.Spec.JobID,
				domain.RunCompleted,
			)
			waitSettled(t, repository)
			view, err := repository.SubmittedJob(t.Context(), accepted.Prepared.Spec.JobID)
			if err != nil || view.Execution == nil || view.Execution.Status != domain.RunCompleted ||
				view.Delivery != jobs.SubmissionAccepted || view.Location.Kind != jobcontract.TargetLocal {
				t.Fatalf("local launch could not be read through the current query boundary: %v", err)
			}
			if kind == jobs.LaunchPerformance {
				result, err := repository.ReadPerformanceResult(t.Context(), accepted.Prepared.Spec.JobID)
				if err != nil || result.Result.ObservationState != jobcontract.PerformanceAvailable {
					t.Fatal("launched performance did not seal the original result")
				}
				presented, err := repository.SubmittedPerformanceResult(t.Context(), accepted.Prepared.Spec.JobID)
				if err != nil || !view.PerformanceResultReady || view.Progress == nil || presented.Observation == nil ||
					presented.Observation.Report.Metrics != result.Result.Observation.Checkpoint.Report.Metrics {
					t.Fatalf("local presentation did not retain the original measured metrics: %v", err)
				}
			} else {
				reports, err := repository.ListReportsForRuns(t.Context(), accepted.Receipt.RunIDs)
				if err != nil || len(reports) != len(accepted.Receipt.RunIDs) {
					t.Fatal("launched run did not seal every original report")
				}
			}
			expected := int64(len(input.Secrets))
			if kind == jobs.LaunchPerformance {
				expected = int64(input.Job.Spec.Performance.Profile.RequestCount)
			}
			if requests.Load() != expected {
				t.Fatal("launcher changed the frozen request count")
			}
		})
	}
}

func TestLauncherRejectsInvalidSelectionBeforePreparationAndClosesFailedPreparation(t *testing.T) {
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
	input := coreSubmission(t, "http://provider.example.test", false)
	defer input.Clear()
	preparation := &launcherPreparation{input: input, leases: []*credentials.Lease{}}
	launcher := launcherFor(t, coordinator, preparation)
	missingLocation, extraPayload := launchCommand(jobs.LaunchPlan), launchCommand(jobs.LaunchPlan)
	missingLocation.Location = jobs.Location{}
	extraPayload.Performance = &quicktest.PerformanceCommand{}
	tooMany := launchCommand(jobs.LaunchComparison)
	tooMany.Comparison.ChannelIDs = make([]string, jobcontract.MaxSecrets+1)
	for _, command := range []jobs.LaunchCommand{missingLocation, extraPayload, tooMany, {Kind: "old-format"}} {
		if _, err := launcher.Launch(t.Context(), command); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal("invalid location or payload was accepted")
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := launcher.Launch(canceled, launchCommand(jobs.LaunchPlan)); !errors.Is(err, context.Canceled) {
		t.Fatal("launcher hid caller cancellation")
	}
	if preparation.called != "" || connections.Load() != 0 {
		t.Fatal("invalid launch accessed preparation or a backend")
	}
	preparation.err = errors.New("preparation failed")
	if _, err := launcher.Launch(t.Context(), launchCommand(jobs.LaunchQuickTask)); !errors.Is(err, preparation.err) {
		t.Fatal("launcher hid the preparation failure")
	}
	assertLaunchLeasesClosed(t, preparation)
	if connections.Load() != 0 {
		t.Fatal("failed preparation reached a backend")
	}
	preparation.err = nil
	command := launchCommand(jobs.LaunchPlan)
	command.Location = jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: coreMeta(t).ID}
	if _, err := launcher.Launch(t.Context(), command); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("unavailable remote launch was accepted")
	}
	if connections.Load() != 1 {
		t.Fatal("unavailable remote launch tried another execution location")
	}
	assertLaunchLeasesClosed(t, preparation)
	page, err := repository.ListPendingSubmissions(t.Context(), "", jobs.MaxSubmissionPageSize)
	if err != nil || len(page.Records) != 0 {
		t.Fatal("failed launch persisted a delivery intent")
	}
	command = launchCommand(jobs.LaunchQuickTask)
	command.QuickTask.APIKey = "must-not-format-this-secret"
	if strings.Contains(fmt.Sprintf("%#v", command), command.QuickTask.APIKey) {
		t.Fatal("launch command formatting leaked a temporary credential")
	}
}
