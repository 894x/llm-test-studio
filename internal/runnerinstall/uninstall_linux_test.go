package runnerinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
	"golang.org/x/sys/unix"
)

type uninstallFixture struct {
	base          *upgradeFixture
	installer     uninstaller
	request       UninstallRequest
	running       bool
	stops         int
	starts        int
	disables      int
	removes       int
	checks        int
	probeError    error
	idleError     error
	uploadBusy    bool
	lostPause     bool
	stopNotLoaded bool
	verifyError   error
}

type uninstallFixtureSystem struct{ fixture *uninstallFixture }

func (system uninstallFixtureSystem) Preflight(context.Context) error { return nil }
func (system uninstallFixtureSystem) Account(context.Context) (account, error) {
	return account{uid: os.Geteuid(), gid: os.Getegid()}, nil
}
func (system uninstallFixtureSystem) Verify(_ context.Context, input verificationInput) error {
	if input.buildID != system.fixture.request.ExpectedBuildID || input.account.uid != os.Geteuid() {
		return ErrIntegrity
	}
	return system.fixture.verifyError
}
func (system uninstallFixtureSystem) VerifyStopped(context.Context) error {
	if system.fixture.running {
		return ErrService
	}
	return system.fixture.verifyError
}
func (system uninstallFixtureSystem) Stop(context.Context) error {
	system.fixture.stops++
	_, err := os.Lstat(system.fixture.installer.layout.unit)
	if errors.Is(err, os.ErrNotExist) && system.fixture.stopNotLoaded {
		return ErrService
	}
	system.fixture.running = false
	return nil
}
func (system uninstallFixtureSystem) Start(context.Context) error {
	system.fixture.starts++
	system.fixture.running = true
	return nil
}
func (system uninstallFixtureSystem) Disable(context.Context) error {
	system.fixture.disables++
	return nil
}
func (uninstallFixtureSystem) Reload(context.Context) error { return nil }
func (system uninstallFixtureSystem) VerifyRemoved(context.Context) error {
	_, err := os.Lstat(system.fixture.installer.layout.unit)
	if system.fixture.running || !errors.Is(err, os.ErrNotExist) {
		return ErrService
	}
	return nil
}

func newUninstallFixture(t *testing.T) *uninstallFixture {
	t.Helper()
	base := newUpgradeFixture(t)
	fixture := &uninstallFixture{base: base, running: true}
	fixture.request = UninstallRequest{Contract: jobcontract.CurrentContract,
		OperationID: "db2810f8-dc34-47ac-a2c0-f7823986dacf", NodeID: base.request.NodeID,
		InstallationID: base.request.InstallationID, ExpectedBuildID: base.request.ExpectedBuildID}
	fixture.installer = uninstaller{layout: base.installer.layout, system: uninstallFixtureSystem{fixture: fixture}}
	fixture.installer.probe = func(
		ctx context.Context,
		config runnerconfig.Config,
		token []byte,
	) (jobcontract.RunnerInfo, error) {
		if fixture.probeError != nil {
			return jobcontract.RunnerInfo{}, fixture.probeError
		}
		if !fixture.running {
			return jobcontract.RunnerInfo{}, ErrService
		}
		return base.installer.probe(ctx, config, token)
	}
	fixture.installer.maintain = func(
		ctx context.Context,
		config runnerconfig.Config,
		token []byte,
		command jobcontract.MaintenanceCommand,
	) (jobhost.MaintenanceResult, error) {
		if fixture.uploadBusy {
			return jobhost.MaintenanceResult{}, ErrBusy
		}
		result, err := base.installer.maintain(
			ctx,
			config,
			token,
			command,
		)
		record := jobcontract.MaintenanceRecord{Contract: command.Contract, NodeInstallationID: config.InstallationID,
			OperationID: command.OperationID, Paused: base.paused != "", ChangedAt: time.Now().UTC()}
		document, _ := json.Marshal(record)
		name := filepath.Join(fixture.installer.layout.state, "maintenance.json")
		if writeErr := os.WriteFile(name, document, 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if fixture.lostPause && command.Action == jobcontract.MaintenancePause {
			fixture.lostPause = false
			return result, ErrService
		}
		return result, err
	}
	fixture.installer.confirmIdle = func(_ context.Context, paths layout, owner account, _ UninstallRequest) error {
		fixture.checks++
		file, err := openOwned(filepath.Join(paths.state, "runner.lock"), owner.uid, true)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
			t.Fatal("idle proof ran without the exclusive original runtime inode")
		}
		return fixture.idleError
	}
	fixture.installer.remove = func(name string) error {
		fixture.removes++
		return os.Remove(name)
	}
	return fixture
}

