package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"

	_ "modernc.org/sqlite"
)

func upgradeDatabase(path string) (string, error) {
	backup := path + ".pre-schema-reset.bak"
	if _, err := os.Stat(backup); err == nil {
		return "", fmt.Errorf("backup already exists: %s", backup)
	}
	if err := copyFile(path, backup); err != nil {
		return "", fmt.Errorf("backup database: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return "", fmt.Errorf("set busy timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("begin upgrade transaction: %w", err)
	}
	committed := false
	backupCreated := true
	defer func() {
		if !committed {
			_, _ = db.ExecContext(context.Background(), "ROLLBACK")
			if backupCreated {
				_ = os.Remove(backup)
			}
		}
	}()
	for _, trigger := range []string{
		"quick_performance_reports_no_update",
		"trg_execution_run_revisions_no_update",
		"comparison_revisions_no_update",
	} {
		if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+trigger); err != nil {
			return "", err
		}
	}
	perfCount, err := upgradeQuickPerformanceTable(ctx, db)
	if err != nil {
		return "", err
	}
	runCount, err := upgradeRunRevisions(ctx, db)
	if err != nil {
		return "", err
	}
	reportCount, err := upgradeReportDocuments(ctx, db)
	if err != nil {
		return "", err
	}
	if err := upgradeCaseResults(ctx, db); err != nil {
		return "", err
	}
	if err := upgradeJSONDocuments(ctx, db, "evidence", stampEvidenceDocument); err != nil {
		return "", err
	}
	if err := upgradeComparisonDocuments(ctx, db); err != nil {
		return "", err
	}
	if err := alignCaseResultsTable(ctx, db); err != nil {
		return "", err
	}
	version, name, checksum := persistence.CurrentSchemaIdentity()
	if _, err := db.ExecContext(ctx, `
		UPDATE schema_migrations SET version = ?, name = ?, checksum = ?, applied_at = ?
	`, version, name, checksum, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("update schema_migrations: %w", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return "", err
	}
	for _, ddl := range []string{
		`CREATE TRIGGER quick_performance_reports_no_update
		BEFORE UPDATE ON quick_performance_reports
		BEGIN
			SELECT RAISE(ABORT, 'quick performance reports are immutable');
		END`,
		`CREATE TRIGGER trg_execution_run_revisions_no_update
		BEFORE UPDATE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END`,
		`CREATE TRIGGER comparison_revisions_no_update
		BEFORE UPDATE ON comparison_revisions
		BEGIN
			SELECT RAISE(ABORT, 'comparison revisions are immutable');
		END`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return "", err
		}
	}
	if _, err := db.ExecContext(ctx, "COMMIT"); err != nil {
		return "", fmt.Errorf("commit upgrade transaction: %w", err)
	}
	committed = true
	if err := db.Close(); err != nil {
		return "", fmt.Errorf("close upgraded database: %w", err)
	}
	if err := persistence.Migrate(ctx, path, persistence.MigrateOptions{AppVersion: "schema-reset"}); err != nil {
		return "", fmt.Errorf("verify migrated database: %w", err)
	}
	return fmt.Sprintf("backup %s; converted %d quick reports, %d run revisions, %d reports", filepath.Base(backup), perfCount, runCount, reportCount), nil
}

