package runnerinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
	"golang.org/x/sys/unix"
)

type upgradeFixture struct {
	installer          upgrader
	request            UpgradeRequest
	info               jobcontract.RunnerInfo
	stops              int
	starts             int
	backups            int
	resumes            int
	paused             string
	pending            uint32
	race               bool
	lostResume         bool
	maintenanceResumed bool
	failVerify         bool
	failSchema         bool
}

type upgradeFixtureSystem struct{ fixture *upgradeFixture }

func (system upgradeFixtureSystem) Preflight(context.Context) error { return nil }
func (system upgradeFixtureSystem) Account(context.Context) (account, error) {
	return account{uid: os.Geteuid(), gid: os.Getegid()}, nil
}
func (system upgradeFixtureSystem) Stop(context.Context) error {
	system.fixture.stops++
	return nil
}
func (system upgradeFixtureSystem) Start(context.Context) error {
	system.fixture.starts++
	return nil
}
func (system upgradeFixtureSystem) Verify(_ context.Context, input verificationInput) error {
	selected, err := system.fixture.installer.layout.currentBuild()
	if err != nil || selected != input.buildID {
		return ErrIntegrity
	}
	if system.fixture.failVerify && selected == system.fixture.request.ArtifactSHA256 {
		return ErrService
	}
	return nil
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	t.Helper()
	base := newProvisionFixture(t)
	result, err := base.installer.provision(t.Context(), base.request)
	if err != nil {
		t.Fatal(err)
	}
	program := []byte("explicit new release fixture")
	digest := sha256.Sum256(program)
	fixture := &upgradeFixture{info: result.Runner}
	fixture.request = UpgradeRequest{
		Contract: jobcontract.CurrentContract, OperationID: "157b21b8-3b40-49b2-b03f-240919ca57ab",
		NodeID: result.Runner.NodeID, InstallationID: result.Runner.NodeInstallationID,
		ExpectedBuildID: result.Runner.EngineBuildID, ArtifactSHA256: hex.EncodeToString(digest[:]),
	}
	fixture.installer = upgrader{
		layout: base.installer.layout, source: bytes.NewReader(program), system: upgradeFixtureSystem{fixture: fixture},
	}
	fixture.installer.probe = func(_ context.Context, config runnerconfig.Config, token []byte) (jobcontract.RunnerInfo, error) {
		if !tokenMatches(config, token) {
			return jobcontract.RunnerInfo{}, ErrPairingRequired
		}
		info := fixture.info.Clone()
		info.EngineBuildID, err = fixture.installer.layout.currentBuild()
		if err != nil {
			return info, err
		}
		info.MaintenanceOperationID, info.Pending = fixture.paused, fixture.pending
		info.Ready = fixture.paused == ""
		return info, nil
	}
	fixture.installer.maintain = func(_ context.Context, _ runnerconfig.Config, _ []byte, command jobcontract.MaintenanceCommand) (jobhost.MaintenanceResult, error) {
		if command.Action == jobcontract.MaintenancePause {
			if fixture.maintenanceResumed {
				return jobhost.MaintenanceResult{}, ErrConflict
			}
			fixture.paused = command.OperationID
			if fixture.race {
				fixture.pending = 1
			}
		} else {
			fixture.resumes++
			fixture.maintenanceResumed = true
			fixture.paused = ""
			if fixture.lostResume {
				fixture.lostResume = false
				return jobhost.MaintenanceResult{}, ErrService
			}
		}
		return jobhost.MaintenanceResult{
			Record: jobcontract.MaintenanceRecord{
				Contract: command.Contract, NodeInstallationID: fixture.request.InstallationID,
				OperationID: command.OperationID, Paused: fixture.paused != "", ChangedAt: time.Now().UTC(),
			},
			Status: jobhost.Status{Healthy: true, Ready: fixture.paused == "", Pending: fixture.pending, MaintenanceOperationID: fixture.paused},
		}, nil
	}
	fixture.installer.verifyDB = func(_ context.Context, name string) (sqlite.RunnerBackup, error) {
		if fixture.failSchema {
			return sqlite.RunnerBackup{}, sqlite.ErrSchemaResetRequired
		}
		document, err := os.ReadFile(name)
		if err != nil {
			return sqlite.RunnerBackup{}, err
		}
		digest := sha256.Sum256(document)
		return sqlite.RunnerBackup{
			SchemaVersion: 1, SchemaName: "current", SchemaSHA256: fixture.request.ExpectedBuildID,
			DatabaseSHA256: hex.EncodeToString(digest[:]), BusinessSHA256: hex.EncodeToString(digest[:]), DatabaseBytes: int64(len(document)),
		}, nil
	}
	fixture.installer.backup = func(ctx context.Context, source, destination string) (sqlite.RunnerBackup, error) {
		fixture.backups++
		proof, err := fixture.installer.verifyDB(ctx, source)
		if err != nil {
			return sqlite.RunnerBackup{}, err
		}
		document, err := os.ReadFile(source)
		if err != nil {
			return sqlite.RunnerBackup{}, err
		}
		if err := os.WriteFile(destination, document, 0600); err != nil {
			return sqlite.RunnerBackup{}, err
		}
		return proof, nil
	}
	if err := os.WriteFile(filepath.Join(fixture.installer.layout.state, "runner.db"), []byte("original accepted business data"), 0600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	document, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestUpgradePendingAndAdmissionRaceDoNotStopOrReplaceData(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(map[bool]string{false: "already_pending", true: "admission_race"}[race], func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			fixture.race = race
			if !race {
				fixture.pending = 1
			}
			before := fixtureBytes(t, filepath.Join(fixture.installer.layout.state, "runner.db"))
			_, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
			if !errors.Is(err, ErrBusy) || fixture.stops != 0 || fixture.backups != 0 {
				t.Fatalf("busy changed service or database: %v stops=%d backup=%d", err, fixture.stops, fixture.backups)
			}
			if race && (fixture.resumes != 0 || fixture.paused != fixture.request.OperationID) {
				t.Fatal("race did not retain the retryable same operation gate")
			}
			if !bytes.Equal(before, fixtureBytes(t, filepath.Join(fixture.installer.layout.state, "runner.db"))) {
				t.Fatal("busy changed data")
			}
			selected, _ := fixture.installer.layout.currentBuild()
			if selected != fixture.request.ExpectedBuildID {
				t.Fatal("busy selected another program")
			}
		})
	}
}

