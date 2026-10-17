package main

import (
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// RemoteNodesSnapshot is the presentation projection. Installation paths,
// credential references and server pairing material never cross this boundary.
type RemoteNodesSnapshot struct {
	SchemaVersion   int                 `json:"schema_version"`
	DefaultRevision uint64              `json:"default_revision"`
	Default         jobs.Location       `json:"default"`
	Nodes           []RemoteNodeSummary `json:"nodes"`
}

type RemoteNodeSummary struct {
	ID                 string                         `json:"id"`
	Revision           uint64                         `json:"revision"`
	Name               string                         `json:"name"`
	Region             string                         `json:"region"`
	Host               string                         `json:"host"`
	Port               uint16                         `json:"port"`
	User               string                         `json:"user"`
	Enabled            bool                           `json:"enabled"`
	AuthenticationKind remotenodes.AuthenticationKind `json:"authentication_kind"`
	HasAuthentication  bool                           `json:"has_authentication"`
	HasPassphrase      bool                           `json:"has_passphrase"`
	HostKey            *remotenodes.HostKey           `json:"host_key"`
	ProposedHostKey    *remotenodes.HostKey           `json:"proposed_host_key"`
	Status             string                         `json:"status"`
	Eligible           bool                           `json:"eligible"`
	Checking           bool                           `json:"checking"`
	CheckedAt          *string                        `json:"checked_at"`
	Runner             *RemoteRunnerSummary           `json:"runner"`
	InstallationStage  string                         `json:"installation_stage"`
	InstallationError  string                         `json:"installation_error"`
	Upgrade            *RemoteNodeUpgradeView         `json:"upgrade"`
}

type RemoteRunnerSummary struct {
	NodeID         string `json:"node_id"`
	InstallationID string `json:"installation_id"`
	Version        string `json:"version"`
	BuildID        string `json:"build_id"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Ready          bool   `json:"ready"`
	Healthy        bool   `json:"healthy"`
	Active         uint32 `json:"active"`
	Queued         uint32 `json:"queued"`
	Pending        uint32 `json:"pending"`
	MaxActiveJobs  uint32 `json:"max_active_jobs"`
	MaxConcurrency uint32 `json:"max_concurrency"`
}

type RemoteNodeDraft struct {
	ID               string                         `json:"id"`
	ExpectedRevision uint64                         `json:"expected_revision"`
	Name             string                         `json:"name"`
	Region           string                         `json:"region"`
	Host             string                         `json:"host"`
	Port             uint16                         `json:"port"`
	User             string                         `json:"user"`
	Enabled          bool                           `json:"enabled"`
	Authentication   remotenodes.AuthenticationKind `json:"authentication"`
	Password         string                         `json:"password"`
	PrivateKey       string                         `json:"private_key"`
	Passphrase       string                         `json:"passphrase"`
	RemovePassphrase bool                           `json:"remove_passphrase"`
}

func (RemoteNodeDraft) String() string   { return "RemoteNodeDraft{redacted}" }
func (RemoteNodeDraft) GoString() string { return "RemoteNodeDraft{redacted}" }

type RemoteNodeAction struct {
	ID               string `json:"id"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

type RemoteNodeHostKeyConfirmation struct {
	ID               string              `json:"id"`
	ExpectedRevision uint64              `json:"expected_revision"`
	Key              remotenodes.HostKey `json:"key"`
}

type DefaultExecutionCommand struct {
	ExpectedRevision uint64        `json:"expected_revision"`
	Location         jobs.Location `json:"location"`
}

func (app *DesktopApp) GetRemoteNodes() (RemoteNodesSnapshot, error) {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return RemoteNodesSnapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.remoteNodes.Snapshot(lease.ctx)
	if err != nil {
		return RemoteNodesSnapshot{}, app.safeBindingError(err)
	}
	result := RemoteNodesSnapshot{SchemaVersion: 1, DefaultRevision: snapshot.Default.Revision,
		Default: jobs.Location{Kind: snapshot.Default.Kind, ConfigurationID: snapshot.Default.NodeID},
		Nodes:   make([]RemoteNodeSummary, 0, len(snapshot.Nodes))}
	for _, node := range snapshot.Nodes {
		view := RemoteNodeSummary{ID: node.ID, Revision: node.Revision, Name: node.Name, Region: node.Region,
			Host: node.Host, Port: node.Port, User: node.User, Enabled: node.Enabled,
			AuthenticationKind: node.AuthenticationKind, HasAuthentication: node.HasAuthentication,
			HasPassphrase: node.HasPassphrase, HostKey: node.HostKey, Status: node.Status,
			Eligible: node.Eligible, Checking: node.Checking}
		if node.Observation != nil {
			checked := node.Observation.CheckedAt.Format(time.RFC3339Nano)
			view.CheckedAt = &checked
			view.ProposedHostKey = node.Observation.ProposedHostKey
			if runner := node.Observation.Runner; runner != nil {
				view.Runner = &RemoteRunnerSummary{NodeID: runner.NodeID, InstallationID: runner.NodeInstallationID,
					Version: runner.RunnerVersion, BuildID: runner.EngineBuildID, OS: runner.OS, Arch: runner.Arch,
					Ready: runner.Ready, Healthy: runner.Healthy, Active: runner.Active, Queued: runner.Queued,
					Pending: runner.Pending, MaxActiveJobs: runner.Budget.MaxActiveJobs,
					MaxConcurrency: runner.Budget.MaxConcurrency}
			}
		}
		if node.Installation != nil {
			view.InstallationStage, view.InstallationError = node.Installation.Stage, node.Installation.ErrorCode
		}
		var observed *jobcontract.RunnerInfo
		if node.Observation != nil {
			observed = node.Observation.Runner
		}
		view.Upgrade = projectRemoteUpgrade(node.ID, node.Upgrade, observed)
		result.Nodes = append(result.Nodes, view)
	}
	return result, nil
}

func (app *DesktopApp) SaveRemoteNode(draft RemoteNodeDraft) error {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	if !domain.IsUUID(draft.ID) {
		return app.safeBindingError(remotenodes.ErrInvalid)
	}
	secrets := remotenodes.SecretUpdate{RemovePassphrase: draft.RemovePassphrase}
	defer func() {
		_ = secrets.Password.Close()
		_ = secrets.PrivateKey.Close()
		_ = secrets.Passphrase.Close()
	}()
	for _, value := range []struct {
		text        string
		limit       int
		destination **credentials.Lease
	}{
		{text: draft.Password, limit: 8192, destination: &secrets.Password},
		{text: draft.PrivateKey, limit: 1 << 20, destination: &secrets.PrivateKey},
		{text: draft.Passphrase, limit: 8192, destination: &secrets.Passphrase},
	} {
		if len(value.text) > value.limit {
			return app.safeBindingError(remotenodes.ErrInvalid)
		}
		if value.text == "" {
			continue
		}
		private := []byte(value.text)
		*value.destination, err = credentials.NewTemporaryLease(private)
		clear(private)
		if err != nil {
			return app.safeBindingError(remotenodes.ErrInvalid)
		}
	}
	// Preserve an existing pin from Go-owned state. Only the separate explicit
	// confirmation action can accept the first observed host key.
	snapshot, err := lease.remoteNodes.Snapshot(lease.ctx)
	if err != nil {
		return app.safeBindingError(err)
	}
	var pin *remotenodes.HostKey
	for _, node := range snapshot.Nodes {
		if node.ID == draft.ID {
			pin = node.HostKey
			break
		}
	}
	_, err = lease.remoteNodes.Save(lease.ctx, remotenodes.SaveCommand{
		ID: draft.ID, ExpectedRevision: draft.ExpectedRevision, Name: draft.Name, Region: draft.Region,
		Host: draft.Host, Port: draft.Port, User: draft.User, Enabled: draft.Enabled,
		Authentication: draft.Authentication, HostKey: pin, Secrets: secrets,
	})
	return app.safeBindingError(err)
}

func (app *DesktopApp) RefreshRemoteNode(command RemoteNodeAction) error {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.remoteNodes.Refresh(lease.ctx, command.ID, command.ExpectedRevision))
}

func (app *DesktopApp) ConfirmRemoteNodeHostKey(command RemoteNodeHostKeyConfirmation) error {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	_, err = lease.remoteNodes.ConfirmHostKey(lease.ctx, remotenodes.HostKeyConfirmation{
		ID: command.ID, ExpectedRevision: command.ExpectedRevision, Key: command.Key,
	})
	return app.safeBindingError(err)
}

func (app *DesktopApp) SelectDefaultExecution(command DefaultExecutionCommand) error {
	if command.Location.Validate() != nil {
		return app.safeBindingError(remotenodes.ErrInvalid)
	}
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.remoteNodes.SelectDefault(lease.ctx, command.ExpectedRevision,
		jobcontract.ExecutionTarget{Kind: command.Location.Kind, NodeID: command.Location.ConfigurationID}))
}

func (app *DesktopApp) RetryRemoteInstallation(command RemoteNodeAction) error {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.remoteNodes.RetryInstallation(lease.ctx, command.ID, command.ExpectedRevision))
}

func (app *DesktopApp) RemoveRemoteNode(command RemoteNodeAction) error {
	lease, err := app.acquire(desktopRequirements{remoteNodes: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	app.executionLocationMu.Lock()
	defer app.executionLocationMu.Unlock()
	if err := lease.ctx.Err(); err != nil {
		return app.safeBindingError(err)
	}
	return app.safeBindingError(lease.remoteNodes.Remove(lease.ctx, command.ID, command.ExpectedRevision))
}