func TestUninstallRetainsDataPairingAndLockInodes(t *testing.T) {
	fixture := newUninstallFixture(t)
	paths := fixture.installer.layout
	content := filepath.Join(paths.state, "content")
	if err := os.Mkdir(content, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "retain"), []byte("private content"), 0600); err != nil {
		t.Fatal(err)
	}
	retained := []string{
		filepath.Join(paths.state, "runner.db"), filepath.Join(paths.state, "runner.json"),
		filepath.Join(paths.install, "identity.json"), filepath.Join(paths.install, PairingName),
		filepath.Join(content, "retain"),
	}
	before := map[string][]byte{}
	for _, name := range retained {
		before[name] = fixtureBytes(t, name)
	}
	locks := []string{filepath.Join(paths.state, "runner.lock"), filepath.Join(paths.install, "runner.lock")}
	inodes := map[string]uint64{}
	for _, name := range locks {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		inodes[name] = info.Sys().(*syscall.Stat_t).Ino
	}
	operation, err := fixture.installer.run(t.Context(), fixture.request, false)
	if err != nil || operation.Stage != "completed" || operation.Result == nil || !operation.Result.DataRetained {
		t.Fatalf("uninstall = %s, %v", operation.Stage, err)
	}
	if fixture.disables != 1 || fixture.stops != 1 || fixture.base.resumes != 0 || fixture.checks != 1 {
		t.Fatal("uninstall did not preserve the paused stop/lock/idle/delete sequence")
	}
	for name, document := range before {
		if !bytes.Equal(document, fixtureBytes(t, name)) {
			t.Fatalf("retained data changed: %s", name)
		}
	}
	for name, inode := range inodes {
		info, err := os.Stat(name)
		if err != nil || info.Sys().(*syscall.Stat_t).Ino != inode {
			t.Fatal("a lock inode was removed or replaced")
		}
	}
	for _, name := range []string{paths.unit, paths.program} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned program or unit survived: %s", name)
		}
	}
	removes := fixture.removes
	repeated, err := fixture.installer.run(t.Context(), fixture.request, false)
	if err != nil || repeated.Result.ObservedAt != operation.Result.ObservedAt || fixture.removes != removes {
		t.Fatal("lost completed acknowledgement was not idempotent")
	}
	_, err = fixture.base.installer.run(t.Context(), fixture.base.request, "upgrade")
	if !errors.Is(err, ErrUninstalled) {
		t.Fatalf("completed uninstall allowed automatic upgrade: %v", err)
	}
}

func TestUninstallPendingAdmissionRaceAndUploadNeverStopEarly(t *testing.T) {
	for _, scenario := range []string{"pending", "admission_race", "upload"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUninstallFixture(t)
			switch scenario {
			case "pending":
				fixture.base.pending = 1
			case "admission_race":
				fixture.base.race = true
			case "upload":
				fixture.uploadBusy = true
			}
			if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrBusy) {
				t.Fatalf("busy uninstall = %v", err)
			}
			if fixture.stops != 0 || fixture.disables != 0 || fixture.removes != 0 || fixture.base.resumes != 0 {
				t.Fatal("pending, settlement, or upload raced past the safety gate")
			}
			fixture.base.pending, fixture.base.race, fixture.uploadBusy = 0, false, false
			if _, err := fixture.installer.run(t.Context(), fixture.request, false); err != nil {
				t.Fatalf("same-operation retry after drain: %v", err)
			}
		})
	}
}

func TestUninstallInitiallyUnavailableFailsClosedAndCanAbandonUntouchedIntent(t *testing.T) {
	fixture := newUninstallFixture(t)
	fixture.probeError = ErrService
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrService) {
		t.Fatal(err)
	}
	if fixture.stops != 0 || fixture.disables != 0 || fixture.removes != 0 {
		t.Fatal("initial API failure was treated as idle")
	}
	operation, err := fixture.installer.run(t.Context(), fixture.request, true)
	if err != nil || operation.Stage != "aborted" || fixture.starts != 0 || fixture.base.resumes != 0 {
		t.Fatal("abandoning an untouched intent changed service state")
	}
}

