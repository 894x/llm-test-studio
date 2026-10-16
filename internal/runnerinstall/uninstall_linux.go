package runnerinstall

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type uninstallSystem interface {
	Preflight(context.Context) error
	Account(context.Context) (account, error)
	Verify(context.Context, verificationInput) error
	VerifyStopped(context.Context) error
	Stop(context.Context) error
	Start(context.Context) error
	Disable(context.Context) error
	Reload(context.Context) error
	VerifyRemoved(context.Context) error
}

type uninstaller struct {
	layout      layout
	system      uninstallSystem
	progress    func(UninstallProgress)
	probe       func(context.Context, runnerconfig.Config, []byte) (jobcontract.RunnerInfo, error)
	maintain    uninstallMaintenance
	confirmIdle func(context.Context, layout, account, UninstallRequest) error
	remove      func(string) error
}

type uninstallMaintenance func(
	context.Context,
	runnerconfig.Config,
	[]byte,
	jobcontract.MaintenanceCommand,
) (jobhost.MaintenanceResult, error)

// Uninstall continues only the exact durable operation. It preserves all state
// and pairing information; completed removal blocks automatic reprovisioning.
func Uninstall(
	ctx context.Context,
	request UninstallRequest,
	progress func(UninstallProgress),
) (UninstallOperation, error) {
	return productionUninstall(
		ctx,
		request,
		progress,
		false,
	)
}

// RecoverUninstall restores service admission only before file removal begins.
// Once removal starts, Uninstall is the sole continuation of that operation.
func RecoverUninstall(
	ctx context.Context,
	request UninstallRequest,
	progress func(UninstallProgress),
) (UninstallOperation, error) {
	return productionUninstall(
		ctx,
		request,
		progress,
		true,
	)
}

func productionUninstall(
	ctx context.Context,
	request UninstallRequest,
	progress func(UninstallProgress),
	recoverService bool,
) (UninstallOperation, error) {
	if ctx == nil {
		return UninstallOperation{}, ErrInvalid
	}
	if err := request.Validate(); err != nil {
		return UninstallOperation{}, err
	}
	if os.Geteuid() != 0 {
		return UninstallOperation{}, ErrRootRequired
	}
	// Admin verification may read a large current database. This bounds the
	// complete new workflow without changing job execution or storage budgets.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	paths := productionLayout()
	system := localUninstallSystem{localUpgradeSystem: localUpgradeSystem{localSystem: localSystem{layout: paths}}}
	installer := uninstaller{
		layout: paths, system: system,
		progress: progress, probe: probeRunner, maintain: applyUpgradeMaintenance,
		confirmIdle: confirmUninstallIdle, remove: os.Remove,
	}
	return installer.run(ctx, request, recoverService)
}