func upgradeQuickPerformanceTable(ctx context.Context, db *sql.DB) (int, error) {
	type item struct {
		id       string
		document []byte
	}
	items, err := collectRows(ctx, db, `SELECT id, document_json FROM quick_performance_reports`, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.id, &next.document)
		return next, err
	})
	if err != nil {
		return 0, err
	}
	for _, next := range items {
		updated, err := upgradeQuickPerformanceDocument(next.document)
		if err != nil {
			return 0, fmt.Errorf("quick performance report %s: %w", next.id, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE quick_performance_reports SET document_json = ? WHERE id = ?`, updated, next.id); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func upgradeRunRevisions(ctx context.Context, db *sql.DB) (int, error) {
	type item struct {
		runID        string
		revision     int64
		snapshotJSON []byte
		documentJSON []byte
	}
	items, err := collectRows(ctx, db, `SELECT run_id, revision, snapshot_json, document_json FROM execution_run_revisions`, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.runID, &next.revision, &next.snapshotJSON, &next.documentJSON)
		return next, err
	})
	if err != nil {
		return 0, err
	}
	for _, next := range items {
		snapshotJSON, err := stampSnapshotDocument(next.snapshotJSON)
		if err != nil {
			return 0, fmt.Errorf("run %s revision %d snapshot: %w", next.runID, next.revision, err)
		}
		documentJSON, err := overlayPlanSnapshot(next.documentJSON, snapshotJSON)
		if err != nil {
			return 0, fmt.Errorf("run %s revision %d overlay snapshot: %w", next.runID, next.revision, err)
		}
		documentJSON, err = stampRunDocument(documentJSON)
		if err != nil {
			return 0, fmt.Errorf("run %s revision %d document: %w", next.runID, next.revision, err)
		}
		snapshotJSON, err = extractRunSnapshotDocument(documentJSON)
		if err != nil {
			return 0, fmt.Errorf("run %s revision %d snapshot alignment: %w", next.runID, next.revision, err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE execution_run_revisions
			SET schema_version = json_extract(?, '$.schema_version'),
			    created_at = json_extract(?, '$.created_at'),
			    updated_at = json_extract(?, '$.updated_at'),
			    snapshot_json = ?,
			    document_json = ?
			WHERE run_id = ? AND revision = ?
		`, documentJSON, documentJSON, documentJSON, snapshotJSON, documentJSON, next.runID, next.revision); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func upgradeReportDocuments(ctx context.Context, db *sql.DB) (int, error) {
	snapshots, err := loadCurrentSnapshots(ctx, db)
	if err != nil {
		return 0, err
	}
	type item struct {
		id       string
		runID    string
		document []byte
	}
	items, err := collectRows(ctx, db, `SELECT id, run_id, document_json FROM reports`, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.id, &next.runID, &next.document)
		return next, err
	})
	if err != nil {
		return 0, err
	}
	for _, next := range items {
		snapshotJSON := snapshots[next.runID]
		updated, err := convertReportDocument(next.document, snapshotJSON)
		if err != nil {
			return 0, fmt.Errorf("report %s: %w", next.id, err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE reports
			SET schema_version = json_extract(?, '$.schema_version'),
			    generated_at = json_extract(?, '$.generated_at'),
			    document_json = ?
			WHERE id = ?
		`, updated, updated, updated, next.id); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func upgradeCaseResults(ctx context.Context, db *sql.DB) error {
	snapshots, err := loadCurrentSnapshots(ctx, db)
	if err != nil {
		return err
	}
	type item struct {
		id       string
		runID    string
		document []byte
	}
	items, err := collectRows(ctx, db, `SELECT id, run_id, document_json FROM case_results`, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.id, &next.runID, &next.document)
		return next, err
	})
	if err != nil {
		return err
	}
	for _, next := range items {
		snapshotJSON, ok := snapshots[next.runID]
		if !ok {
			return fmt.Errorf("case result %s snapshot: missing current run revision", next.id)
		}
		var snapshot domain.RunSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return fmt.Errorf("case result %s snapshot: %w", next.id, err)
		}
		entryID, caseID := "", ""
		if len(snapshot.Entries) > 0 {
			entryID = snapshot.Entries[0].EntryID
			if len(snapshot.Entries[0].Cases) > 0 {
				caseID = snapshot.Entries[0].Cases[0].CaseID
			}
		}
		updated, err := convertResultDocument(next.document, entryID, caseID)
		if err != nil {
			return fmt.Errorf("case_results %s: %w", next.id, err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE case_results
			SET schema_version = json_extract(?, '$.schema_version'),
			    created_at = json_extract(?, '$.created_at'),
			    updated_at = json_extract(?, '$.updated_at'),
			    document_json = ?
			WHERE id = ?
		`, updated, updated, updated, updated, next.id); err != nil {
			return err
		}
	}
	return nil
}

func upgradeJSONDocuments(ctx context.Context, db *sql.DB, table string, stamp func([]byte) ([]byte, error)) error {
	type item struct {
		id       string
		document []byte
	}
	items, err := collectRows(ctx, db, `SELECT id, document_json FROM `+table, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.id, &next.document)
		return next, err
	})
	if err != nil {
		return err
	}
	for _, next := range items {
		updated, err := stamp(next.document)
		if err != nil {
			return fmt.Errorf("%s %s: %w", table, next.id, err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE `+table+`
			SET schema_version = json_extract(?, '$.schema_version'),
			    created_at = json_extract(?, '$.created_at'),
			    updated_at = json_extract(?, '$.updated_at'),
			    document_json = ?
			WHERE id = ?
		`, updated, updated, updated, updated, next.id); err != nil {
			return err
		}
	}
	return nil
}

func upgradeComparisonDocuments(ctx context.Context, db *sql.DB) error {
	type item struct {
		id       string
		revision int64
		document []byte
	}
	items, err := collectRows(ctx, db, `SELECT comparison_id, revision, document_json FROM comparison_revisions`, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.id, &next.revision, &next.document)
		return next, err
	})
	if err != nil {
		return err
	}
	for _, next := range items {
		updated, err := stampComparisonDocument(next.document)
		if err != nil {
			return fmt.Errorf("comparison %s revision %d: %w", next.id, next.revision, err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE comparison_revisions
			SET schema_version = json_extract(?, '$.schema_version'),
			    created_at = json_extract(?, '$.created_at'),
			    updated_at = json_extract(?, '$.updated_at'),
			    document_json = ?
			WHERE comparison_id = ? AND revision = ?
		`, updated, updated, updated, updated, next.id, next.revision); err != nil {
			return err
		}
	}
	return nil
}

