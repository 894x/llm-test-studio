package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
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

type recordingJobController struct {
	record     jobs.ControlRecord
	err        error
	requests   []jobs.ControlInput
	deliveries []string
}

func (controller *recordingJobController) Request(_ context.Context, command jobs.ControlInput) (jobs.ControlRecord, error) {
	controller.requests = append(controller.requests, command)
	return controller.record, controller.err
}

func (controller *recordingJobController) Deliver(_ context.Context, id string) (jobs.ControlRecord, error) {
	controller.deliveries = append(controller.deliveries, id)
	return controller.record, controller.err
}

type recordingControlRecords struct {
	jobs.ControlRepository
	record jobs.ControlRecord
	err    error
	ids    []string
}

func (repository *recordingControlRecords) GetControl(_ context.Context, id string) (jobs.ControlRecord, error) {
	repository.ids = append(repository.ids, id)
	return repository.record, repository.err
}

func desktopLaunchFixture() jobs.LaunchCommand {
	return jobs.LaunchCommand{Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchPlan,
		Plan: &runs.StartCommand{PlanID: desktopJobID, ModelID: desktopJobID, ChannelID: desktopJobID}}
}

func desktopSubmissionFixture(t *testing.T) jobs.SubmissionRecord {
	t.Helper()
	root, executableRoot := t.TempDir(), t.TempDir()
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return root, nil }, appVersion: "desktop-job-test",
		executablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		credentialStore: credentials.NewMemoryStore(),
	})(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dependencies.close(); err != nil {
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

func desktopControlFixture() jobs.ControlRecord {
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	return jobs.ControlRecord{SchemaVersion: 1, Revision: 1, State: jobs.ControlPending,
		Target: jobs.ControlTarget{JobID: desktopJobID, Location: jobs.Location{Kind: jobcontract.TargetLocal},
			NodeID: "83000000-0000-4000-8000-000000000098", InstallationID: "83000000-0000-4000-8000-000000000097",
			SpecSHA256: strings.Repeat("a", 64)},
		CreatedAt: at, UpdatedAt: at, Command: jobcontract.Command{Contract: jobcontract.CurrentContract,
			CommandID: "83000000-0000-4000-8000-000000000001", JobID: desktopJobID,
			Kind: jobcontract.CommandCancel, ExpectedRevision: 7}}
}

func TestDesktopLaunchReturnsAcceptanceIdentityWithoutFollowupQueries(t *testing.T) {
	record := desktopSubmissionFixture(t)
	launcher := &recordingJobLauncher{record: record, err: errors.Join(jobs.ErrPending, jobs.ErrUnavailable)}
	query := &recordingJobQuery{err: errors.New("local mirror unavailable")}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{launcher: launcher, jobQuery: query}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	command := desktopLaunchFixture()
	view, err := app.LaunchJob(command)
	if err != nil || view.JobID != record.Prepared.Spec.JobID || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionPending {
		t.Fatalf("pending launch lost its identity: %v, %v", view, err)
	}
	if view.Execution != nil || view.Submission.Confirmation != nil || launcher.calls != 1 || len(query.calls) != 0 {
		t.Fatal("pending delivery invented execution, resent, or depended on a later query")
	}
	if launcher.ctx != app.ctx || !reflect.DeepEqual(launcher.command, command) {
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

func TestDesktopLaunchRequiresExplicitLocationAndOnePayload(t *testing.T) {
	launcher := &recordingJobLauncher{}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{launcher: launcher}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	for _, mutate := range []func(*jobs.LaunchCommand){
		func(command *jobs.LaunchCommand) { command.Location = jobs.Location{} },
		func(command *jobs.LaunchCommand) { command.Location.Kind = jobcontract.TargetRemote },
		func(command *jobs.LaunchCommand) { command.QuickTask = &runs.QuickTaskCommand{} },
		func(command *jobs.LaunchCommand) { command.Kind = jobs.LaunchQuickTask },
	} {
		command := desktopLaunchFixture()
		mutate(&command)
		_, err := app.LaunchJob(command)
		assertBindingErrorCode(t, err, desktopCodeJobInvalid)
	}
	if launcher.calls != 0 {
		t.Fatal("invalid location or payload reached launcher")
	}
}

func TestDesktopJobErrorsKeepSafeDomainMeaning(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{err: jobs.ErrInvalid, code: desktopCodeJobInvalid},
		{err: jobs.ErrConflict, code: desktopCodeJobConflict},
		{err: jobs.ErrNotFound, code: desktopCodeJobNotFound},
		{err: jobs.ErrUnavailable, code: desktopCodeJobUnavailable},
		{err: jobs.ErrUpgradeRequired, code: desktopCodeRemoteNodeUpgrade},
		{err: jobs.ErrTargetChanged, code: desktopCodeRemoteNodeRePair},
		{err: runs.ErrNotRunnable, code: desktopCodeRunNotRunnable},
		{err: runs.ErrQuickTaskCredential, code: desktopCodeQuickTaskCredential},
		{err: comparisons.ErrNotReady, code: desktopCodeComparisonNotReady},
		{err: comparisons.ErrTargetUnmapped, code: desktopCodeComparisonTargetUnmapped},
		{err: comparisons.ErrChannelNotReady, code: desktopCodeComparisonChannelNotReady},
		{err: errors.New("Bearer sk-private-provider-key"), code: desktopCodeJobUnavailable},
	} {
		t.Run(test.code, func(t *testing.T) {
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{launcher: &recordingJobLauncher{err: test.err}}, nil
			})
			app.onStartup(t.Context())
			t.Cleanup(func() { _ = app.shutdown() })
			_, err := app.LaunchJob(desktopLaunchFixture())
			assertBindingErrorCode(t, err, test.code)
			if strings.Contains(err.Error(), "private") {
				t.Fatal("raw launch error crossed the public boundary")
			}
		})
	}
}

func TestDesktopJobQueriesAreLocalAndValidatePages(t *testing.T) {
	query := &recordingJobQuery{job: jobs.JobView{JobID: desktopJobID},
		result: jobs.PerformanceResultView{JobID: desktopJobID, ObservationState: jobcontract.PerformanceNotStarted}}
	launcher := &recordingJobLauncher{}
	controller := &recordingJobController{}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{jobQuery: query, launcher: launcher, controls: controller}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	if _, err := app.GetJob(desktopJobID); err != nil {
		t.Fatal(err)
	}
	page, err := app.GetJobs("", 25)
	if err != nil || page.Jobs == nil {
		t.Fatalf("page: %v", err)
	}
	result, err := app.GetJobPerformanceResult(desktopJobID)
	if err != nil || result.Observation != nil || result.ObservationState != jobcontract.PerformanceNotStarted {
		t.Fatal("unstarted performance invented an observation")
	}
	controls, err := app.GetJobControls(desktopJobID, desktopJobID, 1)
	if err != nil || controls.Commands == nil || query.after != desktopJobID || query.limit != 1 {
		t.Fatal("control page did not retain its requested bounded cursor")
	}
	for _, limit := range []uint32{0, 26} {
		_, err = app.GetJobs("", limit)
		assertBindingErrorCode(t, err, desktopCodeJobInvalid)
	}
	_, err = app.GetJobs("invalid", 1)
	assertBindingErrorCode(t, err, desktopCodeJobInvalid)
	_, err = app.GetJobControls("invalid", "", 1)
	assertBindingErrorCode(t, err, desktopCodeJobInvalid)
	if len(query.calls) != 4 || launcher.calls != 0 || len(controller.requests)+len(controller.deliveries) != 0 {
		t.Fatal("read-only queries changed task delivery or invalid input reached storage")
	}
	if query.ctx != app.ctx {
		t.Fatal("query ignored lifecycle context")
	}
	query.err = errors.New("secret database path")
	_, err = app.GetJob(desktopJobID)
	assertBindingErrorCode(t, err, desktopCodeJobQueryUnavailable)
}

func TestDesktopControlPreservesPendingIdentityAndOriginalRevision(t *testing.T) {
	record := desktopControlFixture()
	record.Revision, record.Observation = 2, jobs.ObservationUnreachable
	controller := &recordingJobController{record: record, err: errors.Join(jobs.ErrPending, jobs.ErrUnavailable)}
	repository := &recordingControlRecords{err: errors.New("read unavailable")}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{controls: controller, controlRecords: repository}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	input := jobs.ControlInput{JobID: record.Command.JobID, Kind: record.Command.Kind,
		ExpectedRevision: record.Command.ExpectedRevision}
	view, err := app.ControlJob(input)
	if err != nil || view.CommandID != record.Command.CommandID || view.State != jobs.ControlPending || view.Execution != nil {
		t.Fatal("pending control lost its identity or invented execution")
	}
	retried, err := app.RetryJobControl(view.CommandID)
	if err != nil || retried.CommandID != view.CommandID || retried.ExpectedRevision != input.ExpectedRevision {
		t.Fatal("retry changed command identity or expected revision")
	}
	if len(controller.requests) != 1 || !reflect.DeepEqual(controller.requests[0], input) ||
		!reflect.DeepEqual(controller.deliveries, []string{view.CommandID}) || len(repository.ids) != 0 {
		t.Fatal("pending control minted a second request or required a followup query")
	}
}

func TestDesktopControlVerifiesAmbiguousCommitBeforeClaimingSaved(t *testing.T) {
	record := desktopControlFixture()
	changedTarget := record
	changedTarget.Target.InstallationID = "83000000-0000-4000-8000-000000000096"
	for _, test := range []struct {
		name    string
		stored  jobs.ControlRecord
		readErr error
		saved   bool
	}{
		{name: "missing", readErr: jobs.ErrNotFound},
		{name: "unreadable", readErr: errors.New("secret storage error")},
		{name: "invalid", stored: jobs.ControlRecord{}},
		{name: "different frozen target", stored: changedTarget},
		{name: "saved", stored: record, saved: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := &recordingJobController{record: record, err: errors.New("ambiguous storage commit")}
			repository := &recordingControlRecords{record: test.stored, err: test.readErr}
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{controls: controller, controlRecords: repository}, nil
			})
			app.onStartup(t.Context())
			t.Cleanup(func() { _ = app.shutdown() })
			view, err := app.ControlJob(jobs.ControlInput{JobID: desktopJobID,
				Kind: jobcontract.CommandCancel, ExpectedRevision: 7})
			if test.saved {
				if err != nil || view.CommandID != record.Command.CommandID {
					t.Fatal("persisted intent was hidden")
				}
			} else {
				assertBindingErrorCode(t, err, desktopCodeJobCommitUnconfirmed)
				if view.CommandID != "" {
					t.Fatal("unproven intent was reported saved")
				}
			}
			if len(controller.requests) != 1 || len(controller.deliveries) != 0 ||
				!reflect.DeepEqual(repository.ids, []string{record.Command.CommandID}) {
				t.Fatal("ambiguous create was resent or not checked by its stable identity")
			}
		})
	}
}

