package remotenodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

var ErrUpgradeIdentity = errors.New("remote_upgrade_identity_conflict")

// Upgrade is a local, durable intent. Its optional Operation is exclusively a
// confirmed server journal, never an inferred receipt from a failed SSH call.
type Upgrade struct {
	Request            runnerinstall.UpgradeRequest    `json:"request"`
	Release            string                          `json:"release"`
	Arch               string                          `json:"arch"`
	TemporaryDirectory string                          `json:"temporary_directory"`
	Stage              string                          `json:"stage"`
	ErrorCode          string                          `json:"error_code"`
	Operation          *runnerinstall.UpgradeOperation `json:"operation"`
	ChangedAt          time.Time                       `json:"changed_at"`
}

type UpgradeCommand struct {
	ID               string
	ExpectedRevision uint64
	OperationID      string
	ExpectedBuildID  string
}

type UpgradeAction struct {
	ID               string
	ExpectedRevision uint64
	OperationID      string
}

type upgradeTask struct {
	operationID string
	done        chan struct{}
}

type upgradeAction string

const (
	upgradeStart              upgradeAction = "start"
	upgradeRetry              upgradeAction = "retry"
	upgradeRecover            upgradeAction = "recover"
	upgradeObservationTimeout               = 10 * time.Second
)

func (attempt Upgrade) Validate() error {
	validArtifact := runnerartifacts.ValidRelease(attempt.Release) &&
		(attempt.Arch == "amd64" || attempt.Arch == "arm64")
	validDirectory := attempt.TemporaryDirectory == remotessh.UpgradeDirectory(attempt.Request.OperationID)
	_, offset := attempt.ChangedAt.Zone()
	validTime := !attempt.ChangedAt.IsZero() && offset == 0
	validStage := attempt.Stage == "uploading" ||
		(runnerinstall.UpgradeProgress{OperationID: attempt.Request.OperationID, Stage: attempt.Stage}).Validate() == nil
	validError := attempt.ErrorCode == "" || attempt.ErrorCode == installationCode(installationCause(attempt.ErrorCode))
	if !validArtifact || !validDirectory || !validTime || !validStage || !validError {
		return ErrInvalid
	}
	if err := attempt.Request.Validate(); err != nil {
		return err
	}
	if operation := attempt.Operation; operation != nil {
		if operation.Validate() != nil || operation.Request != attempt.Request {
			return ErrInvalid
		}
	}
	return nil
}

func unresolvedUpgrade(attempt *Upgrade) bool {
	if attempt == nil || attempt.Operation == nil {
		return attempt != nil
	}
	return attempt.Operation.Stage != "completed" && attempt.Operation.Stage != "rolled_back"
}

func upgradeTerminal(operation *runnerinstall.UpgradeOperation) bool {
	return operation != nil && (operation.Stage == "completed" || operation.Stage == "rolled_back")
}

// Upgrade intents have their own current canonical file. Adding an operation
// does not reinterpret or change the node configuration's persisted format.
func (store *FileStore) upgradePath(id string) string {
	return store.path + ".upgrade-" + id + ".json"
}

