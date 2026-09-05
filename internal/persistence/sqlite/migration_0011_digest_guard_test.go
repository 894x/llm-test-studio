package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthoredCatalogDigestIsIndependentOfRowAndOperationalWriteOrder(t *testing.T) {
	t.Parallel()

	firstPath := digestGuardVersion10Path(t, "first.db")
	secondPath := digestGuardVersion10Path(t, "second.db")
	digestGuardExec(t, firstPath,
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?), (?, ?)`,
		"alpha", "2026-09-05T00:00:00Z", "beta", "2026-09-05T00:00:01Z",
	)
	digestGuardExec(t, secondPath,
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?), (?, ?)`,
		"beta", "2026-09-05T00:00:01Z", "alpha", "2026-09-05T00:00:00Z",
	)

	first := digestGuardDigest(t, firstPath)
	second := digestGuardDigest(t, secondPath)
	if first != second {
		t.Fatalf("digests for identical authored rows differ by insertion order: %q != %q", first, second)
	}

	digestGuardExec(t, firstPath, `INSERT INTO runs(ts, model) VALUES(?, ?)`, "2026-09-05T00:00:02Z", "operational-only")
	if got := digestGuardDigest(t, firstPath); got != first {
		t.Fatalf("digest changed after operational row insert: got %q, want %q", got, first)
	}
}

func TestAuthoredCatalogDigestChangesForAuthoredInsertUpdateAndDelete(t *testing.T) {
	t.Parallel()

	path := digestGuardVersion10Path(t, "authored-mutations.db")
	empty := digestGuardDigest(t, path)

	digestGuardExec(t, path,
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?)`,
		"digest-fixture", "2026-09-05T00:00:00Z",
	)
	afterInsert := digestGuardDigest(t, path)
	if afterInsert == empty {
		t.Fatal("digest did not change after authored row insert")
	}

	digestGuardExec(t, path,
		`UPDATE builtin_catalog_seeds SET completed_at = ? WHERE seed_key = ?`,
		"2026-09-05T00:00:01Z", "digest-fixture",
	)
	afterUpdate := digestGuardDigest(t, path)
	if afterUpdate == afterInsert {
		t.Fatal("digest did not change after authored row update")
	}

	digestGuardExec(t, path, `DELETE FROM builtin_catalog_seeds WHERE seed_key = ?`, "digest-fixture")
	afterDelete := digestGuardDigest(t, path)
	if afterDelete == afterUpdate {
		t.Fatal("digest did not change after authored row delete")
	}
	if afterDelete != empty {
		t.Fatalf("digest after restoring empty authored state = %q, want %q", afterDelete, empty)
	}
}

func TestBeginAuthoredCatalogExportLocksTheExactDigestUsedForRetirement(t *testing.T) {
	path := digestGuardVersion10Path(t, "locked-export.db")
	wantDigest := digestGuardDigest(t, path)
	repository, err := OpenLegacyCatalogRepository(context.Background(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := repository.BeginAuthoredCatalogExport(context.Background())
	if err != nil {
		_ = repository.Close()
		t.Fatalf("BeginAuthoredCatalogExport() error = %v", err)
	}
	if digest != wantDigest {
		_ = repository.Close()
		t.Fatalf("locked export digest = %q, want %q", digest, wantDigest)
	}

	writer, err := sql.Open("sqlite", path)
	if err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Exec(`PRAGMA busy_timeout = 1`); err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}
	if _, err := writer.Exec(
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?)`,
		"blocked-during-export", "2026-09-05T00:00:00Z",
	); err == nil {
		_ = repository.Close()
		t.Fatal("concurrent authored write succeeded while export snapshot was active")
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("close export snapshot: %v", err)
	}

	if _, err := writer.Exec(
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?)`,
		"late-after-export", "2026-09-05T00:00:01Z",
	); err != nil {
		t.Fatalf("authored write after export snapshot closed: %v", err)
	}
	err = Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    "locked-export",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
	})
	digestGuardAssertRetirementRejected(t, path, err, "changed")
}

func TestMigrateRetirementRequiresMatchingAuthoredCatalogDigest(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		path := digestGuardVersion10Path(t, "missing.db")
		err := Migrate(context.Background(), path, MigrateOptions{
			AppVersion:            "missing-digest",
			RetireAuthoredCatalog: true,
		})
		digestGuardAssertRetirementRejected(t, path, err, "digest")
	})

	t.Run("malformed", func(t *testing.T) {
		path := digestGuardVersion10Path(t, "malformed.db")
		err := Migrate(context.Background(), path, MigrateOptions{
			AppVersion:                    "malformed-digest",
			RetireAuthoredCatalog:         true,
			ExpectedAuthoredCatalogDigest: "not-a-sha-256-digest",
		})
		digestGuardAssertRetirementRejected(t, path, err, "digest")
	})

	t.Run("wrong", func(t *testing.T) {
		path := digestGuardVersion10Path(t, "wrong.db")
		digest := digestGuardDigest(t, path)
		wrong := "0" + digest[1:]
		if wrong == digest {
			wrong = "1" + digest[1:]
		}
		err := Migrate(context.Background(), path, MigrateOptions{
			AppVersion:                    "wrong-digest",
			RetireAuthoredCatalog:         true,
			ExpectedAuthoredCatalogDigest: wrong,
		})
		digestGuardAssertRetirementRejected(t, path, err, "changed")
	})
}

func TestMigrateRetirementRejectsAuthoredWriteAfterDigest(t *testing.T) {
	t.Parallel()

	path := digestGuardVersion10Path(t, "stale-digest.db")
	digest := digestGuardDigest(t, path)
	digestGuardExec(t, path,
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?)`,
		"late-write", "2026-09-05T00:00:00Z",
	)

	err := Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    "stale-digest",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
	})
	digestGuardAssertRetirementRejected(t, path, err, "changed")
}