func TestUpgradeAdmissionRaceRetainsGateForSameOperationRetryOrRecovery(t *testing.T) {
	for _, mode := range []string{"retry", "recover"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			fixture.race = true
			if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
			fixture.race, fixture.pending = false, 0
			operation, err := fixture.installer.run(t.Context(), fixture.request, mode)
			want := "completed"
			if mode == "recover" {
				want = "rolled_back"
			}
			if err != nil || operation.Stage != want || fixture.resumes != 1 || fixture.paused != "" {
				t.Fatalf("same operation %s: %+v %v", mode, operation, err)
			}
		})
	}
}

func TestUpgradePreservesIdentityAndSameOperationResult(t *testing.T) {
	fixture := newUpgradeFixture(t)
	configName := filepath.Join(fixture.installer.layout.state, "runner.json")
	tokenName := filepath.Join(fixture.installer.layout.install, PairingName)
	beforeConfig, beforeToken := fixtureBytes(t, configName), fixtureBytes(t, tokenName)
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err != nil || operation.Stage != "completed" || !operation.ResumeAttempted || operation.Restorable {
		t.Fatalf("upgrade: %+v %v", operation, err)
	}
	stops, backups := fixture.stops, fixture.backups
	repeated, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err != nil || repeated.Result.ObservedAt != operation.Result.ObservedAt || fixture.stops != stops || fixture.backups != backups {
		t.Fatal("same operation repeated mutation")
	}
	if !bytes.Equal(beforeConfig, fixtureBytes(t, configName)) || !bytes.Equal(beforeToken, fixtureBytes(t, tokenName)) {
		t.Fatal("upgrade replaced identity or credentials")
	}
	document, _ := json.Marshal(operation)
	if bytes.Contains(document, beforeToken) || bytes.Contains(document, []byte("commitment_key")) {
		t.Fatal("journal leaked secrets")
	}
}

