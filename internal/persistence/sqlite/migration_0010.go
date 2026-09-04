package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	migration0010Name      = "0010_run_snapshot_v2_backfill"
	migration0010Algorithm = "legacy-run-snapshot-v1-to-complete-v2-and-report-sync-v1"
)

type migration0010RunRow struct {
	runID, createdAt, updatedAt, planID, status string
	schemaVersion, revision, planRevision       int64
	snapshotJSON, documentJSON                  []byte
}

type migration0010RunUpdate struct {
	runID                    string
	revision                 int64
	oldSnapshot, oldDocument []byte
	newSnapshot, newDocument []byte
}

type migration0010RunState struct {
	original domain.RunSnapshot
	upgraded domain.RunSnapshot
}

type migration0010ReportUpdate struct {
	id, runID             string
	oldDocument, document []byte
}

func migration0010Checksum() string {
	definition := strings.Join([]string{
		migration0010Name,
		migration0010Algorithm,
		"DROP TRIGGER trg_execution_run_revisions_no_update",
		migration0008Statements[2],
	}, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

func applyMigration0010(ctx context.Context, conn *sql.Conn, appVersion string) error {
	runUpdates, runStates, err := prepareMigration0010Runs(ctx, conn)
	if err != nil {
		return fmt.Errorf("apply sqlite migration %s: %w", migration0010Name, err)
	}
	reportUpdates, err := prepareMigration0010Reports(ctx, conn, runStates)
	if err != nil {
		return fmt.Errorf("apply sqlite migration %s: %w", migration0010Name, err)
	}

	if len(runUpdates) > 0 {
		if _, err := conn.ExecContext(ctx, `DROP TRIGGER trg_execution_run_revisions_no_update`); err != nil {
			return fmt.Errorf("apply sqlite migration %s: remove run update guard: %w", migration0010Name, err)
		}
		for _, update := range runUpdates {
			result, err := conn.ExecContext(ctx, `
				UPDATE execution_run_revisions
				SET snapshot_json = ?, document_json = ?
				WHERE run_id = ? AND revision = ? AND snapshot_json = ? AND document_json = ?
			`, update.newSnapshot, update.newDocument, update.runID, update.revision, update.oldSnapshot, update.oldDocument)
			if err != nil {
				return fmt.Errorf("apply sqlite migration %s: update legacy run %s revision %d: %w", migration0010Name, update.runID, update.revision, err)
			}
			rows, err := result.RowsAffected()
			if err != nil || rows != 1 {
				return fmt.Errorf("apply sqlite migration %s: legacy run %s revision %d changed during backfill", migration0010Name, update.runID, update.revision)
			}
		}
		if _, err := conn.ExecContext(ctx, migration0008Statements[2]); err != nil {
			return fmt.Errorf("apply sqlite migration %s: restore run update guard: %w", migration0010Name, err)
		}
	}
	for _, update := range reportUpdates {
		result, err := conn.ExecContext(ctx, `
			UPDATE reports SET document_json = ?
			WHERE id = ? AND run_id = ? AND document_json = ?
		`, update.document, update.id, update.runID, update.oldDocument)
		if err != nil {
			return fmt.Errorf("apply sqlite migration %s: update legacy report %s: %w", migration0010Name, update.id, err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return fmt.Errorf("apply sqlite migration %s: legacy report %s changed during backfill", migration0010Name, update.id)
		}
	}

	var remainingLegacyRuns, remainingLegacyRunDocuments, remainingLegacyReports int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_run_revisions WHERE json_extract(snapshot_json, '$.schema_version') != ?`, domain.CurrentRunSnapshotSchemaVersion).Scan(&remainingLegacyRuns); err != nil {
		return fmt.Errorf("apply sqlite migration %s: verify run snapshots: %w", migration0010Name, err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_run_revisions WHERE json_extract(document_json, '$.plan_snapshot.schema_version') != ?`, domain.CurrentRunSnapshotSchemaVersion).Scan(&remainingLegacyRunDocuments); err != nil {
		return fmt.Errorf("apply sqlite migration %s: verify run documents: %w", migration0010Name, err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports WHERE json_extract(document_json, '$.plan_snapshot.schema_version') != ?`, domain.CurrentRunSnapshotSchemaVersion).Scan(&remainingLegacyReports); err != nil {
		return fmt.Errorf("apply sqlite migration %s: verify report documents: %w", migration0010Name, err)
	}
	if remainingLegacyRuns != 0 || remainingLegacyRunDocuments != 0 || remainingLegacyReports != 0 {
		return fmt.Errorf("apply sqlite migration %s: incomplete snapshot backfill", migration0010Name)
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(10, ?, ?, ?, ?)
	`, migration0010Name, migration0010Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0010Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 10"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	return nil
}

func prepareMigration0010Runs(ctx context.Context, conn *sql.Conn) ([]migration0010RunUpdate, map[string]map[int64]migration0010RunState, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT run_id, schema_version, revision, created_at, updated_at,
		       plan_id, plan_revision, status, snapshot_json, document_json
		FROM execution_run_revisions ORDER BY run_id, revision
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("read execution run revisions: %w", err)
	}
	var stored []migration0010RunRow
	for rows.Next() {
		var row migration0010RunRow
		if err := rows.Scan(
			&row.runID, &row.schemaVersion, &row.revision, &row.createdAt, &row.updatedAt,
			&row.planID, &row.planRevision, &row.status, &row.snapshotJSON, &row.documentJSON,
		); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("read execution run revision: %w", err)
		}
		row.snapshotJSON = append([]byte(nil), row.snapshotJSON...)
		row.documentJSON = append([]byte(nil), row.documentJSON...)
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("iterate execution run revisions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, fmt.Errorf("close execution run revisions: %w", err)
	}

	updates := make([]migration0010RunUpdate, 0)
	states := make(map[string]map[int64]migration0010RunState)
	for _, row := range stored {
		snapshot, run, err := decodeMigration0010Run(row)
		if err != nil {
			return nil, nil, err
		}
		upgraded := snapshot
		if snapshot.SchemaVersion == 1 {
			upgraded, err = upgradeMigration0010Snapshot(ctx, conn, row, snapshot)
			if err != nil {
				return nil, nil, err
			}
			newSnapshot, err := marshalCanonical(upgraded)
			if err != nil {
				return nil, nil, migration0010RunFailure(row, "could not encode upgraded snapshot")
			}
			newDocument, err := replaceMigration0010PlanSnapshot(row.documentJSON, newSnapshot)
			if err != nil {
				return nil, nil, migration0010RunFailure(row, "could not encode upgraded run document")
			}
			var upgradedRun domain.Run
			if err := decodeCanonical(newDocument, &upgradedRun, func() error { return upgradedRun.Validate() }); err != nil ||
				!reflect.DeepEqual(upgradedRun.Snapshot(), upgraded) || upgradedRun.Meta() != run.Meta() ||
				upgradedRun.PlanID() != run.PlanID() || upgradedRun.Status() != run.Status() || !reflect.DeepEqual(upgradedRun.Failure(), run.Failure()) {
				return nil, nil, migration0010RunFailure(row, "upgraded run document failed integrity validation")
			}
			updates = append(updates, migration0010RunUpdate{
				runID: row.runID, revision: row.revision,
				oldSnapshot: row.snapshotJSON, oldDocument: row.documentJSON,
				newSnapshot: newSnapshot, newDocument: newDocument,
			})
		}
		if states[row.runID] == nil {
			states[row.runID] = make(map[int64]migration0010RunState)
		}
		states[row.runID][row.revision] = migration0010RunState{original: snapshot, upgraded: upgraded}
	}
	return updates, states, nil
}

func decodeMigration0010Run(row migration0010RunRow) (domain.RunSnapshot, domain.Run, error) {
	if row.revision < 1 || row.planRevision < 1 {
		return domain.RunSnapshot{}, domain.Run{}, migration0010RunFailure(row, "invalid revision columns")
	}
	var snapshot domain.RunSnapshot
	if err := decodeCanonical(row.snapshotJSON, &snapshot, func() error { return snapshot.Validate() }); err != nil {
		return domain.RunSnapshot{}, domain.Run{}, migration0010RunFailure(row, "snapshot document is invalid")
	}
	var run domain.Run
	if err := decodeCanonical(row.documentJSON, &run, func() error { return run.Validate() }); err != nil {
		return domain.RunSnapshot{}, domain.Run{}, migration0010RunFailure(row, "run document is invalid")
	}
	meta := run.Meta()
	if meta.ID != row.runID || int64(meta.SchemaVersion) != row.schemaVersion || int64(meta.Revision) != row.revision ||
		formatTime(meta.CreatedAt) != row.createdAt || formatTime(meta.UpdatedAt) != row.updatedAt ||
		run.PlanID() != row.planID || int64(snapshot.Plan.Revision) != row.planRevision || string(run.Status()) != row.status {
		return domain.RunSnapshot{}, domain.Run{}, migration0010RunFailure(row, "stored columns conflict with the run document")
	}
	if !reflect.DeepEqual(run.Snapshot(), snapshot) {
		return domain.RunSnapshot{}, domain.Run{}, migration0010RunFailure(row, "snapshot_json conflicts with the run document plan_snapshot")
	}
	return snapshot, run, nil
}

func upgradeMigration0010Snapshot(ctx context.Context, conn *sql.Conn, row migration0010RunRow, snapshot domain.RunSnapshot) (domain.RunSnapshot, error) {
	plan, err := loadMigration0010Plan(ctx, conn, row, snapshot.Plan)
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	model, err := loadMigration0010Model(ctx, conn, row, snapshot.Model.EntityRevisionRef)
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	if snapshot.Model.Name != model.Name || snapshot.Model.Protocol != model.Protocol || !reflect.DeepEqual(snapshot.Model.Capabilities, model.Capabilities) {
		return domain.RunSnapshot{}, migration0010RunFailure(row, "model snapshot conflicts with its exact model revision")
	}
	channel, err := loadMigration0010Channel(ctx, conn, row, snapshot.Channel.EntityRevisionRef)
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	if snapshot.Channel.Name != channel.Name || snapshot.Channel.BaseURL != channel.BaseURL || snapshot.Channel.Protocol != channel.Protocol {
		return domain.RunSnapshot{}, migration0010RunFailure(row, "channel snapshot conflicts with its exact channel revision")
	}
	if err := validateMigration0010PlanRelations(ctx, conn, row, plan, snapshot); err != nil {
		return domain.RunSnapshot{}, err
	}
	mapping, err := loadMigration0010Mapping(ctx, conn, row, plan, snapshot)
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	if snapshot.Channel.UpstreamModelName != mapping.UpstreamModelName {
		return domain.RunSnapshot{}, migration0010RunFailure(row, "channel snapshot conflicts with its exact mapping revision")
	}
	cases := make([]domain.TestCase, len(snapshot.Cases))
	for index, ref := range snapshot.Cases {
		cases[index], err = loadMigration0010Case(ctx, conn, row, ref)
		if err != nil {
			return domain.RunSnapshot{}, err
		}
	}

	snapshot.SchemaVersion = domain.CurrentRunSnapshotSchemaVersion
	snapshot.PlanDocument = &plan
	snapshot.Mapping = &mapping
	snapshot.CaseDefinitions = cases
	if err := snapshot.Validate(); err != nil {
		return domain.RunSnapshot{}, migration0010RunFailure(row, "exact catalog revisions do not form a valid complete v2 snapshot")
	}
	return snapshot, nil
}

func loadMigration0010Plan(ctx context.Context, conn *sql.Conn, row migration0010RunRow, ref domain.EntityRevisionRef) (domain.Plan, error) {
	var schemaVersion int64
	var createdAt, updatedAt string
	var document []byte
	err := conn.QueryRowContext(ctx, `SELECT schema_version, created_at, updated_at, document_json FROM test_plans WHERE id = ? AND revision = ?`, ref.ID, ref.Revision).Scan(&schemaVersion, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Plan{}, migration0010RunFailure(row, "exact plan revision is missing")
	}
	if err != nil {
		return domain.Plan{}, fmt.Errorf("read exact plan revision for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	var plan domain.Plan
	if err := decodeCanonical(document, &plan, func() error { return plan.Validate() }); err != nil ||
		verifyEntityRow(document, ref.ID, schemaVersion, int64(ref.Revision), createdAt, updatedAt) != nil {
		return domain.Plan{}, migration0010RunFailure(row, "exact plan document is invalid")
	}
	return plan, nil
}

func loadMigration0010Model(ctx context.Context, conn *sql.Conn, row migration0010RunRow, ref domain.EntityRevisionRef) (domain.Model, error) {
	var schemaVersion int64
	var createdAt, updatedAt string
	var document []byte
	err := conn.QueryRowContext(ctx, `SELECT schema_version, created_at, updated_at, document_json FROM models WHERE id = ? AND revision = ?`, ref.ID, ref.Revision).Scan(&schemaVersion, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Model{}, migration0010RunFailure(row, "exact model revision is missing")
	}
	if err != nil {
		return domain.Model{}, fmt.Errorf("read exact model revision for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	var model domain.Model
	if err := decodeCanonical(document, &model, func() error { return model.Validate() }); err != nil ||
		verifyEntityRow(document, ref.ID, schemaVersion, int64(ref.Revision), createdAt, updatedAt) != nil {
		return domain.Model{}, migration0010RunFailure(row, "exact model document is invalid")
	}
	return model, nil
}

func loadMigration0010Channel(ctx context.Context, conn *sql.Conn, row migration0010RunRow, ref domain.EntityRevisionRef) (domain.Channel, error) {
	var schemaVersion int64
	var createdAt, updatedAt string
	var document []byte
	err := conn.QueryRowContext(ctx, `SELECT schema_version, created_at, updated_at, document_json FROM channels WHERE id = ? AND revision = ?`, ref.ID, ref.Revision).Scan(&schemaVersion, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Channel{}, migration0010RunFailure(row, "exact channel revision is missing")
	}
	if err != nil {
		return domain.Channel{}, fmt.Errorf("read exact channel revision for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	var channel domain.Channel
	if err := decodeCanonical(document, &channel, func() error { return channel.Validate() }); err != nil ||
		verifyEntityRow(document, ref.ID, schemaVersion, int64(ref.Revision), createdAt, updatedAt) != nil {
		return domain.Channel{}, migration0010RunFailure(row, "exact channel document is invalid")
	}
	return channel, nil
}

func validateMigration0010PlanRelations(ctx context.Context, conn *sql.Conn, row migration0010RunRow, plan domain.Plan, snapshot domain.RunSnapshot) error {
	modelIDs, modelRevisions, err := loadMigration0010PlanEntityRefs(ctx, conn, "plan_models", "model_id", "model_revision", plan.ID, plan.Revision)
	if err != nil {
		return fmt.Errorf("read exact plan model revisions for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	channelIDs, channelRevisions, err := loadMigration0010PlanEntityRefs(ctx, conn, "plan_channels", "channel_id", "channel_revision", plan.ID, plan.Revision)
	if err != nil {
		return fmt.Errorf("read exact plan channel revisions for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	if len(plan.ModelIDs) == 0 {
		if len(modelIDs) != 0 || len(channelIDs) != 0 {
			return migration0010RunFailure(row, "targetless plan has unexpected model or channel relations")
		}
	} else {
		if !reflect.DeepEqual(modelIDs, plan.ModelIDs) || !migration0010ContainsExactRef(modelIDs, modelRevisions, snapshot.Model.EntityRevisionRef) {
			return migration0010RunFailure(row, "plan model relations conflict with the exact plan document or selected model")
		}
		if !reflect.DeepEqual(channelIDs, plan.ChannelIDs) || !migration0010ContainsExactRef(channelIDs, channelRevisions, snapshot.Channel.EntityRevisionRef) {
			return migration0010RunFailure(row, "plan channel relations conflict with the exact plan document or selected channel")
		}
	}
	caseIDs, caseRevisions, err := loadMigration0010PlanEntityRefs(ctx, conn, "plan_cases", "case_id", "case_revision", plan.ID, plan.Revision)
	if err != nil {
		return fmt.Errorf("read exact plan case revisions for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	wantCases := make([]domain.CaseRevisionRef, len(caseIDs))
	for index := range caseIDs {
		if caseRevisions[index] < 1 {
			return migration0010RunFailure(row, "plan case relation has an invalid revision")
		}
		wantCases[index] = domain.CaseRevisionRef{CaseID: caseIDs[index], Revision: uint64(caseRevisions[index])}
	}
	if !reflect.DeepEqual(wantCases, plan.Cases) {
		return migration0010RunFailure(row, "plan case relations conflict with the exact plan document")
	}
	return nil
}

func loadMigration0010PlanEntityRefs(ctx context.Context, conn *sql.Conn, table, idColumn, revisionColumn, planID string, planRevision uint64) ([]string, []int64, error) {
	query := fmt.Sprintf("SELECT position, %s, %s FROM %s WHERE plan_id = ? AND plan_revision = ? ORDER BY position", idColumn, revisionColumn, table)
	rows, err := conn.QueryContext(ctx, query, planID, planRevision)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []string
	var revisions []int64
	for rows.Next() {
		var position int64
		var id string
		var revision int64
		if err := rows.Scan(&position, &id, &revision); err != nil {
			return nil, nil, err
		}
		if position != int64(len(ids)) {
			return nil, nil, errors.New("plan relation positions are not contiguous")
		}
		ids = append(ids, id)
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return ids, revisions, nil
}

func migration0010ContainsExactRef(ids []string, revisions []int64, ref domain.EntityRevisionRef) bool {
	for index := range ids {
		if ids[index] == ref.ID && revisions[index] == int64(ref.Revision) {
			return true
		}
	}
	return false
}

func loadMigration0010Mapping(ctx context.Context, conn *sql.Conn, row migration0010RunRow, plan domain.Plan, snapshot domain.RunSnapshot) (domain.ChannelModel, error) {
	var mappingID string
	var mappingRevision int64
	targetless := len(plan.ModelIDs) == 0
	if targetless {
		var err error
		mappingID, mappingRevision, err = resolveMigration0010TargetlessMappingRef(ctx, conn, row, snapshot)
		if err != nil {
			return domain.ChannelModel{}, err
		}
		if mappingID == "" {
			return domain.ChannelModel{}, migration0010RunFailure(row, "exact targetless channel-model binding is missing")
		}
	} else {
		err := conn.QueryRowContext(ctx, `
			SELECT mapping_id, mapping_revision FROM plan_channel_models
			WHERE plan_id = ? AND plan_revision = ? AND channel_id = ? AND model_id = ?
		`, snapshot.Plan.ID, snapshot.Plan.Revision, snapshot.Channel.ID, snapshot.Model.ID).Scan(&mappingID, &mappingRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ChannelModel{}, migration0010RunFailure(row, "exact selected channel-model mapping relation is missing")
		}
		if err != nil {
			return domain.ChannelModel{}, fmt.Errorf("read exact mapping relation for legacy run %s revision %d: %w", row.runID, row.revision, err)
		}
	}
	var schemaVersion, channelRevision, modelRevision int64
	var createdAt, updatedAt, channelID, modelID, bindingKey string
	var document []byte
	err := conn.QueryRowContext(ctx, `
		SELECT schema_version, created_at, updated_at, channel_id, channel_revision,
		       model_id, model_revision, binding_key, document_json
		FROM channel_models WHERE id = ? AND revision = ?
	`, mappingID, mappingRevision).Scan(&schemaVersion, &createdAt, &updatedAt, &channelID, &channelRevision, &modelID, &modelRevision, &bindingKey, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelModel{}, migration0010RunFailure(row, "exact selected channel-model mapping revision is missing")
	}
	if err != nil {
		return domain.ChannelModel{}, fmt.Errorf("read exact mapping revision for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	var mapping domain.ChannelModel
	invalid := decodeCanonical(document, &mapping, func() error { return mapping.Validate() }) != nil || mappingRevision < 1 ||
		verifyEntityRow(document, mappingID, schemaVersion, mappingRevision, createdAt, updatedAt) != nil ||
		mapping.ChannelID != channelID || mapping.ModelID != modelID || bindingKey != channelID+"|"+modelID ||
		channelID != snapshot.Channel.ID || modelID != snapshot.Model.ID
	if !targetless {
		invalid = invalid || channelRevision != int64(snapshot.Channel.Revision) || modelRevision != int64(snapshot.Model.Revision)
	}
	if invalid {
		return domain.ChannelModel{}, migration0010RunFailure(row, "exact selected channel-model mapping document is invalid or conflicting")
	}
	return mapping, nil
}

type migration0010MappingCandidate struct {
	mapping   domain.ChannelModel
	createdAt time.Time
	updatedAt time.Time
}

func resolveMigration0010TargetlessMappingRef(
	ctx context.Context,
	conn *sql.Conn,
	row migration0010RunRow,
	snapshot domain.RunSnapshot,
) (string, int64, error) {
	runCreatedAt, err := time.Parse(time.RFC3339Nano, row.createdAt)
	if err != nil {
		return "", 0, migration0010RunFailure(row, "run creation time is invalid")
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at,
		       channel_id, channel_revision, model_id, model_revision, binding_key, document_json,
		       (SELECT deleted_at FROM catalog_tombstones
		        WHERE entity_table = 'channel_models' AND entity_id = channel_models.id)
		FROM channel_models
		WHERE binding_key = ? AND channel_id = ? AND model_id = ?
		ORDER BY id, revision
	`, snapshot.Channel.ID+"|"+snapshot.Model.ID,
		snapshot.Channel.ID, snapshot.Model.ID)
	if err != nil {
		return "", 0, fmt.Errorf("read targetless mapping history for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	defer rows.Close()

	latestByID := make(map[string]migration0010MappingCandidate)
	for rows.Next() {
		var id, createdAt, updatedAt, channelID, modelID, bindingKey string
		var schemaVersion, revision, channelRevision, modelRevision int64
		var document []byte
		var deletedAt sql.NullString
		if err := rows.Scan(
			&id, &schemaVersion, &revision, &createdAt, &updatedAt,
			&channelID, &channelRevision, &modelID, &modelRevision, &bindingKey, &document, &deletedAt,
		); err != nil {
			return "", 0, fmt.Errorf("read targetless mapping history for legacy run %s revision %d: %w", row.runID, row.revision, err)
		}
		candidateCreatedAt, createdErr := time.Parse(time.RFC3339Nano, createdAt)
		candidateUpdatedAt, updatedErr := time.Parse(time.RFC3339Nano, updatedAt)
		if createdErr != nil || updatedErr != nil {
			return "", 0, migration0010RunFailure(row, "targetless mapping history time is invalid")
		}
		if candidateCreatedAt.After(runCreatedAt) || candidateUpdatedAt.After(runCreatedAt) {
			continue
		}
		if deletedAt.Valid {
			deletedAtTime, err := time.Parse(time.RFC3339Nano, deletedAt.String)
			if err != nil {
				return "", 0, migration0010RunFailure(row, "targetless mapping tombstone time is invalid")
			}
			if !deletedAtTime.After(runCreatedAt) {
				continue
			}
		}
		var mapping domain.ChannelModel
		if decodeCanonical(document, &mapping, func() error { return mapping.Validate() }) != nil ||
			verifyEntityRow(document, id, schemaVersion, revision, createdAt, updatedAt) != nil ||
			mapping.ChannelID != channelID || mapping.ModelID != modelID ||
			channelID != snapshot.Channel.ID || channelRevision < 1 ||
			modelID != snapshot.Model.ID || modelRevision < 1 ||
			bindingKey != channelID+"|"+modelID {
			return "", 0, migration0010RunFailure(row, "targetless mapping history is invalid")
		}
		current, exists := latestByID[id]
		if !exists || candidateUpdatedAt.After(current.updatedAt) ||
			candidateUpdatedAt.Equal(current.updatedAt) && mapping.Revision > current.mapping.Revision {
			latestByID[id] = migration0010MappingCandidate{mapping: mapping, createdAt: candidateCreatedAt, updatedAt: candidateUpdatedAt}
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("iterate targetless mapping history for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}

	var selected migration0010MappingCandidate
	for _, candidate := range latestByID {
		if selected.mapping.ID == "" || candidate.createdAt.Before(selected.createdAt) ||
			candidate.createdAt.Equal(selected.createdAt) && candidate.mapping.ID < selected.mapping.ID {
			selected = candidate
		}
	}
	if selected.mapping.ID != "" && selected.mapping.UpstreamModelName != snapshot.Channel.UpstreamModelName {
		return "", 0, migration0010RunFailure(row, "targetless mapping selected at run creation conflicts with the channel snapshot")
	}
	return selected.mapping.ID, int64(selected.mapping.Revision), nil
}

func loadMigration0010Case(ctx context.Context, conn *sql.Conn, row migration0010RunRow, ref domain.CaseRevisionRef) (domain.TestCase, error) {
	var schemaVersion int64
	var createdAt, updatedAt string
	var document []byte
	err := conn.QueryRowContext(ctx, `SELECT schema_version, created_at, updated_at, document_json FROM test_cases WHERE id = ? AND revision = ?`, ref.CaseID, ref.Revision).Scan(&schemaVersion, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TestCase{}, migration0010RunFailure(row, "exact test case revision is missing")
	}
	if err != nil {
		return domain.TestCase{}, fmt.Errorf("read exact test case revision for legacy run %s revision %d: %w", row.runID, row.revision, err)
	}
	var testCase domain.TestCase
	if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil ||
		verifyEntityRow(document, ref.CaseID, schemaVersion, int64(ref.Revision), createdAt, updatedAt) != nil {
		return domain.TestCase{}, migration0010RunFailure(row, "exact test case document is invalid")
	}
	return testCase, nil
}

func prepareMigration0010Reports(ctx context.Context, conn *sql.Conn, states map[string]map[int64]migration0010RunState) ([]migration0010ReportUpdate, error) {
	current := make(map[string]migration0010RunState)
	rootRows, err := conn.QueryContext(ctx, `SELECT id, current_revision FROM execution_runs ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read execution run roots: %w", err)
	}
	for rootRows.Next() {
		var runID string
		var revision int64
		if err := rootRows.Scan(&runID, &revision); err != nil {
			rootRows.Close()
			return nil, fmt.Errorf("read execution run root: %w", err)
		}
		state, exists := states[runID][revision]
		if !exists {
			rootRows.Close()
			return nil, fmt.Errorf("execution run %s current revision %d is missing", runID, revision)
		}
		current[runID] = state
	}
	if err := rootRows.Err(); err != nil {
		rootRows.Close()
		return nil, fmt.Errorf("iterate execution run roots: %w", err)
	}
	if err := rootRows.Close(); err != nil {
		return nil, fmt.Errorf("close execution run roots: %w", err)
	}

	rows, err := conn.QueryContext(ctx, `SELECT id, schema_version, run_id, generated_at, document_json FROM reports ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read reports: %w", err)
	}
	type reportRow struct {
		id, runID, generatedAt string
		schemaVersion          int64
		document               []byte
	}
	var stored []reportRow
	for rows.Next() {
		var row reportRow
		if err := rows.Scan(&row.id, &row.schemaVersion, &row.runID, &row.generatedAt, &row.document); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read report: %w", err)
		}
		row.document = append([]byte(nil), row.document...)
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate reports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close reports: %w", err)
	}

	updates := make([]migration0010ReportUpdate, 0)
	for _, row := range stored {
		state, exists := current[row.runID]
		if !exists {
			return nil, fmt.Errorf("legacy report %s references a missing current run", row.id)
		}
		var report domain.Report
		if err := decodeCanonical(row.document, &report, func() error { return report.Validate() }); err != nil {
			return nil, fmt.Errorf("legacy report %s document is invalid", row.id)
		}
		if report.ID != row.id || report.RunID != row.runID || int64(report.SchemaVersion) != row.schemaVersion || formatTime(report.GeneratedAt) != row.generatedAt {
			return nil, fmt.Errorf("legacy report %s stored columns conflict with its document", row.id)
		}
		if !reflect.DeepEqual(report.PlanSnapshot, state.original) {
			return nil, fmt.Errorf("legacy report %s plan_snapshot conflicts with its current run", row.id)
		}
		if report.PlanSnapshot.SchemaVersion == domain.CurrentRunSnapshotSchemaVersion {
			continue
		}
		if report.PlanSnapshot.SchemaVersion != 1 {
			return nil, fmt.Errorf("legacy report %s has an unsupported plan_snapshot version", row.id)
		}
		newSnapshot, err := marshalCanonical(state.upgraded)
		if err != nil {
			return nil, fmt.Errorf("legacy report %s upgraded plan_snapshot could not be encoded", row.id)
		}
		document, err := replaceMigration0010PlanSnapshot(row.document, newSnapshot)
		if err != nil {
			return nil, fmt.Errorf("legacy report %s upgraded document could not be encoded", row.id)
		}
		var upgraded domain.Report
		if err := decodeCanonical(document, &upgraded, func() error { return upgraded.Validate() }); err != nil || !reflect.DeepEqual(upgraded.PlanSnapshot, state.upgraded) {
			return nil, fmt.Errorf("legacy report %s upgraded document failed integrity validation", row.id)
		}
		updates = append(updates, migration0010ReportUpdate{id: row.id, runID: row.runID, oldDocument: row.document, document: document})
	}
	return updates, nil
}

func replaceMigration0010PlanSnapshot(document, snapshot []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if len(object) == 0 {
		return nil, errors.New("document is not an object")
	}
	if _, exists := object["plan_snapshot"]; !exists {
		return nil, errors.New("document has no plan_snapshot")
	}
	object["plan_snapshot"] = append(json.RawMessage(nil), snapshot...)
	return marshalCanonical(object)
}

func migration0010RunFailure(row migration0010RunRow, reason string) error {
	return fmt.Errorf("legacy run %s revision %d: %s", row.runID, row.revision, reason)
}

func validateAppliedSchema0010(ctx context.Context, conn *sql.Conn) error {
	if err := validateAppliedSchema0009(ctx, conn); err != nil {
		return err
	}
	checks := []struct {
		name  string
		query string
	}{
		{
			name: "run snapshot",
			query: `SELECT COUNT(*) FROM execution_run_revisions
				WHERE json_type(snapshot_json, '$.schema_version') != 'integer'
				   OR json_extract(snapshot_json, '$.schema_version') IS NOT 2`,
		},
		{
			name: "run document",
			query: `SELECT COUNT(*) FROM execution_run_revisions
				WHERE json_type(document_json, '$.plan_snapshot.schema_version') != 'integer'
				   OR json_extract(document_json, '$.plan_snapshot.schema_version') IS NOT 2`,
		},
		{
			name: "report snapshot",
			query: `SELECT COUNT(*) FROM reports
				WHERE json_type(document_json, '$.plan_snapshot.schema_version') != 'integer'
				   OR json_extract(document_json, '$.plan_snapshot.schema_version') IS NOT 2`,
		},
	}
	for _, check := range checks {
		var invalid int
		if err := conn.QueryRowContext(ctx, check.query).Scan(&invalid); err != nil {
			return fmt.Errorf("validate sqlite migration %s %s completion: %w", migration0010Name, check.name, err)
		}
		if invalid != 0 {
			return fmt.Errorf("sqlite migration %s %s completion invariant failed", migration0010Name, check.name)
		}
	}
	runUpdates, runStates, err := prepareMigration0010Runs(ctx, conn)
	if err != nil {
		return fmt.Errorf("validate sqlite migration %s run documents: %w", migration0010Name, err)
	}
	if len(runUpdates) != 0 {
		return fmt.Errorf("sqlite migration %s run completion invariant failed", migration0010Name)
	}
	reportUpdates, err := prepareMigration0010Reports(ctx, conn, runStates)
	if err != nil {
		return fmt.Errorf("validate sqlite migration %s report documents: %w", migration0010Name, err)
	}
	if len(reportUpdates) != 0 {
		return fmt.Errorf("sqlite migration %s report completion invariant failed", migration0010Name)
	}
	return nil
}
