package runnerinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type upgradeSystem interface {
	Preflight(context.Context) error
	Account(context.Context) (account, error)
	Stop(context.Context) error
	Start(context.Context) error
	Verify(context.Context, verificationInput) error
}

type upgrader struct {
	layout   layout
	system   upgradeSystem
	source   io.ReadSeeker
	progress func(UpgradeProgress)
	probe    func(context.Context, runnerconfig.Config, []byte) (jobcontract.RunnerInfo, error)
	maintain func(context.Context, runnerconfig.Config, []byte, jobcontract.MaintenanceCommand) (jobhost.MaintenanceResult, error)
	backup   func(context.Context, string, string) (sqlite.RunnerBackup, error)
	verifyDB func(context.Context, string) (sqlite.RunnerBackup, error)
}

// Upgrade creates a single durable operation. Repeating the exact request reads
// its journal; RetryUpgrade is the explicit instruction to continue interrupted work.
func Upgrade(ctx context.Context, request UpgradeRequest, progress func(UpgradeProgress)) (UpgradeOperation, error) {
	return productionUpgrade(ctx, request, progress, "upgrade")
}

func RetryUpgrade(ctx context.Context, request UpgradeRequest, progress func(UpgradeProgress)) (UpgradeOperation, error) {
	return productionUpgrade(ctx, request, progress, "retry")
}

func RecoverUpgrade(ctx context.Context, request UpgradeRequest, progress func(UpgradeProgress)) (UpgradeOperation, error) {
	return productionUpgrade(ctx, request, progress, "recover")
}

func productionUpgrade(
	ctx context.Context,
	request UpgradeRequest,
	progress func(UpgradeProgress),
	mode string,
) (UpgradeOperation, error) {
	if ctx == nil {
		return UpgradeOperation{}, ErrInvalid
	}
	if err := request.Validate(); err != nil {
		return UpgradeOperation{}, err
	}
	if os.Geteuid() != 0 {
		return UpgradeOperation{}, ErrRootRequired
	}
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		return UpgradeOperation{}, err
	}
	defer source.Close()
	paths := productionLayout()
	installer := upgrader{
		layout: paths, system: localUpgradeSystem{localSystem{layout: paths}}, source: source, progress: progress,
		probe: probeRunner, maintain: applyUpgradeMaintenance,
		backup: sqlite.BackupRunnerDatabase, verifyDB: sqlite.VerifyRunnerDatabase,
	}
	return installer.run(ctx, request, mode)
}