func TestUninstallLostPauseAcknowledgementRetainsExactOperation(t *testing.T) {
	fixture := newUninstallFixture(t)
	fixture.lostPause = true
	operation, err := fixture.installer.run(t.Context(), fixture.request, false)
	if !errors.Is(err, ErrService) || operation.Stage != "pausing" || fixture.stops != 0 {
		t.Fatal("ambiguous pause was treated as confirmed idle")
	}
	changed := fixture.request
	changed.ExpectedBuildID = fixture.base.request.ArtifactSHA256
	if _, err := fixture.installer.run(t.Context(), changed, false); !errors.Is(err, ErrConflict) {
		t.Fatal("same operation accepted a changed installation manifest")
	}
	continued, err := fixture.installer.run(t.Context(), fixture.request, false)
	if err != nil || continued.Stage != "completed" || fixture.stops != 1 || fixture.base.resumes != 0 {
		t.Fatalf("same operation could not reconcile the lost pause acknowledgement: %v", err)
	}
	sameManifest := continued.Manifest.OwnerSHA256 == operation.Manifest.OwnerSHA256 &&
		continued.Manifest.UnitSHA256 == operation.Manifest.UnitSHA256 &&
		slices.Equal(continued.Manifest.BuildIDs, operation.Manifest.BuildIDs)
	if continued.Request != operation.Request || !sameManifest {
		t.Fatal("pause recovery invented new operation or ownership facts")
	}
}

func TestUninstallRejectsUnknownContentsAndIdentityBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"root_file", "release_file", "unit", "symlink", "mode", "node", "build"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUninstallFixture(t)
			paths := fixture.installer.layout
			switch scenario {
			case "root_file":
				_ = os.WriteFile(filepath.Join(paths.program, "unknown"), []byte("retain"), 0600)
			case "release_file":
				directory := filepath.Dir(paths.programPath(fixture.request.ExpectedBuildID))
				_ = os.WriteFile(filepath.Join(directory, "unknown"), []byte("retain"), 0600)
			case "unit":
				file, _ := os.OpenFile(paths.unit, os.O_APPEND|os.O_WRONLY, 0)
				_, _ = file.WriteString("\n# unknown administrator content\n")
				_ = file.Close()
			case "symlink":
				_ = os.Remove(filepath.Join(paths.program, "current"))
				_ = os.Symlink(t.TempDir(), filepath.Join(paths.program, "current"))
			case "mode":
				_ = os.Chmod(paths.programPath(fixture.request.ExpectedBuildID), 0777)
			case "node":
				fixture.request.NodeID = "cb723abf-b0ab-415a-85ab-b7f67774db90"
			case "build":
				fixture.request.ExpectedBuildID = fixture.base.request.ArtifactSHA256
			}
			if _, err := fixture.installer.run(t.Context(), fixture.request, false); err == nil {
				t.Fatal("unknown ownership or identity accepted")
			}
			if fixture.stops != 0 || fixture.disables != 0 || fixture.removes != 0 {
				t.Fatal("unknown files caused service mutation or removal")
			}
		})
	}
}

func TestUninstallCheckpointsResumeSameOperation(t *testing.T) {
	for _, stage := range []string{"pausing", "paused", "stopping", "stopped", "disabling", "removing"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newUninstallFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			fixture.installer.progress = func(progress UninstallProgress) {
				if progress.Stage == stage {
					cancel()
				}
			}
			if _, err := fixture.installer.run(ctx, fixture.request, false); !errors.Is(err, context.Canceled) {
				t.Fatalf("interrupted %s: %v", stage, err)
			}
			fixture.installer.progress = nil
			if _, err := fixture.base.installer.run(t.Context(), fixture.base.request, "upgrade"); !errors.Is(err, ErrBusy) {
				t.Fatalf("durable uninstall gate disappeared after interruption: %v", err)
			}
			if _, err := fixture.installer.run(t.Context(), fixture.request, false); err != nil {
				t.Fatalf("same-operation continuation from %s: %v", stage, err)
			}
		})
	}
}

