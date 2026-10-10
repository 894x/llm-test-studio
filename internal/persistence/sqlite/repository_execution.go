package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository *Repository) CreateRun(ctx context.Context, run domain.Run) error {
	if err := run.Validate(); err != nil {
		return fmt.Errorf("validate run: %w", err)
	}
	if err := validateWritableRunSnapshot(run.Snapshot()); err != nil {
		return err
	}
	meta := run.Meta()
	if meta.Revision != 1 || run.Status() != domain.RunQueued {
		return errors.New("new run must be queued at revision 1")
	}
	document, err := marshalCanonical(run)
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}
	snapshotDocument, err := marshalCanonical(run.Snapshot())
	if err != nil {
		return fmt.Errorf("encode run snapshot: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run create: %w", err)
	}
	defer tx.Rollback()
	if err := validateRunReferences(ctx, tx, run); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_runs(id, current_revision, created_at, sealed)
		VALUES(?, ?, ?, 0)
	`, meta.ID, meta.Revision, formatTime(meta.CreatedAt)); err != nil {
		return classifyWriteError("create run root", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_run_revisions(
			run_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, status, snapshot_json, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, meta.ID, meta.SchemaVersion, meta.Revision, formatTime(meta.CreatedAt), formatTime(meta.UpdatedAt),
		run.PlanID(), run.Snapshot().Plan.Revision, run.Status(), snapshotDocument, document); err != nil {
		return classifyWriteError("create run revision", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit run", err)
	}
	return nil
}

func (repository *Repository) UpdateRun(ctx context.Context, expectedRevision uint64, run domain.Run) error {
	if err := run.Validate(); err != nil {
		return fmt.Errorf("validate run: %w", err)
	}
	if err := validateWritableRunSnapshot(run.Snapshot()); err != nil {
		return err
	}
	meta := run.Meta()
	if err := validateNextRevision(expectedRevision, meta); err != nil {
		return err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run update: %w", err)
	}
	defer tx.Rollback()
	var currentRevision int64
	var sealed int
	err = tx.QueryRowContext(ctx, `SELECT current_revision, sealed FROM execution_runs WHERE id = ?`, meta.ID).Scan(&currentRevision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: run", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("inspect run revision: %w", err)
	}
	if currentRevision < 1 || (sealed != 0 && sealed != 1) {
		return fmt.Errorf("%w: run revision pointer", ErrCorrupt)
	}
	if sealed != 0 || uint64(currentRevision) != expectedRevision {
		return fmt.Errorf("%w: run", ErrConflict)
	}
	current, err := queryRunRevision(ctx, tx, meta.ID, expectedRevision, false)
	if err != nil {
		return err
	}
	if err := validateStoredRunReferences(ctx, tx, current); err != nil {
		return err
	}
	var want domain.Run
	if failure := run.Failure(); run.Status() == domain.RunFailed && failure != nil {
		want, err = current.Fail(*failure, meta.UpdatedAt)
	} else {
		want, err = current.Transition(run.Status(), meta.UpdatedAt)
	}
	if err != nil {
		return fmt.Errorf("validate persisted run transition: %w", err)
	}
	if !equalCanonicalDocuments(want, run) {
		return errors.New("updated run must be exactly one valid state transition")
	}
	document, err := marshalCanonical(run)
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}
	snapshotDocument, err := marshalCanonical(run.Snapshot())
	if err != nil {
		return fmt.Errorf("encode run snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_run_revisions(
			run_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, status, snapshot_json, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, meta.ID, meta.SchemaVersion, meta.Revision, formatTime(meta.CreatedAt), formatTime(meta.UpdatedAt),
		run.PlanID(), run.Snapshot().Plan.Revision, run.Status(), snapshotDocument, document); err != nil {
		return classifyWriteError("append run revision", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE execution_runs SET current_revision = ?
		WHERE id = ? AND current_revision = ? AND sealed = 0
	`, meta.Revision, meta.ID, expectedRevision)
	if err != nil {
		return classifyWriteError("update run", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated run row count: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: run", ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit run update", err)
	}
	return nil
}

func (repository *Repository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	run, err := queryCurrentRun(ctx, repository.db, id)
	if err != nil {
		return domain.Run{}, err
	}
	if err := validateStoredRunReferences(ctx, repository.db, run); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func (repository *Repository) GetRunRevision(ctx context.Context, id string, revision uint64) (domain.Run, error) {
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.Run{}, fmt.Errorf("begin run revision read: %w", err)
	}
	defer tx.Rollback()
	run, err := queryRunRevision(ctx, tx, id, revision, false)
	if err != nil {
		return domain.Run{}, err
	}
	if err := validateStoredRunReferences(ctx, tx, run); err != nil {
		return domain.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Run{}, fmt.Errorf("commit run revision read: %w", err)
	}
	return run, nil
}