func (installer upgrader) run(
	ctx context.Context,
	request UpgradeRequest,
	mode string,
) (operation UpgradeOperation, runErr error) {
	if ctx == nil || request.Validate() != nil {
		return UpgradeOperation{}, ErrInvalid
	}
	digest, err := sourceDigest(installer.source)
	if err != nil || digest != request.ArtifactSHA256 {
		return UpgradeOperation{}, ErrIntegrity
	}
	if err := installer.system.Preflight(ctx); err != nil {
		return UpgradeOperation{}, err
	}
	if err := installer.layout.verifyBases(); err != nil {
		return UpgradeOperation{}, err
	}
	if err := installer.layout.checkInstallationRoot(); err != nil {
		return UpgradeOperation{}, err
	}
	lock, err := runnerstate.Open(installer.layout.install)
	if errors.Is(err, runnerstate.ErrLocked) {
		return UpgradeOperation{}, ErrBusy
	}
	if err != nil {
		return UpgradeOperation{}, err
	}
	defer lock.Close()
	if err := installer.layout.checkProgramRoot(lock); err != nil {
		return UpgradeOperation{}, err
	}
	operation, exists, err := readUpgradeOperation(lock, request)
	if err != nil {
		return UpgradeOperation{}, err
	}
	if exists && (mode == "upgrade" || operation.Result != nil || operation.Stage == "rolled_back") {
		return operation, nil
	}
	if exists && operation.Stage == "rolling_back" && mode != "recover" {
		return operation, ErrConflict
	}
	if !exists && mode != "upgrade" {
		return UpgradeOperation{}, ErrConflict
	}
	if err := claimUpgrade(lock, request); err != nil {
		return operation, err
	}
	if !exists {
		operation = UpgradeOperation{Request: request, Stage: "checking", ChangedAt: time.Now().UTC()}
		if err := writeUpgradeOperation(lock, operation); err != nil {
			return operation, err
		}
	}
	defer func() {
		if runErr != nil && operation.Result == nil {
			operation.ErrorCode, operation.ChangedAt = ErrorCode(runErr), time.Now().UTC()
			if err := writeUpgradeOperation(lock, operation); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
	}()
	checkpoint := func(stage string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		operation.Stage, operation.ErrorCode, operation.ChangedAt = stage, "", time.Now().UTC()
		if err := writeUpgradeOperation(lock, operation); err != nil {
			return err
		}
		if installer.progress != nil {
			installer.progress(UpgradeProgress{OperationID: request.OperationID, Stage: stage})
		}
		return nil
	}
	owner, err := installer.system.Account(ctx)
	if err != nil {
		return operation, err
	}
	config, token, err := installer.credentials(lock, owner, request)
	if err != nil {
		return operation, err
	}
	defer config.Clear()
	defer clear(token)
	selected, err := installer.layout.currentBuild()
	if err != nil {
		return operation, err
	}
	if operation.ResumeAttempted {
		if selected != request.ArtifactSHA256 {
			return operation, ErrIntegrity
		}
		err := installer.finish(ctx, lock, &operation, owner, config, token, checkpoint)
		return operation, err
	}
	if mode == "recover" {
		err := installer.rollback(ctx, lock, &operation, owner, config, token, checkpoint)
		return operation, err
	}
	validSelection := selected == request.ExpectedBuildID || operation.Restorable && selected == request.ArtifactSHA256
	if !validSelection {
		return operation, ErrConflict
	}
	if !operation.Restorable {
		restartOld := operation.Stage == "stopping" || operation.Stage == "stopped" || operation.Stage == "backing_up"
		if restartOld {
			if err := installer.system.Start(ctx); err != nil {
				return operation, err
			}
		}
		if err := checkpoint("pausing"); err != nil {
			return operation, err
		}
		info, err := installer.probe(ctx, config, token)
		if err != nil {
			return operation, err
		}
		if info.EngineBuildID != request.ExpectedBuildID {
			return operation, ErrConflict
		}
		if info.Pending != 0 {
			return operation, ErrBusy
		}
		if info.MaintenanceOperationID != "" && info.MaintenanceOperationID != request.OperationID {
			return operation, ErrConflict
		}
		paused, err := installer.maintain(ctx, config, token, upgradeCommand(request, jobcontract.MaintenancePause))
		if err != nil {
			return operation, err
		}
		if paused.Status.Pending != 0 {
			// The current maintenance protocol permits one pause/resume cycle
			// per operation. Keep this confirmed gate closed while the raced
			// jobs finish; Retry can reuse it, and Recover explicitly reopens it.
			return operation, ErrBusy
		}
		if err := checkpoint("stopping"); err != nil {
			return operation, err
		}
		if err := installer.system.Stop(ctx); err != nil {
			return operation, err
		}
		if err := checkpoint("stopped"); err != nil {
			return operation, err
		}
		runtimeLock, err := lockUpgradeRuntime(installer.layout, owner)
		if err != nil {
			return operation, err
		}
		defer runtimeLock.Close()
		if err := checkpoint("backing_up"); err != nil {
			return operation, err
		}
		if err := installer.backupFiles(ctx, request, owner); err != nil {
			return operation, err
		}
		operation.Restorable = true
		if err := checkpoint("backed_up"); err != nil {
			return operation, err
		}
		if err := runtimeLock.Close(); err != nil {
			return operation, err
		}
	}
	// A retry must re-establish the service and runtime locks before touching the
	// selected executable, even if the previous process stopped after its backup.
	runtimeLock, err := installer.stopUpgradeIdle(ctx, request, owner, config, token)
	if err != nil {
		return operation, err
	}
	defer runtimeLock.Close()
	if err := installer.verifyBackup(ctx, request, owner); err != nil {
		return operation, err
	}
	if err := checkpoint("cutover"); err != nil {
		return operation, err
	}
	publisher := provisioner{layout: installer.layout, source: installer.source}
	if err := publisher.publishProgram(request.ArtifactSHA256, request.OperationID); err != nil {
		return operation, err
	}
	if err := installer.layout.replaceCurrent(request.ArtifactSHA256); err != nil {
		return operation, err
	}
	if err := runtimeLock.Close(); err != nil {
		return operation, err
	}
	err = installer.finish(ctx, lock, &operation, owner, config, token, checkpoint)
	return operation, err
}

func (installer upgrader) stopUpgradeIdle(
	ctx context.Context,
	request UpgradeRequest,
	owner account,
	config runnerconfig.Config,
	token []byte,
) (*upgradeRuntimeLock, error) {
	info, probeErr := installer.probe(ctx, config, token)
	validatedUnhealthy := errors.Is(probeErr, ErrService) && info.Validate() == nil && !info.Healthy
	if probeErr == nil || validatedUnhealthy {
		validIdentity := info.NodeID == request.NodeID && info.NodeInstallationID == request.InstallationID
		validBuild := info.EngineBuildID == request.ExpectedBuildID || info.EngineBuildID == request.ArtifactSHA256
		if !validIdentity || !validBuild {
			return nil, ErrIntegrity
		}
		if info.Pending != 0 {
			return nil, ErrBusy
		}
		if info.MaintenanceOperationID != request.OperationID {
			return nil, ErrConflict
		}
		if err := installer.system.Stop(ctx); err != nil {
			return nil, err
		}
		return lockUpgradeRuntime(installer.layout, owner)
	}
	// An unavailable API is not evidence that a running runner has no work.
	// Acquiring its runtime lock independently proves that process is stopped.
	lock, err := lockUpgradeRuntime(installer.layout, owner)
	if err != nil {
		return nil, ErrBusy
	}
	if err := installer.system.Stop(ctx); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func (installer upgrader) credentials(
	state *runnerstate.State,
	owner account,
	request UpgradeRequest,
) (runnerconfig.Config, []byte, error) {
	config, exists, err := installer.layout.readConfig(owner)
	if err != nil || !exists {
		config.Clear()
		return runnerconfig.Config{}, nil, ErrPairingRequired
	}
	fail := func(err error) (runnerconfig.Config, []byte, error) {
		config.Clear()
		return runnerconfig.Config{}, nil, err
	}
	if config.NodeID != request.NodeID || config.InstallationID != request.InstallationID {
		return fail(ErrConflict)
	}
	document, err := state.ReadPrivate("identity.json", MaxRequestBytes)
	if err != nil {
		return fail(ErrPairingRequired)
	}
	identity, err := DecodeIdentity(document)
	expected := Identity{
		Contract: config.Contract, NodeID: config.NodeID, InstallationID: config.InstallationID,
		ListenPort: config.ListenPort, TokenSHA256: config.TokenSHA256,
	}
	if err != nil || identity != expected {
		return fail(ErrPairingRequired)
	}
	token, err := state.ReadPrivate(PairingName, 128)
	if err != nil || !tokenMatches(config, token) {
		clear(token)
		return fail(ErrPairingRequired)
	}
	unit, err := installer.layout.checkUnit()
	if err != nil || !unit {
		clear(token)
		return fail(ErrUnownedPath)
	}
	return config, token, nil
}

func (installer upgrader) finish(
	ctx context.Context,
	state *runnerstate.State,
	operation *UpgradeOperation,
	owner account,
	config runnerconfig.Config,
	token []byte,
	checkpoint func(string) error,
) error {
	request := operation.Request
	if !operation.ResumeAttempted {
		if err := checkpoint("verifying"); err != nil {
			return err
		}
	}
	// Start is idempotent for an already active service. Explicit post-resume
	// recovery may start this selected new program, but never stops it or
	// touches the old program/database, including after an ambiguous ACK.
	if err := installer.system.Start(ctx); err != nil {
		return err
	}
	if err := installer.system.Verify(ctx, verificationInput{account: owner, buildID: request.ArtifactSHA256}); err != nil {
		return err
	}
	info, err := installer.probe(ctx, config, token)
	if err != nil || info.EngineBuildID != request.ArtifactSHA256 {
		return ErrService
	}
	if !operation.ResumeAttempted {
		paused := info.MaintenanceOperationID == request.OperationID && !info.Ready && info.Pending == 0
		if !paused {
			return ErrIntegrity
		}
		if _, err := installer.verifyDB(ctx, filepath.Join(installer.layout.state, "runner.db")); err != nil {
			return ErrUpgradeRequired
		}
		operation.ResumeAttempted, operation.Restorable = true, false
		operation.Stage, operation.ErrorCode, operation.ChangedAt = "resume_attempted", "", time.Now().UTC()
		if err := writeUpgradeOperation(state, *operation); err != nil {
			return err
		}
		if installer.progress != nil {
			installer.progress(UpgradeProgress{OperationID: request.OperationID, Stage: operation.Stage})
		}
	}
	if info.MaintenanceOperationID == request.OperationID {
		if _, err := installer.maintain(ctx, config, token, upgradeCommand(request, jobcontract.MaintenanceResume)); err != nil {
			return err
		}
	} else if info.MaintenanceOperationID != "" {
		return ErrConflict
	}
	info, err = installer.probe(ctx, config, token)
	if err != nil || info.EngineBuildID != request.ArtifactSHA256 || !info.Ready {
		return ErrService
	}
	operation.Result = &UpgradeResult{
		Contract: jobcontract.CurrentContract, OperationID: request.OperationID, Runner: info, ObservedAt: time.Now().UTC(),
	}
	operation.Stage, operation.ErrorCode, operation.ChangedAt = "completed", "", time.Now().UTC()
	if err := writeUpgradeOperation(state, *operation); err != nil {
		return err
	}
	return nil
}

func upgradeCommand(request UpgradeRequest, action jobcontract.MaintenanceAction) jobcontract.MaintenanceCommand {
	return jobcontract.MaintenanceCommand{Contract: request.Contract, OperationID: request.OperationID, Action: action}
}

func readUpgradeOperation(state *runnerstate.State, request UpgradeRequest) (UpgradeOperation, bool, error) {
	document, err := state.ReadPrivate(upgradeOperationName(request.OperationID), MaxRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		return UpgradeOperation{}, false, nil
	}
	if err != nil {
		return UpgradeOperation{}, false, err
	}
	operation, err := DecodeUpgradeOperation(document)
	if err != nil {
		return UpgradeOperation{}, false, err
	}
	if operation.Request != request {
		return UpgradeOperation{}, false, ErrConflict
	}
	return operation, true, state.SyncPrivate(upgradeOperationName(request.OperationID))
}

func writeUpgradeOperation(state *runnerstate.State, operation UpgradeOperation) error {
	document, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	if _, err := DecodeUpgradeOperation(document); err != nil {
		return err
	}
	name := upgradeOperationName(operation.Request.OperationID)
	if err := state.ReplacePrivate(name, document); err != nil {
		confirmed, readErr := state.ReadPrivate(name, MaxRecordBytes)
		if readErr != nil || !bytes.Equal(confirmed, document) {
			return err
		}
		return state.SyncPrivate(name)
	}
	return nil
}

func claimUpgrade(state *runnerstate.State, request UpgradeRequest) error {
	const name = "active-upgrade.json"
	document, err := state.ReadPrivate(name, MaxRequestBytes)
	if err == nil {
		active, decodeErr := DecodeUpgradeRequest(bytes.NewReader(document))
		if decodeErr != nil {
			return decodeErr
		}
		if active != request {
			operation, exists, err := readUpgradeOperation(state, active)
			if err != nil || !exists {
				return ErrConflict
			}
			if operation.Stage != "completed" && operation.Stage != "rolled_back" {
				return ErrBusy
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	document, _ = json.Marshal(request)
	if err := state.ReplacePrivate(name, document); err != nil {
		return err
	}
	return nil
}

func checkNoActiveUpgrade(state *runnerstate.State) error {
	document, err := state.ReadPrivate("active-upgrade.json", MaxRequestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	request, err := DecodeUpgradeRequest(bytes.NewReader(document))
	if err != nil {
		return err
	}
	operation, exists, err := readUpgradeOperation(state, request)
	if err != nil || !exists {
		return ErrBusy
	}
	if operation.Stage != "completed" && operation.Stage != "rolled_back" {
		return ErrBusy
	}
	return nil
}