func TestMigration0011TransactionRechecksDigestAfterLateAuthoredWrite(t *testing.T) {
	t.Parallel()

	path := digestGuardVersion10Path(t, "transaction-recheck.db")
	digestText := digestGuardDigest(t, path)
	expectedDigest, err := decodeAuthoredCatalogDigest(digestText)
	if err != nil {
		t.Fatal(err)
	}
	digestGuardExec(t, path,
		`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?)`,
		"late-transaction-write", "2026-09-05T00:00:00Z",
	)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	err = applyMigration0011WithExpectedDigest(context.Background(), conn, "transaction-recheck", expectedDigest)
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("applyMigration0011WithExpectedDigest() error = %v, want transaction digest mismatch", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close transaction connection: %v", err)
	}
	if got := migration0011QueryInt(t, db, "PRAGMA user_version"); got != CatalogExportSchemaVersion {
		t.Fatalf("user_version after rejected transaction = %d, want %d", got, CatalogExportSchemaVersion)
	}
	if !migration0011ObjectExists(t, db, "table", "models") {
		t.Fatal("models table was dropped despite transaction digest mismatch")
	}
}

func TestMigrateRetirementBlocksIntegrationsBeforeBackup(t *testing.T) {
	t.Parallel()

	path := digestGuardVersion10Path(t, "blocked-integration.db")
	digest := digestGuardDigest(t, path)
	digestGuardExec(t, path, `
		INSERT INTO integrations(
			id, schema_version, revision, created_at, updated_at,
			credential_id, credential_revision, document_json
		) VALUES(
			'blocked-integration', 1, 1,
			'2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z',
			NULL, NULL, '{}'
		)
	`)

	err := Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    "blocked-integration",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
	})
	digestGuardAssertRetirementRejected(t, path, err, "integrations")
}

