package runnerinstall

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

type historicalUpgradeSystem struct{ upgradeFixtureSystem }

func (system historicalUpgradeSystem) Start(ctx context.Context) error {
	if err := system.upgradeFixtureSystem.Start(ctx); err != nil {
		return err
	}
	fixture := system.fixture
	selected, err := fixture.installer.layout.currentBuild()
	if err != nil || selected != fixture.request.ArtifactSHA256 {
		return err
	}
	return sqlite.Migrate(ctx, filepath.Join(fixture.installer.layout.state, "runner.db"), sqlite.MigrateOptions{
		AppVersion: "upgrade-integration-new-runner",
	})
}

func TestUpgradeHistoricalSQLiteCompletesAndRestoresItsOriginalVersion(t *testing.T) {
	for _, source := range []string{"00b18026", "e72dc1fc", "82e10184"} {
		t.Run(source, func(t *testing.T) {
			fixture := newHistoricalUpgradeFixture(t, source)
			database := filepath.Join(fixture.installer.layout.state, "runner.db")
			before, err := sqlite.VerifyRunnerUpgradeDatabase(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			fixture.failVerify = true
			operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
			if !errors.Is(err, ErrService) || !operation.Restorable || operation.Stage != "verifying" {
				t.Fatalf("upgrade did not preserve a restorable old backup: %+v %v", operation, err)
			}
			if got, err := sqlite.SchemaVersion(t.Context(), database); err != nil || got != sqlite.CurrentSchemaVersion {
				t.Fatalf("new runner did not use the shipped migration chain: %d %v", got, err)
			}
			backupPath := filepath.Join(fixture.installer.layout.install, upgradeDataName(fixture.request.OperationID), "runner.db")
			backup, err := sqlite.VerifyRunnerUpgradeDatabase(t.Context(), backupPath)
			if err != nil || backup.SchemaVersion != before.SchemaVersion || backup.BusinessSHA256 != before.BusinessSHA256 {
				t.Fatalf("original-version backup differs: %+v %v", backup, err)
			}
			fixture.failVerify = false
			operation, err = fixture.installer.run(t.Context(), fixture.request, "recover")
			if err != nil || operation.Stage != "rolled_back" || operation.Restorable {
				t.Fatalf("old-version recovery: %+v %v", operation, err)
			}
			restored, err := sqlite.VerifyRunnerUpgradeDatabase(t.Context(), database)
			if err != nil || restored.SchemaVersion != before.SchemaVersion || restored.BusinessSHA256 != before.BusinessSHA256 {
				t.Fatalf("recovery did not preserve the original version and business values: %+v %v", restored, err)
			}
			stops, starts := fixture.stops, fixture.starts
			if _, err := fixture.installer.run(t.Context(), fixture.request, "recover"); err != nil ||
				fixture.stops != stops || fixture.starts != starts {
				t.Fatalf("repeat recovery changed the restored service: %v", err)
			}
		})
	}
	t.Run("successful-v1-upgrade", func(t *testing.T) {
		fixture := newHistoricalUpgradeFixture(t, "00b18026")
		operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
		if err != nil || operation.Stage != "completed" || !operation.ResumeAttempted || operation.Restorable {
			t.Fatalf("historical upgrade: %+v %v", operation, err)
		}
		stops, starts := fixture.stops, fixture.starts
		repeated, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
		if err != nil || repeated.Result == nil || fixture.stops != stops || fixture.starts != starts {
			t.Fatalf("repeat upgrade executed again: %+v %v", repeated, err)
		}
	})
}

func TestUpgradePreflightRejectsOldSQLiteBeforeAdmissionAndStop(t *testing.T) {
	for _, statement := range []string{
		"PRAGMA user_version = 999",
		"UPDATE schema_migrations SET checksum = 'unknown'",
		"UPDATE reports SET document_json = '{}'",
	} {
		t.Run(statement, func(t *testing.T) {
			fixture := newHistoricalUpgradeFixture(t, "00b18026")
			database := filepath.Join(fixture.installer.layout.state, "runner.db")
			db, err := sql.Open("sqlite", database)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := fixtureBytes(t, database)
			maintenanceCalls := 0
			maintain := fixture.installer.maintain
			fixture.installer.maintain = func(
				ctx context.Context,
				config runnerconfig.Config,
				token []byte,
				command jobcontract.MaintenanceCommand,
			) (jobhost.MaintenanceResult, error) {
				maintenanceCalls++
				return maintain(ctx, config, token, command)
			}
			operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
			if !errors.Is(err, ErrUpgradeRequired) || operation.Stage != "checking" || operation.ErrorCode == "" {
				t.Fatalf("failed preflight was not durably reported: %+v %v", operation, err)
			}
			if maintenanceCalls != 0 || fixture.stops != 0 || fixture.starts != 0 || fixture.paused != "" {
				t.Fatalf("failed preflight touched the running service: maintain=%d stop=%d start=%d",
					maintenanceCalls, fixture.stops, fixture.starts)
			}
			if !bytes.Equal(before, fixtureBytes(t, database)) {
				t.Fatal("rejected source was modified")
			}
			selected, err := fixture.installer.layout.currentBuild()
			if err != nil || selected != fixture.request.ExpectedBuildID {
				t.Fatal("preflight failure changed the selected old runner")
			}
		})
	}
}

func TestUpgradeInterruptedHistoricalBackupIsRehearsedBeforeRetry(t *testing.T) {
	fixture := newHistoricalUpgradeFixture(t, "00b18026")
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	before := fixtureBytes(t, database)
	fixture.installer.backup = func(ctx context.Context, source, destination string) (sqlite.RunnerBackup, error) {
		if _, err := sqlite.BackupIncompatibleDatabase(ctx, source, destination); err != nil {
			return sqlite.RunnerBackup{}, err
		}
		db, err := sql.Open("sqlite", destination)
		if err != nil {
			return sqlite.RunnerBackup{}, err
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx, "UPDATE reports SET document_json = '{}'"); err != nil {
			return sqlite.RunnerBackup{}, err
		}
		return sqlite.RunnerBackup{}, errors.New("interrupted before the old snapshot was rehearsed")
	}
	operation, err := fixture.installer.run(t.Context(), fixture.request, "upgrade")
	if err == nil || operation.Restorable || operation.Stage != "backing_up" {
		t.Fatalf("expected an incomplete historical backup: %+v %v", operation, err)
	}
	fixture.installer.backup = sqlite.BackupRunnerUpgradeDatabase
	operation, err = fixture.installer.run(t.Context(), fixture.request, "retry")
	if !errors.Is(err, ErrUpgradeRequired) || operation.Restorable || operation.Stage != "backing_up" {
		t.Fatalf("retry accepted an unrehearsed incompatible backup: %+v %v", operation, err)
	}
	if !bytes.Equal(before, fixtureBytes(t, database)) {
		t.Fatal("retry modified the original historical source")
	}
	selected, err := fixture.installer.layout.currentBuild()
	if err != nil || selected != fixture.request.ExpectedBuildID || fixture.paused != fixture.request.OperationID {
		t.Fatal("retry cut over or reopened admission with an unverified backup")
	}
}

func newHistoricalUpgradeFixture(t *testing.T, source string) *upgradeFixture {
	t.Helper()
	fixture := newUpgradeFixture(t)
	database := filepath.Join(fixture.installer.layout.state, "runner.db")
	loadHistoricalUpgradeDatabase(t, database, source)
	fixture.installer.system = historicalUpgradeSystem{upgradeFixtureSystem: upgradeFixtureSystem{fixture: fixture}}
	fixture.installer.preflightDB = sqlite.PreflightRunnerUpgradeDatabase
	fixture.installer.backup = sqlite.BackupRunnerUpgradeDatabase
	fixture.installer.verifyBackupDB = sqlite.VerifyRunnerUpgradeDatabase
	fixture.installer.verifyDB = sqlite.VerifyRunnerDatabase
	return fixture
}

func loadHistoricalUpgradeDatabase(t *testing.T, path, source string) {
	t.Helper()
	root := filepath.Join("..", "persistence", "sqlite")
	document := fixtureBytes(t, filepath.Join(root, "testdata", "migrations", source+".json"))
	var fixture struct {
		SourceCommit string                 `json:"source_commit"`
		Schema       []struct{ DDL string } `json:"schema"`
		Tables       []struct {
			Table   string   `json:"table"`
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		} `json:"tables"`
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil || !strings.HasPrefix(fixture.SourceCommit, source) {
		t.Fatalf("historical writer fixture identity: %v", err)
	}
	statements := []string{}
	version := 1
	if len(fixture.Schema) != 0 {
		for _, object := range fixture.Schema {
			statements = append(statements, object.DDL)
		}
		version = map[string]int{"e72dc1fc": 2, "82e10184": 3}[source]
	} else {
		var catalog struct {
			Objects   map[string]struct{ DDL string } `json:"objects"`
			Baselines []struct {
				Commit  string   `json:"commit"`
				Objects []string `json:"objects"`
			} `json:"baselines"`
		}
		if err := json.Unmarshal(fixtureBytes(t, filepath.Join(root, "migrations", "operational_baselines.json")), &catalog); err != nil {
			t.Fatal(err)
		}
		for _, baseline := range catalog.Baselines {
			if baseline.Commit == fixture.SourceCommit {
				for _, key := range baseline.Objects {
					statements = append(statements, catalog.Objects[key].DDL)
				}
			}
		}
	}
	if len(statements) == 0 || version == 0 {
		t.Fatal("historical fixture has no recognized schema")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range fixture.Tables {
		columns := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			columns = append(columns, `"`+strings.ReplaceAll(column, `"`, `""`)+`"`)
		}
		query := `INSERT INTO "` + table.Table + `" (` + strings.Join(columns, ",") + `) VALUES (` +
			strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + `)`
		for _, row := range table.Rows {
			for index, value := range row {
				switch typed := value.(type) {
				case json.Number:
					row[index], err = typed.Int64()
					if err != nil {
						row[index], err = typed.Float64()
					}
				case map[string]any:
					row[index], err = base64.StdEncoding.DecodeString(typed["blob"].(string))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(t.Context(), query, row...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version = "+map[int]string{1: "1", 2: "2", 3: "3"}[version]); err != nil {
		t.Fatal(err)
	}
}
