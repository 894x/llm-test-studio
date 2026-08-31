package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const migration0002Name = "0002_go_core_domain"

// migration0002Statements is append-only. Its exact contents are checksummed
// into schema_migrations and are also the canonical DDL used for drift checks.
var migration0002Statements = []string{
	`CREATE TABLE models (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision)
	)`,
	`CREATE TABLE credential_refs (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		store_ref TEXT NOT NULL,
		purpose TEXT NOT NULL,
		masked_suffix TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision)
	)`,
	`CREATE TABLE channels (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		credential_id TEXT,
		credential_revision INTEGER,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision),
		FOREIGN KEY(credential_id, credential_revision) REFERENCES credential_refs(id, revision),
		CHECK((credential_id IS NULL AND credential_revision IS NULL) OR
		      (credential_id IS NOT NULL AND credential_revision IS NOT NULL))
	)`,
	`CREATE TABLE channel_models (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		channel_revision INTEGER NOT NULL,
		model_id TEXT NOT NULL,
		model_revision INTEGER NOT NULL,
		binding_key TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision),
		FOREIGN KEY(channel_id, channel_revision) REFERENCES channels(id, revision),
		FOREIGN KEY(model_id, model_revision) REFERENCES models(id, revision),
		UNIQUE(binding_key, revision)
	)`,
	`CREATE TABLE test_cases (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision)
	)`,
	`CREATE TABLE test_suites (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision)
	)`,
	`CREATE TABLE suite_cases (
		suite_id TEXT NOT NULL,
		suite_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		case_id TEXT NOT NULL,
		case_revision INTEGER NOT NULL,
		PRIMARY KEY(suite_id, suite_revision, position),
		UNIQUE(suite_id, suite_revision, case_id),
		FOREIGN KEY(suite_id, suite_revision) REFERENCES test_suites(id, revision) ON DELETE CASCADE,
		FOREIGN KEY(case_id, case_revision) REFERENCES test_cases(id, revision)
	)`,
	`CREATE TABLE test_plans (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		suite_id TEXT,
		suite_revision INTEGER,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision),
		FOREIGN KEY(suite_id, suite_revision) REFERENCES test_suites(id, revision),
		CHECK((suite_id IS NULL AND suite_revision IS NULL) OR
		      (suite_id IS NOT NULL AND suite_revision IS NOT NULL))
	)`,
	`CREATE TABLE plan_models (
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		model_id TEXT NOT NULL,
		model_revision INTEGER NOT NULL,
		PRIMARY KEY(plan_id, plan_revision, position),
		UNIQUE(plan_id, plan_revision, model_id),
		FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision) ON DELETE CASCADE,
		FOREIGN KEY(model_id, model_revision) REFERENCES models(id, revision)
	)`,
	`CREATE TABLE plan_channels (
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		channel_id TEXT NOT NULL,
		channel_revision INTEGER NOT NULL,
		PRIMARY KEY(plan_id, plan_revision, position),
		UNIQUE(plan_id, plan_revision, channel_id),
		FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision) ON DELETE CASCADE,
		FOREIGN KEY(channel_id, channel_revision) REFERENCES channels(id, revision)
	)`,
	`CREATE TABLE plan_cases (
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		case_id TEXT NOT NULL,
		case_revision INTEGER NOT NULL,
		PRIMARY KEY(plan_id, plan_revision, position),
		UNIQUE(plan_id, plan_revision, case_id),
		FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision) ON DELETE CASCADE,
		FOREIGN KEY(case_id, case_revision) REFERENCES test_cases(id, revision)
	)`,
	`CREATE TABLE plan_channel_models (
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		channel_id TEXT NOT NULL,
		model_id TEXT NOT NULL,
		mapping_id TEXT NOT NULL,
		mapping_revision INTEGER NOT NULL,
		PRIMARY KEY(plan_id, plan_revision, position),
		UNIQUE(plan_id, plan_revision, channel_id, model_id),
		FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision) ON DELETE CASCADE,
		FOREIGN KEY(plan_id, plan_revision, channel_id) REFERENCES plan_channels(plan_id, plan_revision, channel_id),
		FOREIGN KEY(plan_id, plan_revision, model_id) REFERENCES plan_models(plan_id, plan_revision, model_id),
		FOREIGN KEY(mapping_id, mapping_revision) REFERENCES channel_models(id, revision)
	)`,
	`CREATE TABLE execution_runs (
		id TEXT PRIMARY KEY,
		current_revision INTEGER NOT NULL CHECK(current_revision > 0),
		created_at TEXT NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1)),
		FOREIGN KEY(id, current_revision) REFERENCES execution_run_revisions(run_id, revision)
			DEFERRABLE INITIALLY DEFERRED
	)`,
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
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision)
	)`,
	`CREATE TABLE evidence (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		run_id TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE case_results (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		run_id TEXT NOT NULL,
		case_id TEXT,
		request_id TEXT,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, request_id)
	)`,
	`CREATE TABLE reports (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		run_id TEXT NOT NULL UNIQUE,
		generated_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE artifacts (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		name TEXT NOT NULL,
		relative_path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		media_type TEXT NOT NULL,
		redacted INTEGER NOT NULL CHECK(redacted IN (0, 1)),
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, relative_path)
	)`,
	`CREATE TABLE report_attachments (
		report_id TEXT NOT NULL,
		artifact_id TEXT NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		PRIMARY KEY(report_id, artifact_id),
		UNIQUE(report_id, position),
		FOREIGN KEY(report_id) REFERENCES reports(id) ON DELETE CASCADE,
		FOREIGN KEY(artifact_id) REFERENCES artifacts(id)
	)`,
	`CREATE TABLE integrations (
		id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		credential_id TEXT,
		credential_revision INTEGER,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(id, revision),
		FOREIGN KEY(credential_id, credential_revision) REFERENCES credential_refs(id, revision),
		CHECK((credential_id IS NULL AND credential_revision IS NULL) OR
		      (credential_id IS NOT NULL AND credential_revision IS NOT NULL))
	)`,
	`CREATE INDEX idx_models_latest ON models(id, revision DESC)`,
	`CREATE INDEX idx_credential_refs_latest ON credential_refs(id, revision DESC)`,
	`CREATE INDEX idx_channels_latest ON channels(id, revision DESC)`,
	`CREATE INDEX idx_channel_models_latest ON channel_models(id, revision DESC)`,
	`CREATE INDEX idx_test_cases_latest ON test_cases(id, revision DESC)`,
	`CREATE INDEX idx_test_suites_latest ON test_suites(id, revision DESC)`,
	`CREATE INDEX idx_test_plans_latest ON test_plans(id, revision DESC)`,
	`CREATE INDEX idx_execution_run_revisions_updated ON execution_run_revisions(updated_at DESC, run_id)`,
	`CREATE INDEX idx_case_results_run ON case_results(run_id, created_at, id)`,
	`CREATE UNIQUE INDEX idx_case_results_case_summary ON case_results(run_id, case_id) WHERE request_id IS NULL AND case_id IS NOT NULL`,
	`CREATE INDEX idx_evidence_run ON evidence(run_id, created_at, id)`,
	`CREATE INDEX idx_reports_run ON reports(run_id)`,
	`CREATE INDEX idx_integrations_latest ON integrations(id, revision DESC)`,
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

var migration0002ObjectPattern = regexp.MustCompile(`(?i)^CREATE\s+(?:UNIQUE\s+)?(TABLE|INDEX|TRIGGER)\s+([a-z_][a-z0-9_]*)\b`)
var migrationTriggerTablePattern = regexp.MustCompile(`(?i)\bON\s+([a-z_][a-z0-9_]*)\b`)

func migration0002Checksum() string {
	definition := migration0002Name + "\n" + strings.Join(migration0002Statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
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
			return 0, fmt.Errorf("sqlite schema is unknown: user_version is %d without migration history", userVersion)
		}
		return 0, nil
	}
	if migrationTableCount != 1 {
		return 0, errors.New("sqlite schema is unknown: duplicate schema_migrations tables")
	}
	if err := validateMigrationTable(ctx, conn); err != nil {
		return 0, err
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT version, name, checksum, applied_at, app_version
		FROM schema_migrations ORDER BY version
	`)
	if err != nil {
		return 0, fmt.Errorf("read sqlite migration history: %w", err)
	}
	defer rows.Close()
	type record struct {
		version    int
		name       string
		checksum   string
		appliedAt  string
		appVersion string
	}
	var history []record
	for rows.Next() {
		var item record
		if err := rows.Scan(&item.version, &item.name, &item.checksum, &item.appliedAt, &item.appVersion); err != nil {
			return 0, fmt.Errorf("read sqlite migration record: %w", err)
		}
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate sqlite migration history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close sqlite migration history: %w", err)
	}
	if len(history) < 1 || len(history) > CurrentSchemaVersion {
		return 0, fmt.Errorf("sqlite schema is unknown: migration history contains %d rows", len(history))
	}
	want := []record{
		{version: 1, name: migration0001Name, checksum: migration0001Checksum()},
		{version: 2, name: migration0002Name, checksum: migration0002Checksum()},
		{version: 3, name: migration0003Name, checksum: migration0003Checksum()},
		{version: 4, name: migration0004Name, checksum: migration0004Checksum()},
		{version: 5, name: migration0005Name, checksum: migration0005Checksum()},
	}
	for index, got := range history {
		expected := want[index]
		if got.version != expected.version || got.name != expected.name {
			return 0, fmt.Errorf("sqlite schema is unknown: migration record %d is version %d name %q", index, got.version, got.name)
		}
		if got.checksum != expected.checksum {
			return 0, fmt.Errorf("sqlite migration %04d checksum mismatch: got %q", expected.version, got.checksum)
		}
		appliedAt, err := time.Parse(time.RFC3339Nano, got.appliedAt)
		if err != nil || appliedAt.IsZero() {
			return 0, fmt.Errorf("sqlite schema is unknown: migration %04d has invalid applied_at", got.version)
		}
		_, offset := appliedAt.Zone()
		if offset != 0 || strings.TrimSpace(got.appVersion) == "" {
			return 0, fmt.Errorf("sqlite schema is unknown: migration %04d has invalid metadata", got.version)
		}
	}
	version := history[len(history)-1].version
	if userVersion != version {
		return 0, fmt.Errorf("sqlite schema is unknown: migration %04d has user_version %d", version, userVersion)
	}
	if version == 1 {
		if err := validateAppliedSchema0001(ctx, conn); err != nil {
			return 0, err
		}
	} else if version == 2 {
		if err := validateAppliedSchema0002(ctx, conn); err != nil {
			return 0, err
		}
	} else if version == 3 {
		if err := validateAppliedSchema0003(ctx, conn); err != nil {
			return 0, err
		}
	} else if version == 4 {
		if err := validateAppliedSchema0004(ctx, conn); err != nil {
			return 0, err
		}
	} else if err := validateAppliedSchema0005(ctx, conn); err != nil {
		return 0, err
	}
	return version, nil
}