func loadCurrentSnapshots(ctx context.Context, db *sql.DB) (map[string][]byte, error) {
	type item struct {
		runID        string
		snapshotJSON []byte
	}
	items, err := collectRows(ctx, db, `
		SELECT root.id, revision.snapshot_json
		FROM execution_runs AS root
		JOIN execution_run_revisions AS revision
		  ON revision.run_id = root.id AND revision.revision = root.current_revision
	`, func(rows *sql.Rows) (item, error) {
		var next item
		err := rows.Scan(&next.runID, &next.snapshotJSON)
		return next, err
	})
	if err != nil {
		return nil, err
	}
	snapshots := make(map[string][]byte, len(items))
	for _, next := range items {
		snapshots[next.runID] = next.snapshotJSON
	}
	return snapshots, nil
}

func collectRows[T any](ctx context.Context, db *sql.DB, query string, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	var items []T
	var scanErr error
	for rows.Next() {
		var next T
		next, scanErr = scan(rows)
		if scanErr != nil {
			break
		}
		items = append(items, next)
	}
	iterErr := rows.Err()
	closeErr := rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if iterErr != nil {
		return nil, iterErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return items, nil
}

func overlayPlanSnapshot(reportJSON, snapshotJSON []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(reportJSON)
	if err != nil {
		return nil, err
	}
	var report map[string]any
	if err := json.Unmarshal(rewritten, &report); err != nil {
		return nil, err
	}
	report["plan_snapshot"] = json.RawMessage(snapshotJSON)
	return json.Marshal(report)
}

func stampSnapshotDocument(raw []byte) ([]byte, error) {
	return convertRunSnapshotDocument(raw)
}

func stampRunDocument(raw []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(raw)
	if err != nil {
		return nil, err
	}
	var run domain.Run
	if err := json.Unmarshal(rewritten, &run); err != nil {
		return nil, err
	}
	return marshalCanonical(run)
}

func stampReportDocument(raw []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(raw)
	if err != nil {
		return nil, err
	}
	var report domain.Report
	if err := json.Unmarshal(rewritten, &report); err != nil {
		return nil, err
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return marshalCanonical(report)
}

func stampResultDocument(raw []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(raw)
	if err != nil {
		return nil, err
	}
	var result domain.Result
	if err := json.Unmarshal(rewritten, &result); err != nil {
		return nil, err
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return marshalCanonical(result)
}

func stampEvidenceDocument(raw []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(raw)
	if err != nil {
		return nil, err
	}
	var evidence domain.Evidence
	if err := json.Unmarshal(rewritten, &evidence); err != nil {
		return nil, err
	}
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	return marshalCanonical(evidence)
}

func stampComparisonDocument(raw []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(raw)
	if err != nil {
		return nil, err
	}
	var comparison domain.Comparison
	if err := json.Unmarshal(rewritten, &comparison); err != nil {
		return nil, err
	}
	if err := comparison.Validate(); err != nil {
		return nil, err
	}
	return marshalCanonical(comparison)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Close()
}