func TestUpgradeLostResumeAcknowledgementNeverRestoresDatabase(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.lostResume = true
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err == nil || !operation.ResumeAttempted || operation.Restorable {
		t.Fatalf("resume boundary not durable: %+v %v", operation, err)
	}
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	accepted := []byte("new job accepted after lost resume acknowledgement")
	if err := os.WriteFile(database, accepted, 0600); err != nil {
		t.Fatal(err)
	}
	stops, backups := fixture.stops, fixture.backups
	operation, err = fixture.installer.run(t.Context(), fixture.request, "recover")
	if err != nil || operation.Stage != "completed" || fixture.stops != stops || fixture.backups != backups {
		t.Fatalf("post resume recovery changed old state: %+v %v", operation, err)
	}
	if !bytes.Equal(accepted, fixtureBytes(t, database)) {
		t.Fatal("post resume recovery lost accepted data")
	}
}

func TestUpgradeBeforeResumeCanExplicitlyRestoreVerifiedBackup(t *testing.T) {
	fixture := newUpgradeFixture(t)
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	before := fixtureBytes(t, database)
	fixture.failVerify = true
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err == nil || !operation.Restorable || operation.ResumeAttempted {
		t.Fatalf("pre resume boundary: %+v %v", operation, err)
	}
	if err := os.WriteFile(database, []byte("new unresumed database state"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture.failVerify = false
	operation, err = fixture.installer.run(t.Context(), fixture.request, "recover")
	if err != nil || operation.Stage != "rolled_back" || operation.Restorable {
		t.Fatalf("recover: %+v %v", operation, err)
	}
	if !bytes.Equal(before, fixtureBytes(t, database)) {
		t.Fatal("rollback did not recover original business data")
	}
	selected, _ := fixture.installer.layout.currentBuild()
	if selected != fixture.request.ExpectedBuildID {
		t.Fatal("rollback did not select old program")
	}
}

func TestUpgradeCASActiveOperationAndRuntimeLock(t *testing.T) {
	fixture := newUpgradeFixture(t)
	wrong := fixture.request
	wrong.ExpectedBuildID = wrong.ArtifactSHA256
	wrong.ArtifactSHA256 = fixture.request.ExpectedBuildID
	fixture.installer.source = bytes.NewReader([]byte("immutable binary fixture"))
	if _, err := fixture.installer.run(t.Context(), wrong, "upgrade"); !errors.Is(err, ErrConflict) {
		t.Fatalf("CAS: %v", err)
	}
	fixture = newUpgradeFixture(t)
	fixture.pending = 1
	if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	other := fixture.request
	other.OperationID = "cbced924-7c9d-4077-b912-66c958e808cf"
	if _, err := fixture.installer.run(t.Context(), other, "upgrade"); !errors.Is(err, ErrBusy) {
		t.Fatalf("active operation: %v", err)
	}
	fixture = newUpgradeFixture(t)
	lock, err := runnerstate.Open(fixture.installer.layout.state)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); !errors.Is(err, ErrBusy) {
		t.Fatalf("runtime lock: %v", err)
	}
	if fixture.backups != 0 {
		t.Fatal("backup bypassed runtime lock")
	}
}

func TestUpgradeRejectsSchemaAndRetryContinuesSameOperation(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.failSchema = true
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if !errors.Is(err, ErrUpgradeRequired) || operation.Restorable {
		t.Fatalf("schema: %+v %v", operation, err)
	}
	selected, _ := fixture.installer.layout.currentBuild()
	if selected != fixture.request.ExpectedBuildID {
		t.Fatal("unsupported schema changed program")
	}
	fixture.failSchema = false
	operation, err = fixture.installer.run(t.Context(), fixture.request, "retry")
	if err != nil || operation.Stage != "completed" || operation.Request != fixture.request {
		t.Fatalf("retry: %+v %v", operation, err)
	}
}

func TestUpgradeUsesCurrentSQLiteBackupAndRestoresItsBusinessState(t *testing.T) {
	mask := unix.Umask(0077)
	t.Cleanup(func() { unix.Umask(mask) })
	fixture := newUpgradeFixture(t)
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(t.Context(), database, sqlite.MigrateOptions{AppVersion: "upgrade-test"}); err != nil {
		t.Fatal(err)
	}
	fixture.installer.backup, fixture.installer.verifyDB = sqlite.BackupRunnerDatabase, sqlite.VerifyRunnerDatabase
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO runs(config) VALUES(?)`, "preserve-private-original-data"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := sqlite.VerifyRunnerDatabase(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	fixture.failVerify = true
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err == nil || !operation.Restorable {
		t.Fatalf("current backup: %+v %v", operation, err)
	}
	backupPath := filepath.Join(fixture.installer.layout.install, upgradeDataName(fixture.request.OperationID), "runner.db")
	backup, err := sqlite.VerifyRunnerDatabase(t.Context(), backupPath)
	if err != nil || backup.BusinessSHA256 != before.BusinessSHA256 {
		t.Fatalf("backup lost business state: %v", err)
	}
	db, err = sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO runs(config) VALUES(?)`, "new-unresumed-data"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.failVerify = false
	operation, err = fixture.installer.run(t.Context(), fixture.request, "recover")
	if err != nil || operation.Stage != "rolled_back" {
		t.Fatalf("real SQLite restore: %+v %v", operation, err)
	}
	after, err := sqlite.VerifyRunnerDatabase(t.Context(), database)
	if err != nil || after.BusinessSHA256 != before.BusinessSHA256 {
		t.Fatalf("restored business state differs: %v", err)
	}
}

func TestUpgradeRefusesRealUnsupportedSQLiteWithoutChangingSourceOrProgram(t *testing.T) {
	mask := unix.Umask(0077)
	t.Cleanup(func() { unix.Umask(mask) })
	fixture := newUpgradeFixture(t)
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(t.Context(), database, sqlite.MigrateOptions{AppVersion: "upgrade-test"}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `PRAGMA user_version=999`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := fixtureBytes(t, database)
	fixture.installer.backup, fixture.installer.verifyDB = sqlite.BackupRunnerDatabase, sqlite.VerifyRunnerDatabase
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if !errors.Is(err, ErrUpgradeRequired) || operation.Restorable {
		t.Fatalf("unsupported SQLite: %+v %v", operation, err)
	}
	if !bytes.Equal(before, fixtureBytes(t, database)) {
		t.Fatal("unsupported database mutated")
	}
	selected, _ := fixture.installer.layout.currentBuild()
	if selected != fixture.request.ExpectedBuildID {
		t.Fatal("unsupported database selected new release")
	}
}

func TestUpgradeInstallationLockAndProvisionShareActiveOperationGate(t *testing.T) {
	fixture := newUpgradeFixture(t)
	lock, err := runnerstate.Open(fixture.installer.layout.install)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); !errors.Is(err, ErrBusy) {
		t.Fatalf("installation lock: %v", err)
	}
	if fixture.stops != 0 || fixture.backups != 0 {
		t.Fatal("installation lock did not serialize mutations")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.pending = 1
	if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	system := &fixtureSystem{layout: fixture.installer.layout, buildID: fixture.request.ExpectedBuildID}
	installer := provisioner{layout: fixture.installer.layout, system: system, source: bytes.NewReader([]byte("immutable binary fixture"))}
	request := Request{Contract: jobcontract.CurrentContract, OperationID: "dfc08c64-8508-4451-afbe-02e829b3e344", ArtifactSHA256: fixture.request.ExpectedBuildID, ListenPort: 44917}
	if _, err := installer.provision(t.Context(), request); !errors.Is(err, ErrBusy) {
		t.Fatalf("provision bypassed active upgrade: %v", err)
	}
	if system.accounts.Load() != 0 || system.starts.Load() != 0 {
		t.Fatal("provision changed active upgrade")
	}
}

func TestUpgradeLostRollbackResumeCannotRestoreOrRestartUpgrade(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.failVerify = true
	if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); err == nil {
		t.Fatal("expected pre-resume verification failure")
	}
	fixture.failVerify, fixture.lostResume = false, true
	operation, err := fixture.installer.run(t.Context(), fixture.request, "recover")
	if err == nil || operation.Restorable || operation.Stage != "rolling_back" {
		t.Fatalf("rollback resume boundary: %+v %v", operation, err)
	}
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	accepted := []byte("old runner job accepted after ambiguous rollback resume")
	if err := os.WriteFile(database, accepted, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.installer.run(t.Context(), fixture.request, "retry"); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry reopened superseded backup: %v", err)
	}
	stops := fixture.stops
	operation, err = fixture.installer.run(t.Context(), fixture.request, "recover")
	if err != nil || operation.Stage != "rolled_back" || operation.Result == nil || fixture.stops != stops {
		t.Fatalf("rollback recovery: %+v %v", operation, err)
	}
	if !bytes.Equal(accepted, fixtureBytes(t, database)) {
		t.Fatal("rollback recovery lost newly accepted data")
	}
}

func TestUpgradeRetryAndRecoveryRecheckPendingBeforeStopping(t *testing.T) {
	for _, mode := range []string{"retry", "recover"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			fixture.failVerify = true
			if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); err == nil {
				t.Fatal("expected interrupted new verification")
			}
			fixture.failVerify = false
			fixture.pending = 1
			stops := fixture.stops
			before := fixtureBytes(t, filepath.Join(fixture.installer.layout.state, "runner.db"))
			if _, err := fixture.installer.run(t.Context(), fixture.request, mode); !errors.Is(err, ErrBusy) {
				t.Fatalf("pending %s: %v", mode, err)
			}
			if fixture.stops != stops || !bytes.Equal(before, fixtureBytes(t, filepath.Join(fixture.installer.layout.state, "runner.db"))) {
				t.Fatal("pending continuation stopped or restored business data")
			}
		})
	}
}

