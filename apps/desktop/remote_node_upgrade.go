package main

import (
	"time"

	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type RemoteNodeUpgradeCommand struct {
	ID               string `json:"id"`
	ExpectedRevision uint64 `json:"expected_revision"`
	OperationID      string `json:"operation_id"`
	ExpectedBuildID  string `json:"expected_build_id"`
}

type RemoteNodeUpgradeAction struct {
	ID               string `json:"id"`
	ExpectedRevision uint64 `json:"expected_revision"`
	OperationID      string `json:"operation_id"`
}

type RemoteNodeUpgradeView struct {
	ConfigurationID string               `json:"configuration_id"`
	NodeID          string               `json:"node_id"`
	InstallationID  string               `json:"installation_id"`
	OperationID     string               `json:"operation_id"`
	Stage           string               `json:"stage"`
	ErrorCode       string               `json:"error_code"`
	ExpectedBuildID string               `json:"expected_build_id"`
	TargetBuildID   string               `json:"target_build_id"`
	ActualRunner    *RemoteRunnerSummary `json:"actual_runner"`
	ResumeAttempted bool                 `json:"resume_attempted"`
	Restorable      bool                 `json:"restorable"`
	Confirmed       bool                 `json:"confirmed"`
	ChangedAt       string               `json:"changed_at"`
}

func (app *DesktopApp) StartRemoteNodeUpgrade(command RemoteNodeUpgradeCommand) (*RemoteNodeUpgradeView, error) {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return nil, app.safeBindingError(err)
	}
	defer lease.release()
	attempt, err := lease.remoteNodes.StartUpgrade(lease.ctx, remotenodes.UpgradeCommand{
		ID: command.ID, ExpectedRevision: command.ExpectedRevision,
		OperationID: command.OperationID, ExpectedBuildID: command.ExpectedBuildID,
	})
	return projectRemoteUpgrade(command.ID, attempt, nil), app.safeBindingError(err)
}

func (app *DesktopApp) GetRemoteNodeUpgrade(id string) (*RemoteNodeUpgradeView, error) {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return nil, app.safeBindingError(err)
	}
	defer lease.release()
	attempt, err := lease.remoteNodes.GetUpgrade(lease.ctx, id)
	return projectRemoteUpgrade(id, attempt, nil), app.safeBindingError(err)
}

func (app *DesktopApp) RetryRemoteNodeUpgrade(command RemoteNodeUpgradeAction) (*RemoteNodeUpgradeView, error) {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return nil, app.safeBindingError(err)
	}
	defer lease.release()
	attempt, err := lease.remoteNodes.RetryUpgrade(lease.ctx, remotenodes.UpgradeAction{
		ID: command.ID, ExpectedRevision: command.ExpectedRevision, OperationID: command.OperationID,
	})
	return projectRemoteUpgrade(command.ID, attempt, nil), app.safeBindingError(err)
}

func (app *DesktopApp) RecoverRemoteNodeUpgrade(command RemoteNodeUpgradeAction) (*RemoteNodeUpgradeView, error) {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return nil, app.safeBindingError(err)
	}
	defer lease.release()
	attempt, err := lease.remoteNodes.RecoverUpgrade(lease.ctx, remotenodes.UpgradeAction{
		ID: command.ID, ExpectedRevision: command.ExpectedRevision, OperationID: command.OperationID,
	})
	return projectRemoteUpgrade(command.ID, attempt, nil), app.safeBindingError(err)
}

func projectRemoteUpgrade(
	configurationID string,
	attempt *remotenodes.Upgrade,
	observed *jobcontract.RunnerInfo,
) *RemoteNodeUpgradeView {
	if attempt == nil {
		return nil
	}
	view := &RemoteNodeUpgradeView{
		ConfigurationID: configurationID, NodeID: attempt.Request.NodeID,
		InstallationID: attempt.Request.InstallationID, OperationID: attempt.Request.OperationID,
		Stage: attempt.Stage, ErrorCode: attempt.ErrorCode, ExpectedBuildID: attempt.Request.ExpectedBuildID,
		TargetBuildID: attempt.Request.ArtifactSHA256, ChangedAt: attempt.ChangedAt.Format(time.RFC3339Nano),
	}
	if operation := attempt.Operation; operation != nil {
		view.Confirmed, view.ResumeAttempted, view.Restorable = true, operation.ResumeAttempted, operation.Restorable
		uncertainPhase := attempt.Stage != operation.Stage
		uncertainFailure := attempt.ErrorCode != "" && attempt.ErrorCode != operation.ErrorCode
		if uncertainPhase || uncertainFailure {
			view.Restorable = false
		}
		if operation.Result != nil {
			observed = &operation.Result.Runner
		}
	}
	if observed != nil && observed.NodeID == view.NodeID && observed.NodeInstallationID == view.InstallationID {
		view.ActualRunner = projectRemoteRunner(*observed)
	}
	return view
}

func projectRemoteRunner(runner jobcontract.RunnerInfo) *RemoteRunnerSummary {
	return &RemoteRunnerSummary{
		NodeID: runner.NodeID, InstallationID: runner.NodeInstallationID,
		Version: runner.RunnerVersion, BuildID: runner.EngineBuildID, OS: runner.OS, Arch: runner.Arch,
		Ready: runner.Ready, Healthy: runner.Healthy, Active: runner.Active, Queued: runner.Queued,
		Pending: runner.Pending, MaxActiveJobs: runner.Budget.MaxActiveJobs, MaxConcurrency: runner.Budget.MaxConcurrency,
	}
}
