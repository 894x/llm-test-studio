// Package sqlite owns the local operational SQLite schema.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	currentSchemaName    = "0002_operational_upgrades"
	defaultBusyTime      = 5 * time.Second
	CurrentSchemaVersion = 2
)

// CurrentSchemaIdentity returns the single operational schema marker.
func CurrentSchemaIdentity() (version int, name, checksum string) {
	return CurrentSchemaVersion, currentSchemaName, currentSchemaChecksum()
}

// ErrSchemaResetRequired classifies unsupported or invalid persisted formats.
// The name is retained for callers; this error never authorizes a data reset.
var ErrSchemaResetRequired = errors.New("unsupported sqlite schema; preserve this database and explicitly upgrade it before opening")

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

// Migrate upgrades supported operational baselines before normal workspace use.
// All changes commit together; the pre-upgrade SQLite backup is retained.
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
	catalog, err := loadMigrationCatalog()
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
	// SQLite's documented table-rebuild procedure requires this before BEGIN.
	// Both the source and the completed transaction are checked for FK integrity.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("prepare sqlite migration constraints: %w", err)
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
	state, err := inspectMigrationState(ctx, conn, catalog)
	if err != nil {
		return err
	}
	if state.version == 0 {
		if err := validateEmptyDatabase(ctx, conn); err != nil {
			return err
		}
		for _, object := range currentSchemaObjects {
			if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("apply sqlite migration %s object %q: %w", currentSchemaName, object.name, err)
			}
		}
		baseline := catalog.Baselines[len(catalog.Baselines)-1]
		if err := recordMigration(ctx, conn, migrationRecord{
			version: 1, name: baseline.Name, checksum: baseline.Checksum,
		}, options.AppVersion); err != nil {
			return err
		}
		if err := recordCurrentMigration(ctx, conn, options.AppVersion); err != nil {
			return err
		}
	}
	if state.version == 1 {
		if err := upgradeOperationalDatabase(ctx, conn, path, state.baseline, options); err != nil {
			return err
		}
	}
	if err := validateAppliedSchema(ctx, conn); err != nil {
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
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", runnerBackupURI(absolute, "ro"))
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
	catalog, err := loadMigrationCatalog()
	if err != nil {
		return 0, err
	}
	state, err := inspectMigrationState(ctx, conn, catalog)
	if err != nil {
		return 0, err
	}
	return state.version, nil
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

func validateAppliedSchema(ctx context.Context, conn *sql.Conn) error {
	return validateSchemaObjects(ctx, conn, currentSchemaObjects)
}

func validateSchemaObjects(ctx context.Context, conn *sql.Conn, objects []schemaObject) error {
	expected := make(map[string]schemaObject, len(objects))
	for _, object := range objects {
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
			return schemaResetRequired("operational object %q does not match current schema", name)
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
			return schemaResetRequired("operational current schema is missing object %q", name)
		}
	}
	return nil
}

func schemaResetRequired(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s; preserve the database and run an explicit upgrade before opening",
		ErrSchemaResetRequired,
		fmt.Sprintf(format, arguments...),
	)
}

func normalizeDDL(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func currentSchemaChecksum() string {
	return schemaChecksum(currentSchemaName, currentSchemaObjects)
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
