// Package sqlite owns the local SQLite schema and its versioned migrations.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	migration0001Name    = "0001_legacy_baseline"
	defaultBusyTime      = 5 * time.Second
	CurrentSchemaVersion = AuthoredCatalogRetirementSchemaVersion
)

// MigrateOptions identifies the application applying the schema and controls
// how long SQLite waits for a competing writer.
type MigrateOptions struct {
	AppVersion                    string
	BusyTimeout                   time.Duration
	RetireAuthoredCatalog         bool
	ExpectedAuthoredCatalogDigest string
	// BeforeAuthoredCatalogRetirement runs after the v10 digest and retirement
	// eligibility have been revalidated under BEGIN IMMEDIATE, but before any
	// authored table is dropped. It must not access this SQLite database.
	BeforeAuthoredCatalogRetirement func(context.Context) error
}

var migration0001Statements = []string{
	`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL,
		app_version TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT, url TEXT, model TEXT, requests INTEGER,
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT,
		concurrency INTEGER, timeout INTEGER, failures INTEGER,
		elapsed REAL, qps REAL, total_tpm REAL, peak_in_flight INTEGER,
		request_template TEXT, capture_policy TEXT,
		capture_sample_limit INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS run_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER NOT NULL,
		request_id INTEGER NOT NULL,
		status INTEGER,
		e2e_ms REAL,
		ttft_ms REAL,
		tpot_ms REAL,
		queue_ms REAL,
		started_at_s REAL,
		completed_at_s REAL,
		prompt_tokens INTEGER,
		completion_tokens INTEGER,
		cached_tokens INTEGER,
		chunks INTEGER,
		error TEXT,
		request_body TEXT,
		response_body TEXT,
		response_text TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_run_results_run_id ON run_results(run_id)`,
	`CREATE TABLE IF NOT EXISTS audit_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL,
		suite TEXT NOT NULL,
		base_url TEXT NOT NULL,
		model TEXT NOT NULL,
		total INTEGER NOT NULL,
		overall TEXT NOT NULL,
		verdict TEXT NOT NULL,
		summary_json TEXT NOT NULL,
		config_json TEXT NOT NULL,
		report_dir TEXT NOT NULL,
		report_json TEXT NOT NULL,
		report_html TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS audit_case_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		audit_run_id INTEGER NOT NULL,
		sequence INTEGER NOT NULL,
		case_id TEXT NOT NULL,
		name TEXT NOT NULL,
		dimension TEXT NOT NULL,
		protocol TEXT NOT NULL,
		model TEXT NOT NULL,
		status TEXT NOT NULL,
		severity TEXT NOT NULL,
		elapsed_ms INTEGER NOT NULL,
		evidence TEXT NOT NULL,
		http_status INTEGER NOT NULL,
		metrics_json TEXT NOT NULL,
		result_json TEXT NOT NULL,
		artifact_dir TEXT NOT NULL,
		FOREIGN KEY(audit_run_id) REFERENCES audit_runs(id) ON DELETE CASCADE,
		UNIQUE(audit_run_id, sequence)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_runs_ts ON audit_runs(ts DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_case_results_run_id ON audit_case_results(audit_run_id)`,
}

var legacyRunExtensionColumns = []struct {
	name       string
	columnType string
}{
	{name: "concurrency", columnType: "INTEGER"},
	{name: "timeout", columnType: "INTEGER"},
	{name: "failures", columnType: "INTEGER"},
	{name: "elapsed", columnType: "REAL"},
	{name: "qps", columnType: "REAL"},
	{name: "total_tpm", columnType: "REAL"},
	{name: "peak_in_flight", columnType: "INTEGER"},
	{name: "request_template", columnType: "TEXT"},
	{name: "capture_policy", columnType: "TEXT"},
	{name: "capture_sample_limit", columnType: "INTEGER"},
}