func TestUpgradeRecoveryCanStopValidatedUnhealthyIdleRunner(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.failVerify = true
	if _, err := fixture.installer.run(t.Context(), fixture.request, "upgrade"); err == nil {
		t.Fatal("expected interrupted verification")
	}
	fixture.failVerify = false
	probe := fixture.installer.probe
	fixture.installer.probe = func(ctx context.Context, config runnerconfig.Config, token []byte) (jobcontract.RunnerInfo, error) {
		info, err := probe(ctx, config, token)
		if info.EngineBuildID == fixture.request.ArtifactSHA256 {
			info.Healthy, info.Ready = false, false
			return info, ErrService
		}
		return info, err
	}
	operation, err := fixture.installer.run(t.Context(), fixture.request, "recover")
	if err != nil || operation.Stage != "rolled_back" || operation.Result == nil || !operation.Result.Runner.Healthy {
		t.Fatalf("unhealthy recovery: %+v %v", operation, err)
	}
}

func TestUpgradePartialBackupJournalDoesNotBlockExplicitOldRecovery(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.failSchema = true
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err == nil || operation.Restorable {
		t.Fatal("expected incomplete backup")
	}
	sidecar := filepath.Join(fixture.installer.layout.install, upgradeDataName(fixture.request.OperationID), "runner.db-journal")
	sentinel := []byte("interrupted backup evidence")
	if err := os.WriteFile(sidecar, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	fixture.failSchema = false
	operation, err = fixture.installer.run(t.Context(), fixture.request, "recover")
	if err != nil || operation.Stage != "rolled_back" {
		t.Fatalf("incomplete backup recovery: %+v %v", operation, err)
	}
	if !bytes.Equal(sentinel, fixtureBytes(t, sidecar)) {
		t.Fatal("recovery removed incomplete backup evidence")
	}
}
