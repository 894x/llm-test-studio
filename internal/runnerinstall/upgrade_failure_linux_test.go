package runnerinstall

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type failingUpgradeSystem struct {
	upgradeFixtureSystem
	stopError, startError error
}

func (system failingUpgradeSystem) Stop(ctx context.Context) error {
	if err := system.upgradeFixtureSystem.Stop(ctx); err != nil {
		return err
	}
	return system.stopError
}

func (system failingUpgradeSystem) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := system.upgradeFixtureSystem.Start(ctx); err != nil {
		return err
	}
	return system.startError
}

func TestUpgradeBackupFailureRestartsOldServiceWithoutReopeningAdmission(t *testing.T) {
	for _, continuation := range []string{"retry", "recover"} {
		t.Run(continuation, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			database := filepath.Join(fixture.installer.layout.state, "runner.db")
			before := fixtureBytes(t, database)
			backup := fixture.installer.backup
			backupFailure := errors.New("simulated full destination disk")
			fixture.installer.backup = func(context.Context, string, string) (sqlite.RunnerBackup, error) {
				return sqlite.RunnerBackup{}, backupFailure
			}
			operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
			if !errors.Is(err, ErrUpgradeRequired) || !errors.Is(err, backupFailure) || operation.Stage != "backing_up" || operation.Restorable ||
				operation.ResumeAttempted || operation.ErrorCode != ErrorCode(ErrUpgradeRequired) {
				t.Fatalf("failure evidence changed: %+v %v", operation, err)
			}
			if fixture.starts != 1 || fixture.stops != 1 || fixture.paused != fixture.request.OperationID || fixture.resumes != 0 {
				t.Fatalf("failed backup did not safely restart the gated old service: starts=%d stops=%d gate=%q resumes=%d",
					fixture.starts, fixture.stops, fixture.paused, fixture.resumes)
			}
			if !bytes.Equal(before, fixtureBytes(t, database)) {
				t.Fatal("failed backup changed the original database")
			}
			fixture.installer.backup = backup
			operation, err = fixture.installer.run(t.Context(), fixture.request, continuation)
			wantStage := map[string]string{"retry": "completed", "recover": "rolled_back"}[continuation]
			if err != nil || operation.Stage != wantStage || operation.Request != fixture.request {
				t.Fatalf("same operation %s: %+v %v", continuation, operation, err)
			}
		})
	}
}

func TestUpgradeRestartFailureRetainsOriginalAndRecoveryCauses(t *testing.T) {
	fixture := newUpgradeFixture(t)
	original := errors.New("original stop failure")
	recovery := errors.New("old service restart failure")
	fixture.installer.system = failingUpgradeSystem{
		upgradeFixtureSystem: upgradeFixtureSystem{fixture: fixture}, stopError: original, startError: recovery,
	}
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if !errors.Is(err, original) || !errors.Is(err, recovery) || operation.Stage != "stopping" || operation.ErrorCode == "" {
		t.Fatalf("restart replaced failure evidence: %+v %v", operation, err)
	}
	if fixture.starts != 1 || fixture.resumes != 0 || fixture.paused != fixture.request.OperationID {
		t.Fatal("restart failure reopened admission or did not attempt the selected old runner")
	}
}

func TestUpgradeCancelledBackupStillRestartsTheUnchangedOldService(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.installer.system = failingUpgradeSystem{upgradeFixtureSystem: upgradeFixtureSystem{fixture: fixture}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.installer.backup = func(context.Context, string, string) (sqlite.RunnerBackup, error) {
		cancel()
		return sqlite.RunnerBackup{}, context.Canceled
	}
	operation, err := fixture.installer.run(ctx, fixture.request, "upgrade")
	if !errors.Is(err, context.Canceled) || operation.Stage != "backing_up" || fixture.starts != 1 || fixture.resumes != 0 {
		t.Fatalf("cancelled caller prevented old service recovery: %+v %v", operation, err)
	}
}

func TestUpgradePreflightCancellationNeverChangesAdmission(t *testing.T) {
	fixture := newUpgradeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.installer.preflightDB = func(context.Context, string) error {
		cancel()
		return context.Canceled
	}
	operation, err := fixture.installer.run(ctx, fixture.request, "upgrade")
	if !errors.Is(err, context.Canceled) || operation.Stage != "checking" || fixture.stops != 0 ||
		fixture.starts != 0 || fixture.paused != "" || fixture.resumes != 0 {
		t.Fatalf("cancelled preflight changed service state: %+v %v", operation, err)
	}
}
