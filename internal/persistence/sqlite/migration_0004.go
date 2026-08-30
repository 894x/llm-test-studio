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

const migration0004Name = "0004_catalog_tombstones"

var migration0004Statements = []string{
	`CREATE TABLE catalog_tombstones (
		entity_table TEXT NOT NULL CHECK(entity_table IN ('models', 'channels', 'channel_models', 'test_cases', 'test_suites', 'test_plans')),
		entity_id TEXT NOT NULL,
		deleted_revision INTEGER NOT NULL CHECK(deleted_revision > 1),
		deleted_at TEXT NOT NULL,
		PRIMARY KEY(entity_table, entity_id)
	)`,
}

func migration0004Checksum() string {
	definition := migration0004Name + "\n" + strings.Join(migration0004Statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0004(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0004Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0004Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(4, ?, ?, ?, ?)
	`, migration0004Name, migration0004Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0004Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0004(ctx context.Context, conn *sql.Conn) error {
	additional := append(append([]string(nil), migration0003Statements...), migration0004Statements...)
	return validateAppliedDomainSchema(ctx, conn, additional)
}
