package remotenodes

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func (manager *Manager) resolveInstallation(ctx context.Context, node Node, session RemoteSession) (Node, error) {
	resolved := node
	err := manager.configuration.store.withNodeOperation(ctx, node.ID, func() error {
		current, err := manager.node(ctx, node.ID, node.Revision)
		if err != nil {
			return err
		}
		resolved, err = manager.resolveInstallationLocked(ctx, current, session)
		return err
	})
	return resolved, err
}

func (manager *Manager) resolveInstallationLocked(ctx context.Context, node Node, session RemoteSession) (Node, error) {
	if node.Installation == nil {
		return node, nil
	}
	attempt := *node.Installation
	changed := false
	if attempt.CleanupPending {
		if err := session.CleanupBootstrap(ctx, attempt.TemporaryDirectory); err != nil {
			return node, err
		}
		attempt.TemporaryDirectory, attempt.CleanupPending, changed = "", false, true
	}
	if attempt.Stage != "completed" || attempt.ErrorCode != "" {
		operation, err := session.Operation(ctx, attempt.Request.OperationID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return node, err
		}
		if err == nil {
			if operation.Request != attempt.Request {
				return node, remotessh.ErrIntegrity
			}
			if operation.Result != nil {
				valid := operation.Stage == "completed" && operation.Result.Validate() == nil &&
					operation.Result.OperationID == attempt.Request.OperationID
				if !valid {
					return node, remotessh.ErrIntegrity
				}
				attempt.Stage, attempt.ErrorCode, changed = "completed", "", true
			}
		}
	}
	if changed {
		if err := manager.recordInstallation(ctx, node, attempt); err != nil {
			return node, err
		}
		node.Installation = &attempt
	}
	return node, nil
}

func (manager *Manager) install(
	ctx context.Context,
	node Node,
	system remotessh.SystemInfo,
	session RemoteSession,
) error {
	return manager.configuration.store.withNodeOperation(ctx, node.ID, func() error {
		return manager.installLocked(
			ctx,
			node,
			system,
			session,
		)
	})
}

