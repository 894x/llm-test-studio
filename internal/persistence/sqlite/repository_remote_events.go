package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func applyRemoteEventTx(
	ctx context.Context,
	tx *sql.Tx,
	mirror remoteresults.Mirror,
	event jobcontract.Event,
) (jobcontract.Snapshot, error) {
	next := mirror.Snapshot
	next.LatestSequence = event.Sequence
	if event.CreatedAt.After(next.UpdatedAt) {
		next.UpdatedAt = event.CreatedAt
	}
	var err error
	switch event.Kind {
	case jobcontract.EventState:
		var state jobcontract.Snapshot
		if err = decodeCanonical(event.Payload, &state, nil); err != nil {
			break
		}
		previous := mirror.Snapshot.Receipt
		validTransition := validJobTransition(previous.Status, state.Receipt.Status) && state.Receipt.Revision == previous.Revision+1
		validTime := state.UpdatedAt.Equal(event.CreatedAt) && !state.UpdatedAt.Before(mirror.Snapshot.UpdatedAt)
		if state.Validate(mirror.Spec) != nil || !validTransition || !validTime {
			err = remoteresults.ErrInvalid
			break
		}
		next.Receipt.Status, next.Receipt.Revision, next.Failure = state.Receipt.Status, state.Receipt.Revision, state.Failure
		if !equalCanonicalDocuments(next, state) {
			err = remoteresults.ErrConflict
		}
	case jobcontract.EventCommand:
		var confirmation jobcontract.CommandConfirmation
		if err = decodeCanonical(event.Payload, &confirmation, func() error { return confirmation.Validate() }); err != nil {
			break
		}
		if !confirmation.AcceptedAt.Equal(event.CreatedAt) || !equalCanonicalDocuments(next, confirmation.Snapshot) {
			err = remoteresults.ErrConflict
		}
	case jobcontract.EventRun:
		err = importRemoteRunTx(
			ctx,
			tx,
			mirror.Spec.JobID,
			event.Payload,
		)
	case jobcontract.EventEvidence:
		err = importRemoteEvidenceTx(
			ctx,
			tx,
			mirror.Spec.JobID,
			event.Payload,
		)
	case jobcontract.EventResults:
		err = importRemoteResultTx(
			ctx,
			tx,
			mirror.Spec.JobID,
			event.Payload,
		)
	case jobcontract.EventComparison:
		err = importRemoteComparisonTx(
			ctx,
			tx,
			mirror.Spec,
			event.Payload,
		)
	case jobcontract.EventReport:
		err = importRemoteReportReferenceTx(
			ctx,
			tx,
			mirror.Spec,
			event.Payload,
		)
	case jobcontract.EventPerformanceResult:
		err = importRemotePerformanceReferenceTx(
			ctx,
			tx,
			mirror.Spec,
			event.Payload,
		)
	case jobcontract.EventProgress:
		var progress quicktest.PerformanceProgress
		err = decodeCanonical(event.Payload, &progress, nil)
		if mirror.Spec.Kind != jobcontract.KindPerformance {
			err = remoteresults.ErrInvalid
		}
		switch progress.Phase {
		case load.PhaseSending, load.PhaseDraining, load.PhaseCompleted, load.PhaseCancelled:
		case quicktest.PerformancePhaseNotStarted, quicktest.PerformancePhaseWarmingUp, quicktest.PerformancePhaseRamping:
		default:
			err = remoteresults.ErrInvalid
		}
	case jobcontract.EventPerformanceCheckpoint:
		err = importRemoteCheckpointTx(
			ctx,
			tx,
			mirror.Spec,
			event.Payload,
		)
	default:
		err = remoteresults.ErrInvalid
	}
	if err != nil {
		return jobcontract.Snapshot{}, fmt.Errorf("import remote %s: %w", event.Kind, err)
	}
	if err := next.Validate(mirror.Spec); err != nil {
		return jobcontract.Snapshot{}, err
	}
	return next, nil
}

