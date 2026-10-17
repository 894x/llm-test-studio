package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ remoteresults.Repository = (*Repository)(nil)

// ApplyRemoteBatch imports only already committed facts. It never creates an
// execution_jobs owner, submits traffic, or infers failure from disconnection.
func (repository *Repository) ApplyRemoteBatch(
	ctx context.Context,
	batch remoteresults.Batch,
) (remoteresults.Mirror, error) {
	if err := batch.Validate(); err != nil {
		return remoteresults.Mirror{}, err
	}
	// Freeze caller-owned payloads and bound the page before starting a write.
	documents := make([][]byte, len(batch.Events))
	pageBytes := 0
	for index, event := range batch.Events {
		if err := remoteresults.CheckEventBudget(event.Payload); err != nil {
			return remoteresults.Mirror{}, err
		}
		document, err := marshalCanonical(event)
		if err != nil {
			return remoteresults.Mirror{}, remoteresults.ErrInvalid
		}
		pageBytes += len(document)
		if pageBytes > jobcontract.MaxEventPageBytes {
			return remoteresults.Mirror{}, remoteresults.ErrInvalid
		}
		documents[index] = document
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return remoteresults.Mirror{}, fmt.Errorf("begin remote import: %w", err)
	}
	defer tx.Rollback()
	if err := rejectArchivedJob(ctx, tx, batch.JobID); err != nil {
		return remoteresults.Mirror{}, err
	}
	mirror, err := queryRemoteMirror(ctx, tx, batch.JobID)
	if errors.Is(err, remoteresults.ErrNotFound) {
		if batch.AfterSequence != 0 || len(documents) == 0 {
			return remoteresults.Mirror{}, remoteresults.ErrInvalid
		}
		first, err := decodeStoredJobEvent(documents[0])
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		mirror, err = createRemoteMirrorTx(
			ctx,
			tx,
			batch.Origin,
			first,
		)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
	} else if err != nil {
		return remoteresults.Mirror{}, err
	}
	if mirror.Origin != batch.Origin || batch.AfterSequence > mirror.Snapshot.LatestSequence {
		return remoteresults.Mirror{}, remoteresults.ErrConflict
	}
	state, err := newRemoteBatchState(mirror.Spec)
	if err != nil {
		return remoteresults.Mirror{}, err
	}
	for _, document := range documents {
		event, err := decodeStoredJobEvent(document)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		if event.Sequence <= mirror.Snapshot.LatestSequence {
			var previous []byte
			err := tx.QueryRowContext(
				ctx,
				`SELECT document_json FROM remote_job_events
				WHERE job_id = ? AND sequence = ?`,
				batch.JobID,
				event.Sequence,
			).Scan(&previous)
			if err != nil {
				return remoteresults.Mirror{}, fmt.Errorf("read imported event: %w", err)
			}
			if !bytes.Equal(previous, document) {
				return remoteresults.Mirror{}, remoteresults.ErrConflict
			}
			continue
		}
		if event.Sequence != mirror.Snapshot.LatestSequence+1 {
			return remoteresults.Mirror{}, remoteresults.ErrConflict
		}
		next, err := applyRemoteEventTx(
			ctx,
			tx,
			mirror,
			event,
			state,
		)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		if err := insertRemoteEventTx(
			ctx,
			tx,
			event,
			document,
		); err != nil {
			return remoteresults.Mirror{}, err
		}
		snapshot, err := marshalCanonical(next)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		result, err := tx.ExecContext(
			ctx,
			`UPDATE remote_job_mirrors SET snapshot_json = ?, applied_sequence = ?
			WHERE job_id = ? AND applied_sequence = ?`,
			snapshot,
			event.Sequence,
			batch.JobID,
			mirror.Snapshot.LatestSequence,
		)
		if err != nil {
			return remoteresults.Mirror{}, classifyWriteError("advance remote cursor", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return remoteresults.Mirror{}, fmt.Errorf("read remote cursor update: %w", err)
		}
		if count != 1 {
			return remoteresults.Mirror{}, remoteresults.ErrConflict
		}
		mirror.Snapshot = next
	}
	if err := tx.Commit(); err != nil {
		return remoteresults.Mirror{}, classifyWriteError("commit remote import", err)
	}
	return mirror, nil
}

func (repository *Repository) GetRemoteMirror(ctx context.Context, id string) (remoteresults.Mirror, error) {
	if !domain.IsUUID(id) {
		return remoteresults.Mirror{}, remoteresults.ErrInvalid
	}
	return queryRemoteMirror(ctx, repository.db, id)
}

func queryRemoteMirror(ctx context.Context, queryer rowQueryer, id string) (remoteresults.Mirror, error) {
	stored, err := queryStoredRemoteMirror(ctx, queryer, id)
	if err != nil {
		return remoteresults.Mirror{}, err
	}
	return stored.decode(id)
}

func queryStoredRemoteMirror(ctx context.Context, queryer rowQueryer, id string) (storedRemoteMirrorRow, error) {
	var stored storedRemoteMirrorRow
	err := stored.scan(queryer.QueryRowContext(
		ctx,
		`SELECT configuration_id, node_id, installation_id, spec_sha256,
		spec_document, snapshot_document, applied_sequence, accepted_document,
		CASE WHEN json_valid(snapshot_document) THEN
			CASE WHEN json_type(snapshot_document, '$.receipt.kind') = 'text'
				AND length(CAST(json_extract(snapshot_document, '$.receipt.kind') AS BLOB)) <= 32
			THEN json_extract(snapshot_document, '$.receipt.kind') ELSE '' END
		ELSE '' END
		FROM (
			SELECT
				CASE WHEN length(CAST(configuration_id AS BLOB)) <= 36
					THEN configuration_id ELSE '!' END AS configuration_id,
				CASE WHEN length(CAST(node_id AS BLOB)) <= 36
					THEN node_id ELSE '!' END AS node_id,
				CASE WHEN length(CAST(installation_id AS BLOB)) <= 36
					THEN installation_id ELSE '!' END AS installation_id,
				CASE WHEN length(CAST(spec_sha256 AS BLOB)) <= 64
					THEN spec_sha256 ELSE '!' END AS spec_sha256,
				CASE WHEN length(CAST(spec_json AS BLOB)) <= ?
					THEN spec_json ELSE '' END AS spec_document,
				CASE WHEN length(CAST(snapshot_json AS BLOB)) <= ?
					THEN snapshot_json ELSE '' END AS snapshot_document,
				CASE WHEN length(CAST(applied_sequence AS BLOB)) <= 20
					THEN applied_sequence ELSE -1 END AS applied_sequence,
				COALESCE((SELECT
					CASE WHEN length(CAST(event.document_json AS BLOB)) <= ?
						THEN event.document_json ELSE '' END
					FROM remote_job_events AS event
					WHERE event.job_id = mirror.job_id AND event.sequence = 1), '') AS accepted_document
			FROM remote_job_mirrors AS mirror WHERE job_id = ?
		)`,
		jobcontract.MaxSubmissionBytes,
		jobcontract.MaxEventBytes,
		jobcontract.MaxEventBytes,
		id,
	))
	if err != nil {
		return storedRemoteMirrorRow{}, err
	}
	return stored, nil
}

func createRemoteMirrorTx(
	ctx context.Context,
	tx *sql.Tx,
	origin remoteresults.Origin,
	event jobcontract.Event,
) (remoteresults.Mirror, error) {
	if event.Kind != jobcontract.EventAccepted || event.Sequence != 1 {
		return remoteresults.Mirror{}, remoteresults.ErrInvalid
	}
	var accepted jobcontract.AcceptedEvent
	if err := decodeCanonical(event.Payload, &accepted, nil); err != nil {
		return remoteresults.Mirror{}, remoteresults.ErrInvalid
	}
	spec := accepted.Spec
	initial, err := spec.InitialSnapshot(event.CreatedAt, accepted.Snapshot.Receipt.Origin)
	validOrigin := spec.Target.Kind == jobcontract.TargetRemote && spec.Target.NodeID == origin.NodeID &&
		spec.NodeInstallationID == origin.InstallationID && spec.JobID == event.JobID
	if err != nil || !validOrigin || !equalCanonicalDocuments(initial, accepted.Snapshot) {
		return remoteresults.Mirror{}, remoteresults.ErrInvalid
	}
	var local bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_jobs WHERE id = ?)`, spec.JobID).Scan(&local); err != nil {
		return remoteresults.Mirror{}, fmt.Errorf("read local job owner: %w", err)
	}
	if local {
		return remoteresults.Mirror{}, remoteresults.ErrConflict
	}
	var comparisonID, performanceID any
	if spec.Kind == jobcontract.KindComparison {
		comparisonID = spec.Spec.Comparison.Meta.ID
	}
	if spec.Kind == jobcontract.KindPerformance {
		performanceID = spec.Spec.Performance.ReportID
		if err := rejectHistoricalResource(ctx, tx, "performance", spec.Spec.Performance.ReportID); err != nil {
			return remoteresults.Mirror{}, err
		}
		var exists bool
		if err := tx.QueryRowContext(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM quick_performance_reports WHERE id = ?)
			OR EXISTS(SELECT 1 FROM execution_jobs WHERE kind = 'performance'
			AND json_extract(spec_json, '$.spec.performance.report_id') = ?)`,
			performanceID,
			performanceID,
		).Scan(&exists); err != nil {
			return remoteresults.Mirror{}, fmt.Errorf("read performance owner: %w", err)
		}
		if exists {
			return remoteresults.Mirror{}, remoteresults.ErrConflict
		}
	}
	specDocument, err := marshalCanonical(spec)
	if err != nil {
		return remoteresults.Mirror{}, err
	}
	snapshotDocument, err := marshalCanonical(initial)
	if err != nil {
		return remoteresults.Mirror{}, err
	}
	digest, err := spec.Digest()
	if err != nil {
		return remoteresults.Mirror{}, err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO remote_job_mirrors(job_id, configuration_id, node_id,
		installation_id, spec_sha256, spec_json, snapshot_json, applied_sequence, comparison_id, performance_report_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		spec.JobID,
		origin.ConfigurationID,
		origin.NodeID,
		origin.InstallationID,
		digest,
		specDocument,
		snapshotDocument,
		comparisonID,
		performanceID,
	)
	if err != nil {
		return remoteresults.Mirror{}, classifyWriteError("create remote mirror", err)
	}
	for position, input := range spec.PreparedRuns() {
		run, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		prepared, err := prepareRunCreate(run)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		if err := createRunTx(ctx, tx, prepared); err != nil {
			return remoteresults.Mirror{}, err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO remote_job_runs(run_id, job_id, position) VALUES(?, ?, ?)`,
			run.Meta().ID,
			spec.JobID,
			position,
		); err != nil {
			return remoteresults.Mirror{}, classifyWriteError("claim remote run", err)
		}
	}
	if spec.Kind == jobcontract.KindComparison {
		comparison, err := spec.Spec.Comparison.Comparison()
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		prepared, err := prepareComparisonCreate(comparison)
		if err != nil {
			return remoteresults.Mirror{}, err
		}
		if err := createComparisonTx(ctx, tx, prepared); err != nil {
			return remoteresults.Mirror{}, err
		}
	}
	document, err := marshalCanonical(event)
	if err != nil {
		return remoteresults.Mirror{}, err
	}
	if err := insertRemoteEventTx(
		ctx,
		tx,
		event,
		document,
	); err != nil {
		return remoteresults.Mirror{}, err
	}
	return remoteresults.Mirror{Origin: origin, Spec: spec, Snapshot: initial}, nil
}

func insertRemoteEventTx(ctx context.Context, tx *sql.Tx, event jobcontract.Event, document []byte) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO remote_job_events(job_id, sequence, kind, document_json) VALUES(?, ?, ?, ?)`,
		event.JobID,
		event.Sequence,
		event.Kind,
		document,
	)
	if err != nil {
		return classifyWriteError("import remote event", err)
	}
	return nil
}