func (store *FileStore) readUpgrade(ctx context.Context, id string) (*Upgrade, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := directoryIdentity(filepath.Dir(store.path))
	if err != nil || !os.SameFile(root, store.rootInfo) {
		return nil, ErrConflict
	}
	name := store.upgradePath(id)
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > runnerinstall.MaxRecordBytes {
		return nil, ErrInvalid
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrConflict
	}
	document, err := io.ReadAll(io.LimitReader(file, runnerinstall.MaxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	var attempt Upgrade
	if len(document) > runnerinstall.MaxRecordBytes || json.Unmarshal(document, &attempt) != nil {
		return nil, ErrInvalid
	}
	if err := attempt.Validate(); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(attempt)
	if err != nil || !bytes.Equal(canonical, document) {
		return nil, ErrInvalid
	}
	return &attempt, nil
}

// writeUpgrade is called under the node operation lock. An ambiguous rename is
// confirmed with the same bytes and another durable replacement, never a new ID.
func (store *FileStore) writeUpgrade(ctx context.Context, id string, attempt Upgrade) error {
	if err := attempt.Validate(); err != nil {
		return err
	}
	previous, err := store.readUpgrade(ctx, id)
	if err != nil {
		return err
	}
	if previous != nil && !attempt.ChangedAt.After(previous.ChangedAt) {
		// This timestamp orders local observations, independently of a server
		// clock or a desktop wall-clock correction across process restarts.
		attempt.ChangedAt = previous.ChangedAt.Add(time.Nanosecond)
	}
	document, err := json.Marshal(attempt)
	if err != nil || len(document) > runnerinstall.MaxRecordBytes {
		return ErrInvalid
	}
	name := store.upgradePath(id)
	if err := store.writeFile(ctx, name, document); err != nil {
		visible, readErr := store.readUpgrade(context.WithoutCancel(ctx), id)
		if readErr != nil || visible == nil {
			return err
		}
		confirmed, _ := json.Marshal(visible)
		if !bytes.Equal(document, confirmed) {
			return err
		}
		confirm, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if store.writeFile(confirm, name, document) != nil {
			return ErrCommitUnconfirmed
		}
	}
	return nil
}

// GetUpgrade reads only the local intent. It never authenticates, starts work,
// polls a remote node or turns an uncertain acknowledgment into confirmation.
func (manager *Manager) GetUpgrade(ctx context.Context, id string) (*Upgrade, error) {
	if !domain.IsUUID(id) {
		return nil, ErrInvalid
	}
	config, err := manager.configuration.store.Read(ctx)
	if err != nil {
		return nil, err
	}
	if nodeIndex(config, id) < 0 {
		return nil, ErrNotFound
	}
	return manager.configuration.store.readUpgrade(ctx, id)
}

func (manager *Manager) StartUpgrade(ctx context.Context, command UpgradeCommand) (*Upgrade, error) {
	valid := domain.IsUUID(command.ID) && domain.IsUUID(command.OperationID) &&
		command.ExpectedRevision > 0 && jobcontract.IsSHA256(command.ExpectedBuildID)
	if !valid {
		return nil, ErrInvalid
	}
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	var result *Upgrade
	err = manager.configuration.store.withNodeOperation(ctx, command.ID, func() error {
		node, err := manager.node(ctx, command.ID, command.ExpectedRevision)
		if err != nil {
			return err
		}
		current, err := manager.configuration.store.readUpgrade(ctx, command.ID)
		if err != nil {
			return err
		}
		if current != nil && current.Request.OperationID == command.OperationID {
			if current.Request.ExpectedBuildID != command.ExpectedBuildID {
				return ErrConflict
			}
			result = current
			return nil
		}
		if unresolvedUpgrade(current) {
			return ErrBusy
		}
		if node.Binding == nil || node.HostKey == nil || !node.Enabled || unresolvedInstallation(node.Installation) {
			return ErrRePairRequired
		}
		observation := node.Observation
		if observation == nil || observation.Runner == nil {
			return ErrConflict
		}
		info := observation.Runner
		age := manager.configuration.now().UTC().Sub(observation.CheckedAt)
		if age < 0 || age > ObservationLifetime || info.EngineBuildID != command.ExpectedBuildID {
			return ErrConflict
		}
		if info.NodeID != node.Binding.Identity.NodeID || info.NodeInstallationID != node.Binding.Identity.InstallationID {
			return ErrUpgradeIdentity
		}
		if manager.artifacts == nil {
			return runnerartifacts.ErrUnavailable
		}
		artifact, err := manager.artifacts.Open(ctx, manager.release, info.Arch)
		if err != nil {
			return err
		}
		defer artifact.Close()
		validArtifact := artifact.Manifest.Release == manager.release && artifact.Entry.OS == "linux" &&
			artifact.Entry.Arch == info.Arch && jobcontract.IsSHA256(artifact.Entry.SHA256)
		if !validArtifact {
			return runnerartifacts.ErrIntegrity
		}
		if artifact.Entry.SHA256 == command.ExpectedBuildID {
			return ErrConflict
		}
		attempt := Upgrade{
			Request: runnerinstall.UpgradeRequest{Contract: jobcontract.CurrentContract, OperationID: command.OperationID,
				NodeID: node.Binding.Identity.NodeID, InstallationID: node.Binding.Identity.InstallationID,
				ExpectedBuildID: command.ExpectedBuildID, ArtifactSHA256: artifact.Entry.SHA256},
			Release: manager.release, Arch: info.Arch, TemporaryDirectory: remotessh.UpgradeDirectory(command.OperationID),
			Stage: "uploading", ChangedAt: manager.configuration.now().UTC(),
		}
		if err := manager.configuration.store.writeUpgrade(ctx, node.ID, attempt); err != nil {
			return err
		}
		result = &attempt
		return nil
	})
	if err != nil {
		return result, err
	}
	// The durable ID is accepted even if shutdown wins before worker dispatch.
	// A later explicit Retry queries this exact operation before any execution.
	_ = manager.scheduleUpgrade(command.ID, command.ExpectedRevision, result.Request.OperationID, upgradeStart)
	return result, nil
}

func (manager *Manager) RetryUpgrade(ctx context.Context, action UpgradeAction) (*Upgrade, error) {
	return manager.continueUpgrade(ctx, action, upgradeRetry)
}

func (manager *Manager) RecoverUpgrade(ctx context.Context, action UpgradeAction) (*Upgrade, error) {
	return manager.continueUpgrade(ctx, action, upgradeRecover)
}

func (manager *Manager) continueUpgrade(
	ctx context.Context,
	action UpgradeAction,
	mode upgradeAction,
) (*Upgrade, error) {
	valid := domain.IsUUID(action.ID) && domain.IsUUID(action.OperationID) && action.ExpectedRevision > 0
	if !valid {
		return nil, ErrInvalid
	}
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if _, err := manager.node(ctx, action.ID, action.ExpectedRevision); err != nil {
		return nil, err
	}
	attempt, err := manager.configuration.store.readUpgrade(ctx, action.ID)
	if err != nil {
		return nil, err
	}
	if attempt == nil {
		return nil, ErrNotFound
	}
	if attempt.Request.OperationID != action.OperationID {
		return nil, ErrConflict
	}
	if upgradeTerminal(attempt.Operation) {
		return attempt, nil
	}
	if err := manager.scheduleUpgrade(action.ID, action.ExpectedRevision, action.OperationID, mode); err != nil {
		return attempt, err
	}
	return attempt, nil
}

func (manager *Manager) scheduleUpgrade(id string, revision uint64, operationID string, mode upgradeAction) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || manager.ctx.Err() != nil {
		return context.Canceled
	}
	if task := manager.upgrades[id]; task != nil {
		if task.operationID != operationID {
			return ErrBusy
		}
		return nil
	}
	task := &upgradeTask{operationID: operationID, done: make(chan struct{})}
	manager.upgrades[id] = task
	manager.workers.Add(1)
	go func() {
		defer manager.workers.Done()
		ctx, cancel := context.WithTimeout(manager.ctx, 15*time.Minute)
		defer cancel()
		for {
			err := manager.configuration.store.withNodeOperation(ctx, id, func() error {
				return manager.runUpgrade(ctx, id, revision, operationID, mode)
			})
			if !errors.Is(err, ErrBusy) {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(25 * time.Millisecond):
				continue
			}
			break
		}
		manager.mu.Lock()
		delete(manager.upgrades, id)
		close(task.done)
		manager.mu.Unlock()
	}()
	return nil
}

func (manager *Manager) runUpgrade(
	ctx context.Context,
	id string,
	revision uint64,
	operationID string,
	mode upgradeAction,
) error {
	attempt, err := manager.configuration.store.readUpgrade(ctx, id)
	if err != nil || attempt == nil {
		return err
	}
	if attempt.Request.OperationID != operationID {
		return ErrConflict
	}
	if upgradeTerminal(attempt.Operation) {
		return nil
	}
	defer func() {
		if err == nil {
			return
		}
		latest := attempt.Operation
		if latest != nil {
			attempt.Stage = latest.Stage
		}
		attempt.ErrorCode, attempt.ChangedAt = installationCode(err), manager.configuration.now().UTC()
		uncertainObservation := attempt.ErrorCode == "remote_connection_failed"
		if latest != nil && (upgradeTerminal(latest) || latest.ErrorCode != "" && uncertainObservation) {
			attempt.ErrorCode = latest.ErrorCode
		}
		confirm, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = manager.configuration.store.writeUpgrade(confirm, id, *attempt)
	}()
	connection, err := manager.configuration.AcquireSSH(ctx, id, revision)
	if err != nil {
		return err
	}
	defer connection.Close()
	node := connection.Node
	if node.Binding == nil || node.Binding.Identity.NodeID != attempt.Request.NodeID ||
		node.Binding.Identity.InstallationID != attempt.Request.InstallationID {
		err = ErrUpgradeIdentity
		return err
	}
	session, err := manager.connect(ctx, node.Endpoint(), connection.Authentication)
	if err != nil {
		return err
	}
	defer session.Close()
	accept := func(operation runnerinstall.UpgradeOperation) error {
		return manager.acceptUpgrade(
			ctx,
			id,
			attempt,
			operation,
		)
	}
	operation, queryErr := session.UpgradeOperation(ctx, operationID)
	if queryErr == nil {
		if err = accept(operation); err != nil {
			return err
		}
		terminal := upgradeTerminal(attempt.Operation)
		if mode == upgradeStart || terminal {
			return nil
		}
	} else if !errors.Is(queryErr, os.ErrNotExist) {
		err = queryErr
		return err
	} else if mode == upgradeRecover {
		err = ErrNotFound
		return err
	}
	upload := remotessh.Artifact{OS: "linux", Arch: attempt.Arch, SHA256: attempt.Request.ArtifactSHA256}
	var artifact *runnerartifacts.Artifact
	artifactErr := error(runnerartifacts.ErrUnavailable)
	if manager.artifacts != nil {
		artifact, artifactErr = manager.artifacts.Open(ctx, attempt.Release, attempt.Arch)
	}
	if artifactErr != nil {
		// A confirmed same-operation stage can retain its verified executable
		// after a desktop release is removed locally. The SSH boundary must
		// verify those exact bytes; it cannot create or substitute a program.
		if queryErr != nil || mode == upgradeStart {
			err = artifactErr
			return err
		}
	} else {
		defer artifact.Close()
		validArtifact := artifact.Manifest.Release == attempt.Release && artifact.Entry.OS == "linux" &&
			artifact.Entry.Arch == attempt.Arch && artifact.Entry.SHA256 == attempt.Request.ArtifactSHA256
		if !validArtifact {
			err = runnerartifacts.ErrIntegrity
			return err
		}
		upload.Size, upload.Source = artifact.Entry.Size, artifact.Source
	}
	progress := func(progress runnerinstall.UpgradeProgress) {
		if progress.OperationID != operationID || progress.Validate() != nil {
			return
		}
		// Keep the complete journal together with the displayed phase. A phase
		// notification alone cannot confirm the irreversible resume boundary.
		confirmed, queryErr := session.UpgradeOperation(ctx, operationID)
		if queryErr == nil {
			_ = accept(confirmed)
			return
		}
		if progress.Stage == "resume_attempted" || progress.Stage == "rolling_back" {
			attempt.Stage, attempt.ErrorCode = progress.Stage, "remote_connection_failed"
			attempt.ChangedAt = manager.configuration.now().UTC()
			_ = manager.configuration.store.writeUpgrade(ctx, id, *attempt)
		}
	}
	switch mode {
	case upgradeRecover:
		operation, err = session.RecoverUpgrade(ctx, attempt.Request, upload, progress)
	case upgradeRetry:
		if errors.Is(queryErr, os.ErrNotExist) {
			operation, err = session.Upgrade(ctx, attempt.Request, upload, progress)
		} else {
			operation, err = session.RetryUpgrade(ctx, attempt.Request, upload, progress)
		}
	default:
		operation, err = session.Upgrade(ctx, attempt.Request, upload, progress)
	}
	if operation.Validate() == nil && operation.Request == attempt.Request {
		if recordErr := accept(operation); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
	} else if err == nil {
		err = remotessh.ErrIntegrity
	}
	return err
}

// observeUpgrade performs one journal read, independently of the explicit-action
// worker. Missing or unreachable journals never enter an execution retry path.
func (manager *Manager) observeUpgrade(
	ctx context.Context,
	node Node,
	request runnerinstall.UpgradeRequest,
	session RemoteSession,
) error {
	ctx, cancel := context.WithTimeout(ctx, upgradeObservationTimeout)
	defer cancel()
	err := manager.configuration.store.withNodeOperation(ctx, node.ID, func() error {
		current, err := manager.node(ctx, node.ID, node.Revision)
		if err != nil {
			return err
		}
		sameIdentity := current.Binding != nil && current.Binding.Identity.NodeID == request.NodeID &&
			current.Binding.Identity.InstallationID == request.InstallationID
		if !sameIdentity {
			return ErrUpgradeIdentity
		}
		attempt, err := manager.configuration.store.readUpgrade(ctx, node.ID)
		if err != nil {
			return err
		}
		if attempt == nil || attempt.Request != request {
			return ErrConflict
		}
		if upgradeTerminal(attempt.Operation) {
			return nil
		}
		operation, err := session.UpgradeOperation(ctx, request.OperationID)
		if err != nil {
			return err
		}
		return manager.acceptUpgrade(
			ctx,
			node.ID,
			attempt,
			operation,
		)
	})
	if errors.Is(err, ErrBusy) {
		// An explicit action or another client's observer owns this node. It
		// will persist its result; observation must not delay or replace it.
		return nil
	}
	return err
}

// acceptUpgrade is shared by action acknowledgments and read-only observations.
// Callers hold the node operation lock and have checked the node revision.
func (manager *Manager) acceptUpgrade(
	ctx context.Context,
	id string,
	attempt *Upgrade,
	operation runnerinstall.UpgradeOperation,
) error {
	if operation.Validate() != nil || operation.Request != attempt.Request {
		return remotessh.ErrIntegrity
	}
	if previous := attempt.Operation; previous != nil {
		// Irreversible facts order observations even across server clock drift.
		resumeRegressed := previous.ResumeAttempted && !operation.ResumeAttempted
		if upgradeTerminal(previous) || resumeRegressed {
			return nil
		}
		changedBranch := operation.Stage != "rolling_back" && operation.Stage != "rolled_back"
		restoreReenabled := !previous.Restorable && operation.Restorable
		rollbackRegressed := previous.Stage == "rolling_back" && (changedBranch || restoreReenabled)
		if rollbackRegressed {
			return nil
		}
		strongerResume := operation.ResumeAttempted && !previous.ResumeAttempted
		strongerRollback := operation.Stage == "rolling_back" && !operation.Restorable &&
			(previous.Stage != "rolling_back" || previous.Restorable)
		stronger := strongerResume || strongerRollback || upgradeTerminal(&operation)
		if !stronger && operation.ChangedAt.Before(previous.ChangedAt) {
			return nil
		}
		if !stronger && operation.ChangedAt.Equal(previous.ChangedAt) {
			before, _ := json.Marshal(previous)
			after, _ := json.Marshal(operation)
			if !bytes.Equal(before, after) {
				return nil
			}
		}
	}
	attempt.Operation, attempt.Stage, attempt.ErrorCode = &operation, operation.Stage, operation.ErrorCode
	attempt.ChangedAt = manager.configuration.now().UTC()
	return manager.configuration.store.writeUpgrade(ctx, id, *attempt)
}
