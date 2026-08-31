package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const migration0005Name = "0005_channel_comparisons"

var migration0005Statements = []string{
	`CREATE TABLE case_catalog_cutover (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		completed_at TEXT NOT NULL
	)`,
	`CREATE TABLE pending_test_case_snapshots (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision)
	)`,
	`CREATE TABLE comparisons (
		id TEXT PRIMARY KEY,
		current_revision INTEGER NOT NULL CHECK(current_revision > 0),
		created_at TEXT NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1)),
		FOREIGN KEY(id, current_revision) REFERENCES comparison_revisions(comparison_id, revision)
			DEFERRABLE INITIALLY DEFERRED
	)`,
	`CREATE TABLE comparison_revisions (
		comparison_id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		model_id TEXT NOT NULL,
		model_revision INTEGER NOT NULL,
		status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'cancelled')),
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(comparison_id, revision),
		FOREIGN KEY(comparison_id) REFERENCES comparisons(id) ON DELETE CASCADE,
		FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision),
		FOREIGN KEY(model_id, model_revision) REFERENCES models(id, revision)
	)`,
	`CREATE TABLE comparison_runs (
		comparison_id TEXT NOT NULL,
		comparison_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		channel_id TEXT NOT NULL,
		channel_revision INTEGER NOT NULL,
		run_id TEXT NOT NULL,
		PRIMARY KEY(comparison_id, comparison_revision, position),
		UNIQUE(comparison_id, comparison_revision, channel_id),
		UNIQUE(comparison_id, comparison_revision, run_id),
		FOREIGN KEY(comparison_id, comparison_revision) REFERENCES comparison_revisions(comparison_id, revision) ON DELETE CASCADE,
		FOREIGN KEY(channel_id, channel_revision) REFERENCES channels(id, revision),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id)
	)`,
	`CREATE INDEX idx_comparison_revisions_updated ON comparison_revisions(updated_at DESC, comparison_id)`,
	`CREATE TRIGGER comparison_revisions_no_update
	BEFORE UPDATE ON comparison_revisions
	BEGIN
		SELECT RAISE(ABORT, 'comparison revisions are immutable');
	END`,
	`CREATE TRIGGER comparison_revisions_no_delete
	BEFORE DELETE ON comparison_revisions
	BEGIN
		SELECT RAISE(ABORT, 'comparison revisions are immutable');
	END`,
}

func migration0005Checksum() string {
	definition := migration0005Name + "\n" + strings.Join(migration0005Statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0005(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0005Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0005Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(5, ?, ?, ?, ?)
	`, migration0005Name, migration0005Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0005Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0005(ctx context.Context, conn *sql.Conn) error {
	additional := append(append(append([]string(nil), migration0003Statements...), migration0004Statements...), migration0005Statements...)
	return validateAppliedDomainSchema(ctx, conn, additional)
}
