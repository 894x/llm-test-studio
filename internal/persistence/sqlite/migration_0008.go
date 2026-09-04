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

const migration0008Name = "0008_execution_run_snapshot_plan_decoupling"

// migration0008Statements contains the canonical replacement DDL. Keeping the
// final CREATE statements separate lets cumulative schema validation replace
// migration 0002's definitions for the same objects without changing its
// historical checksum.
var migration0008Statements = []string{
	`CREATE TABLE execution_run_revisions (
		run_id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		status TEXT NOT NULL,
		snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(run_id, revision),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX idx_execution_run_revisions_updated ON execution_run_revisions(updated_at DESC, run_id)`,
	`CREATE TRIGGER trg_execution_run_revisions_no_update
		BEFORE UPDATE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END`,
	`CREATE TRIGGER trg_execution_run_revisions_no_delete
		BEFORE DELETE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END`,
	`CREATE TRIGGER trg_execution_run_revisions_contiguous_insert
		BEFORE INSERT ON execution_run_revisions
		WHEN NEW.revision != COALESCE(
			(SELECT MAX(revision) + 1 FROM execution_run_revisions WHERE run_id = NEW.run_id),
			1
		)
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions must be contiguous');
		END`,
}

// These statements are migration mechanics rather than final schema objects.
// They remain explicit so the destructive part of the table rebuild is covered
// by the migration checksum as well as the canonical replacement DDL.
var migration0008BeforeCreateStatements = []string{
	`PRAGMA defer_foreign_keys = ON`,
	`DROP TRIGGER trg_execution_run_revisions_no_update`,
	`DROP TRIGGER trg_execution_run_revisions_no_delete`,
	`DROP TRIGGER trg_execution_run_revisions_contiguous_insert`,
	`DROP INDEX idx_execution_run_revisions_updated`,
	`CREATE TEMP TABLE execution_run_revisions_v8_backup AS
		SELECT run_id, schema_version, revision, created_at, updated_at,
		       plan_id, plan_revision, status, snapshot_json, document_json
		FROM execution_run_revisions`,
	`DROP TABLE execution_run_revisions`,
}

var migration0008AfterCreateStatements = []string{
	`INSERT INTO execution_run_revisions(
		run_id, schema_version, revision, created_at, updated_at,
		plan_id, plan_revision, status, snapshot_json, document_json
	)
	SELECT run_id, schema_version, revision, created_at, updated_at,
	       plan_id, plan_revision, status, snapshot_json, document_json
	FROM execution_run_revisions_v8_backup`,
	`DROP TABLE execution_run_revisions_v8_backup`,
}

func migration0008Checksum() string {
	statements := make([]string, 0, len(migration0008BeforeCreateStatements)+len(migration0008AfterCreateStatements)+len(migration0008Statements))
	statements = append(statements, migration0008BeforeCreateStatements...)
	statements = append(statements, migration0008Statements[0])
	statements = append(statements, migration0008AfterCreateStatements...)
	statements = append(statements, migration0008Statements[1:]...)
	definition := migration0008Name + "\n" + strings.Join(statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0008(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0008BeforeCreateStatements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0008Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, migration0008Statements[0]); err != nil {
		return fmt.Errorf("apply sqlite migration %s: %w", migration0008Name, err)
	}
	for _, statement := range migration0008AfterCreateStatements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0008Name, err)
		}
	}
	for _, statement := range migration0008Statements[1:] {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0008Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(8, ?, ?, ?, ?)
	`, migration0008Name, migration0008Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0008Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 8"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0008(ctx context.Context, conn *sql.Conn) error {
	additional := append(append(append(append(append(append([]string(nil), migration0003Statements...), migration0004Statements...), migration0005Statements...), migration0006Statements...), migration0007Statements...), migration0008Statements...)
	return validateAppliedDomainSchema(ctx, conn, additional)
}