func (installer uninstaller) run(
	ctx context.Context,
	request UninstallRequest,
	recoverService bool,
) (operation UninstallOperation, runErr error) {
	if ctx == nil {
		return operation, ErrInvalid
	}
	if err := request.Validate(); err != nil {
		return operation, err
	}
	if err := installer.system.Preflight(ctx); err != nil {
		return operation, err
	}
	if err := installer.layout.verifyBases(); err != nil {
		return operation, err
	}
	// Uninstall must not create an installation directory or service account.
	if err := trustedDirectory(installer.layout.install, os.Geteuid(), true); err != nil {
		return operation, ErrUnownedPath
	}
	if err := installer.layout.checkInstallationRoot(); err != nil {
		return operation, err
	}
	state, err := runnerstate.Open(installer.layout.install)
	if errors.Is(err, runnerstate.ErrLocked) {
		return operation, ErrBusy
	}
	if err != nil {
		return operation, err
	}
	defer state.Close()
	operation, exists, err := readUninstallOperation(state, request)
	if err != nil {
		return operation, err
	}
	if exists && operation.Result != nil {
		if recoverService == (operation.Stage == "aborted") {
			return operation, nil
		}
		return operation, ErrConflict
	}
	if recoverService && !exists || !recoverService && operation.Stage == "aborting" {
		return operation, ErrConflict
	}
	if err := checkNoActiveUpgrade(state); err != nil {
		return operation, err
	}
	active, err := activeUninstall(state)
	if err != nil {
		return operation, err
	}
	if active != nil && active.Request != request {
		if err := checkNoUninstall(state); err != nil {
			return operation, err
		}
	}
	owner, err := installer.system.Account(ctx)
	if err != nil {
		return operation, err
	}
	config, token, err := installer.layout.credentials(state, owner)
	if err != nil {
		return operation, err
	}
	defer config.Clear()
	defer clear(token)
	if config.NodeID != request.NodeID || config.InstallationID != request.InstallationID {
		return operation, ErrConflict
	}
	if !exists {
		manifest, err := installer.layout.uninstallManifest(state, request)
		if err != nil {
			return operation, err
		}
		operation = UninstallOperation{Request: request, Manifest: manifest, Stage: "checking", ChangedAt: time.Now().UTC()}
		if err := writeUninstallOperation(state, operation); err != nil {
			return operation, err
		}
	}
	// A journal may precede the durable active pointer. If another completed
	// operation changed the installation in that gap, reject the stale orphan
	// before it can reserve a gate that neither forward nor recovery can use.
	unclaimed := active == nil || active.Request != request
	if exists && operation.Stage == "checking" && unclaimed {
		if err := installer.layout.verifyUninstallFiles(operation.Manifest, request.ExpectedBuildID, false); err != nil {
			return operation, err
		}
	}
	if err := claimUninstall(state, request); err != nil {
		return operation, err
	}
	defer func() {
		if runErr != nil && operation.Result == nil {
			operation.ErrorCode, operation.ChangedAt = ErrorCode(runErr), time.Now().UTC()
			runErr = errors.Join(runErr, writeUninstallOperation(state, operation))
		}
	}()
	checkpoint := func(stage string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		operation.Stage, operation.ErrorCode, operation.ChangedAt = stage, "", time.Now().UTC()
		if err := writeUninstallOperation(state, operation); err != nil {
			return err
		}
		if installer.progress != nil {
			installer.progress(UninstallProgress{OperationID: request.OperationID, Stage: stage})
		}
		return ctx.Err()
	}
	if recoverService {
		err := installer.recover(
			ctx,
			&operation,
			config,
			token,
			checkpoint,
		)
		return operation, err
	}
	partial := operation.Stage == "removing"
	if err := installer.layout.verifyUninstallFiles(operation.Manifest, request.ExpectedBuildID, partial); err != nil {
		return operation, err
	}
	if !operation.PauseConfirmed {
		info, err := installer.probe(ctx, config, token)
		if err != nil {
			return operation, err // An unavailable API is never initial idle evidence.
		}
		if err := uninstallInfo(info, request); err != nil {
			return operation, err
		}
		if info.Pending != 0 {
			return operation, ErrBusy
		}
		input := verificationInput{account: owner, buildID: request.ExpectedBuildID}
		if err := installer.system.Verify(ctx, input); err != nil {
			return operation, err
		}
		foreignPause := info.MaintenanceOperationID != request.OperationID || operation.Stage == "checking"
		if info.MaintenanceOperationID != "" && foreignPause {
			return operation, ErrConflict
		}
		if err := checkpoint("pausing"); err != nil {
			return operation, err
		}
		paused, err := installer.maintain(
			ctx,
			config,
			token,
			uninstallCommand(request, jobcontract.MaintenancePause),
		)
		if err != nil {
			return operation, err // Upload busy and ambiguous pause ACK keep the same intent.
		}
		validRecord := paused.Record.Validate(request.InstallationID) == nil &&
			paused.Record.OperationID == request.OperationID && paused.Record.Paused
		validPause := validRecord && paused.Status.Healthy && !paused.Status.Ready &&
			paused.Status.MaintenanceOperationID == request.OperationID
		if !validPause {
			return operation, ErrIntegrity
		}
		if paused.Status.Pending != 0 {
			return operation, ErrBusy
		}
		operation.PauseConfirmed = true
		if err := checkpoint("paused"); err != nil {
			return operation, err
		}
	}
	if operation.Stage == "paused" {
		if err := checkpoint("stopping"); err != nil {
			return operation, err
		}
	}
	runtimeLock, err := installer.stopIdle(
		ctx,
		request,
		owner,
		config,
		token,
		partial,
	)
	if err != nil {
		return operation, err
	}
	defer runtimeLock.Close()
	// Recheck durable settled facts, including WAL-only commits, after acquiring
	// the real runtime inode. No database data or lock file is removed.
	if err := installer.confirmIdle(
		ctx,
		installer.layout,
		owner,
		request,
	); err != nil {
		return operation, err
	}
	if operation.Stage == "stopping" {
		if err := checkpoint("stopped"); err != nil {
			return operation, err
		}
	}
	if err := installer.layout.verifyUninstallFiles(operation.Manifest, request.ExpectedBuildID, partial); err != nil {
		return operation, err
	}
	if operation.Stage != "removing" {
		if err := checkpoint("disabling"); err != nil {
			return operation, err
		}
		if err := installer.system.Disable(ctx); err != nil {
			return operation, err
		}
		if err := checkpoint("removing"); err != nil {
			return operation, err
		}
	}
	if err := installer.removePrograms(ctx, operation); err != nil {
		return operation, err
	}
	if err := installer.system.Reload(ctx); err != nil {
		return operation, err
	}
	if err := installer.system.VerifyRemoved(ctx); err != nil {
		return operation, err
	}
	operation.Result = &UninstallResult{Contract: request.Contract, OperationID: request.OperationID,
		Uninstalled: true, DataRetained: true, ObservedAt: time.Now().UTC()}
	err = checkpoint("completed")
	return operation, err
}