// Migrate applies the authoritative local schema using one connection and one
// BEGIN IMMEDIATE transaction.
func Migrate(ctx context.Context, path string, options MigrateOptions) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("sqlite migration path is required")
	}
	if strings.TrimSpace(options.AppVersion) == "" {
		return errors.New("sqlite migration app version is required")
	}
	busyTimeout := options.BusyTimeout
	if busyTimeout == 0 {
		busyTimeout = defaultBusyTime
	}
	if busyTimeout < 0 {
		return errors.New("sqlite migration busy timeout cannot be negative")
	}
	startingVersion, err := SchemaVersion(ctx, path)
	if err != nil {
		return fmt.Errorf("inspect sqlite schema before migration: %w", err)
	}
	if options.RetireAuthoredCatalog && startingVersion < CatalogExportSchemaVersion {
		return fmt.Errorf(
			"retire authored sqlite catalog requires the database to start at version %d, got version %d",
			CatalogExportSchemaVersion,
			startingVersion,
		)
	}
	var expectedAuthoredCatalogDigest []byte
	if options.RetireAuthoredCatalog && startingVersion == CatalogExportSchemaVersion {
		expectedAuthoredCatalogDigest, err = decodeAuthoredCatalogDigest(options.ExpectedAuthoredCatalogDigest)
		if err != nil {
			return err
		}
		currentDigest, digestErr := readAuthoredCatalogDigest(ctx, path)
		if digestErr != nil {
			return fmt.Errorf("inspect authored sqlite catalog before retirement backup: %w", digestErr)
		}
		if !authoredCatalogDigestsEqual(currentDigest, expectedAuthoredCatalogDigest) {
			return errAuthoredCatalogChangedAfterExport
		}
	}
	targetVersion := CatalogExportSchemaVersion
	if options.RetireAuthoredCatalog || startingVersion == AuthoredCatalogRetirementSchemaVersion {
		targetVersion = AuthoredCatalogRetirementSchemaVersion
	}
	if err := backupBeforeMigration(ctx, path, targetVersion); err != nil {
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
	busyMilliseconds := busyTimeout.Milliseconds()
	if busyTimeout > 0 && busyMilliseconds == 0 {
		busyMilliseconds = 1
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", busyMilliseconds)); err != nil {
		return fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	var configuredBusyMilliseconds int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&configuredBusyMilliseconds); err != nil {
		return fmt.Errorf("verify sqlite busy timeout: %w", err)
	}
	if configuredBusyMilliseconds != busyMilliseconds {
		return fmt.Errorf("sqlite busy timeout is %dms, want %dms", configuredBusyMilliseconds, busyMilliseconds)
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
	if options.RetireAuthoredCatalog && version < CatalogExportSchemaVersion {
		return fmt.Errorf(
			"retire authored sqlite catalog requires the database to start at version %d, got version %d",
			CatalogExportSchemaVersion,
			version,
		)
	}
	applyCatalogExportMigrations := !options.RetireAuthoredCatalog
	if applyCatalogExportMigrations && version == 0 {
		if err := validateLegacySchema(ctx, conn); err != nil {
			return err
		}
		for _, statement := range migration0001Statements {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply sqlite migration %s: %w", migration0001Name, err)
			}
		}
		if err := addLegacyRunColumns(ctx, conn); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
			VALUES(1, ?, ?, ?, ?)
		`, migration0001Name, migration0001Checksum(), time.Now().UTC().Format(time.RFC3339Nano), options.AppVersion); err != nil {
			return fmt.Errorf("record sqlite migration %s: %w", migration0001Name, err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("set sqlite user version: %w", err)
		}
		version = 1
	}
	if applyCatalogExportMigrations && version == 1 {
		if err := applyMigration0002(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 2
	}
	if applyCatalogExportMigrations && version == 2 {
		if err := applyMigration0003(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 3
	}
	if applyCatalogExportMigrations && version == 3 {
		if err := applyMigration0004(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 4
	}
	if applyCatalogExportMigrations && version == 4 {
		if err := applyMigration0005(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 5
	}
	if applyCatalogExportMigrations && version == 5 {
		if err := applyMigration0006(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 6
	}
	if applyCatalogExportMigrations && version == 6 {
		if err := applyMigration0007(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 7
	}
	if applyCatalogExportMigrations && version == 7 {
		if err := applyMigration0008(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 8
	}
	if applyCatalogExportMigrations && version == 8 {
		if err := applyMigration0009(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 9
	}
	if applyCatalogExportMigrations && version == 9 {
		if err := applyMigration0010(ctx, conn, options.AppVersion); err != nil {
			return err
		}
		version = 10
	}
	if options.RetireAuthoredCatalog && version == CatalogExportSchemaVersion {
		if err := applyMigration0011WithExpectedDigestAndPreparation(
			ctx, conn, options.AppVersion, expectedAuthoredCatalogDigest, options.BeforeAuthoredCatalogRetirement,
		); err != nil {
			return err
		}
		version = AuthoredCatalogRetirementSchemaVersion
	}
	if version != CatalogExportSchemaVersion && version != AuthoredCatalogRetirementSchemaVersion {
		return fmt.Errorf("sqlite schema is unknown: unsupported migration version %d", version)
	}
	if version == CatalogExportSchemaVersion {
		if err := validateAppliedSchema0010(ctx, conn); err != nil {
			return err
		}
	} else {
		if err := validateAppliedSchema0011(ctx, conn); err != nil {
			return err
		}
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

// SchemaVersion returns the fully validated migration version without mutating
// the database. A path that does not exist yet is an unmigrated version 0.
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
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		return 0, err
	}
	return version, nil
}

func backupBeforeMigration(ctx context.Context, path string, targetVersion int) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect sqlite database before migration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite database before migration backup: %w", err)
	}
	var version int
	queryErr := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	closeErr := db.Close()
	if queryErr != nil {
		return fmt.Errorf("inspect sqlite version before migration backup: %w", queryErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close sqlite version inspection: %w", closeErr)
	}
	if version >= targetVersion {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	destination := filepath.Join(filepath.Dir(path), "backups", fmt.Sprintf("%s-before-v%d-%s.db", base, targetVersion, stamp))
	if err := Backup(ctx, path, destination); err != nil {
		return fmt.Errorf("create pre-migration sqlite backup: %w", err)
	}
	return nil
}

var legacyTableColumns = map[string][]string{
	"runs": {
		"id", "ts", "url", "model", "requests", "duration", "max_tokens",
		"input_tokens", "stream", "random", "success", "total", "ttft_p50",
		"ttft_p90", "tpot_p50", "tpot_p90", "e2e_p50", "e2e_p90", "cached",
		"prompt_total", "config",
	},
	"run_results": {
		"id", "run_id", "request_id", "status", "e2e_ms", "ttft_ms", "tpot_ms",
		"queue_ms", "started_at_s", "completed_at_s", "prompt_tokens",
		"completion_tokens", "cached_tokens", "chunks", "error", "request_body",
		"response_body", "response_text",
	},
	"audit_runs": {
		"id", "ts", "suite", "base_url", "model", "total", "overall", "verdict",
		"summary_json", "config_json", "report_dir", "report_json", "report_html",
	},
	"audit_case_results": {
		"id", "audit_run_id", "sequence", "case_id", "name", "dimension", "protocol",
		"model", "status", "severity", "elapsed_ms", "evidence", "http_status",
		"metrics_json", "result_json", "artifact_dir",
	},
}

type indexDefinition struct {
	table      string
	columns    []string
	descending []bool
}

var legacyIndexes = map[string]indexDefinition{
	"idx_run_results_run_id": {
		table: "run_results", columns: []string{"run_id"}, descending: []bool{false},
	},
	"idx_audit_runs_ts": {
		table: "audit_runs", columns: []string{"ts"}, descending: []bool{true},
	},
	"idx_audit_case_results_run_id": {
		table: "audit_case_results", columns: []string{"audit_run_id"}, descending: []bool{false},
	},
}

func validateLegacySchema(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT type, name, tbl_name FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return fmt.Errorf("inspect legacy sqlite objects: %w", err)
	}
	var tables []string
	type existingIndex struct {
		name  string
		table string
	}
	var indexes []existingIndex
	for rows.Next() {
		var objectType, name, table string
		if err := rows.Scan(&objectType, &name, &table); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy sqlite object: %w", err)
		}
		switch objectType {
		case "table":
			if _, allowed := legacyTableColumns[name]; !allowed {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: table %q is not part of the legacy baseline", name)
			}
			tables = append(tables, name)
		case "index":
			expected, allowed := legacyIndexes[name]
			if !allowed {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: index %q is not part of the legacy baseline", name)
			}
			if table != expected.table {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: index %q belongs to table %q, want %q", name, table, expected.table)
			}
			indexes = append(indexes, existingIndex{name: name, table: table})
		default:
			rows.Close()
			return fmt.Errorf("sqlite schema is unknown: %s %q is not part of the legacy baseline", objectType, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy sqlite objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy sqlite objects: %w", err)
	}
	for _, index := range indexes {
		if err := validateLegacyIndex(ctx, conn, index.name, legacyIndexes[index.name]); err != nil {
			return err
		}
	}

	for _, table := range tables {
		if err := validateLegacyTable(ctx, conn, table, false); err != nil {
			return err
		}
	}
	return nil
}

func validateLegacyTable(ctx context.Context, conn *sql.Conn, table string, requireExtensions bool) error {
	if err := validateLegacyTableDDL(ctx, conn, table); err != nil {
		return err
	}
	columns, err := columnNames(ctx, conn, table)
	if err != nil {
		return err
	}
	shapes, err := columnShapes(ctx, conn, table)
	if err != nil {
		return err
	}
	required := legacyTableColumns[table]
	allowed := make(map[string]bool, len(required)+len(legacyRunExtensionColumns))
	for _, column := range required {
		allowed[column] = true
	}
	if table == "runs" {
		for _, column := range legacyRunExtensionColumns {
			allowed[column.name] = true
		}
	}
	seen := make(map[string]bool, len(columns))
	for _, column := range columns {
		if !allowed[column] {
			return fmt.Errorf("sqlite schema is unknown: table %q has unexpected column %q", table, column)
		}
		want, ok := legacyColumnShape(table, column)
		if !ok {
			return fmt.Errorf("sqlite schema is unknown: table %q has unsupported column %q", table, column)
		}
		got := shapes[column]
		if got.columnType != want.columnType || got.notNull != want.notNull || got.primaryKey != want.primaryKey || got.hasDefault || got.hidden != 0 {
			return fmt.Errorf(
				"sqlite schema is unknown: table %q column %q has shape (%s, notnull=%d, pk=%d, default=%t, hidden=%d), want (%s, notnull=%d, pk=%d, default=false, hidden=0)",
				table, column, got.columnType, got.notNull, got.primaryKey, got.hasDefault, got.hidden,
				want.columnType, want.notNull, want.primaryKey,
			)
		}
		seen[column] = true
	}
	for _, column := range required {
		if !seen[column] {
			return fmt.Errorf("sqlite schema is unknown: table %q is missing legacy column %q", table, column)
		}
	}
	if requireExtensions && table == "runs" {
		for _, column := range legacyRunExtensionColumns {
			if !seen[column.name] {
				return fmt.Errorf("sqlite schema is unknown: migrated table %q is missing column %q", table, column.name)
			}
		}
	}
	if err := validateLegacyForeignKeys(ctx, conn, table); err != nil {
		return err
	}
	return validateLegacyUniqueConstraints(ctx, conn, table)
}

var forbiddenLegacyDDL = regexp.MustCompile(`(?i)\b(CHECK|COLLATE|STRICT|WITHOUT|DEFERRABLE|INITIALLY|GENERATED)\b|\bON\s+CONFLICT\b`)
var autoIncrementPrimaryKey = regexp.MustCompile(`(?i)\bID\s+INTEGER\s+PRIMARY\s+KEY\s+AUTOINCREMENT\b`)

func validateLegacyTableDDL(ctx context.Context, conn *sql.Conn, table string) error {
	var definition string
	if err := conn.QueryRowContext(ctx, `
		SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?
	`, table).Scan(&definition); err != nil {
		return fmt.Errorf("read legacy table %q DDL: %w", table, err)
	}
	if forbiddenLegacyDDL.MatchString(definition) {
		return fmt.Errorf("sqlite schema is unknown: table %q DDL contains a non-baseline constraint or option", table)
	}
	if strings.Count(strings.ToUpper(definition), "AUTOINCREMENT") != 1 || !autoIncrementPrimaryKey.MatchString(definition) {
		return fmt.Errorf("sqlite schema is unknown: table %q DDL must use the legacy INTEGER PRIMARY KEY AUTOINCREMENT identity", table)
	}
	return nil
}

func validateLegacyIndex(ctx context.Context, conn *sql.Conn, name string, expected indexDefinition) error {
	rows, err := conn.QueryContext(ctx, "PRAGMA index_xinfo("+name+")")
	if err != nil {
		return fmt.Errorf("inspect legacy index %q: %w", name, err)
	}
	var columns []string
	var descending []bool
	for rows.Next() {
		var sequence, cid, desc, key int
		var column any
		var collation string
		if err := rows.Scan(&sequence, &cid, &column, &desc, &collation, &key); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy index %q: %w", name, err)
		}
		if key == 0 {
			continue
		}
		columnName, ok := column.(string)
		if !ok || columnName == "" {
			rows.Close()
			return fmt.Errorf("sqlite schema is unknown: index %q contains an expression", name)
		}
		columns = append(columns, columnName)
		descending = append(descending, desc != 0)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy index %q: %w", name, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy index %q: %w", name, err)
	}
	if !equalStringSlices(columns, expected.columns) || !equalBoolSlices(descending, expected.descending) {
		return fmt.Errorf("sqlite schema is unknown: index %q has columns %v descending %v, want %v descending %v", name, columns, descending, expected.columns, expected.descending)
	}

	rows, err = conn.QueryContext(ctx, "PRAGMA index_list("+expected.table+")")
	if err != nil {
		return fmt.Errorf("inspect legacy table %q indexes: %w", expected.table, err)
	}
	found := false
	for rows.Next() {
		var sequence, unique, partial int
		var indexName, origin string
		if err := rows.Scan(&sequence, &indexName, &unique, &origin, &partial); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy table %q indexes: %w", expected.table, err)
		}
		if indexName == name {
			found = true
			if unique != 0 || origin != "c" || partial != 0 {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: index %q has unique=%d origin=%q partial=%d", name, unique, origin, partial)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy table %q indexes: %w", expected.table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy table %q indexes: %w", expected.table, err)
	}
	if !found {
		return fmt.Errorf("sqlite schema is unknown: index %q is not attached to table %q", name, expected.table)
	}
	return nil
}

type foreignKeyDefinition struct {
	parent   string
	from     string
	to       string
	onUpdate string
	onDelete string
	match    string
}

var legacyForeignKeys = map[string][]foreignKeyDefinition{
	"runs":       {},
	"audit_runs": {},
	"run_results": {
		{parent: "runs", from: "run_id", to: "id", onUpdate: "NO ACTION", onDelete: "NO ACTION", match: "NONE"},
	},
	"audit_case_results": {
		{parent: "audit_runs", from: "audit_run_id", to: "id", onUpdate: "NO ACTION", onDelete: "CASCADE", match: "NONE"},
	},
}

func validateLegacyForeignKeys(ctx context.Context, conn *sql.Conn, table string) error {
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_list("+table+")")
	if err != nil {
		return fmt.Errorf("inspect legacy table %q foreign keys: %w", table, err)
	}
	var actual []foreignKeyDefinition
	for rows.Next() {
		var id, sequence int
		var definition foreignKeyDefinition
		if err := rows.Scan(&id, &sequence, &definition.parent, &definition.from, &definition.to, &definition.onUpdate, &definition.onDelete, &definition.match); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy table %q foreign keys: %w", table, err)
		}
		actual = append(actual, definition)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy table %q foreign keys: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy table %q foreign keys: %w", table, err)
	}
	expected := legacyForeignKeys[table]
	if len(actual) != len(expected) {
		return fmt.Errorf("sqlite schema is unknown: table %q has %d foreign keys, want %d", table, len(actual), len(expected))
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return fmt.Errorf("sqlite schema is unknown: table %q foreign key %d is %+v, want %+v", table, index, actual[index], expected[index])
		}
	}
	return nil
}

func validateLegacyUniqueConstraints(ctx context.Context, conn *sql.Conn, table string) error {
	wantUnique := map[string][]string{}
	if table == "audit_case_results" {
		wantUnique["u"] = []string{"audit_run_id", "sequence"}
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA index_list("+table+")")
	if err != nil {
		return fmt.Errorf("inspect legacy table %q unique constraints: %w", table, err)
	}
	var uniqueIndexes []string
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy table %q unique constraints: %w", table, err)
		}
		if unique != 0 && origin == "u" && partial == 0 {
			uniqueIndexes = append(uniqueIndexes, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy table %q unique constraints: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy table %q unique constraints: %w", table, err)
	}
	wantCount := 0
	if table == "audit_case_results" {
		wantCount = 1
	}
	if len(uniqueIndexes) != wantCount {
		return fmt.Errorf("sqlite schema is unknown: table %q has %d unique constraints, want %d", table, len(uniqueIndexes), wantCount)
	}
	if wantCount == 1 {
		columns, err := indexColumns(ctx, conn, uniqueIndexes[0])
		if err != nil {
			return err
		}
		if !equalStringSlices(columns, wantUnique["u"]) {
			return fmt.Errorf("sqlite schema is unknown: table %q unique columns are %v, want %v", table, columns, wantUnique["u"])
		}
	}
	return nil
}

func indexColumns(ctx context.Context, conn *sql.Conn, name string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA index_info("+name+")")
	if err != nil {
		return nil, fmt.Errorf("inspect sqlite index %q columns: %w", name, err)
	}
	var columns []string
	for rows.Next() {
		var sequence, cid int
		var column string
		if err := rows.Scan(&sequence, &cid, &column); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read sqlite index %q columns: %w", name, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate sqlite index %q columns: %w", name, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close sqlite index %q columns: %w", name, err)
	}
	return columns, nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalBoolSlices(left, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type columnShape struct {
	columnType string
	notNull    int
	primaryKey int
	hasDefault bool
	hidden     int
}

func columnShapes(ctx context.Context, conn *sql.Conn, table string) (map[string]columnShape, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_xinfo("+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect sqlite table %q shapes: %w", table, err)
	}
	shapes := make(map[string]columnShape)
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read sqlite table %q shapes: %w", table, err)
		}
		shapes[name] = columnShape{
			columnType: strings.ToUpper(strings.TrimSpace(columnType)),
			notNull:    notNull,
			primaryKey: primaryKey,
			hasDefault: defaultValue != nil,
			hidden:     hidden,
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate sqlite table %q shapes: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close sqlite table %q shapes: %w", table, err)
	}
	return shapes, nil
}

func legacyColumnShape(table, column string) (columnShape, bool) {
	shape := columnShape{columnType: "INTEGER"}
	if column == "id" {
		shape.primaryKey = 1
	}
	switch table {
	case "runs":
		if containsColumn(column, "ts", "url", "model", "config", "request_template", "capture_policy") {
			shape.columnType = "TEXT"
		} else if containsColumn(column,
			"ttft_p50", "ttft_p90", "tpot_p50", "tpot_p90", "e2e_p50", "e2e_p90",
			"elapsed", "qps", "total_tpm",
		) {
			shape.columnType = "REAL"
		}
	case "run_results":
		if containsColumn(column, "error", "request_body", "response_body", "response_text") {
			shape.columnType = "TEXT"
		} else if containsColumn(column, "e2e_ms", "ttft_ms", "tpot_ms", "queue_ms", "started_at_s", "completed_at_s") {
			shape.columnType = "REAL"
		}
		if column == "run_id" || column == "request_id" {
			shape.notNull = 1
		}
	case "audit_runs":
		if column != "id" && column != "total" {
			shape.columnType = "TEXT"
		}
		if column != "id" {
			shape.notNull = 1
		}
	case "audit_case_results":
		if !containsColumn(column, "id", "audit_run_id", "sequence", "elapsed_ms", "http_status") {
			shape.columnType = "TEXT"
		}
		if column != "id" {
			shape.notNull = 1
		}
	default:
		return columnShape{}, false
	}
	return shape, true
}

func containsColumn(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func columnNames(ctx context.Context, conn *sql.Conn, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_xinfo("+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect sqlite table %q: %w", table, err)
	}
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read sqlite table %q: %w", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate sqlite table %q: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close sqlite table %q: %w", table, err)
	}
	return columns, nil
}

func validateAppliedSchema0001(ctx context.Context, conn *sql.Conn) error {
	expectedTables := map[string]bool{
		"schema_migrations":  true,
		"runs":               true,
		"run_results":        true,
		"audit_runs":         true,
		"audit_case_results": true,
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT type, name, tbl_name FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return fmt.Errorf("inspect migrated sqlite objects: %w", err)
	}
	seenTables := make(map[string]bool)
	seenIndexes := make(map[string]bool)
	for rows.Next() {
		var objectType, name, table string
		if err := rows.Scan(&objectType, &name, &table); err != nil {
			rows.Close()
			return fmt.Errorf("read migrated sqlite object: %w", err)
		}
		switch objectType {
		case "table":
			if !expectedTables[name] {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated database has table %q", name)
			}
			seenTables[name] = true
		case "index":
			expected, ok := legacyIndexes[name]
			if !ok || expected.table != table {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated database has index %q on table %q", name, table)
			}
			seenIndexes[name] = true
		default:
			rows.Close()
			return fmt.Errorf("sqlite schema is unknown: migrated database has %s %q", objectType, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate migrated sqlite objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close migrated sqlite objects: %w", err)
	}
	for table := range expectedTables {
		if !seenTables[table] {
			return fmt.Errorf("sqlite schema is unknown: migrated database is missing table %q", table)
		}
	}
	for name, expected := range legacyIndexes {
		if !seenIndexes[name] {
			return fmt.Errorf("sqlite schema is unknown: migrated database is missing index %q", name)
		}
		if err := validateLegacyIndex(ctx, conn, name, expected); err != nil {
			return err
		}
	}
	for table := range legacyTableColumns {
		if err := validateLegacyTable(ctx, conn, table, true); err != nil {
			return err
		}
	}
	return validateMigrationTable(ctx, conn)
}

func validateMigrationTable(ctx context.Context, conn *sql.Conn) error {
	wantColumns := []string{"version", "name", "checksum", "applied_at", "app_version"}
	columns, err := columnNames(ctx, conn, "schema_migrations")
	if err != nil {
		return err
	}
	if !equalStringSlices(columns, wantColumns) {
		return fmt.Errorf("sqlite schema is unknown: schema_migrations columns are %v, want %v", columns, wantColumns)
	}
	shapes, err := columnShapes(ctx, conn, "schema_migrations")
	if err != nil {
		return err
	}
	for _, column := range wantColumns {
		want := columnShape{columnType: "TEXT", notNull: 1}
		if column == "version" {
			want = columnShape{columnType: "INTEGER", primaryKey: 1}
		}
		got := shapes[column]
		if got != want {
			return fmt.Errorf("sqlite schema is unknown: schema_migrations column %q has shape %+v, want %+v", column, got, want)
		}
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA index_list(schema_migrations)")
	if err != nil {
		return fmt.Errorf("inspect schema_migrations indexes: %w", err)
	}
	var uniqueIndexes []string
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return fmt.Errorf("read schema_migrations indexes: %w", err)
		}
		if unique != 0 && origin == "u" && partial == 0 {
			uniqueIndexes = append(uniqueIndexes, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate schema_migrations indexes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close schema_migrations indexes: %w", err)
	}
	if len(uniqueIndexes) != 1 {
		return fmt.Errorf("sqlite schema is unknown: schema_migrations has %d unique constraints, want 1", len(uniqueIndexes))
	}
	columns, err = indexColumns(ctx, conn, uniqueIndexes[0])
	if err != nil {
		return err
	}
	if !equalStringSlices(columns, []string{"name"}) {
		return fmt.Errorf("sqlite schema is unknown: schema_migrations unique columns are %v, want [name]", columns)
	}
	return nil
}

func migration0001Checksum() string {
	definition := migration0001Name + "\n" + strings.Join(migration0001Statements, "\n-- statement --\n")
	for _, column := range legacyRunExtensionColumns {
		definition += "\nALTER runs ADD " + column.name + " " + column.columnType
	}
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func addLegacyRunColumns(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_info(runs)")
	if err != nil {
		return fmt.Errorf("inspect legacy runs columns: %w", err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy runs columns: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate legacy runs columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy runs columns: %w", err)
	}
	for _, column := range legacyRunExtensionColumns {
		if existing[column.name] {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE runs ADD COLUMN %s %s", column.name, column.columnType)
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add legacy runs column %q: %w", column.name, err)
		}
	}
	return nil
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
