package main

import (
	"context"
	"database/sql"
	"fmt"
)

func alignCaseResultsTable(ctx context.Context, db *sql.DB) error {
	var tableCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'case_results'`).Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_case_results_run`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_case_results_case_summary`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_case_results_entry_marker`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE case_results_current (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		run_id TEXT NOT NULL,
		entry_id TEXT NOT NULL,
		case_id TEXT,
		request_id TEXT,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, request_id)
	)`); err != nil {
		return fmt.Errorf("create staging case_results: %w", err)
	}
	var hasEntryID int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('case_results') WHERE name = 'entry_id'`).Scan(&hasEntryID); err != nil {
		return err
	}
	if hasEntryID == 1 {
		if _, err := db.ExecContext(ctx, `INSERT INTO case_results_current(
			id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
		) SELECT id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json FROM case_results`); err != nil {
			return fmt.Errorf("copy case_results with entry_id: %w", err)
		}
	} else {
		if _, err := db.ExecContext(ctx, `INSERT INTO case_results_current(
			id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
		) SELECT
			id, schema_version, revision, created_at, updated_at, run_id,
			json_extract(document_json, '$.entry_id'),
			CASE WHEN json_extract(document_json, '$.case_id') IS NULL THEN case_id ELSE json_extract(document_json, '$.case_id') END,
			request_id, document_json
		FROM case_results`); err != nil {
			return fmt.Errorf("copy case_results into current table: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE case_results`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE case_results (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		run_id TEXT NOT NULL,
		entry_id TEXT NOT NULL,
		case_id TEXT,
		request_id TEXT,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, request_id)
	)`); err != nil {
		return fmt.Errorf("create current case_results: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO case_results(
		id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
	) SELECT id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json FROM case_results_current`); err != nil {
		return fmt.Errorf("restore current case_results: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE case_results_current`); err != nil {
		return err
	}
	for _, ddl := range []string{
		`CREATE INDEX idx_case_results_run ON case_results(run_id, created_at, id)`,
		`CREATE UNIQUE INDEX idx_case_results_case_summary ON case_results(run_id, entry_id, case_id) WHERE request_id IS NULL AND case_id IS NOT NULL AND entry_id IS NOT NULL`,
		`CREATE UNIQUE INDEX idx_case_results_entry_marker ON case_results(run_id, entry_id) WHERE entry_id IS NOT NULL AND case_id IS NULL AND request_id IS NULL`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("recreate case_results index: %w", err)
		}
	}
	return nil
}
