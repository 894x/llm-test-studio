package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository *Repository) CreateComparison(ctx context.Context, comparison domain.Comparison) error {
	if err := comparison.Validate(); err != nil {
		return fmt.Errorf("validate comparison: %w", err)
	}
	meta := comparison.Meta()
	if meta.Revision != 1 || comparison.Status() != domain.ComparisonRunning {
		return errors.New("new comparison must be running at revision 1")
	}
	document, err := marshalCanonical(comparison)
	if err != nil {
		return fmt.Errorf("encode comparison: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin comparison create: %w", err)
	}
	defer tx.Rollback()
	if err := validateComparisonReferences(ctx, tx, comparison); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comparisons(id, current_revision, created_at, sealed)
		VALUES(?, ?, ?, 0)
	`, meta.ID, meta.Revision, formatTime(meta.CreatedAt)); err != nil {
		return classifyWriteError("create comparison root", err)
	}
	if err := insertComparisonRevision(ctx, tx, comparison, document); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit comparison", err)
	}
	return nil
}

func (repository *Repository) UpdateComparison(ctx context.Context, expectedRevision uint64, comparison domain.Comparison) error {
	if err := comparison.Validate(); err != nil {
		return fmt.Errorf("validate comparison: %w", err)
	}
	meta := comparison.Meta()
	if err := validateNextRevision(expectedRevision, meta); err != nil {
		return err
	}
	if comparison.Status() == domain.ComparisonRunning {
		return errors.New("updated comparison must be terminal")
	}
	document, err := marshalCanonical(comparison)
	if err != nil {
		return fmt.Errorf("encode comparison: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin comparison update: %w", err)
	}
	defer tx.Rollback()
	current, err := queryCurrentComparison(ctx, tx, meta.ID)
	if err != nil {
		return err
	}
	if current.Meta().Revision != expectedRevision || current.Status() != domain.ComparisonRunning {
		return fmt.Errorf("%w: comparison", ErrConflict)
	}
	want, err := current.Transition(comparison.Status(), meta.UpdatedAt)
	if err != nil {
		return fmt.Errorf("validate persisted comparison transition: %w", err)
	}
	if !reflect.DeepEqual(want, comparison) {
		return errors.New("updated comparison must be exactly one valid state transition")
	}
	if err := validateComparisonReferences(ctx, tx, comparison); err != nil {
		return err
	}
	if err := insertComparisonRevision(ctx, tx, comparison, document); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE comparisons SET current_revision = ?, sealed = 1
		WHERE id = ? AND current_revision = ? AND sealed = 0
	`, meta.Revision, meta.ID, expectedRevision)
	if err != nil {
		return classifyWriteError("update comparison", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated comparison row count: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: comparison", ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit comparison update", err)
	}
	return nil
}

func (repository *Repository) GetComparison(ctx context.Context, id string) (domain.Comparison, error) {
	if err := ctx.Err(); err != nil {
		return domain.Comparison{}, err
	}
	return queryCurrentComparison(ctx, repository.conn, id)
}

func (repository *Repository) ListComparisons(ctx context.Context) ([]domain.Comparison, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repository.conn.QueryContext(ctx, `SELECT id FROM comparisons ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list comparison ids: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read comparison id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate comparison ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close comparison ids: %w", err)
	}
	values := make([]domain.Comparison, 0, len(ids))
	for _, id := range ids {
		value, err := queryCurrentComparison(ctx, repository.conn, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func insertComparisonRevision(ctx context.Context, tx *sql.Tx, comparison domain.Comparison, document []byte) error {
	meta := comparison.Meta()
	plan, model := comparison.Plan(), comparison.Model()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comparison_revisions(
			comparison_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, model_id, model_revision, status, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, meta.ID, meta.SchemaVersion, meta.Revision, formatTime(meta.CreatedAt), formatTime(meta.UpdatedAt),
		plan.ID, plan.Revision, model.ID, model.Revision, comparison.Status(), document); err != nil {
		return classifyWriteError("append comparison revision", err)
	}
	for position, item := range comparison.Runs() {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO comparison_runs(
				comparison_id, comparison_revision, position,
				channel_id, channel_revision, run_id
			) VALUES(?, ?, ?, ?, ?, ?)
		`, meta.ID, meta.Revision, position, item.Channel.ID, item.Channel.Revision, item.RunID); err != nil {
			return classifyWriteError("append comparison run", err)
		}
	}
	return nil
}