func TestDesktopSharedJobQueriesPreserveNullSubmissionAndOriginalExecutor(t *testing.T) {
	view := jobs.JobView{SchemaVersion: 1, JobID: desktopJobID, Kind: jobcontract.KindPerformance,
		Location:               jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: "83000000-0000-4000-8000-000000000095"},
		Executor:               jobs.ExecutorView{Version: "original-r4", BuildID: strings.Repeat("a", 64)},
		Execution:              &jobs.ExecutionView{Status: domain.RunCompleted, Revision: 7, LatestSequence: 15},
		PerformanceResultReady: true}
	query := &recordingJobQuery{job: view, result: jobs.PerformanceResultView{SchemaVersion: 1,
		JobID: desktopJobID, Executor: view.Executor, Location: view.Location}}
	launcher, controller := &recordingJobLauncher{}, &recordingJobController{}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{jobQuery: query, launcher: launcher, controls: controller}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	actual, err := app.GetJob(desktopJobID)
	if err != nil || !reflect.DeepEqual(actual, view) || actual.Submission != nil {
		t.Fatal("shared job getter invented local delivery or changed actual executor")
	}
	result, err := app.GetJobPerformanceResult(desktopJobID)
	if err != nil || result.Executor != view.Executor {
		t.Fatal("shared performance getter replaced original executor")
	}
	if _, err := app.GetJobControls(desktopJobID, "", 25); err != nil {
		t.Fatal(err)
	}
	if len(query.calls) != 3 || launcher.calls != 0 || len(controller.requests)+len(controller.deliveries) != 0 {
		t.Fatal("shared reads performed a submission or control operation")
	}
}

