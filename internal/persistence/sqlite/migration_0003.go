package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/domain"
)

const migration0003Name = "0003_case_import_tracking"
const migration0003LegacyTestCaseBackfill = "legacy-test-case-policy-v1"

type migration0003LegacyTestCase struct {
	domain.EntityMeta
	Name       string                    `json:"name"`
	Protocol   domain.Protocol           `json:"protocol"`
	Definition domain.TestCaseDefinition `json:"definition"`
}

var migration0003Statements = []string{
	`CREATE TABLE test_case_import_sources (
		namespace TEXT NOT NULL CHECK(length(namespace) > 0 AND namespace = trim(namespace)),
		source_key TEXT NOT NULL CHECK(length(source_key) > 0 AND source_key = trim(source_key)),
		source_path TEXT NOT NULL CHECK(length(source_path) > 0 AND source_path = trim(source_path)),
		source_bytes_sha256 TEXT NOT NULL CHECK(length(source_bytes_sha256) = 64 AND source_bytes_sha256 = lower(source_bytes_sha256) AND source_bytes_sha256 NOT GLOB '*[^0-9a-f]*'),
		semantic_sha256 TEXT NOT NULL CHECK(length(semantic_sha256) = 64 AND semantic_sha256 = lower(semantic_sha256) AND semantic_sha256 NOT GLOB '*[^0-9a-f]*'),
		materialized_sha256 TEXT NOT NULL CHECK(length(materialized_sha256) = 64 AND materialized_sha256 = lower(materialized_sha256) AND materialized_sha256 NOT GLOB '*[^0-9a-f]*'),
		converter_version INTEGER NOT NULL CHECK(converter_version > 0),
		entity_id TEXT NOT NULL,
		imported_revision INTEGER NOT NULL CHECK(imported_revision > 0),
		bundle_manifest_sha256 TEXT NOT NULL CHECK(length(bundle_manifest_sha256) = 64 AND bundle_manifest_sha256 = lower(bundle_manifest_sha256) AND bundle_manifest_sha256 NOT GLOB '*[^0-9a-f]*'),
		imported_at TEXT NOT NULL,
		retired_at TEXT,
		PRIMARY KEY(namespace, source_key),
		UNIQUE(namespace, source_path),
		UNIQUE(entity_id),
		FOREIGN KEY(entity_id, imported_revision) REFERENCES test_cases(id, revision)
	)`,
}

func migration0003Checksum() string {
	definition := migration0003Name + "\n" + migration0003LegacyTestCaseBackfill + "\n" + strings.Join(migration0003Statements, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0003(ctx context.Context, conn *sql.Conn, appVersion string) error {
	for _, statement := range migration0003Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0003Name, err)
		}
	}
	if err := upgradeMigration0003TestCases(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(3, ?, ?, ?, ?)
	`, migration0003Name, migration0003Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0003Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func upgradeMigration0003TestCases(ctx context.Context, conn *sql.Conn) error {
	var afterID string
	var afterRevision int64
	for {
		var (
			id                      string
			schemaVersion, revision int64
			createdAt, updatedAt    string
			document                []byte
		)
		err := conn.QueryRowContext(ctx, `
			SELECT id, schema_version, revision, created_at, updated_at, document_json
			FROM test_cases
			WHERE id > ? OR (id = ? AND revision > ?)
			ORDER BY id, revision
			LIMIT 1
		`, afterID, afterID, afterRevision).Scan(&id, &schemaVersion, &revision, &createdAt, &updatedAt, &document)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read legacy test case for sqlite migration %s: %w", migration0003Name, err)
		}
		afterID, afterRevision = id, revision
		if err := verifyEntityRow(document, id, schemaVersion, revision, createdAt, updatedAt); err != nil {
			return fmt.Errorf("sqlite migration %s found corrupt test case metadata: %w", migration0003Name, err)
		}

		var current domain.TestCase
		if err := decodeCanonical(document, &current, func() error { return current.Validate() }); err == nil {
			continue
		}
		var legacy migration0003LegacyTestCase
		if err := decodeCanonical(document, &legacy, func() error { return validateMigration0003LegacyTestCase(legacy) }); err != nil {
			return fmt.Errorf("sqlite migration %s cannot upgrade test case %s revision %d: %w", migration0003Name, id, revision, err)
		}
		upgraded := domain.TestCase{
			EntityMeta:    legacy.EntityMeta,
			Key:           "migrated." + legacy.ID,
			Name:          legacy.Name,
			Dimension:     "legacy",
			Protocol:      legacy.Protocol,
			Enabled:       true,
			Default:       false,
			Severity:      domain.CaseSeverityNormal,
			ExecutionMode: domain.CaseExecutionAutomatic,
			Definition:    legacy.Definition,
		}
		if err := upgraded.Validate(); err != nil {
			return fmt.Errorf("sqlite migration %s produced invalid test case %s revision %d: %w", migration0003Name, id, revision, err)
		}
		canonical, err := marshalCanonical(upgraded)
		if err != nil {
			return fmt.Errorf("encode upgraded test case %s revision %d: %w", id, revision, err)
		}
		result, err := conn.ExecContext(ctx, `
			UPDATE test_cases
			SET document_json = ?
			WHERE id = ? AND revision = ? AND CAST(document_json AS BLOB) = ?
		`, canonical, id, revision, document)
		if err != nil {
			return fmt.Errorf("write upgraded test case %s revision %d: %w", id, revision, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("confirm upgraded test case %s revision %d: %w", id, revision, err)
		}
		if updated != 1 {
			return fmt.Errorf("sqlite migration %s lost test case %s revision %d during upgrade", migration0003Name, id, revision)
		}
	}
}

func validateMigration0003LegacyTestCase(testCase migration0003LegacyTestCase) error {
	if err := testCase.EntityMeta.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(testCase.Name) == "" {
		return fmt.Errorf("test case name must not be empty")
	}
	if err := testCase.Protocol.Validate(); err != nil {
		return err
	}
	return testCase.Definition.Validate()
}

func validateAppliedSchema0003(ctx context.Context, conn *sql.Conn) error {
	return validateAppliedDomainSchema(ctx, conn, migration0003Statements)
}
