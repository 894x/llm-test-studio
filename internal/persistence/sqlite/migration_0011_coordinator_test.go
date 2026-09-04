package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestMigrateCoordinatorStopsAtCatalogExportSchemaByDefault(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "staged.db")
	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "stage-v10"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if got, err := SchemaVersion(context.Background(), path); err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	} else if got != CatalogExportSchemaVersion {
		t.Fatalf("SchemaVersion() = %d, want %d", got, CatalogExportSchemaVersion)
	}
}

func TestMigrateCoordinatorRetirementRequiresDatabaseToStartAtV10(t *testing.T) {
	t.Parallel()

	t.Run("fresh", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fresh.db")
		err := Migrate(context.Background(), path, MigrateOptions{
			AppVersion:            "reject-fresh-retirement",
			RetireAuthoredCatalog: true,
		})
		if err == nil || !strings.Contains(err.Error(), "version 10") {
			t.Fatalf("Migrate() error = %v, want version 10 gate", err)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("fresh rejected retirement created database: stat error = %v", statErr)
		}
	})

	t.Run("version 9", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v9.db")
		seedCoordinatorVersion10(t, path)
		downgradeCoordinatorFixtureToV9(t, path)

		err := Migrate(context.Background(), path, MigrateOptions{
			AppVersion:            "reject-v9-retirement",
			RetireAuthoredCatalog: true,
		})
		if err == nil || !strings.Contains(err.Error(), "version 10") {
			t.Fatalf("Migrate() error = %v, want version 10 gate", err)
		}
		if got := coordinatorQueryInt(t, path, "PRAGMA user_version"); got != 9 {
			t.Fatalf("user_version after rejected retirement = %d, want 9", got)
		}
		if got := coordinatorQueryInt(t, path, "SELECT COUNT(*) FROM schema_migrations WHERE version = 10"); got != 0 {
			t.Fatalf("version 10 records after rejected retirement = %d, want 0", got)
		}
	})
}

func TestMigrateCoordinatorRetiresV10AndTreatsV11AsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "retired.db")
	seedCoordinatorVersion10(t, path)
	retireCoordinatorVersion10(t, path, "retire-v11")
	if got, err := SchemaVersion(context.Background(), path); err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	} else if got != AuthoredCatalogRetirementSchemaVersion {
		t.Fatalf("SchemaVersion() = %d, want %d", got, AuthoredCatalogRetirementSchemaVersion)
	}
	originalAppliedAt := coordinatorQueryString(t, path, "SELECT applied_at FROM schema_migrations WHERE version = 11")

	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "default-on-v11"}); err != nil {
		t.Fatalf("Migrate(default on v11) error = %v", err)
	}
	if got := coordinatorQueryString(t, path, "SELECT applied_at FROM schema_migrations WHERE version = 11"); got != originalAppliedAt {
		t.Fatalf("version 11 applied_at changed from %q to %q", originalAppliedAt, got)
	}
	if err := Migrate(context.Background(), path, MigrateOptions{
		AppVersion:            "retire-on-v11",
		RetireAuthoredCatalog: true,
	}); err != nil {
		t.Fatalf("Migrate(retire on v11) error = %v", err)
	}
	if got := coordinatorQueryString(t, path, "SELECT applied_at FROM schema_migrations WHERE version = 11"); got != originalAppliedAt {
		t.Fatalf("version 11 applied_at after repeated retirement changed from %q to %q", originalAppliedAt, got)
	}
}

func TestMigrateCoordinatorRunsRetirementPreparationAfterEligibilityAndRollsBackOnFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "preparation.db")
	seedCoordinatorVersion10(t, path)
	digest, err := AuthoredCatalogDigest(context.Background(), path)
	if err != nil {
		t.Fatalf("AuthoredCatalogDigest() error = %v", err)
	}
	want := errors.New("keyring cleanup unavailable")
	calls := 0
	err = Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    "retirement-preparation-failure",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
		BeforeAuthoredCatalogRetirement: func(context.Context) error {
			calls++
			return want
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("Migrate() error = %v, want preparation failure", err)
	}
	if calls != 1 {
		t.Fatalf("retirement preparation calls = %d, want 1", calls)
	}
	if got, versionErr := SchemaVersion(context.Background(), path); versionErr != nil || got != CatalogExportSchemaVersion {
		t.Fatalf("SchemaVersion() after failed preparation = %d, %v; want %d", got, versionErr, CatalogExportSchemaVersion)
	}
	if got := coordinatorQueryInt(t, path, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'credential_refs'"); got != 1 {
		t.Fatalf("credential_refs tables after failed preparation = %d, want 1", got)
	}

	if err := Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    "retirement-preparation-retry",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
		BeforeAuthoredCatalogRetirement: func(context.Context) error {
			calls++
			return nil
		},
	}); err != nil {
		t.Fatalf("Migrate() retry error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("retirement preparation calls after retry = %d, want 2", calls)
	}
}