func TestDesktopControlRefusalDoesNotInventTaskState(t *testing.T) {
	record := desktopControlFixture()
	record.Revision, record.State, record.Observation = 2, jobs.ControlRejected, jobs.ObservationRejected
	controller := &recordingJobController{record: record, err: jobs.ErrConflict}
	repository := &recordingControlRecords{record: record}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{controls: controller, controlRecords: repository}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	view, err := app.ControlJob(jobs.ControlInput{JobID: desktopJobID, Kind: jobcontract.CommandCancel, ExpectedRevision: 7})
	if err != nil || view.State != jobs.ControlRejected || view.Execution != nil || view.ExpectedRevision != 7 {
		t.Fatal("explicit stale revision refusal was rebased or treated as task termination")
	}
	_, err = app.ControlJob(jobs.ControlInput{JobID: desktopJobID, Kind: jobcontract.CommandCancel})
	assertBindingErrorCode(t, err, desktopCodeJobInvalid)
	_, err = app.RetryJobControl("invalid")
	assertBindingErrorCode(t, err, desktopCodeJobInvalid)
	if len(controller.requests) != 1 || len(controller.deliveries) != 0 {
		t.Fatal("invalid control reached controller")
	}
}

func TestDesktopJobLifecycleDrainsAndReleasesServices(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	launcher := &recordingJobLauncher{launch: func(ctx context.Context) (jobs.SubmissionRecord, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return jobs.SubmissionRecord{}, ctx.Err()
	}}
	closed := false
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{launcher: launcher, jobQuery: &recordingJobQuery{},
			controls: &recordingJobController{}, controlRecords: &recordingControlRecords{}, close: func() error {
				select {
				case <-exited:
				default:
					t.Error("services closed before launch drained")
				}
				closed = true
				return nil
			}}, nil
	})
	_, err := app.LaunchJob(desktopLaunchFixture())
	assertBindingErrorCode(t, err, desktopCodeNotStarted)
	app.onStartup(t.Context())
	done := make(chan error, 1)
	go func() { _, err := app.LaunchJob(desktopLaunchFixture()); done <- err }()
	<-entered
	if err := app.shutdown(); err != nil {
		t.Fatal(err)
	}
	assertBindingErrorCode(t, <-done, desktopCodeOperationCancelled)
	if !closed || app.launcher != nil || app.jobQuery != nil || app.controls != nil || app.controlRecords != nil {
		t.Fatal("shutdown retained execution service references")
	}
	_, err = app.GetJobs("", 1)
	assertBindingErrorCode(t, err, desktopCodeStopped)
}

func TestDesktopBindingExposesOnlyCurrentExecutionMethods(t *testing.T) {
	typeOfApp := reflect.TypeFor[*DesktopApp]()
	for _, name := range []string{"LaunchJob", "GetJobs", "GetJob", "GetJobPerformanceResult",
		"ControlJob", "GetJobControls", "RetryJobControl"} {
		if _, present := typeOfApp.MethodByName(name); !present {
			t.Fatalf("missing current method %s", name)
		}
	}
	for _, name := range []string{"StartRunTarget", "StartQuickTask", "StartComparison", "RunQuickPerformanceTest", "StopSending", "CancelRun"} {
		if _, present := typeOfApp.MethodByName(name); present {
			t.Fatalf("obsolete execution binding remains: %s", name)
		}
	}
}