func requireRemoteRun(ctx context.Context, tx *sql.Tx, jobID, runID string) error {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT job_id FROM remote_job_runs WHERE run_id = ?`, runID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteresults.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("read remote run owner: %w", err)
	}
	if owner != jobID {
		return remoteresults.ErrConflict
	}
	return nil
}

func importRemoteRunTx(ctx context.Context, tx *sql.Tx, jobID string, raw []byte) error {
	run, err := decodeRunDocument(raw)
	if err != nil {
		return err
	}
	if err := requireRemoteRun(
		ctx,
		tx,
		jobID,
		run.Meta().ID,
	); err != nil {
		return err
	}
	current, err := queryCurrentRun(ctx, tx, run.Meta().ID)
	if err != nil {
		return err
	}
	if run.Meta().Revision <= current.Meta().Revision {
		previous, err := queryRunRevision(
			ctx,
			tx,
			run.Meta().ID,
			run.Meta().Revision,
			false,
		)
		if err != nil {
			return err
		}
		if !equalCanonicalDocuments(previous, run) {
			return remoteresults.ErrConflict
		}
		return nil
	}
	return updateRunTx(
		ctx,
		tx,
		current.Meta().Revision,
		run,
	)
}

// Existing immutable IDs are accepted only with the exact current document.
func importedDocumentMatches(ctx context.Context, row *sql.Row, raw []byte) (bool, error) {
	var previous []byte
	err := row.Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read imported document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !bytes.Equal(previous, raw) {
		return false, remoteresults.ErrConflict
	}
	return true, nil
}

func importRemoteEvidenceTx(ctx context.Context, tx *sql.Tx, jobID string, raw []byte) error {
	evidence, err := decodeEvidenceDocument(raw)
	if err != nil {
		return err
	}
	if evidence.Revision != 1 || len(raw) > MaxReportProjectionItemBytes {
		return remoteresults.ErrInvalid
	}
	if err := requireRemoteRun(
		ctx,
		tx,
		jobID,
		evidence.RunID,
	); err != nil {
		return err
	}
	matched, err := importedDocumentMatches(ctx, tx.QueryRowContext(ctx, `SELECT document_json FROM evidence WHERE id = ?`, evidence.ID), raw)
	if matched || err != nil {
		return err
	}
	if err := requireUnsealedRemoteRun(ctx, tx, evidence.RunID); err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO evidence(id, schema_version, revision, created_at, updated_at, run_id, document_json)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		evidence.ID,
		evidence.SchemaVersion,
		evidence.Revision,
		formatTime(evidence.CreatedAt),
		formatTime(evidence.UpdatedAt),
		evidence.RunID,
		raw,
	)
	if err != nil {
		return classifyWriteError("import evidence", err)
	}
	return nil
}

func requireUnsealedRemoteRun(ctx context.Context, tx *sql.Tx, runID string) error {
	var sealed bool
	if err := tx.QueryRowContext(ctx, `SELECT sealed FROM execution_runs WHERE id = ?`, runID).Scan(&sealed); err != nil {
		return fmt.Errorf("read imported run seal: %w", err)
	}
	if sealed {
		return remoteresults.ErrConflict
	}
	return nil
}

func importRemoteResultTx(ctx context.Context, tx *sql.Tx, jobID string, raw []byte) error {
	result, err := decodeResultDocument(raw)
	if err != nil {
		return err
	}
	validStorage := result.Revision == 1 && len(raw) <= MaxReportProjectionItemBytes
	if !validStorage || result.EntryID == "" {
		return remoteresults.ErrInvalid
	}
	if err := requireRemoteRun(
		ctx,
		tx,
		jobID,
		result.RunID,
	); err != nil {
		return err
	}
	matched, err := importedDocumentMatches(ctx, tx.QueryRowContext(ctx, `SELECT document_json FROM case_results WHERE id = ?`, result.ID), raw)
	if matched || err != nil {
		return err
	}
	if err := requireUnsealedRemoteRun(ctx, tx, result.RunID); err != nil {
		return err
	}
	run, err := queryCurrentRun(ctx, tx, result.RunID)
	if err != nil {
		return err
	}
	if err := validateStoredResultAgainstSnapshot(
		ctx,
		tx,
		result,
		run.Snapshot(),
		map[string]domain.Evidence{},
	); err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, insertResultSQL)
	if err != nil {
		return fmt.Errorf("prepare remote result: %w", err)
	}
	defer statement.Close()
	return insertResult(
		ctx,
		statement,
		result,
		raw,
	)
}

func importRemoteComparisonTx(ctx context.Context, tx *sql.Tx, spec jobcontract.JobSpec, raw []byte) error {
	var comparison domain.Comparison
	if err := decodeCanonical(raw, &comparison, func() error { return comparison.Validate() }); err != nil {
		return err
	}
	if spec.Kind != jobcontract.KindComparison || comparison.Meta().ID != spec.Spec.Comparison.Meta.ID {
		return remoteresults.ErrConflict
	}
	current, err := queryCurrentComparison(ctx, tx, comparison.Meta().ID)
	if err != nil {
		return err
	}
	if equalCanonicalDocuments(current, comparison) {
		return nil
	}
	return updateComparisonTx(
		ctx,
		tx,
		current.Meta().Revision,
		comparison,
		raw,
	)
}

func importRemoteReportReferenceTx(ctx context.Context, tx *sql.Tx, spec jobcontract.JobSpec, raw []byte) error {
	var reference jobcontract.ReportReference
	if err := decodeCanonical(raw, &reference, nil); err != nil {
		return err
	}
	validContent := domain.IsUUID(reference.ID) && jobcontract.IsSHA256(reference.SHA256) && reference.Size > 0
	if !validContent || reference.Size > jobcontract.MaxRunReportBytes {
		return remoteresults.ErrInvalid
	}
	switch reference.Kind {
	case jobcontract.KindRun:
		if err := requireRemoteRun(
			ctx,
			tx,
			spec.JobID,
			reference.RunID,
		); err != nil {
			return err
		}
		run, err := queryCurrentRun(ctx, tx, reference.RunID)
		if err != nil {
			return err
		}
		if !jobcontract.Terminal(run.Status()) {
			return remoteresults.ErrInvalid
		}
		var conflict bool
		if err := tx.QueryRowContext(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM reports WHERE id = ? AND run_id != ?)
			OR EXISTS(SELECT 1 FROM remote_job_report_refs WHERE reference_type = 'report'
			AND json_extract(document_json, '$.run_id') = ? AND report_id != ?)`,
			reference.ID,
			reference.RunID,
			reference.RunID,
			reference.ID,
		).Scan(&conflict); err != nil {
			return fmt.Errorf("read imported report owner: %w", err)
		}
		if conflict {
			return remoteresults.ErrConflict
		}
	case jobcontract.KindPerformance:
		if spec.Kind != jobcontract.KindPerformance || reference.ID != spec.Spec.Performance.ReportID || reference.RunID != "" {
			return remoteresults.ErrConflict
		}
	default:
		return remoteresults.ErrInvalid
	}
	return insertRemoteReferenceTx(
		ctx,
		tx,
		spec.JobID,
		jobcontract.EventReport,
		reference.ID,
		raw,
	)
}

