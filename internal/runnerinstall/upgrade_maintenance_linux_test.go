package runnerinstall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

type upgradeMaintenanceStore struct {
	record *jobcontract.MaintenanceRecord
	writes int
}

func (store *upgradeMaintenanceStore) Read(context.Context) (*jobcontract.MaintenanceRecord, error) {
	if store.record == nil {
		return nil, nil
	}
	record := *store.record
	return &record, nil
}

func (store *upgradeMaintenanceStore) Write(_ context.Context, record jobcontract.MaintenanceRecord) error {
	store.record = &record
	store.writes++
	return nil
}

func (*upgradeMaintenanceStore) Sync(context.Context) error { return nil }

type upgradeMaintenanceClock struct{}

func (upgradeMaintenanceClock) Now() time.Time { return time.Now() }

type upgradeMaintenanceGuard struct{}

func (upgradeMaintenanceGuard) Reserve(context.Context, jobcontract.JobSpec) (io.Closer, error) {
	return io.NopCloser(bytes.NewReader([]byte{})), nil
}

type upgradeMaintenanceExecutor struct{}

func (upgradeMaintenanceExecutor) Execute(context.Context, jobhost.Execution) (jobhost.Outcome, error) {
	return jobhost.Outcome{Status: domain.RunCompleted}, nil
}
func (upgradeMaintenanceExecutor) Settle(context.Context, jobcontract.JobSpec, jobhost.Outcome) error {
	return nil
}

func TestUpgradeRaceUsesActualOneWayMaintenanceApply(t *testing.T) {
	for _, mode := range []string{"retry", "recover"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			path := filepath.Join(t.TempDir(), "maintenance-host.db")
			if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "upgrade-maintenance-test"}); err != nil {
				t.Fatal(err)
			}
			repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repository.Close() })
			host, err := jobhost.New(t.Context(), jobhost.Dependencies{
				Repository: repository, Executor: upgradeMaintenanceExecutor{}, Guard: upgradeMaintenanceGuard{},
				Clock: upgradeMaintenanceClock{}, Policy: jobhost.RemotePolicy(),
				Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: fixture.request.NodeID},
				NodeID: fixture.request.NodeID, InstallationID: fixture.request.InstallationID,
				EngineBuildID: fixture.request.ExpectedBuildID, RunnerVersion: "upgrade-maintenance-test",
				CommitmentKey: bytes.Repeat([]byte{7}, 32),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close() })
			store := &upgradeMaintenanceStore{}
			maintenance, err := jobhost.NewMaintenance(t.Context(), host, store)
			if err != nil {
				t.Fatal(err)
			}
			injectRace := true
			fixture.installer.maintain = func(ctx context.Context, _ runnerconfig.Config, _ []byte, command jobcontract.MaintenanceCommand) (jobhost.MaintenanceResult, error) {
				result, err := maintenance.Apply(ctx, command)
				if err != nil {
					return result, err
				}
				fixture.paused = result.Status.MaintenanceOperationID
				if command.Action == jobcontract.MaintenancePause && injectRace {
					// Model a job admitted between the preflight GET and the
					// atomic maintenance gate. Actual Apply owns the gate and
					// its single pause/resume cycle throughout this test.
					fixture.pending, result.Status.Pending = 1, 1
					injectRace = false
				}
				return result, nil
			}
			fixture.pending = 1
			before := fixtureBytes(t, filepath.Join(fixture.installer.layout.state, "runner.db"))
			if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); !errors.Is(err, ErrBusy) {
				t.Fatalf("already pending: %v", err)
			}
			if store.writes != 0 || fixture.stops != 0 || fixture.backups != 0 {
				t.Fatal("initial pending work changed maintenance, program, or database")
			}
			fixture.pending = 0
			if _, err := fixture.installer.run(t.Context(), fixture.request, "retry"); !errors.Is(err, ErrBusy) {
				t.Fatalf("race: %v", err)
			}
			if store.writes != 1 || store.record == nil || !store.record.Paused || fixture.stops != 0 || fixture.backups != 0 {
				t.Fatal("race stopped, backed up, or resumed the current runner")
			}
			if !bytes.Equal(before, fixtureBytes(t, filepath.Join(fixture.installer.layout.state, "runner.db"))) {
				t.Fatal("pending race changed business data")
			}
			fixture.pending = 0
			operation, err := fixture.installer.run(t.Context(), fixture.request, mode)
			want := "completed"
			if mode == "recover" {
				want = "rolled_back"
			}
			if err != nil || operation.Stage != want || store.writes != 2 || store.record.Paused {
				t.Fatalf("same operation %s: %+v %v writes=%d", mode, operation, err, store.writes)
			}
			if _, err := maintenance.Apply(t.Context(), upgradeCommand(fixture.request, jobcontract.MaintenancePause)); !errors.Is(err, jobhost.ErrConflict) {
				t.Fatal("fixture did not enforce actual one-way maintenance semantics")
			}
		})
	}
}
