package runnerinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func (installer upgrader) rollback(
	ctx context.Context,
	state *runnerstate.State,
	operation *UpgradeOperation,
	owner account,
	config runnerconfig.Config,
	token []byte,
	checkpoint func(string) error,
) error {
	// This guard also covers a resume RPC whose acknowledgement was lost.
	if operation.ResumeAttempted {
		return ErrConflict
	}
	request := operation.Request
	selected, err := installer.layout.currentBuild()
	if err != nil {
		return err
	}
	validSelection := selected == request.ExpectedBuildID || operation.Restorable && selected == request.ArtifactSHA256
	if !validSelection {
		return ErrConflict
	}
	if err := checkpoint("rolling_back"); err != nil {
		return err
	}
	if operation.Restorable {
		runtimeLock, err := installer.stopUpgradeIdle(ctx, request, owner, config, token)
		if err != nil {
			return err
		}
		defer runtimeLock.Close()
		if err := installer.verifyBackup(ctx, request, owner); err != nil {
			return err
		}
		directory := filepath.Join(installer.layout.install, upgradeDataName(request.OperationID))
		for _, suffix := range []string{"-wal", "-shm"} {
			name := filepath.Join(installer.layout.state, "runner.db"+suffix)
			file, err := openOwned(name, owner.uid, true)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			_ = file.Close()
			if err := os.Remove(name); err != nil {
				return err
			}
		}
		if err := restoreUpgradeFile(ctx, filepath.Join(directory, "runner.db"), filepath.Join(installer.layout.state, "runner.db"), owner, 0600); err != nil {
			return err
		}
		if err := restoreUpgradeFile(ctx, filepath.Join(directory, "runner.json"), filepath.Join(installer.layout.state, runnerprocess.ConfigName), owner, 0600); err != nil {
			return err
		}
		rootOwner := account{uid: os.Geteuid(), gid: os.Getegid()}
		if err := restoreUpgradeFile(ctx, filepath.Join(directory, "runner.service"), installer.layout.unit, rootOwner, 0644); err != nil {
			return err
		}
		if err := restoreUpgradeFile(ctx, filepath.Join(directory, "runner.program"), installer.layout.programPath(request.ExpectedBuildID), rootOwner, 0755); err != nil {
			return err
		}
		if err := installer.layout.replaceCurrent(request.ExpectedBuildID); err != nil {
			return err
		}
		if err := runtimeLock.Close(); err != nil {
			return err
		}
	}
	if err := installer.system.Start(ctx); err != nil {
		return err
	}
	if err := installer.system.Verify(ctx, verificationInput{account: owner, buildID: request.ExpectedBuildID}); err != nil {
		return err
	}
	info, err := installer.probe(ctx, config, token)
	if err != nil || info.EngineBuildID != request.ExpectedBuildID {
		return ErrService
	}
	if info.MaintenanceOperationID == request.OperationID {
		if info.Pending != 0 {
			return ErrBusy
		}
		// Rollback has already restored and verified the old state. Persist the
		// end of its restore permission before reopening admission, including an
		// ambiguous resume acknowledgement on a later Recover invocation.
		operation.Restorable = false
		operation.ChangedAt = time.Now().UTC()
		if err := writeUpgradeOperation(state, *operation); err != nil {
			return err
		}
		if _, err := installer.maintain(ctx, config, token, upgradeCommand(request, jobcontract.MaintenanceResume)); err != nil {
			return err
		}
	} else if info.MaintenanceOperationID != "" {
		return ErrConflict
	}
	info, err = installer.probe(ctx, config, token)
	if err != nil || !info.Ready || info.EngineBuildID != request.ExpectedBuildID {
		return ErrService
	}
	operation.Restorable = false
	operation.Stage, operation.ErrorCode, operation.ChangedAt = "rolled_back", "", time.Now().UTC()
	operation.Result = &UpgradeResult{
		Contract: jobcontract.CurrentContract, OperationID: request.OperationID, Runner: info, ObservedAt: time.Now().UTC(),
	}
	return writeUpgradeOperation(state, *operation)
}