func TestMigrateCoordinatorRequiresKeyringPreparationWhenCredentialRefsExist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credential-preparation.db")
	seedCoordinatorVersion10(t, path)
	repository, err := OpenLegacyCatalogRepository(ctx, path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	id := "75000000-0000-4000-8000-000000000001"
	credential := domain.CredentialRef{
		EntityMeta: domain.EntityMeta{
			ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: at, UpdatedAt: at,
		},
		StoreRef: "llm-test-studio/v1/channel_api_key/" + id, Purpose: domain.CredentialChannelAPIKey,
		MaskedSuffix: "Ab12", Fingerprint: "sha256:" + strings.Repeat("a", 64),
	}
	if err := repository.CreateCredentialRef(ctx, credential); err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	digest, err := AuthoredCatalogDigest(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = Migrate(ctx, path, MigrateOptions{
		AppVersion:                    "missing-keyring-preparation",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
	})
	if err == nil || !strings.Contains(err.Error(), "requires keyring preparation") {
		t.Fatalf("Migrate() error = %v, want keyring preparation gate", err)
	}
	if got, versionErr := SchemaVersion(ctx, path); versionErr != nil || got != CatalogExportSchemaVersion {
		t.Fatalf("SchemaVersion() after missing preparation = %d, %v; want %d", got, versionErr, CatalogExportSchemaVersion)
	}
}

func TestMigrateCoordinatorBacksUpEachActualTarget(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "two-stage.db")
	seedCoordinatorVersion10(t, path)
	downgradeCoordinatorFixtureToV9(t, path)

	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "upgrade-v10"}); err != nil {
		t.Fatalf("Migrate(to v10) error = %v", err)
	}
	v10Backups, err := filepath.Glob(filepath.Join(directory, "backups", "two-stage-before-v10-*.db"))
	if err != nil || len(v10Backups) != 1 {
		t.Fatalf("v10 backups = %v, error = %v, want one", v10Backups, err)
	}
	if got := coordinatorQueryInt(t, v10Backups[0], "PRAGMA user_version"); got != 9 {
		t.Fatalf("v10 backup user_version = %d, want 9", got)
	}

	retireCoordinatorVersion10(t, path, "upgrade-v11")
	v11Backups, err := filepath.Glob(filepath.Join(directory, "backups", "two-stage-before-v11-*.db"))
	if err != nil || len(v11Backups) != 1 {
		t.Fatalf("v11 backups = %v, error = %v, want one", v11Backups, err)
	}
	if got := coordinatorQueryInt(t, v11Backups[0], "PRAGMA user_version"); got != 10 {
		t.Fatalf("v11 backup user_version = %d, want 10", got)
	}
}

func TestRepositoryOpenersEnforceOperationalAndLegacyBoundaries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "openers.db")
	seedCoordinatorVersion10(t, path)
	legacy, err := OpenLegacyCatalogRepository(context.Background(), path, RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenLegacyCatalogRepository(v10) error = %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("Close legacy repository: %v", err)
	}
	if repository, err := OpenRepository(context.Background(), path, RepositoryOptions{}); err == nil {
		repository.Close()
		t.Fatal("OpenRepository(v10) succeeded, want operational schema rejection")
	}

	retireCoordinatorVersion10(t, path, "retire-for-openers")
	operational, err := OpenRepository(context.Background(), path, RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository(v11) error = %v", err)
	}
	if err := operational.Close(); err != nil {
		t.Fatalf("Close operational repository: %v", err)
	}
	if repository, err := OpenLegacyCatalogRepository(context.Background(), path, RepositoryOptions{}); err == nil {
		repository.Close()
		t.Fatal("OpenLegacyCatalogRepository(v11) succeeded, want export schema rejection")
	}
}

func seedCoordinatorVersion10(t *testing.T, path string) {
	t.Helper()
	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "seed-v10"}); err != nil {
		t.Fatalf("seed version 10: %v", err)
	}
}

func retireCoordinatorVersion10(t *testing.T, path, appVersion string) {
	t.Helper()
	digest, err := AuthoredCatalogDigest(context.Background(), path)
	if err != nil {
		t.Fatalf("AuthoredCatalogDigest() error = %v", err)
	}
	if err := Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    appVersion,
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
	}); err != nil {
		t.Fatalf("Migrate(retire) error = %v", err)
	}
}

func downgradeCoordinatorFixtureToV9(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open downgrade fixture: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = 10"); err != nil {
		t.Fatalf("delete version 10 migration record: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 9"); err != nil {
		t.Fatalf("set fixture user_version 9: %v", err)
	}
}

func coordinatorQueryInt(t *testing.T, path, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open query database: %v", err)
	}
	defer db.Close()
	var value int
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

func coordinatorQueryString(t *testing.T, path, query string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open query database: %v", err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}