func (manager *Manager) installLocked(
	ctx context.Context,
	node Node,
	system remotessh.SystemInfo,
	session RemoteSession,
) error {
	node, err := manager.node(ctx, node.ID, node.Revision)
	if err != nil {
		return err
	}
	if system.UID != 0 {
		return runnerinstall.ErrRootRequired
	}
	if node.Installation != nil {
		if node.Installation.Stage == "completed" && node.Installation.ErrorCode == "" {
			// A completed operation with missing server identity needs repair,
			// never an automatic reinstall or node-token replacement.
			return runnerinstall.ErrPairingRequired
		}
		if node.Installation.ErrorCode != "" {
			return installationCause(node.Installation.ErrorCode)
		}
	}
	if manager.artifacts == nil {
		return runnerartifacts.ErrUnavailable
	}
	release := manager.release
	if node.Installation != nil {
		release = node.Installation.Release
		if node.Installation.Arch != system.Arch {
			return remotessh.ErrIntegrity
		}
	}
	artifact, err := manager.artifacts.Open(ctx, release, system.Arch)
	if err != nil {
		return err
	}
	defer artifact.Close()
	attempt := Installation{Release: release, Arch: system.Arch, Stage: "uploading"}
	if node.Installation != nil {
		attempt = *node.Installation
	}
	if attempt.Request.OperationID == "" {
		meta, err := domain.NewEntityMeta(manager.configuration.now())
		if err != nil {
			return err
		}
		attempt.Request = runnerinstall.Request{
			Contract: jobcontract.CurrentContract, OperationID: meta.ID,
			ArtifactSHA256: artifact.Entry.SHA256, ListenPort: 44917,
		}
	}
	validArtifact := artifact.Manifest.Release == release && artifact.Entry.OS == "linux" &&
		artifact.Entry.Arch == system.Arch && artifact.Entry.SHA256 == attempt.Request.ArtifactSHA256
	if !validArtifact {
		return runnerartifacts.ErrIntegrity
	}
	if err := manager.recordInstallation(ctx, node, attempt); err != nil {
		return err
	}
	node.Installation = &attempt
	if err := manager.configuration.RecordObservation(
		ctx,
		node.ID,
		node.Revision,
		Observation{CheckedAt: manager.configuration.now().UTC(), Status: "installing", System: &system},
	); err != nil {
		return err
	}
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var progressErr error
	result, provisionErr := session.Provision(
		operationCtx,
		attempt.Request,
		remotessh.Artifact{OS: artifact.Entry.OS, Arch: artifact.Entry.Arch,
			Size: artifact.Entry.Size, SHA256: artifact.Entry.SHA256, Source: artifact.Source},
		func(progress runnerinstall.Progress) {
			if progressErr != nil {
				return
			}
			valid := progress.OperationID == attempt.Request.OperationID && progress.Validate() == nil
			if !valid {
				progressErr = remotessh.ErrIntegrity
				cancel()
				return
			}
			attempt.Stage = progress.Stage
			progressErr = manager.recordInstallation(operationCtx, node, attempt)
			if progressErr != nil {
				cancel()
			}
		},
	)
	if progressErr != nil {
		provisionErr = errors.Join(progressErr, provisionErr)
	}
	if provisionErr == nil {
		if result.Validate() != nil || result.OperationID != attempt.Request.OperationID {
			provisionErr = remotessh.ErrIntegrity
		}
	}
	if provisionErr == nil {
		attempt.Stage, attempt.ErrorCode = "completed", ""
		attempt.TemporaryDirectory, attempt.CleanupPending = "", false
		return manager.recordInstallation(ctx, node, attempt)
	}
	attempt.ErrorCode = installationCode(provisionErr)
	if ctx.Err() != nil {
		// No failure acknowledgment is known. Next startup resolves the same
		// operation rather than inventing an independent installation attempt.
		attempt.ErrorCode = ""
	}
	var provision *remotessh.ProvisionError
	if errors.As(provisionErr, &provision) {
		attempt.TemporaryDirectory, attempt.CleanupPending = provision.TemporaryDirectory, provision.CleanupPending
	}
	confirm, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	return errors.Join(provisionErr, manager.recordInstallation(confirm, node, attempt))
}

func (manager *Manager) recordInstallation(ctx context.Context, node Node, attempt Installation) error {
	expectedOperation := ""
	if node.Installation != nil {
		expectedOperation = node.Installation.Request.OperationID
	}
	return manager.configuration.RecordInstallation(ctx, InstallationCommand{
		ID: node.ID, ExpectedRevision: node.Revision, ExpectedOperationID: expectedOperation, Installation: attempt,
	})
}

func installationCode(cause error) string {
	switch {
	case errors.Is(cause, runnerartifacts.ErrUnavailable):
		return "runner_release_unavailable"
	case errors.Is(cause, runnerartifacts.ErrInvalid):
		return "invalid_runner_release"
	case errors.Is(cause, runnerartifacts.ErrUnsupported):
		return "runner_release_upgrade_required"
	case errors.Is(cause, runnerartifacts.ErrIntegrity):
		return "runner_release_integrity_failed"
	case errors.Is(cause, remotessh.ErrConnection):
		return "remote_connection_failed"
	case errors.Is(cause, remotessh.ErrIntegrity):
		return "remote_file_integrity_failed"
	default:
		return runnerinstall.ErrorCode(cause)
	}
}

func installationCause(code string) error {
	switch code {
	case "runner_release_unavailable":
		return runnerartifacts.ErrUnavailable
	case "invalid_runner_release":
		return runnerartifacts.ErrInvalid
	case "runner_release_upgrade_required":
		return runnerartifacts.ErrUnsupported
	case "runner_release_integrity_failed":
		return runnerartifacts.ErrIntegrity
	case "remote_connection_failed":
		return remotessh.ErrConnection
	case "remote_file_integrity_failed":
		return remotessh.ErrIntegrity
	default:
		return runnerinstall.ErrorFromCode(code)
	}
}