func importRemotePerformanceReferenceTx(ctx context.Context, tx *sql.Tx, spec jobcontract.JobSpec, raw []byte) error {
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(raw, &reference, nil); err != nil {
		return err
	}
	validOwner := spec.Kind == jobcontract.KindPerformance && reference.JobID == spec.JobID &&
		reference.ReportID == spec.Spec.Performance.ReportID
	validContent := jobcontract.IsSHA256(reference.SHA256) && reference.Size > 0 &&
		reference.Size <= jobcontract.MaxPerformanceResultBytes
	if !validOwner || !validContent {
		return remoteresults.ErrInvalid
	}
	return insertRemoteReferenceTx(
		ctx,
		tx,
		spec.JobID,
		jobcontract.EventPerformanceResult,
		reference.ReportID,
		raw,
	)
}

func insertRemoteReferenceTx(
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	kind jobcontract.EventKind,
	reportID string,
	raw []byte,
) error {
	matched, err := importedDocumentMatches(ctx, tx.QueryRowContext(
		ctx,
		`SELECT document_json FROM remote_job_report_refs
		WHERE job_id = ? AND reference_type = ? AND report_id = ?`,
		jobID,
		kind,
		reportID,
	), raw)
	if matched || err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO remote_job_report_refs(job_id, reference_type, report_id, document_json)
		VALUES(?, ?, ?, ?)`,
		jobID,
		kind,
		reportID,
		raw,
	)
	if err != nil {
		return classifyWriteError("import report reference", err)
	}
	return nil
}

func importRemoteCheckpointTx(ctx context.Context, tx *sql.Tx, spec jobcontract.JobSpec, raw []byte) error {
	var checkpoint jobcontract.PerformanceCheckpointEvent
	if err := decodeCanonical(raw, &checkpoint, nil); err != nil {
		return err
	}
	validOwner := spec.Kind == jobcontract.KindPerformance && checkpoint.ReportID == spec.Spec.Performance.ReportID
	if !validOwner || checkpoint.Revision == 0 {
		return remoteresults.ErrInvalid
	}
	prepared := spec.Spec.Performance
	envelope := quicktest.PerformanceCheckpoint{
		Stage: checkpoint.Stage, Boundary: checkpoint.Boundary, RungIndex: checkpoint.RungIndex,
		Report: quicktest.PerformanceReport{
			SchemaVersion: quicktest.PerformanceSchemaVersion, ReportID: prepared.ReportID,
			Protocol: prepared.Protocol, AddressMode: prepared.AddressMode, BaseURL: prepared.BaseURL,
			Endpoint: prepared.Endpoint, ModelID: prepared.ModelID, Profile: prepared.Profile,
		},
	}
	if err := envelope.ValidateEnvelope(); err != nil {
		return remoteresults.ErrInvalid
	}
	var previous uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(json_extract(document_json,
		'$.payload.observation_revision')), 0) FROM remote_job_events WHERE job_id = ? AND kind = 'performance_checkpoint'`,
		spec.JobID).Scan(&previous); err != nil {
		return fmt.Errorf("read imported checkpoint revision: %w", err)
	}
	if checkpoint.Revision != previous+1 {
		return remoteresults.ErrConflict
	}
	return nil
}
