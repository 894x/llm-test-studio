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

const migration0007Name = "0007_builtin_catalog_seeds"

var migration0007Statements = []string{
	`CREATE TABLE builtin_catalog_seeds (
		seed_key TEXT PRIMARY KEY,
		completed_at TEXT NOT NULL
	) STRICT`,
}

func migration0007Checksum() string {
	definition := migration0007Name + "\n" + strings.Join(migration0007Statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0007(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0007Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0007Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(7, ?, ?, ?, ?)
	`, migration0007Name, migration0007Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0007Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0007(ctx context.Context, conn *sql.Conn) error {
	additional := append(append(append(append(append([]string(nil), migration0003Statements...), migration0004Statements...), migration0005Statements...), migration0006Statements...), migration0007Statements...)
	return validateAppliedDomainSchema(ctx, conn, additional)
}