func TestMigrateRetirementAcceptsUnchangedAuthoredCatalogDigest(t *testing.T) {
	t.Parallel()

	path := digestGuardVersion10Path(t, "matching-digest.db")
	digest := digestGuardDigest(t, path)
	if err := Migrate(context.Background(), path, MigrateOptions{
		AppVersion:                    "matching-digest",
		RetireAuthoredCatalog:         true,
		ExpectedAuthoredCatalogDigest: digest,
	}); err != nil {
		t.Fatalf("Migrate(retire) error = %v", err)
	}
	if got, err := SchemaVersion(context.Background(), path); err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	} else if got != AuthoredCatalogRetirementSchemaVersion {
		t.Fatalf("SchemaVersion() = %d, want %d", got, AuthoredCatalogRetirementSchemaVersion)
	}
}

func TestRepositoryOpenersRejectForeignKeyOrphansAfterSchemaValidation(t *testing.T) {
	t.Parallel()

	t.Run("legacy v10", func(t *testing.T) {
		path := digestGuardVersion10Path(t, "legacy-orphan.db")
		digestGuardInsertOrphanEvidence(t, path)
		repository, err := OpenLegacyCatalogRepository(context.Background(), path, RepositoryOptions{})
		if repository != nil {
			_ = repository.Close()
		}
		if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "foreign key") {
			t.Fatalf("OpenLegacyCatalogRepository() error = %v, want foreign-key ErrCorrupt", err)
		}
	})

	t.Run("operational v11", func(t *testing.T) {
		path := digestGuardVersion10Path(t, "operational-orphan.db")
		digest := digestGuardDigest(t, path)
		if err := Migrate(context.Background(), path, MigrateOptions{
			AppVersion:                    "retire-for-orphan-test",
			RetireAuthoredCatalog:         true,
			ExpectedAuthoredCatalogDigest: digest,
		}); err != nil {
			t.Fatalf("Migrate(retire) error = %v", err)
		}
		digestGuardInsertOrphanEvidence(t, path)
		repository, err := OpenRepository(context.Background(), path, RepositoryOptions{})
		if repository != nil {
			_ = repository.Close()
		}
		if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "foreign key") {
			t.Fatalf("OpenRepository() error = %v, want foreign-key ErrCorrupt", err)
		}
	})
}

func digestGuardVersion10Path(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "digest-guard-fixture"}); err != nil {
		t.Fatalf("Migrate(to v10) error = %v", err)
	}
	return path
}

func digestGuardDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := AuthoredCatalogDigest(context.Background(), path)
	if err != nil {
		t.Fatalf("AuthoredCatalogDigest() error = %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("AuthoredCatalogDigest() length = %d, want 64", len(digest))
	}
	return digest
}

func digestGuardExec(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execute digest guard fixture query: %v", err)
	}
}

func digestGuardAssertRetirementRejected(t *testing.T, path string, err error, reason string) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(reason)) {
		t.Fatalf("Migrate(retire) error = %v, want %q rejection", err, reason)
	}
	if got, versionErr := SchemaVersion(context.Background(), path); versionErr != nil {
		t.Fatalf("SchemaVersion() after rejected retirement error = %v", versionErr)
	} else if got != CatalogExportSchemaVersion {
		t.Fatalf("SchemaVersion() after rejected retirement = %d, want %d", got, CatalogExportSchemaVersion)
	}
	db, openErr := sql.Open("sqlite", path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	if !migration0011ObjectExists(t, db, "table", "models") {
		t.Fatal("models table was dropped despite rejected retirement")
	}
	backups, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "*-before-v11-*.db"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(backups) != 0 {
		t.Fatalf("rejected preflight created %d v11 backup(s), want none", len(backups))
	}
}

func digestGuardInsertOrphanEvidence(t *testing.T, path string) {
	t.Helper()
	digestGuardExec(t, path, `
		INSERT INTO evidence(
			id, schema_version, revision, created_at, updated_at, run_id, document_json
		) VALUES(
			'orphan-evidence', 1, 1,
			'2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z',
			'missing-run', '{}'
		)
	`)
}
