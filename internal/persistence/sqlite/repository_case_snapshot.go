package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/894x/llm-studio/internal/domain"
)

func (repository *Repository) CaseCatalogCutoverCompleted(ctx context.Context) (bool, error) {
	var completed int
	if err := repository.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM case_catalog_cutover WHERE id = 1)`).Scan(&completed); err != nil {
		return false, fmt.Errorf("inspect case catalog cutover: %w", err)
	}
	return completed != 0, nil
}

func (repository *Repository) CompleteCaseCatalogCutover(ctx context.Context, completedAt time.Time) error {
	if completedAt.IsZero() {
		return errors.New("case catalog cutover timestamp is required")
	}
	_, err := repository.conn.ExecContext(ctx, `INSERT OR IGNORE INTO case_catalog_cutover(id, completed_at) VALUES(1, ?)`, formatTime(completedAt.UTC()))
	if err != nil {
		return classifyWriteError("complete case catalog cutover", err)
	}
	return nil
}

func (repository *Repository) PruneUnreferencedTestCaseSnapshots(ctx context.Context) error {
	if _, err := repository.conn.ExecContext(ctx, `DELETE FROM pending_test_case_snapshots`); err != nil {
		return classifyWriteError("prune pending filesystem case snapshots", err)
	}
	// Files are authoritative after cutover, so legacy import metadata must not keep snapshots alive.
	if _, err := repository.conn.ExecContext(ctx, `DELETE FROM test_case_import_sources`); err != nil {
		return classifyWriteError("prune legacy case import sources", err)
	}
	_, err := repository.conn.ExecContext(ctx, `
		DELETE FROM test_cases
		WHERE NOT EXISTS (
			SELECT 1 FROM suite_cases WHERE case_id = test_cases.id AND case_revision = test_cases.revision
		) AND NOT EXISTS (
			SELECT 1 FROM plan_cases WHERE case_id = test_cases.id AND case_revision = test_cases.revision
		)
	`)
	if err != nil {
		return classifyWriteError("prune unreferenced filesystem case snapshots", err)
	}
	return nil
}

// EnsureTestCaseSnapshot materializes one immutable filesystem case revision
// only when a suite or plan needs a durable historical reference. Files remain
// the catalog source of truth; this row is an execution snapshot, not an editor.
func (repository *Repository) EnsureTestCaseSnapshot(ctx context.Context, testCase domain.TestCase) (bool, error) {
	if err := testCase.Validate(); err != nil {
		return false, fmt.Errorf("validate filesystem case snapshot: %w", err)
	}
	document, err := marshalCanonical(testCase)
	if err != nil {
		return false, fmt.Errorf("encode filesystem case snapshot: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin filesystem case snapshot: %w", err)
	}
	defer tx.Rollback()
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT document_json FROM test_cases WHERE id = ? AND revision = ?`, testCase.ID, testCase.Revision).Scan(&existing)
	if err == nil {
		if !bytes.Equal(existing, document) {
			return false, fmt.Errorf("%w: filesystem case snapshot", ErrConflict)
		}
		return false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("inspect filesystem case snapshot: %w", err)
	}
	var pending []byte
	err = tx.QueryRowContext(ctx, `SELECT document_json FROM pending_test_case_snapshots WHERE id = ? AND revision = ?`, testCase.ID, testCase.Revision).Scan(&pending)
	if err == nil {
		if !bytes.Equal(pending, document) {
			return false, fmt.Errorf("%w: pending filesystem case snapshot", ErrConflict)
		}
		return true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("inspect pending filesystem case snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO pending_test_case_snapshots(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, testCase.ID, testCase.SchemaVersion, testCase.Revision, formatTime(testCase.CreatedAt), formatTime(testCase.UpdatedAt), document); err != nil {
		return false, classifyWriteError("stage filesystem case snapshot", err)
	}
	if err := tx.Commit(); err != nil {
		return false, classifyWriteError("commit filesystem case snapshot", err)
	}
	return true, nil
}

func (repository *Repository) DeleteUnreferencedTestCaseSnapshot(ctx context.Context, id string, revision uint64) error {
	if !domain.IsUUID(id) || revision == 0 {
		return errors.New("invalid filesystem case snapshot reference")
	}
	if _, err := repository.conn.ExecContext(ctx, `DELETE FROM pending_test_case_snapshots WHERE id = ? AND revision = ?`, id, revision); err != nil {
		return classifyWriteError("delete pending filesystem case snapshot", err)
	}
	result, err := repository.conn.ExecContext(ctx, `
		DELETE FROM test_cases
		WHERE id = ? AND revision = ?
		  AND NOT EXISTS (SELECT 1 FROM suite_cases WHERE case_id = ? AND case_revision = ?)
		  AND NOT EXISTS (SELECT 1 FROM plan_cases WHERE case_id = ? AND case_revision = ?)
	`, id, revision, id, revision, id, revision)
	if err != nil {
		return classifyWriteError("delete unreferenced filesystem case snapshot", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted filesystem case snapshot count: %w", err)
	}
	if rows > 1 {
		return fmt.Errorf("%w: filesystem case snapshot delete", ErrCorrupt)
	}
	return nil
}

func promotePendingCaseSnapshot(ctx context.Context, tx *sql.Tx, ref domain.CaseRevisionRef) error {
	var schemaVersion int
	var createdAt, updatedAt string
	var document []byte
	err := tx.QueryRowContext(ctx, `
		SELECT schema_version, created_at, updated_at, document_json
		FROM pending_test_case_snapshots WHERE id = ? AND revision = ?
	`, ref.CaseID, ref.Revision).Scan(&schemaVersion, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return requireExactVersion(ctx, tx, "test_cases", ref.CaseID, ref.Revision, "test case")
	}
	if err != nil {
		return fmt.Errorf("read pending filesystem case snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_tombstones WHERE entity_table = 'test_cases' AND entity_id = ?`, ref.CaseID); err != nil {
		return fmt.Errorf("reactivate filesystem case snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_cases(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, ref.CaseID, schemaVersion, ref.Revision, createdAt, updatedAt, document); err != nil {
		return classifyWriteError("promote filesystem case snapshot", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pending_test_case_snapshots WHERE id = ? AND revision = ?`, ref.CaseID, ref.Revision); err != nil {
		return classifyWriteError("clear pending filesystem case snapshot", err)
	}
	return nil
}

func (repository *Repository) IsTestCaseSnapshotReferenced(ctx context.Context, id string, revision uint64) (bool, error) {
	if !domain.IsUUID(id) || revision == 0 {
		return false, errors.New("invalid filesystem case snapshot reference")
	}
	var referenced int
	if err := repository.conn.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM suite_cases WHERE case_id = ? AND case_revision = ?
			UNION ALL
			SELECT 1 FROM plan_cases WHERE case_id = ? AND case_revision = ?
		)
	`, id, revision, id, revision).Scan(&referenced); err != nil {
		return false, fmt.Errorf("inspect filesystem case snapshot references: %w", err)
	}
	return referenced != 0, nil
}
