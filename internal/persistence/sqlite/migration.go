// Package sqlite owns the local operational SQLite schema.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	migration0001Name    = "0001_operational_baseline"
	defaultBusyTime      = 5 * time.Second
	CurrentSchemaVersion = 1
)

// ErrSchemaResetRequired classifies databases that do not exactly match the
// current operational schema. The product intentionally provides no migration
// or import path for those databases.
var ErrSchemaResetRequired = errors.New("sqlite schema reset required")

// MigrateOptions identifies the application creating the operational schema
// and controls how long SQLite waits for a competing writer.
type MigrateOptions struct {
	AppVersion  string
	BusyTimeout time.Duration
}

type schemaObject struct {
	kind  string
	name  string
	table string
	ddl   string
}

// Migrate creates or validates the single operational schema. Earlier schemas
// are deliberately unsupported: resetting the database is the cutover path.
func Migrate(ctx context.Context, path string, options MigrateOptions) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("sqlite migration path is required")
	}
	if strings.TrimSpace(options.AppVersion) == "" {
		return errors.New("sqlite migration app version is required")
	}
	busyTimeout, err := normalizeBusyTimeout(options.BusyTimeout)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite migration connection: %w", err)
	}
	defer conn.Close()
	if err := configureConnection(ctx, conn, busyTimeout); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin sqlite migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		return err
	}
	if version == 0 {
		if err := validateEmptyDatabase(ctx, conn); err != nil {
			return err
		}
		for _, object := range schemaV1Objects {
			if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("apply sqlite migration %s object %q: %w", migration0001Name, object.name, err)
			}
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version) VALUES(1, ?, ?, ?, ?)`,
			migration0001Name, migration0001Checksum(), time.Now().UTC().Format(time.RFC3339Nano), options.AppVersion); err != nil {
			return fmt.Errorf("record sqlite migration %s: %w", migration0001Name, err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("set sqlite user version: %w", err)
		}
		version = 1
	}
	if version != CurrentSchemaVersion {
		return schemaResetRequired("unsupported migration version %d", version)
	}
	if err := validateAppliedSchemaV1(ctx, conn); err != nil {
		return err
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit sqlite migration: %w", err)
	}
	committed = true
	return nil
}

// SchemaVersion returns the validated operational schema version without
// mutating the database. A missing path is version zero.
func SchemaVersion(ctx context.Context, path string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(path) == "" {
		return 0, errors.New("sqlite schema path is required")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect sqlite schema path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("sqlite schema path must be a regular file")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, fmt.Errorf("open sqlite schema inspection: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire sqlite schema inspection connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return 0, fmt.Errorf("enable read-only sqlite schema inspection: %w", err)
	}
	return appliedMigrationVersion(ctx, conn)
}

func normalizeBusyTimeout(value time.Duration) (time.Duration, error) {
	if value == 0 {
		return defaultBusyTime, nil
	}
	if value < 0 {
		return 0, errors.New("sqlite busy timeout cannot be negative")
	}
	return value, nil
}

func configureConnection(ctx context.Context, conn *sql.Conn, busyTimeout time.Duration) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify sqlite foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("sqlite foreign keys could not be enabled")
	}
	milliseconds := busyTimeout.Milliseconds()
	if milliseconds == 0 {
		milliseconds = 1
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", milliseconds)); err != nil {
		return fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	var configured int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&configured); err != nil {
		return fmt.Errorf("verify sqlite busy timeout: %w", err)
	}
	if configured != milliseconds {
		return fmt.Errorf("sqlite busy timeout is %dms, want %dms", configured, milliseconds)
	}
	return nil
}

func appliedMigrationVersion(ctx context.Context, conn *sql.Conn) (int, error) {
	var userVersion int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return 0, fmt.Errorf("read sqlite user version: %w", err)
	}
	var migrationTableCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&migrationTableCount); err != nil {
		return 0, fmt.Errorf("inspect sqlite migration table: %w", err)
	}
	if migrationTableCount == 0 {
		if userVersion != 0 {
			return 0, schemaResetRequired("user_version is %d without migration history", userVersion)
		}
		return 0, nil
	}
	var version, count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&count, &version); err != nil {
		return 0, fmt.Errorf("read sqlite migration history: %w", err)
	}
	if count != 1 || version != CurrentSchemaVersion {
		return 0, schemaResetRequired("migration history contains %d rows ending at version %d", count, version)
	}
	var name, checksum, appliedAt, appVersion string
	if err := conn.QueryRowContext(ctx, `SELECT name, checksum, applied_at, app_version FROM schema_migrations WHERE version = 1`).Scan(&name, &checksum, &appliedAt, &appVersion); err != nil {
		return 0, fmt.Errorf("read sqlite migration record: %w", err)
	}
	if name != migration0001Name || checksum != migration0001Checksum() {
		return 0, schemaResetRequired("operational baseline migration identity does not match")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, appliedAt)
	if err != nil || timestamp.IsZero() || strings.TrimSpace(appVersion) == "" {
		return 0, schemaResetRequired("operational baseline migration metadata is invalid")
	}
	_, offset := timestamp.Zone()
	if offset != 0 || userVersion != CurrentSchemaVersion {
		return 0, schemaResetRequired("operational baseline has user_version %d", userVersion)
	}
	if err := validateAppliedSchemaV1(ctx, conn); err != nil {
		return 0, err
	}
	return CurrentSchemaVersion, nil
}

func validateEmptyDatabase(ctx context.Context, conn *sql.Conn) error {
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		return fmt.Errorf("inspect empty sqlite schema: %w", err)
	}
	if count != 0 {
		return schemaResetRequired("unversioned database is not empty")
	}
	return nil
}

func validateAppliedSchemaV1(ctx context.Context, conn *sql.Conn) error {
	expected := make(map[string]schemaObject, len(schemaV1Objects))
	for _, object := range schemaV1Objects {
		expected[object.name] = object
	}
	rows, err := conn.QueryContext(ctx, `SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		return fmt.Errorf("inspect operational sqlite schema: %w", err)
	}
	seen := make(map[string]bool, len(expected))
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if err := rows.Scan(&kind, &name, &table, &ddl); err != nil {
			rows.Close()
			return fmt.Errorf("read operational sqlite object: %w", err)
		}
		want, ok := expected[name]
		if !ok || kind != want.kind || table != want.table || !ddl.Valid || normalizeDDL(ddl.String) != normalizeDDL(want.ddl) {
			rows.Close()
			return schemaResetRequired("operational object %q does not match schema v1", name)
		}
		seen[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate operational sqlite objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close operational sqlite objects: %w", err)
	}
	for name := range expected {
		if !seen[name] {
			return schemaResetRequired("operational schema v1 is missing object %q", name)
		}
	}
	return nil
}

func schemaResetRequired(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s; reset the operational database",
		ErrSchemaResetRequired,
		fmt.Sprintf(format, arguments...),
	)
}

func normalizeDDL(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func migration0001Checksum() string {
	parts := make([]string, 0, len(schemaV1Objects)+1)
	parts = append(parts, migration0001Name)
	for _, object := range schemaV1Objects {
		parts = append(parts, object.ddl)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n-- object --\n")))
	return hex.EncodeToString(sum[:])
}

func validateIntegrity(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("run sqlite foreign key check: %w", err)
	}
	if rows.Next() {
		var table string
		var rowID any
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			rows.Close()
			return fmt.Errorf("read sqlite foreign key check: %w", err)
		}
		rows.Close()
		return fmt.Errorf("sqlite foreign key check failed for table %q row %v", table, rowID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate sqlite foreign key check: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close sqlite foreign key check: %w", err)
	}
	var result string
	if err := conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("run sqlite quick check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("sqlite quick check failed: %s", result)
	}
	return nil
}
