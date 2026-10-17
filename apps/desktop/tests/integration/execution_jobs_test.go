package integration

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	bootstrap "github.com/894x/llm-test-studio/apps/desktop/internal/bootstrap"
	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const desktopJobID = "83000000-0000-4000-8000-000000000099"

type recordingJobLauncher struct {
	record  jobs.SubmissionRecord
	err     error
	calls   int
	command jobs.LaunchCommand
	ctx     context.Context
	launch  func(context.Context) (jobs.SubmissionRecord, error)
}

func (launcher *recordingJobLauncher) Launch(ctx context.Context, command jobs.LaunchCommand) (jobs.SubmissionRecord, error) {
	launcher.calls++
	launcher.ctx, launcher.command = ctx, command
	if launcher.launch != nil {
		return launcher.launch(ctx)
	}
	return launcher.record, launcher.err
}

type recordingJobQuery struct {
	job    jobs.JobView
	result jobs.PerformanceResultView
	err    error
	calls  []string
	ctx    context.Context
	jobID  string
	after  string
	limit  uint32
}

func (query *recordingJobQuery) Job(ctx context.Context, id string) (jobs.JobView, error) {
	query.ctx, query.jobID = ctx, id
	query.calls = append(query.calls, "job")
	return query.job, query.err
}

func (query *recordingJobQuery) Jobs(ctx context.Context, after string, limit uint32) (jobs.JobPage, error) {
	query.ctx, query.after, query.limit = ctx, after, limit
	query.calls = append(query.calls, "jobs")
	return jobs.JobPage{SchemaVersion: 1, Jobs: []jobs.JobView{}}, query.err
}

func (query *recordingJobQuery) PerformanceResult(ctx context.Context, id string) (jobs.PerformanceResultView, error) {
	query.ctx, query.jobID = ctx, id
	query.calls = append(query.calls, "performance")
	return query.result, query.err
}

func (query *recordingJobQuery) JobControls(ctx context.Context, id, after string, limit uint32) (jobs.ControlPageView, error) {
	query.ctx, query.jobID, query.after, query.limit = ctx, id, after, limit
	query.calls = append(query.calls, "controls")
	return jobs.ControlPageView{SchemaVersion: 1, JobID: id, Commands: []jobs.ControlView{}}, query.err
}

func desktopLaunchFixture() jobs.LaunchCommand {
	return jobs.LaunchCommand{Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchPlan,
		Plan: &runs.StartCommand{PlanID: desktopJobID, ModelID: desktopJobID, ChannelID: desktopJobID}}
}

func desktopSubmissionFixture(t *testing.T) jobs.SubmissionRecord {
	t.Helper()
	root, executableRoot := t.TempDir(), t.TempDir()
	dependencies, err := bootstrap.NewInitializer(bootstrap.Options{
		UserConfigDir: func() (string, error) { return root, nil }, AppVersion: "desktop-job-test",
		ExecutablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		CredentialStore: credentials.NewMemoryStore(),
	})(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dependencies.Close(); err != nil {
			t.Error(err)
		}
	})
	record, _ := productionLocalPerformance(t, dependencies, "https://example.invalid/v1")
	record.State, record.Revision = jobs.SubmissionPending, 2
	record.Observation = jobs.ObservationUnreachable
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDesktopLaunchReturnsAcceptanceIdentityWithoutFollowupQueries(t *testing.T) {
	record := desktopSubmissionFixture(t)
	launcher := &recordingJobLauncher{record: record, err: errors.Join(jobs.ErrPending, jobs.ErrUnavailable)}
	query := &recordingJobQuery{err: errors.New("local mirror unavailable")}
	var lifecycleContext context.Context
	app := bridge.New(func(ctx context.Context) (bridge.Dependencies, error) {
		lifecycleContext = ctx
		return bridge.Dependencies{Launcher: launcher, JobQuery: query}, nil
	})
	bridge.Startup(app, t.Context())
	t.Cleanup(func() { _ = bridge.Shutdown(app) })
	command := desktopLaunchFixture()
	view, err := app.LaunchJob(command)
	if err != nil || view.JobID != record.Prepared.Spec.JobID || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionPending {
		t.Fatalf("pending launch lost its identity: %v, %v", view, err)
	}
	if view.Execution != nil || view.Submission.Confirmation != nil || launcher.calls != 1 || len(query.calls) != 0 {
		t.Fatal("pending delivery invented execution, resent, or depended on a later query")
	}
	if launcher.ctx != lifecycleContext || !reflect.DeepEqual(launcher.command, command) {
		t.Fatal("launch did not receive the current command and lifecycle context")
	}
	spec := record.Prepared.Spec
	record.State, record.Revision, record.Observation = jobs.SubmissionAccepted, 3, jobs.ObservationNone
	record.Receipt = &jobcontract.Receipt{Contract: jobcontract.CurrentContract, JobID: spec.JobID, Kind: spec.Kind,
		AcceptedAt: record.CreatedAt,
		Origin: domain.ExecutionOrigin{Kind: "local", NodeID: record.Prepared.Runner.NodeID,
			InstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID,
			RunnerVersion: "accepted-desktop-job-test", OS: "windows", Arch: "amd64"},
		Status: domain.RunQueued, Revision: 1, RunIDs: []string{}, ReportIDs: []string{spec.Spec.Performance.ReportID}}
	launcher.record, launcher.err = record, nil
	view, err = app.LaunchJob(command)
	if err != nil || view.JobID != spec.JobID || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionAccepted || view.Submission.Confirmation == nil {
		t.Fatalf("accepted launch lost its direct confirmation: %v, %v", view, err)
	}
	if view.Execution != nil || launcher.calls != 2 || len(query.calls) != 0 {
		t.Fatal("accepted launch substituted a later query for its direct confirmation")
	}
}
