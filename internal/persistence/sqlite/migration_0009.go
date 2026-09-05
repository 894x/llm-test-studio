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

const migration0009Name = "0009_comparison_snapshot_catalog_decoupling"

// migration0009Statements is the canonical v9 definition for the comparison
// history tables. Plan, model, and channel revisions are pinned inside the
// immutable comparison/run documents, so only operational records remain
// relational foreign-key targets.
var migration0009Statements = []string{
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
		FOREIGN KEY(comparison_id) REFERENCES comparisons(id) ON DELETE CASCADE
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

var migration0009BeforeCreateStatements = []string{
	`PRAGMA defer_foreign_keys = ON`,
	`DROP TRIGGER comparison_revisions_no_update`,
	`DROP TRIGGER comparison_revisions_no_delete`,
	`DROP INDEX idx_comparison_revisions_updated`,
	`CREATE TEMP TABLE comparison_revisions_v9_backup AS
		SELECT comparison_id, schema_version, revision, created_at, updated_at,
		       plan_id, plan_revision, model_id, model_revision, status, document_json
		FROM comparison_revisions`,
	`CREATE TEMP TABLE comparison_runs_v9_backup AS
		SELECT comparison_id, comparison_revision, position,
		       channel_id, channel_revision, run_id
		FROM comparison_runs`,
	`DROP TABLE comparison_runs`,
	`DROP TABLE comparison_revisions`,
}

var migration0009AfterCreateStatements = []string{
	`INSERT INTO comparison_revisions(
		comparison_id, schema_version, revision, created_at, updated_at,
		plan_id, plan_revision, model_id, model_revision, status, document_json
	)
	SELECT comparison_id, schema_version, revision, created_at, updated_at,
	       plan_id, plan_revision, model_id, model_revision, status, document_json
	FROM comparison_revisions_v9_backup`,
	`INSERT INTO comparison_runs(
		comparison_id, comparison_revision, position,
		channel_id, channel_revision, run_id
	)
	SELECT comparison_id, comparison_revision, position,
	       channel_id, channel_revision, run_id
	FROM comparison_runs_v9_backup`,
	`DROP TABLE comparison_runs_v9_backup`,
	`DROP TABLE comparison_revisions_v9_backup`,
}

func migration0009Checksum() string {
	statements := make([]string, 0, len(migration0009BeforeCreateStatements)+len(migration0009AfterCreateStatements)+len(migration0009Statements))
	statements = append(statements, migration0009BeforeCreateStatements...)
	statements = append(statements, migration0009Statements[:2]...)
	statements = append(statements, migration0009AfterCreateStatements...)
	statements = append(statements, migration0009Statements[2:]...)
	definition := migration0009Name + "\n" + strings.Join(statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0009(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0009BeforeCreateStatements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0009Name, err)
		}
	}
	for _, statement := range migration0009Statements[:2] {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0009Name, err)
		}
	}
	for _, statement := range migration0009AfterCreateStatements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0009Name, err)
		}
	}
	for _, statement := range migration0009Statements[2:] {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0009Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(9, ?, ?, ?, ?)
	`, migration0009Name, migration0009Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0009Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 9"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0009(ctx context.Context, conn *sql.Conn) error {
	additional := append(append(append(append(append(append(append([]string(nil), migration0003Statements...), migration0004Statements...), migration0005Statements...), migration0006Statements...), migration0007Statements...), migration0008Statements...), migration0009Statements...)
	return validateAppliedDomainSchema(ctx, conn, additional)
}