func TestUninstallPartialDeletionLostAcknowledgementIsIdempotent(t *testing.T) {
	for failAt := 1; failAt <= 7; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			fixture := newUninstallFixture(t)
			count := 0
			fixture.installer.remove = func(name string) error {
				count++
				err := os.Remove(name)
				if count == failAt {
					return ErrService // deletion committed, acknowledgement lost
				}
				return err
			}
			if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrService) {
				t.Fatalf("fault did not interrupt partial removal: %v", err)
			}
			if _, err := fixture.installer.run(t.Context(), fixture.request, true); !errors.Is(err, ErrConflict) {
				t.Fatal("partial deletion allowed service recovery")
			}
			fixture.installer.remove = os.Remove
			fixture.stopNotLoaded = true // Reboot/reload forgot the already removed unit.
			operation, err := fixture.installer.run(t.Context(), fixture.request, false)
			if err != nil || operation.Stage != "completed" {
				t.Fatalf("partial deletion retry %d: %v", failAt, err)
			}
		})
	}
}

func TestUninstallStorageFailureAndLostResumeKeepOneWayGate(t *testing.T) {
	fixture := newUninstallFixture(t)
	fixture.idleError = ErrIntegrity
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	if fixture.disables != 0 || fixture.removes != 0 {
		t.Fatal("known storage failure permitted deletion")
	}
	fixture.base.lostResume = true
	if _, err := fixture.installer.run(t.Context(), fixture.request, true); !errors.Is(err, ErrService) {
		t.Fatal("lost resume acknowledgement was not preserved")
	}
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrConflict) {
		t.Fatal("lost resume allowed the same operation to pause again")
	}
	operation, err := fixture.installer.run(t.Context(), fixture.request, true)
	if err != nil || operation.Stage != "aborted" || fixture.base.resumes != 1 {
		t.Fatal("resume acknowledgement recovery repeated a one-way transition")
	}
}

func TestUninstallUnavailableLiveRuntimeAndDurableUnsettledBothRefuseDeletion(t *testing.T) {
	fixture := newUninstallFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	fixture.installer.progress = func(progress UninstallProgress) {
		if progress.Stage == "paused" {
			cancel()
		}
	}
	_, _ = fixture.installer.run(ctx, fixture.request, false)
	fixture.installer.progress, fixture.probeError = nil, ErrService
	lock, err := runnerstate.Open(fixture.installer.layout.state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrBusy) {
		t.Fatal("unavailable live process was treated as stopped")
	}
	_ = lock.Close()
	fixture.running = false
	if fixture.stops != 0 {
		t.Fatal("unavailable live process was stopped without idle evidence")
	}
	fixture.idleError = ErrBusy
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrBusy) {
		t.Fatal("stopped but unsettled storage was treated as idle")
	}
	if fixture.disables != 0 || fixture.removes != 0 {
		t.Fatal("an acquired runtime lock bypassed settled facts")
	}
}

func TestUninstallOrphanCheckingCannotClaimAfterInstallationChanges(t *testing.T) {
	fixture := newUninstallFixture(t)
	paths := fixture.installer.layout
	state, err := runnerstate.Open(paths.install)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := paths.uninstallManifest(state, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	orphan := UninstallOperation{Request: fixture.request, Manifest: manifest,
		Stage: "checking", ChangedAt: time.Now().UTC()}
	if err := writeUninstallOperation(state, orphan); err != nil {
		t.Fatal(err)
	}
	_ = state.Close() // Crash before active-uninstall.json is committed.
	if _, err := fixture.base.installer.run(t.Context(), fixture.base.request, "upgrade"); err != nil {
		t.Fatalf("unclaimed journal blocked legitimate upgrade: %v", err)
	}
	for _, recoverService := range []bool{false, true} {
		if _, err := fixture.installer.run(t.Context(), fixture.request, recoverService); err == nil {
			t.Fatal("stale unclaimed checking journal was accepted")
		}
		if _, err := os.Lstat(filepath.Join(paths.install, activeUninstallName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("stale retry claimed a permanent active gate")
		}
	}
	current := fixture.request
	current.OperationID = "21905cf6-f224-4097-9d98-90026e5c392b"
	current.ExpectedBuildID = fixture.base.request.ArtifactSHA256
	fixture.request = current
	fixture.base.maintenanceResumed = false
	if _, err := fixture.installer.run(t.Context(), current, false); err != nil {
		t.Fatalf("stale orphan prevented current uninstall: %v", err)
	}
}

func TestUninstallInstallationLockSerializesDifferentOperations(t *testing.T) {
	fixture := newUninstallFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	fixture.installer.progress = func(progress UninstallProgress) {
		if progress.Stage == "pausing" {
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := fixture.installer.run(t.Context(), fixture.request, false)
		done <- err
	}()
	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal("uninstall did not reach its durable checkpoint")
	}
	other := fixture.request
	other.OperationID = "21905cf6-f224-4097-9d98-90026e5c392b"
	_, uninstallErr := fixture.installer.run(t.Context(), other, false)
	_, upgradeErr := fixture.base.installer.run(t.Context(), fixture.base.request, "upgrade")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(uninstallErr, ErrBusy) || !errors.Is(upgradeErr, ErrBusy) {
		t.Fatalf("installation lock allowed another operation: %v / %v", uninstallErr, upgradeErr)
	}
	name := filepath.Join(fixture.installer.layout.install, uninstallOperationName(other.OperationID))
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("competing operation created a journal before obtaining the installation lock")
	}
}

func TestUninstallCancellationStopsDeletionAndResumesSameManifest(t *testing.T) {
	fixture := newUninstallFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	deletions := 0
	fixture.installer.remove = func(name string) error {
		deletions++
		err := os.Remove(name)
		cancel()
		return err
	}
	operation, err := fixture.installer.run(ctx, fixture.request, false)
	if !errors.Is(err, context.Canceled) || operation.Stage != "removing" || deletions != 1 {
		t.Fatalf(
			"cancellation continued deleting: %s, %v, %d",
			operation.Stage,
			err,
			deletions,
		)
	}
	fixture.installer.remove, fixture.stopNotLoaded = os.Remove, true
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); err != nil {
		t.Fatalf("cancelled removal could not resume: %v", err)
	}
}

