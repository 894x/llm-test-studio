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

const migration0006Name = "0006_quick_performance_reports"

var migration0006Statements = []string{
	`CREATE TABLE quick_performance_reports (
		id TEXT PRIMARY KEY,
		generated_at TEXT NOT NULL,
		generated_at_unix_nano INTEGER NOT NULL,
		success INTEGER NOT NULL CHECK(success IN (0, 1)),
		model_id TEXT NOT NULL,
		base_url TEXT NOT NULL,
		phase TEXT NOT NULL CHECK(phase IN ('completed', 'cancelled')),
		completed INTEGER NOT NULL CHECK(completed > 0 AND completed <= 10000),
		failed INTEGER NOT NULL CHECK(failed >= 0 AND failed <= completed),
		document_json TEXT NOT NULL CHECK(json_valid(document_json))
	)`,
	`CREATE INDEX idx_quick_performance_reports_generated
	ON quick_performance_reports(generated_at_unix_nano DESC, id DESC)`,
	`CREATE TRIGGER quick_performance_reports_no_update
	BEFORE UPDATE ON quick_performance_reports
	BEGIN
		SELECT RAISE(ABORT, 'quick performance reports are immutable');
	END`,
	`CREATE TRIGGER quick_performance_reports_no_delete
	BEFORE DELETE ON quick_performance_reports
	BEGIN
		SELECT RAISE(ABORT, 'quick performance reports are immutable');
	END`,
}

func migration0006Checksum() string {
	definition := migration0006Name + "\n" + strings.Join(migration0006Statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0006(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0006Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0006Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(6, ?, ?, ?, ?)
	`, migration0006Name, migration0006Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0006Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0006(ctx context.Context, conn *sql.Conn) error {
	additional := append(append(append(append([]string(nil), migration0003Statements...), migration0004Statements...), migration0005Statements...), migration0006Statements...)
	return validateAppliedDomainSchema(ctx, conn, additional)
}