func uninstallInfo(info jobcontract.RunnerInfo, request UninstallRequest) error {
	if info.Validate() != nil || info.NodeID != request.NodeID || info.NodeInstallationID != request.InstallationID ||
		info.EngineBuildID != request.ExpectedBuildID || !info.Healthy {
		return ErrIntegrity
	}
	return nil
}

func uninstallCommand(request UninstallRequest, action jobcontract.MaintenanceAction) jobcontract.MaintenanceCommand {
	return jobcontract.MaintenanceCommand{Contract: request.Contract, OperationID: request.OperationID, Action: action}
}

func (installer uninstaller) stopIdle(
	ctx context.Context,
	request UninstallRequest,
	owner account,
	config runnerconfig.Config,
	token []byte,
	removing bool,
) (*upgradeRuntimeLock, error) {
	info, probeErr := installer.probe(ctx, config, token)
	if probeErr == nil {
		if err := uninstallInfo(info, request); err != nil {
			return nil, err
		}
		if info.Pending != 0 {
			return nil, ErrBusy
		}
		if info.MaintenanceOperationID != request.OperationID {
			return nil, ErrConflict
		}
		input := verificationInput{account: owner, buildID: request.ExpectedBuildID}
		if err := installer.system.Verify(ctx, input); err != nil {
			return nil, err
		}
		if err := installer.system.Stop(ctx); err != nil {
			return nil, err
		}
		return lockUpgradeRuntime(installer.layout, owner)
	}
	// Only a previously confirmed pause reaches this continuation. A held lock
	// still rejects a live but unresponsive runner; durable pending is checked next.
	if !errors.Is(probeErr, ErrService) || info.Validate() == nil {
		return nil, probeErr
	}
	lock, err := lockUpgradeRuntime(installer.layout, owner)
	if err != nil {
		return nil, ErrBusy
	}
	refuse := func(err error) (*upgradeRuntimeLock, error) {
		_ = lock.Close()
		return nil, err
	}
	_, unitErr := os.Lstat(installer.layout.unit)
	if removing && errors.Is(unitErr, os.ErrNotExist) {
		// The same removing journal may have unlinked the exact owned unit but
		// lost daemon-reload's acknowledgement. Reload before proving not-found;
		// never stop a service now loaded from a different unit or drop-in.
		if err := installer.system.Reload(ctx); err != nil {
			return refuse(err)
		}
		if err := installer.system.VerifyRemoved(ctx); err != nil {
			return refuse(err)
		}
		return lock, nil
	}
	if unitErr != nil {
		return refuse(ErrUnownedPath)
	}
	if err := installer.system.VerifyStopped(ctx); err != nil {
		return refuse(err)
	}
	if err := installer.system.Stop(ctx); err != nil {
		return refuse(err)
	}
	return lock, nil
}

func (installer uninstaller) recover(
	ctx context.Context,
	operation *UninstallOperation,
	config runnerconfig.Config,
	token []byte,
	checkpoint func(string) error,
) error {
	if operation.Stage == "removing" {
		return ErrConflict
	}
	err := installer.layout.verifyUninstallFiles(operation.Manifest, operation.Request.ExpectedBuildID, false)
	if err != nil {
		return err
	}
	if operation.Stage != "aborting" {
		operation.RestoreService = operation.Stage != "checking"
	}
	if err := checkpoint("aborting"); err != nil {
		return err
	}
	if operation.RestoreService {
		if err := installer.system.Start(ctx); err != nil {
			return err
		}
		info, err := installer.probe(ctx, config, token)
		if err != nil {
			return err
		}
		if err := uninstallInfo(info, operation.Request); err != nil {
			return err
		}
		if info.MaintenanceOperationID == operation.Request.OperationID {
			if _, err := installer.maintain(
				ctx,
				config,
				token,
				uninstallCommand(operation.Request, jobcontract.MaintenanceResume),
			); err != nil {
				return err
			}
		} else if info.MaintenanceOperationID != "" {
			return ErrConflict
		}
		info, err = installer.probe(ctx, config, token)
		if err != nil || uninstallInfo(info, operation.Request) != nil || !info.Ready {
			return ErrService
		}
	}
	operation.Result = &UninstallResult{Contract: operation.Request.Contract, OperationID: operation.Request.OperationID,
		Uninstalled: false, DataRetained: true, ObservedAt: time.Now().UTC()}
	return checkpoint("aborted")
}
