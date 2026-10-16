package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrJobQueryUnavailable    = errors.New("job query is unavailable")
	ErrJobCommandsUnavailable = errors.New("job commands are unavailable")
	ErrJobCommitUnconfirmed   = errors.New("job command persistence is unconfirmed")
)

const (
	desktopCodeJobInvalid             = "job_invalid"
	desktopCodeJobNotFound            = "job_not_found"
	desktopCodeJobConflict            = "job_conflict"
	desktopCodeJobUnavailable         = "job_unavailable"
	desktopCodeJobPending             = "job_confirmation_pending"
	desktopCodeJobCommitUnconfirmed   = "job_commit_unconfirmed"
	desktopCodeJobQueryUnavailable    = "job_query_unavailable"
	desktopCodeJobCommandsUnavailable = "job_commands_unavailable"
)

type JobLauncher interface {
	Launch(context.Context, jobs.LaunchCommand) (jobs.SubmissionRecord, error)
}

type JobController interface {
	Request(context.Context, jobs.ControlInput) (jobs.ControlRecord, error)
	Deliver(context.Context, string) (jobs.ControlRecord, error)
}

func (app *DesktopApp) LaunchJob(command jobs.LaunchCommand) (jobs.JobView, error) {
	if command.Validate() != nil {
		return jobs.JobView{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{launcher: true})
	if err != nil {
		return jobs.JobView{}, app.safeBindingError(err)
	}
	defer lease.release()
	// Keep configuration removal outside preparation and durable submission.
	// Launch itself has a bounded context; shutdown cancellation releases this gate.
	app.executionLocationMu.RLock()
	defer app.executionLocationMu.RUnlock()
	if err := lease.ctx.Err(); err != nil {
		return jobs.JobView{}, app.safeBindingError(err)
	}
	record, err := lease.launcher.Launch(lease.ctx, command)
	if err != nil && !errors.Is(err, jobs.ErrPending) {
		return jobs.JobView{}, app.jobOperationError(err)
	}
	// Project the returned durable acceptance directly. A later query cannot
	// obscure its identity or cause the caller to submit a second job.
	view, err := jobs.ProjectSubmittedJob(record, nil, nil, false)
	if err != nil {
		return jobs.JobView{}, app.safeBindingError(ErrJobCommitUnconfirmed)
	}
	return view, nil
}

func (app *DesktopApp) GetJobs(after string, limit uint32) (jobs.JobPage, error) {
	if !validJobPage(after, limit) {
		return jobs.JobPage{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{jobQuery: true})
	if err != nil {
		return jobs.JobPage{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.jobQuery.SubmittedJobs(lease.ctx, after, limit)
	if err != nil {
		return jobs.JobPage{}, app.jobQueryError(err)
	}
	return page, nil
}

func (app *DesktopApp) GetJob(jobID string) (jobs.JobView, error) {
	if !domain.IsUUID(jobID) {
		return jobs.JobView{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{jobQuery: true})
	if err != nil {
		return jobs.JobView{}, app.safeBindingError(err)
	}
	defer lease.release()
	view, err := lease.jobQuery.SubmittedJob(lease.ctx, jobID)
	if err != nil {
		return jobs.JobView{}, app.jobQueryError(err)
	}
	return view, nil
}

func (app *DesktopApp) GetJobPerformanceResult(jobID string) (jobs.PerformanceResultView, error) {
	if !domain.IsUUID(jobID) {
		return jobs.PerformanceResultView{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{jobQuery: true})
	if err != nil {
		return jobs.PerformanceResultView{}, app.safeBindingError(err)
	}
	defer lease.release()
	view, err := lease.jobQuery.SubmittedPerformanceResult(lease.ctx, jobID)
	if err != nil {
		return jobs.PerformanceResultView{}, app.jobQueryError(err)
	}
	if view.Observation != nil {
		app.reportQuickPerformanceDiagnostics(view.Observation.Report)
	}
	return view, nil
}

func (app *DesktopApp) ControlJob(command jobs.ControlInput) (jobs.ControlView, error) {
	validation := jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: command.JobID,
		JobID: command.JobID, Kind: command.Kind, ExpectedRevision: command.ExpectedRevision}
	if validation.Validate() != nil {
		return jobs.ControlView{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{controls: true, controlRecords: true})
	if err != nil {
		return jobs.ControlView{}, app.safeBindingError(err)
	}
	defer lease.release()
	app.executionLocationMu.RLock()
	defer app.executionLocationMu.RUnlock()
	if err := lease.ctx.Err(); err != nil {
		return jobs.ControlView{}, app.safeBindingError(err)
	}
	record, err := lease.controls.Request(lease.ctx, command)
	ambiguousCreate := err != nil && record.Validate() == nil && record.Revision == 1 &&
		record.State == jobs.ControlPending && record.Observation == jobs.ObservationNone
	if ambiguousCreate {
		// Request can return the proposed record after an ambiguous Create.
		// Only a matching persisted command proves that this intent is saved.
		stored, readErr := lease.controlRecords.GetControl(lease.ctx, record.Command.CommandID)
		matching := readErr == nil && stored.Validate() == nil && stored.Command == record.Command &&
			stored.CreatedAt.Equal(record.CreatedAt)
		if !matching {
			return jobs.ControlView{}, app.safeBindingError(ErrJobCommitUnconfirmed)
		}
		record = stored
	}
	return app.controlResult(record, err)
}

func (app *DesktopApp) GetJobControls(jobID, after string, limit uint32) (jobs.ControlPageView, error) {
	if !domain.IsUUID(jobID) || !validJobPage(after, limit) {
		return jobs.ControlPageView{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{jobQuery: true})
	if err != nil {
		return jobs.ControlPageView{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.jobQuery.SubmittedJobControls(lease.ctx, jobID, after, limit)
	if err != nil {
		return jobs.ControlPageView{}, app.jobQueryError(err)
	}
	return page, nil
}

func (app *DesktopApp) RetryJobControl(commandID string) (jobs.ControlView, error) {
	if !domain.IsUUID(commandID) {
		return jobs.ControlView{}, app.safeBindingError(jobs.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{controls: true})
	if err != nil {
		return jobs.ControlView{}, app.safeBindingError(err)
	}
	defer lease.release()
	record, err := lease.controls.Deliver(lease.ctx, commandID)
	return app.controlResult(record, err)
}

func (app *DesktopApp) controlResult(record jobs.ControlRecord, err error) (jobs.ControlView, error) {
	if record.Validate() == nil {
		// A durable pending or explicitly refused instruction is a command fact.
		// It never substitutes for the independently observed job execution.
		return jobs.ProjectControl(record)
	}
	if err == nil {
		err = jobs.ErrInvalid
	}
	return jobs.ControlView{}, app.jobOperationError(err)
}

func validJobPage(after string, limit uint32) bool {
	return limit > 0 && limit <= jobs.MaxSubmissionPageSize && (after == "" || domain.IsUUID(after))
}

func (app *DesktopApp) jobQueryError(err error) error {
	if errors.Is(err, jobs.ErrInvalid) || errors.Is(err, jobs.ErrNotFound) || errors.Is(err, jobs.ErrConflict) {
		return app.safeBindingError(err)
	}
	return app.safeBindingError(fmt.Errorf("%w: %w", ErrJobQueryUnavailable, err))
}

func (app *DesktopApp) jobOperationError(err error) error {
	var preparation jobs.PreparationError
	if errors.As(err, &preparation) {
		if preparation.Code == quicktest.ErrorCredentialRequired {
			return app.safeBindingError(runs.ErrQuickTaskCredential)
		}
		return app.safeBindingError(jobs.ErrInvalid)
	}
	public := app.safeBindingError(err)
	if public.Error() == desktopCodeOperationFailed {
		return DesktopBindingError{Code: desktopCodeJobUnavailable}
	}
	return public
}