func applyMigration0002(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0002Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0002Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(2, ?, ?, ?, ?)
	`, migration0002Name, migration0002Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0002Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func validateAppliedSchema0002(ctx context.Context, conn *sql.Conn) error {
	return validateAppliedDomainSchema(ctx, conn, nil)
}

func validateAppliedDomainSchema(ctx context.Context, conn *sql.Conn, additionalStatements []string) error {
	expectedTables := map[string]bool{
		"schema_migrations": true, "runs": true, "run_results": true,
		"audit_runs": true, "audit_case_results": true,
	}
	expectedIndexes := make(map[string]string, len(legacyIndexes))
	for name, definition := range legacyIndexes {
		expectedIndexes[name] = definition.table
	}
	expectedTriggers := make(map[string]string)
	expectedDDL := make(map[string]string, len(migration0002Statements))
	for _, statement := range migration0002Statements {
		match := migration0002ObjectPattern.FindStringSubmatch(strings.TrimSpace(statement))
		if len(match) != 3 {
			return errors.New("sqlite migration 0002 contains an unrecognized statement")
		}
		kind := strings.ToLower(match[1])
		name := strings.ToLower(match[2])
		expectedDDL[kind+":"+name] = normalizeDDL(statement)
		if kind == "table" {
			expectedTables[name] = true
		} else if kind == "index" {
			expectedIndexes[name] = ""
		} else {
			tableMatch := migrationTriggerTablePattern.FindStringSubmatch(statement)
			if len(tableMatch) != 2 {
				return errors.New("sqlite migration 0002 contains a trigger without a target table")
			}
			expectedTriggers[name] = strings.ToLower(tableMatch[1])
		}
	}
	for _, statement := range additionalStatements {
		match := migration0002ObjectPattern.FindStringSubmatch(strings.TrimSpace(statement))
		if len(match) != 3 {
			return errors.New("sqlite migration contains an unrecognized statement")
		}
		kind := strings.ToLower(match[1])
		name := strings.ToLower(match[2])
		expectedDDL[kind+":"+name] = normalizeDDL(statement)
		if kind == "table" {
			expectedTables[name] = true
		} else if kind == "index" {
			expectedIndexes[name] = ""
		} else {
			tableMatch := migrationTriggerTablePattern.FindStringSubmatch(statement)
			if len(tableMatch) != 2 {
				return errors.New("sqlite migration contains a trigger without a target table")
			}
			expectedTriggers[name] = strings.ToLower(tableMatch[1])
		}
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT type, name, tbl_name, sql FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return fmt.Errorf("inspect migrated sqlite v2 objects: %w", err)
	}
	seenTables := make(map[string]bool)
	seenIndexes := make(map[string]bool)
	seenTriggers := make(map[string]bool)
	for rows.Next() {
		var objectType, name, table string
		var definition sql.NullString
		if err := rows.Scan(&objectType, &name, &table, &definition); err != nil {
			rows.Close()
			return fmt.Errorf("read migrated sqlite v2 object: %w", err)
		}
		switch objectType {
		case "table":
			if !expectedTables[name] {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated database has table %q", name)
			}
			seenTables[name] = true
		case "index":
			expectedTable, ok := expectedIndexes[name]
			if !ok || (expectedTable != "" && expectedTable != table) {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated database has index %q on table %q", name, table)
			}
			seenIndexes[name] = true
		case "trigger":
			expectedTable, ok := expectedTriggers[name]
			if !ok || table != expectedTable {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated database has trigger %q on table %q", name, table)
			}
			seenTriggers[name] = true
		default:
			rows.Close()
			return fmt.Errorf("sqlite schema is unknown: migrated database has %s %q", objectType, name)
		}
		if expected, ok := expectedDDL[objectType+":"+name]; ok {
			if !definition.Valid || normalizeDDL(definition.String) != expected {
				rows.Close()
				return fmt.Errorf("sqlite schema drift detected for %s %q", objectType, name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate migrated sqlite v2 objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close migrated sqlite v2 objects: %w", err)
	}
	for table := range expectedTables {
		if !seenTables[table] {
			return fmt.Errorf("sqlite schema is unknown: migrated database is missing table %q", table)
		}
	}
	for index := range expectedIndexes {
		if !seenIndexes[index] {
			return fmt.Errorf("sqlite schema is unknown: migrated database is missing index %q", index)
		}
	}
	for trigger := range expectedTriggers {
		if !seenTriggers[trigger] {
			return fmt.Errorf("sqlite schema is unknown: migrated database is missing trigger %q", trigger)
		}
	}
	for name, expected := range legacyIndexes {
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

func normalizeDDL(value string) string {
	return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(value), ";")), " ")
}
