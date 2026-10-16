package runnerhttp_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestSubmissionViewsFreezeAcceptedOriginAcrossProbeVersionChanges(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	repository, _ := coordinatorRepository(t)
	var closed atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) {})
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed, probe: func(info *jobcontract.RunnerInfo) {
				info.RunnerVersion, info.OS, info.Arch = "prepared-probe", "probe-os", "probe-arch"
			}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := prepareCoordinatedJob(t, coordinator, fixture.submission(t, nil))
	pending := prepared
	pending.State, pending.Revision = jobs.SubmissionPending, 2
	for _, record := range []jobs.SubmissionRecord{prepared, pending} {
		view, err := jobs.ProjectSubmittedJob(record, nil, nil, false)
		if err != nil || view.Executor.Version != "prepared-probe" || view.Executor.OS != "probe-os" {
			t.Fatalf("unconfirmed preparation lost its probed executor: %v", err)
		}
	}
	origin := fixture.host.Identity().Origin
	snapshot, err := prepared.Prepared.Spec.InitialSnapshot(time.Now().UTC(), origin)
	if err != nil {
		t.Fatal(err)
	}
	accepted := pending
	accepted.State, accepted.Revision, accepted.Receipt = jobs.SubmissionAccepted, 3, &snapshot.Receipt
	accepted.UpdatedAt = snapshot.UpdatedAt
	want := jobs.ExecutorView{NodeID: origin.NodeID, InstallationID: origin.InstallationID,
		Version: origin.RunnerVersion, BuildID: origin.EngineBuildID, OS: origin.OS, Arch: origin.Arch}
	for _, input := range []struct {
		record    jobs.SubmissionRecord
		execution *jobcontract.Snapshot
	}{
		{record: accepted},
		{record: accepted, execution: &snapshot},
		{record: pending, execution: &snapshot},
	} {
		view, err := jobs.ProjectSubmittedJob(input.record, input.execution, nil, false)
		if err != nil || view.Executor != want {
			t.Fatalf("accepted execution reused preparation probe identity: %v", err)
		}
	}
	terminal := snapshot
	terminal.Receipt.Status, terminal.Receipt.Revision = domain.RunCancelled, 2
	terminal.LatestSequence, terminal.UpdatedAt = 2, snapshot.UpdatedAt.Add(time.Second)
	result := jobcontract.PerformanceResult{Contract: jobcontract.CurrentContract,
		JobID: snapshot.Receipt.JobID, ReportID: snapshot.Receipt.ReportIDs[0], Terminal: terminal,
		SealedAt: terminal.UpdatedAt, ObservationState: jobcontract.PerformanceNotStarted}
	view, err := jobs.ProjectSubmittedPerformanceResult(accepted, result)
	if err != nil || view.Executor != want {
		t.Fatalf("sealed result reused preparation probe identity: %v", err)
	}
	for _, mutate := range []func(*domain.ExecutionOrigin){
		func(origin *domain.ExecutionOrigin) { origin.Kind = "local" },
		func(origin *domain.ExecutionOrigin) { origin.NodeID = coordinatorLocation.ConfigurationID },
		func(origin *domain.ExecutionOrigin) { origin.InstallationID = coordinatorLocation.ConfigurationID },
		func(origin *domain.ExecutionOrigin) { origin.EngineBuildID = strings.Repeat("c", 64) },
		func(origin *domain.ExecutionOrigin) { origin.RunnerVersion = "replaced-runner" },
		func(origin *domain.ExecutionOrigin) { origin.OS = "another-os" },
		func(origin *domain.ExecutionOrigin) { origin.Arch = "another-arch" },
	} {
		changed := terminal
		mutate(&changed.Receipt.Origin)
		if _, err := jobs.ProjectSubmittedJob(accepted, &changed, nil, false); !errors.Is(err, jobs.ErrConflict) {
			t.Fatalf("changed accepted origin entered execution view: %v", err)
		}
		changedResult := result
		changedResult.Terminal = changed
		if _, err := jobs.ProjectSubmittedPerformanceResult(accepted, changedResult); !errors.Is(err, jobs.ErrConflict) {
			t.Fatalf("changed accepted origin entered sealed result: %v", err)
		}
	}
	command := jobcontract.Command{Contract: jobcontract.CurrentContract,
		CommandID: coordinatorLocation.ConfigurationID, JobID: result.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: 1}
	confirmation := jobcontract.CommandConfirmation{Command: command, AcceptedAt: terminal.UpdatedAt, Snapshot: terminal}
	control := jobs.ControlRecord{SchemaVersion: 1, Command: command, Revision: 2, State: jobs.ControlConfirmed,
		CreatedAt: terminal.UpdatedAt, UpdatedAt: terminal.UpdatedAt, Confirmation: &confirmation}
	if err := control.ValidateFor(accepted); err != nil {
		t.Fatal(err)
	}
	confirmation.Snapshot.Receipt.Origin.RunnerVersion = "replacement-at-same-acceptance-time"
	if err := control.ValidateFor(accepted); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("control confirmation changed the accepted executor version: %v", err)
	}
}

func TestCoordinatorRequiresExecutionOriginForReadOnlyReconciliation(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	repository, _ := coordinatorRepository(t)
	var closed, gets, posts atomic.Int64
	client := coordinatorClient(t, fixture, func(*http.Response) { posts.Add(1) })
	missingOrigin := false
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: repository, Now: time.Now,
		Open: func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error) {
			return &coordinatorConnection{Client: client, closed: &closed, gets: &gets,
				probe: func(info *jobcontract.RunnerInfo) {
					if missingOrigin {
						info.Capabilities.Features = slices.DeleteFunc(info.Capabilities.Features, func(feature string) bool {
							return feature == jobcontract.FeatureExecutionOrigin
						})
					}
				}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := prepareCoordinatedJob(t, coordinator, fixture.submission(t, nil))
	pending := prepared
	pending.State, pending.Revision = jobs.SubmissionPending, 2
	if err := repository.UpdateSubmission(t.Context(), pending, prepared.Revision); err != nil {
		t.Fatal(err)
	}
	missingOrigin = true
	observed, err := coordinator.Reconcile(t.Context(), pending.Prepared.Spec.JobID)
	if !errors.Is(err, jobs.ErrUpgradeRequired) || observed.State != jobs.SubmissionPending ||
		observed.Observation != jobs.ObservationRejected || gets.Load() != 0 || posts.Load() != 0 {
		t.Fatalf("old runner capability did not require an explicit upgrade before observing: %v", err)
	}
}

func TestTypedClientProbeRejectsRunnerWithoutExecutionOriginCapability(t *testing.T) {
	caps := capabilities()
	caps.Features = slices.DeleteFunc(caps.Features, func(feature string) bool {
		return feature == jobcontract.FeatureExecutionOrigin
	})
	fixture := newHTTPFixture(t, false, caps)
	client := coordinatorClient(t, fixture, func(*http.Response) { t.Error("probe submitted work") })
	if _, err := client.Probe(t.Context()); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatalf("old runner capability was accepted as the current protocol: %v", err)
	}
	if fixture.requests.Load() != 0 {
		t.Fatal("an incompatible runner probe executed provider work")
	}
}