func (repository *Repository) ListRuns(ctx context.Context) ([]domain.Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin run list: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT root.id, root.current_revision, root.created_at, root.sealed,
		       (SELECT COUNT(*) FROM reports WHERE run_id = root.id),
		       (SELECT COUNT(*) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       (SELECT COALESCE(MIN(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       (SELECT COALESCE(MAX(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       revision.schema_version, revision.revision, revision.created_at, revision.updated_at,
		       revision.plan_id, revision.plan_revision, revision.status,
		       revision.snapshot_json, revision.document_json
		FROM execution_runs AS root
		JOIN execution_run_revisions AS revision
		  ON revision.run_id = root.id AND revision.revision = root.current_revision
		ORDER BY root.created_at, root.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	stored := make([]storedRunRow, 0)
	for rows.Next() {
		var row storedRunRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close runs: %w", err)
	}
	var rootCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_runs`).Scan(&rootCount); err != nil {
		return nil, fmt.Errorf("count run roots: %w", err)
	}
	if rootCount != len(stored) {
		return nil, fmt.Errorf("%w: run current revision pointer", ErrCorrupt)
	}
	runs := make([]domain.Run, 0, len(stored))
	for _, row := range stored {
		run, err := row.decode(true)
		if err != nil {
			return nil, err
		}
		if err := validateStoredRunReferences(ctx, tx, run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit run list: %w", err)
	}
	return runs, nil
}

type storedRunRow struct {
	id, rootCreated, revisionCreated, revisionUpdated, planID, status string
	rootCurrent, schemaVersion, revision, planRevision                int64
	historyCount, historyMin, historyMax                              int64
	sealed, reportCount                                               int
	snapshotDocument, document                                        []byte
}

type rowScanner interface {
	Scan(...any) error
}

func (row *storedRunRow) scan(scanner rowScanner) error {
	return scanner.Scan(
		&row.id, &row.rootCurrent, &row.rootCreated, &row.sealed, &row.reportCount,
		&row.historyCount, &row.historyMin, &row.historyMax,
		&row.schemaVersion, &row.revision, &row.revisionCreated, &row.revisionUpdated,
		&row.planID, &row.planRevision, &row.status, &row.snapshotDocument, &row.document,
	)
}

func (row storedRunRow) decode(requireCurrent bool) (domain.Run, error) {
	if row.revision < 1 || row.rootCurrent < 1 || (requireCurrent && row.rootCurrent != row.revision) || (row.sealed != 0 && row.sealed != 1) {
		return domain.Run{}, fmt.Errorf("%w: invalid run revision pointer", ErrCorrupt)
	}
	if row.historyCount != row.rootCurrent || row.historyMin != 1 || row.historyMax != row.rootCurrent {
		return domain.Run{}, fmt.Errorf("%w: run revision history is not contiguous", ErrCorrupt)
	}
	if err := verifyDocumentIdentity(row.document, row.id, uint64(row.revision)); err != nil {
		return domain.Run{}, fmt.Errorf("%w: run document identity", ErrCorrupt)
	}
	run, err := decodeRunDocument(row.document)
	if err != nil {
		return domain.Run{}, err
	}
	snapshot := run.Snapshot()
	snapshotDocument, err := marshalCanonical(snapshot)
	if err != nil {
		return domain.Run{}, fmt.Errorf("%w: run snapshot document", ErrCorrupt)
	}
	meta := run.Meta()
	if int64(meta.SchemaVersion) != row.schemaVersion || int64(meta.Revision) != row.revision ||
		formatTime(meta.CreatedAt) != row.revisionCreated || formatTime(meta.UpdatedAt) != row.revisionUpdated ||
		row.rootCreated != row.revisionCreated || run.PlanID() != row.planID ||
		int64(snapshot.Plan.Revision) != row.planRevision || string(run.Status()) != row.status ||
		!bytes.Equal(snapshotDocument, row.snapshotDocument) {
		return domain.Run{}, fmt.Errorf("%w: run columns do not match its document", ErrCorrupt)
	}
	if requireCurrent && row.sealed == 1 {
		switch run.Status() {
		case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
		default:
			return domain.Run{}, fmt.Errorf("%w: non-terminal run is sealed", ErrCorrupt)
		}
	}
	if (row.sealed == 0 && row.reportCount != 0) || (row.sealed == 1 && row.reportCount != 1) {
		return domain.Run{}, fmt.Errorf("%w: run report seal", ErrCorrupt)
	}
	return run, nil
}

func queryCurrentRun(ctx context.Context, queryer rowQueryer, id string) (domain.Run, error) {
	var row storedRunRow
	err := row.scan(queryer.QueryRowContext(ctx, `
		SELECT root.id, root.current_revision, root.created_at, root.sealed,
		       (SELECT COUNT(*) FROM reports WHERE run_id = root.id),
		       (SELECT COUNT(*) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       (SELECT COALESCE(MIN(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       (SELECT COALESCE(MAX(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       revision.schema_version, revision.revision, revision.created_at, revision.updated_at,
		       revision.plan_id, revision.plan_revision, revision.status,
		       revision.snapshot_json, revision.document_json
		FROM execution_runs AS root
		JOIN execution_run_revisions AS revision
		  ON revision.run_id = root.id AND revision.revision = root.current_revision
		WHERE root.id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		var rootCount int
		if countErr := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_runs WHERE id = ?`, id).Scan(&rootCount); countErr != nil {
			return domain.Run{}, fmt.Errorf("get run root: %w", countErr)
		}
		if rootCount != 0 {
			return domain.Run{}, fmt.Errorf("%w: run current revision pointer", ErrCorrupt)
		}
		return domain.Run{}, fmt.Errorf("%w: run", ErrNotFound)
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("get run: %w", err)
	}
	return row.decode(true)
}

func queryRunRevision(ctx context.Context, queryer rowQueryer, id string, revision uint64, requireCurrent bool) (domain.Run, error) {
	if revision == 0 || revision > uint64(^uint64(0)>>1) {
		return domain.Run{}, fmt.Errorf("%w: run revision", ErrNotFound)
	}
	var row storedRunRow
	err := row.scan(queryer.QueryRowContext(ctx, `
		SELECT root.id, root.current_revision, root.created_at, root.sealed,
		       (SELECT COUNT(*) FROM reports WHERE run_id = root.id),
		       (SELECT COUNT(*) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       (SELECT COALESCE(MIN(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       (SELECT COALESCE(MAX(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
		       revision.schema_version, revision.revision, revision.created_at, revision.updated_at,
		       revision.plan_id, revision.plan_revision, revision.status,
		       revision.snapshot_json, revision.document_json
		FROM execution_runs AS root
		JOIN execution_run_revisions AS revision ON revision.run_id = root.id
		WHERE root.id = ? AND revision.revision = ?
	`, id, revision))
	if errors.Is(err, sql.ErrNoRows) {
		var currentRevision, historyCount, historyMin, historyMax int64
		inspectErr := queryer.QueryRowContext(ctx, `
			SELECT root.current_revision,
			       (SELECT COUNT(*) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
			       (SELECT COALESCE(MIN(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
			       (SELECT COALESCE(MAX(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id)
			FROM execution_runs AS root WHERE root.id = ?
		`, id).Scan(&currentRevision, &historyCount, &historyMin, &historyMax)
		if errors.Is(inspectErr, sql.ErrNoRows) {
			return domain.Run{}, fmt.Errorf("%w: run revision", ErrNotFound)
		}
		if inspectErr != nil {
			return domain.Run{}, fmt.Errorf("inspect run revision history: %w", inspectErr)
		}
		if currentRevision < 1 || historyCount != currentRevision || historyMin != 1 || historyMax != currentRevision || int64(revision) <= currentRevision {
			return domain.Run{}, fmt.Errorf("%w: run revision history", ErrCorrupt)
		}
		return domain.Run{}, fmt.Errorf("%w: run revision", ErrNotFound)
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("get run revision: %w", err)
	}
	return row.decode(requireCurrent)
}

func decodeRunDocument(document []byte) (domain.Run, error) {
	var run domain.Run
	if err := decodeCanonical(document, &run, func() error { return run.Validate() }); err != nil {
		return domain.Run{}, fmt.Errorf("%w: run document", ErrCorrupt)
	}
	return run, nil
}

func validateRunReferences(ctx context.Context, _ relationQueryer, run domain.Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := run.Validate(); err != nil {
		return fmt.Errorf("validate run snapshot: %w", err)
	}
	return nil
}

func validateWritableRunSnapshot(snapshot domain.RunSnapshot) error {
	writeable := snapshot.SchemaVersion == domain.CurrentRunSnapshotSchemaVersion
	if !writeable {
		return fmt.Errorf(
			"%w: writable run snapshot schema version %d",
			ErrCorrupt,
			snapshot.SchemaVersion,
		)
	}
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("validate writable run snapshot: %w", err)
	}
	return nil
}

func validateStoredRunReferences(ctx context.Context, queryer relationQueryer, run domain.Run) error {
	if err := validateRunReferences(ctx, queryer, run); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("%w: run pinned references", ErrCorrupt)
	}
	return nil
}

func (repository *Repository) CreateEvidence(ctx context.Context, evidence domain.Evidence) error {
	if err := evidence.Validate(); err != nil {
		return fmt.Errorf("validate evidence: %w", err)
	}
	if evidence.Revision != 1 {
		return errors.New("new evidence revision must be 1")
	}
	document, err := marshalCanonical(evidence)
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	if len(document) > MaxReportProjectionItemBytes {
		return fmt.Errorf("evidence exceeds %d-byte storage budget", MaxReportProjectionItemBytes)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin evidence create: %w", err)
	}
	defer tx.Rollback()
	if err := requireWritableRun(ctx, tx, evidence.RunID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO evidence(id, schema_version, revision, created_at, updated_at, run_id, document_json)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, evidence.ID, evidence.SchemaVersion, evidence.Revision, formatTime(evidence.CreatedAt), formatTime(evidence.UpdatedAt), evidence.RunID, document); err != nil {
		return classifyWriteError("create evidence", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit evidence", err)
	}
	return nil
}

func (repository *Repository) GetEvidence(ctx context.Context, id string) (domain.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return domain.Evidence{}, err
	}
	var row storedEvidenceRow
	err := row.scan(repository.db.QueryRowContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
		FROM evidence WHERE id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Evidence{}, fmt.Errorf("%w: evidence", ErrNotFound)
	}
	if err != nil {
		return domain.Evidence{}, fmt.Errorf("get evidence: %w", err)
	}
	evidence, err := row.decode("")
	if err != nil {
		return domain.Evidence{}, err
	}
	if err := validateStoredEvidenceReferences(ctx, repository.db, evidence); err != nil {
		return domain.Evidence{}, err
	}
	return evidence, nil
}

func (repository *Repository) ListEvidence(ctx context.Context, runID string) ([]domain.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
		FROM evidence WHERE run_id = ? ORDER BY created_at, id
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	defer rows.Close()
	stored := make([]storedEvidenceRow, 0)
	for rows.Next() {
		var row storedEvidenceRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan evidence: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close evidence: %w", err)
	}
	var owner domain.Run
	if len(stored) != 0 {
		owner, err = queryCurrentRun(ctx, repository.db, runID)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, fmt.Errorf("%w: evidence owner", ErrCorrupt)
		}
		if err := validateStoredRunReferences(ctx, repository.db, owner); err != nil {
			return nil, err
		}
	}
	result := make([]domain.Evidence, 0, len(stored))
	for _, row := range stored {
		evidence, err := row.decode(runID)
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, nil
}

type storedEvidenceRow struct {
	id, createdAt, updatedAt, runID string
	schemaVersion, revision         int64
	document                        []byte
}

func (row *storedEvidenceRow) scan(scanner rowScanner) error {
	return scanner.Scan(&row.id, &row.schemaVersion, &row.revision, &row.createdAt, &row.updatedAt, &row.runID, &row.document)
}

func (row storedEvidenceRow) decode(expectedRunID string) (domain.Evidence, error) {
	if err := verifyEntityRow(row.document, row.id, row.schemaVersion, row.revision, row.createdAt, row.updatedAt); err != nil {
		return domain.Evidence{}, fmt.Errorf("%w: evidence row does not match document", ErrCorrupt)
	}
	evidence, err := decodeEvidenceDocument(row.document)
	if err != nil {
		return domain.Evidence{}, err
	}
	if evidence.RunID != row.runID || (expectedRunID != "" && evidence.RunID != expectedRunID) {
		return domain.Evidence{}, fmt.Errorf("%w: evidence owner does not match document", ErrCorrupt)
	}
	return evidence, nil
}

func validateStoredEvidenceReferences(ctx context.Context, queryer relationQueryer, evidence domain.Evidence) error {
	run, err := queryCurrentRun(ctx, queryer, evidence.RunID)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("%w: evidence owner", ErrCorrupt)
	}
	if err := validateStoredRunReferences(ctx, queryer, run); err != nil {
		return err
	}
	return nil
}

func decodeEvidenceDocument(document []byte) (domain.Evidence, error) {
	var evidence domain.Evidence
	if err := decodeCanonical(document, &evidence, func() error { return evidence.Validate() }); err != nil {
		return domain.Evidence{}, fmt.Errorf("%w: evidence document", ErrCorrupt)
	}
	return evidence, nil
}

// AppendResults writes a batch atomically. Callers resolve suite and case identities
// from the validated run snapshot before writing; appends do not reload run documents.
func (repository *Repository) AppendResults(ctx context.Context, results ...domain.Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(results) == 0 {
		return nil
	}
	documents := make([][]byte, len(results))
	for index, result := range results {
		if err := result.Validate(); err != nil {
			return fmt.Errorf("validate result: %w", err)
		}
		if result.EntryID == "" {
			return errors.New("stored result requires a suite entry id")
		}
		if result.EntryStatus == "" && result.CaseID == "" {
			return errors.New("stored request or summary result requires a case id")
		}
		if result.EntryStatus != "" && result.RequestID != "" {
			return errors.New("stored suite marker must not identify a request")
		}
		if result.Revision != 1 {
			return errors.New("new result revision must be 1")
		}
		document, err := marshalCanonical(result)
		if err != nil {
			return fmt.Errorf("encode result: %w", err)
		}
		if len(document) > MaxReportProjectionItemBytes {
			return fmt.Errorf("result exceeds %d-byte storage budget", MaxReportProjectionItemBytes)
		}
		documents[index] = document
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin result append: %w", err)
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO case_results(
			id, schema_version, revision, created_at, updated_at,
			run_id, entry_id, case_id, request_id, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare result append: %w", err)
	}
	defer statement.Close()
	writableRuns := make(map[string]bool)
	for index, result := range results {
		if !writableRuns[result.RunID] {
			if err := requireWritableRun(ctx, tx, result.RunID); err != nil {
				return err
			}
			writableRuns[result.RunID] = true
		}
		if err := validateResultEvidence(ctx, tx, result); err != nil {
			return err
		}
		if err := insertResult(
			ctx,
			statement,
			result,
			documents[index],
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit result", err)
	}
	return nil
}

func validateResultEvidence(ctx context.Context, queryer rowQueryer, result domain.Result) error {
	for _, evidenceID := range result.EvidenceIDs {
		var evidenceRow storedEvidenceRow
		err := evidenceRow.scan(queryer.QueryRowContext(ctx, `
			SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
			FROM evidence WHERE id = ?
		`, evidenceID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: evidence", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("resolve result evidence: %w", err)
		}
		evidence, err := evidenceRow.decode("")
		if err != nil {
			return fmt.Errorf("%w: result evidence", ErrCorrupt)
		}
		if evidence.RunID != result.RunID {
			return errors.New("result evidence belongs to another run")
		}
	}
	return nil
}

func insertResult(ctx context.Context, statement *sql.Stmt, result domain.Result, document []byte) error {
	var caseID any
	if result.CaseID != "" {
		caseID = result.CaseID
	}
	var requestID any
	if result.RequestID != "" {
		requestID = result.RequestID
	}
	if _, err := statement.ExecContext(
		ctx,
		result.ID,
		result.SchemaVersion,
		result.Revision,
		formatTime(result.CreatedAt),
		formatTime(result.UpdatedAt),
		result.RunID,
		result.EntryID,
		caseID,
		requestID,
		document,
	); err != nil {
		return classifyWriteError("append result", err)
	}
	return nil
}

func (repository *Repository) GetResult(ctx context.Context, id string) (domain.Result, error) {
	if err := ctx.Err(); err != nil {
		return domain.Result{}, err
	}
	var row storedResultRow
	err := row.scan(repository.db.QueryRowContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
		FROM case_results WHERE id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Result{}, fmt.Errorf("%w: result", ErrNotFound)
	}
	if err != nil {
		return domain.Result{}, fmt.Errorf("get result: %w", err)
	}
	result, err := row.decode("")
	if err != nil {
		return domain.Result{}, err
	}
	if err := validateStoredResultReferences(ctx, repository.db, result); err != nil {
		return domain.Result{}, err
	}
	return result, nil
}

func (repository *Repository) ListResults(ctx context.Context, runID string) ([]domain.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
		FROM case_results WHERE run_id = ? ORDER BY created_at, id
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list results: %w", err)
	}
	defer rows.Close()
	stored := make([]storedResultRow, 0)
	for rows.Next() {
		var row storedResultRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate results: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close results: %w", err)
	}
	var owner domain.Run
	if len(stored) != 0 {
		owner, err = queryCurrentRun(ctx, repository.db, runID)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, fmt.Errorf("%w: result owner", ErrCorrupt)
		}
		if err := validateStoredRunReferences(ctx, repository.db, owner); err != nil {
			return nil, err
		}
	}
	evidenceCache := make(map[string]domain.Evidence)
	snapshot := owner.Snapshot()
	result := make([]domain.Result, 0, len(stored))
	for _, row := range stored {
		item, err := row.decode(runID)
		if err != nil {
			return nil, err
		}
		if err := validateStoredResultAgainstSnapshot(
			ctx,
			repository.db,
			item,
			snapshot,
			evidenceCache,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

type storedResultRow struct {
	id, createdAt, updatedAt, runID string
	entryID, caseID, requestID      sql.NullString
	schemaVersion, revision         int64
	document                        []byte
}

func (row *storedResultRow) scan(scanner rowScanner) error {
	return scanner.Scan(
		&row.id, &row.schemaVersion, &row.revision, &row.createdAt, &row.updatedAt,
		&row.runID, &row.entryID, &row.caseID, &row.requestID, &row.document,
	)
}

func (row storedResultRow) decode(expectedRunID string) (domain.Result, error) {
	if err := verifyEntityRow(row.document, row.id, row.schemaVersion, row.revision, row.createdAt, row.updatedAt); err != nil {
		return domain.Result{}, fmt.Errorf("%w: result row does not match document", ErrCorrupt)
	}
	result, err := decodeResultDocument(row.document)
	if err != nil {
		return domain.Result{}, err
	}
	if result.RunID != row.runID || (expectedRunID != "" && result.RunID != expectedRunID) ||
		row.entryID.Valid != (result.EntryID != "") || (row.entryID.Valid && row.entryID.String != result.EntryID) ||
		row.caseID.Valid != (result.CaseID != "") || (row.caseID.Valid && row.caseID.String != result.CaseID) ||
		row.requestID.Valid != (result.RequestID != "") || (row.requestID.Valid && row.requestID.String != result.RequestID) {
		return domain.Result{}, fmt.Errorf("%w: result owner columns do not match document", ErrCorrupt)
	}
	return result, nil
}

func validateStoredResultReferences(ctx context.Context, queryer relationQueryer, result domain.Result) error {
	run, err := queryCurrentRun(ctx, queryer, result.RunID)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("%w: result owner", ErrCorrupt)
	}
	if err := validateStoredRunReferences(ctx, queryer, run); err != nil {
		return err
	}
	return validateStoredResultAgainstSnapshot(
		ctx,
		queryer,
		result,
		run.Snapshot(),
		make(map[string]domain.Evidence),
	)
}

func validateStoredResultAgainstSnapshot(
	ctx context.Context,
	queryer relationQueryer,
	result domain.Result,
	snapshot domain.RunSnapshot,
	evidenceCache map[string]domain.Evidence,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !resultBelongsToSnapshot(snapshot, result) {
		return fmt.Errorf("%w: result suite or case is outside run snapshot", ErrCorrupt)
	}
	for _, evidenceID := range result.EvidenceIDs {
		if evidence, exists := evidenceCache[evidenceID]; exists {
			if evidence.RunID != result.RunID {
				return fmt.Errorf("%w: result evidence reference", ErrCorrupt)
			}
			continue
		}
		var row storedEvidenceRow
		err := row.scan(queryer.QueryRowContext(ctx, `
			SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
			FROM evidence WHERE id = ?
		`, evidenceID))
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			return fmt.Errorf("%w: result evidence reference", ErrCorrupt)
		}
		evidence, err := row.decode(result.RunID)
		if err != nil {
			return fmt.Errorf("%w: result evidence reference", ErrCorrupt)
		}
		evidenceCache[evidenceID] = evidence
	}
	return nil
}

func resultBelongsToSnapshot(snapshot domain.RunSnapshot, result domain.Result) bool {
	if result.EntryID == "" {
		return false
	}
	for _, entry := range snapshot.Entries {
		if entry.EntryID != result.EntryID {
			continue
		}
		if result.EntryStatus != "" {
			return result.CaseID == "" && result.RequestID == ""
		}
		return result.CaseID != "" && containsCase(entry.Cases, result.CaseID)
	}
	return false
}

func decodeResultDocument(document []byte) (domain.Result, error) {
	var result domain.Result
	if err := decodeCanonical(document, &result, func() error { return result.Validate() }); err != nil {
		return domain.Result{}, fmt.Errorf("%w: result document", ErrCorrupt)
	}
	return result, nil
}

func (repository *Repository) CreateReport(ctx context.Context, report domain.Report) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	if err := validateWritableRunSnapshot(report.PlanSnapshot); err != nil {
		return err
	}
	if err := checkReportProjectionItemBudgets(report); err != nil {
		return fmt.Errorf("validate report byte budget: %w", err)
	}
	document, err := marshalCanonical(report)
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if len(document) > MaxReportProjectionDocumentBytes {
		return fmt.Errorf("report exceeds %d-byte storage budget", MaxReportProjectionDocumentBytes)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin report create: %w", err)
	}
	defer tx.Rollback()
	run, err := queryCurrentRun(ctx, tx, report.RunID)
	if err != nil {
		return err
	}
	if err := validateStoredRunReferences(ctx, tx, run); err != nil {
		return err
	}
	if run.Status() != report.RunStatus || !equalCanonicalDocuments(run.Snapshot(), report.PlanSnapshot) {
		return errors.New("report does not match the persisted terminal run")
	}
	if report.GeneratedAt.Before(run.Meta().UpdatedAt) {
		return errors.New("report generation timestamp precedes the terminal run update")
	}
	if err := matchReportResults(ctx, tx, report); err != nil {
		return err
	}
	if err := matchReportEvidence(ctx, tx, report); err != nil {
		return err
	}
	seal, err := tx.ExecContext(ctx, `
		UPDATE execution_runs SET sealed = 1
		WHERE id = ? AND current_revision = ? AND sealed = 0
	`, report.RunID, run.Meta().Revision)
	if err != nil {
		return classifyWriteError("seal run report output", err)
	}
	sealedRows, err := seal.RowsAffected()
	if err != nil {
		return fmt.Errorf("read run seal row count: %w", err)
	}
	if sealedRows != 1 {
		return fmt.Errorf("%w: run output is already sealed", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reports(id, schema_version, run_id, generated_at, document_json)
		VALUES(?, ?, ?, ?, ?)
	`, report.ID, report.SchemaVersion, report.RunID, formatTime(report.GeneratedAt), document); err != nil {
		return classifyWriteError("create report", err)
	}
	for position, attachment := range report.Attachments {
		attachmentDocument, err := marshalCanonical(attachment)
		if err != nil {
			return fmt.Errorf("encode report attachment: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO artifacts(id, run_id, name, relative_path, sha256, media_type, redacted, document_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		`, attachment.ArtifactID, attachment.RunID, attachment.Name, attachment.RelativePath, attachment.SHA256, attachment.MediaType, boolInt(attachment.Redacted), attachmentDocument); err != nil {
			return classifyWriteError("create report artifact", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO report_attachments(report_id, artifact_id, position) VALUES(?, ?, ?)`, report.ID, attachment.ArtifactID, position); err != nil {
			return classifyWriteError("link report attachment", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit report", err)
	}
	return nil
}

func (repository *Repository) GetReport(ctx context.Context, id string) (domain.Report, error) {
	if err := ctx.Err(); err != nil {
		return domain.Report{}, err
	}
	var row storedReportRow
	err := row.scan(repository.db.QueryRowContext(ctx, `
		SELECT id, schema_version, run_id, generated_at, document_json FROM reports WHERE id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Report{}, fmt.Errorf("%w: report", ErrNotFound)
	}
	if err != nil {
		return domain.Report{}, fmt.Errorf("get report: %w", err)
	}
	report, err := row.decode()
	if err != nil {
		return domain.Report{}, err
	}
	if err := validateReportStorage(ctx, repository.db, report); err != nil {
		return domain.Report{}, err
	}
	return report, nil
}

func (repository *Repository) ListReports(ctx context.Context) ([]domain.Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id, schema_version, run_id, generated_at, document_json
		FROM reports
		ORDER BY `+reportGeneratedAtSortKeySQL+`, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	stored := make([]storedReportRow, 0)
	for rows.Next() {
		var row storedReportRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close reports: %w", err)
	}
	result := make([]domain.Report, 0, len(stored))
	for _, row := range stored {
		report, err := row.decode()
		if err != nil {
			return nil, err
		}
		if err := validateReportStorage(ctx, repository.db, report); err != nil {
			return nil, err
		}
		result = append(result, report)
	}
	return result, nil
}

func (repository *Repository) ListReportsForRuns(ctx context.Context, runIDs []string) ([]domain.Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(runIDs) == 0 {
		return []domain.Report{}, nil
	}
	arguments := make([]any, len(runIDs))
	for index, runID := range runIDs {
		if !domain.IsUUID(runID) {
			return nil, errors.New("invalid report run id")
		}
		arguments[index] = runID
	}
	query := `SELECT id, schema_version, run_id, generated_at, document_json FROM reports WHERE run_id IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(runIDs)), ",") + `) ORDER BY ` + reportGeneratedAtSortKeySQL + `, id`
	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list reports for runs: %w", err)
	}
	defer rows.Close()
	stored := make([]storedReportRow, 0, len(runIDs))
	for rows.Next() {
		var row storedReportRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan report for run: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reports for runs: %w", err)
	}
	result := make([]domain.Report, 0, len(stored))
	for _, row := range stored {
		report, err := row.decode()
		if err != nil {
			return nil, err
		}
		if err := validateReportStorage(ctx, repository.db, report); err != nil {
			return nil, err
		}
		result = append(result, report)
	}
	return result, nil
}

type storedReportRow struct {
	id, runID, generatedAt string
	schemaVersion          int64
	document               []byte
}

func (row *storedReportRow) scan(scanner rowScanner) error {
	return scanner.Scan(&row.id, &row.schemaVersion, &row.runID, &row.generatedAt, &row.document)
}

func (row storedReportRow) decode() (domain.Report, error) {
	report, err := decodeReportDocument(row.document)
	if err != nil {
		return domain.Report{}, err
	}
	if report.ID != row.id || int64(report.SchemaVersion) != row.schemaVersion || report.RunID != row.runID || formatTime(report.GeneratedAt) != row.generatedAt {
		return domain.Report{}, fmt.Errorf("%w: report columns do not match document", ErrCorrupt)
	}
	return report, nil
}

func decodeReportDocument(document []byte) (domain.Report, error) {
	var report domain.Report
	if err := decodeCanonical(document, &report, func() error { return report.Validate() }); err != nil {
		return domain.Report{}, fmt.Errorf("%w: report document", ErrCorrupt)
	}
	return report, nil
}

func matchReportResults(ctx context.Context, tx *sql.Tx, report domain.Report) error {
	return validateReportResults(ctx, tx, report)
}

func matchReportEvidence(ctx context.Context, tx *sql.Tx, report domain.Report) error {
	return validateReportEvidence(ctx, tx, report)
}

func requireWritableRun(ctx context.Context, queryer rowQueryer, id string) error {
	// Run revisions are validated when created or read. Their scalar columns are
	// sufficient to guard output writes inside the same transaction.
	var sealed, reportExists int
	var revision, schemaVersion sql.NullInt64
	var status sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT root.sealed, revision.revision, revision.schema_version, revision.status,
		       EXISTS(SELECT 1 FROM reports WHERE run_id = root.id)
		FROM execution_runs AS root
		LEFT JOIN execution_run_revisions AS revision
		  ON revision.run_id = root.id AND revision.revision = root.current_revision
		WHERE root.id = ?
	`, id).Scan(&sealed, &revision, &schemaVersion, &status, &reportExists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: run", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("inspect run output state: %w", err)
	}
	if !revision.Valid {
		return fmt.Errorf("%w: run current revision pointer", ErrCorrupt)
	}
	if !schemaVersion.Valid || schemaVersion.Int64 != int64(domain.CurrentEntitySchemaVersion) {
		return fmt.Errorf("%w: run schema version", ErrCorrupt)
	}
	if sealed != 0 || reportExists != 0 {
		return fmt.Errorf("%w: run output is sealed", ErrConflict)
	}
	if status.String != string(domain.RunRunning) && status.String != string(domain.RunDraining) {
		return fmt.Errorf("%w: run is not accepting execution output", ErrConflict)
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsCase(values []domain.CaseRevisionRef, id string) bool {
	for _, value := range values {
		if value.CaseID == id {
			return true
		}
	}
	return false
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// equalCanonicalDocuments compares complete validated documents while ignoring
// JSON object key order inside RawMessage values.
func equalCanonicalDocuments(left, right any) bool {
	leftJSON, leftErr := marshalCanonical(left)
	rightJSON, rightErr := marshalCanonical(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