func TestUninstallRejectsChangedLoadedServiceBeforePauseOrStop(t *testing.T) {
	for _, scenario := range []string{"drop_in", "fragment_path", "process_executable"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUninstallFixture(t)
			fixture.verifyError = ErrIntegrity
			if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrIntegrity) {
				t.Fatal("loaded service verification failure was ignored")
			}
			if fixture.stops != 0 || fixture.removes != 0 || fixture.base.paused != "" {
				t.Fatal("an overridden service was paused, stopped or deleted")
			}
		})
	}
}

func TestUninstallStoppedRetryRejectsUnknownLoadedService(t *testing.T) {
	fixture := newUninstallFixture(t)
	fixture.idleError = ErrBusy
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	stops := fixture.stops
	fixture.verifyError = ErrIntegrity
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrIntegrity) {
		t.Fatal("stopped retry accepted an unknown loaded service")
	}
	if fixture.stops != stops || fixture.removes != 0 {
		t.Fatal("stopped retry touched an unknown loaded service")
	}
}

func TestUninstallConfirmedPauseRechecksLoadedServiceBeforeStop(t *testing.T) {
	fixture := newUninstallFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	fixture.installer.progress = func(progress UninstallProgress) {
		if progress.Stage == "paused" {
			cancel()
		}
	}
	_, _ = fixture.installer.run(ctx, fixture.request, false)
	fixture.installer.progress, fixture.verifyError = nil, ErrIntegrity
	if _, err := fixture.installer.run(t.Context(), fixture.request, false); !errors.Is(err, ErrIntegrity) {
		t.Fatal("confirmed pause bypassed loaded service verification")
	}
	if fixture.stops != 0 || fixture.removes != 0 {
		t.Fatal("confirmed pause stopped or deleted an overridden service")
	}
}

func TestUninstallStoppedMetadataRequiresOriginalLoadedUnit(t *testing.T) {
	paths := productionLayout()
	valid := map[string]string{"MainPID": "0", "ActiveState": "inactive", "LoadState": "loaded",
		"FragmentPath": paths.unit, "UnitFileState": "enabled"}
	for field, value := range map[string]string{"MainPID": "42", "ActiveState": "active", "LoadState": "not-found",
		"FragmentPath": "/tmp/unknown.service", "UnitFileState": "masked"} {
		changed := map[string]string{}
		for key, original := range valid {
			changed[key] = original
		}
		changed[field] = value
		if err := verifyStoppedUninstallUnit(changed, paths); err == nil {
			t.Fatalf("stopped metadata accepted changed %s", field)
		}
	}
	if err := verifyStoppedUninstallUnit(valid, paths); err != nil {
		t.Fatal(err)
	}
	valid["UnitFileState"] = "disabled"
	if err := verifyStoppedUninstallUnit(valid, paths); err != nil {
		t.Fatal("lost disable acknowledgement could not continue")
	}
}