func queryCurrentComparison(ctx context.Context, queryer relationQueryer, id string) (domain.Comparison, error) {
	var (
		rootID, rootCreated, revisionCreated, revisionUpdated string
		planID, modelID, status                               string
		currentRevision, sealed, schemaVersion                int64
		revision, planRevision, modelRevision                 int64
		historyCount, historyMin, historyMax                  int64
		document                                              []byte
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT root.id, root.current_revision, root.created_at, root.sealed,
		       (SELECT COUNT(*) FROM comparison_revisions history WHERE history.comparison_id = root.id),
		       (SELECT COALESCE(MIN(history.revision), 0) FROM comparison_revisions history WHERE history.comparison_id = root.id),
		       (SELECT COALESCE(MAX(history.revision), 0) FROM comparison_revisions history WHERE history.comparison_id = root.id),
		       revision.schema_version, revision.revision, revision.created_at, revision.updated_at,
		       revision.plan_id, revision.plan_revision, revision.model_id, revision.model_revision,
		       revision.status, revision.document_json
		FROM comparisons root
		JOIN comparison_revisions revision
		  ON revision.comparison_id = root.id AND revision.revision = root.current_revision
		WHERE root.id = ?
	`, id).Scan(
		&rootID, &currentRevision, &rootCreated, &sealed,
		&historyCount, &historyMin, &historyMax,
		&schemaVersion, &revision, &revisionCreated, &revisionUpdated,
		&planID, &planRevision, &modelID, &modelRevision, &status, &document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if countErr := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM comparisons WHERE id = ?`, id).Scan(&count); countErr != nil {
			return domain.Comparison{}, fmt.Errorf("inspect comparison root: %w", countErr)
		}
		if count != 0 {
			return domain.Comparison{}, fmt.Errorf("%w: comparison current revision pointer", ErrCorrupt)
		}
		return domain.Comparison{}, fmt.Errorf("%w: comparison", ErrNotFound)
	}
	if err != nil {
		return domain.Comparison{}, fmt.Errorf("get comparison: %w", err)
	}
	if rootID != id || currentRevision < 1 || currentRevision != revision ||
		historyCount != currentRevision || historyMin != 1 || historyMax != currentRevision ||
		(sealed != 0 && sealed != 1) || rootCreated != revisionCreated {
		return domain.Comparison{}, fmt.Errorf("%w: comparison revision history", ErrCorrupt)
	}
	if err := verifyEntityRow(document, rootID, schemaVersion, revision, revisionCreated, revisionUpdated); err != nil {
		return domain.Comparison{}, fmt.Errorf("%w: comparison metadata", ErrCorrupt)
	}
	var comparison domain.Comparison
	if err := decodeCanonical(document, &comparison, func() error { return comparison.Validate() }); err != nil {
		return domain.Comparison{}, fmt.Errorf("%w: comparison document", ErrCorrupt)
	}
	if comparison.Plan() != (domain.EntityRevisionRef{ID: planID, Revision: uint64(planRevision)}) ||
		comparison.Model() != (domain.EntityRevisionRef{ID: modelID, Revision: uint64(modelRevision)}) ||
		string(comparison.Status()) != status || (sealed == 0) != (comparison.Status() == domain.ComparisonRunning) {
		return domain.Comparison{}, fmt.Errorf("%w: comparison columns", ErrCorrupt)
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT position, channel_id, channel_revision, run_id
		FROM comparison_runs WHERE comparison_id = ? AND comparison_revision = ? ORDER BY position
	`, id, revision)
	if err != nil {
		return domain.Comparison{}, fmt.Errorf("read comparison run relations: %w", err)
	}
	runs := comparison.Runs()
	position := 0
	for rows.Next() {
		var storedPosition int
		var channelID, runID string
		var channelRevision int64
		if err := rows.Scan(&storedPosition, &channelID, &channelRevision, &runID); err != nil {
			rows.Close()
			return domain.Comparison{}, fmt.Errorf("scan comparison run relation: %w", err)
		}
		if position >= len(runs) || storedPosition != position || channelID != runs[position].Channel.ID ||
			uint64(channelRevision) != runs[position].Channel.Revision || runID != runs[position].RunID {
			rows.Close()
			return domain.Comparison{}, fmt.Errorf("%w: comparison run relations", ErrCorrupt)
		}
		position++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.Comparison{}, fmt.Errorf("iterate comparison run relations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return domain.Comparison{}, fmt.Errorf("close comparison run relations: %w", err)
	}
	if position != len(runs) {
		return domain.Comparison{}, fmt.Errorf("%w: comparison run relations", ErrCorrupt)
	}
	if err := validateComparisonReferences(ctx, queryer, comparison); err != nil {
		return domain.Comparison{}, fmt.Errorf("%w: comparison references: %v", ErrCorrupt, err)
	}
	return comparison, nil
}

func validateComparisonReferences(ctx context.Context, queryer relationQueryer, comparison domain.Comparison) error {
	planRef, modelRef := comparison.Plan(), comparison.Model()
	for _, item := range comparison.Runs() {
		run, err := queryCurrentRun(ctx, queryer, item.RunID)
		if err != nil {
			return err
		}
		snapshot := run.Snapshot()
		if snapshot.Plan != planRef || snapshot.Model.EntityRevisionRef != modelRef || snapshot.Channel.EntityRevisionRef != item.Channel {
			return errors.New("comparison run snapshot does not match its pinned target")
		}
		if snapshot.SchemaVersion != domain.CurrentRunSnapshotSchemaVersion || snapshot.PlanDocument == nil ||
			snapshot.PlanDocument.ID != planRef.ID || snapshot.PlanDocument.Revision != planRef.Revision ||
			!containsString(snapshot.PlanDocument.ModelIDs, modelRef.ID) ||
			!containsString(snapshot.PlanDocument.ChannelIDs, item.Channel.ID) {
			return errors.New("comparison run configuration does not match its pinned target")
		}
	}
	return nil
}
