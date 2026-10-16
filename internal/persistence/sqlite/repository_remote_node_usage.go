package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// RemoteNodeRemovalBlocked reads only this desktop's durable associations. It
// does not inspect or alter jobs owned by other clients on the remote runner.
// Empty executor IDs identify an unbound configuration, which must have no jobs.
func (repository *Repository) RemoteNodeRemovalBlocked(
	ctx context.Context,
	configurationID string,
	nodeID string,
	installationID string,
) (bool, error) {
	unbound := nodeID == "" && installationID == ""
	bound := domain.IsUUID(nodeID) && domain.IsUUID(installationID)
	if ctx == nil || !domain.IsUUID(configurationID) || (!unbound && !bound) {
		return false, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("begin remote node usage read: %w", err)
	}
	defer tx.Rollback()
	origin := remoteresults.Origin{ConfigurationID: configurationID, NodeID: nodeID, InstallationID: installationID}
	after := ""
	for {
		ids, err := remoteNodeJobIDs(ctx, tx, configurationID, after)
		if err != nil {
			return false, err
		}
		if unbound && len(ids) != 0 {
			return true, nil
		}
		for _, id := range ids {
			blocked, err := remoteNodeJobBlocked(ctx, tx, origin, id)
			if err != nil || blocked {
				return blocked, err
			}
		}
		if len(ids) < int(jobs.MaxSubmissionPageSize) {
			break
		}
		after = ids[len(ids)-1]
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit remote node usage read: %w", err)
	}
	return false, nil
}

func remoteNodeJobIDs(ctx context.Context, tx *sql.Tx, configurationID, after string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT job_id FROM (
		SELECT job_id FROM job_submissions WHERE configuration_id = ?
		UNION SELECT job_id FROM remote_job_mirrors WHERE configuration_id = ?)
		WHERE job_id > ? ORDER BY job_id LIMIT ?`,
		configurationID, configurationID, after, jobs.MaxSubmissionPageSize)
	if err != nil {
		return nil, fmt.Errorf("list remote node usage: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan remote node usage: %w", err)
		}
		if !domain.IsUUID(id) {
			return nil, storageCorrupt("remote node usage job identity")
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func remoteNodeJobBlocked(ctx context.Context, tx *sql.Tx, origin remoteresults.Origin, id string) (bool, error) {
	record, submissionErr := querySubmission(ctx, tx, id)
	if submissionErr != nil && !errors.Is(submissionErr, jobs.ErrNotFound) {
		return false, submissionErr
	}
	if submissionErr == nil {
		prepared := record.Prepared
		sameOwner := prepared.ConfigurationID == origin.ConfigurationID && prepared.Runner.NodeID == origin.NodeID &&
			prepared.Runner.NodeInstallationID == origin.InstallationID
		if !sameOwner || prepared.Spec.Target.Kind != jobcontract.TargetRemote {
			return false, storageCorrupt("remote node submission owner")
		}
		if record.State != jobs.SubmissionAccepted {
			return true, nil
		}
	}
	mirror, err := queryRemoteMirror(ctx, tx, id)
	if errors.Is(err, remoteresults.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if mirror.Origin != origin {
		return false, storageCorrupt("remote node mirror owner")
	}
	if !jobcontract.Terminal(mirror.Snapshot.Receipt.Status) {
		return true, nil
	}
	var unknownEvents bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_job_events WHERE job_id = ?
		AND kind NOT IN ('accepted', 'state', 'run', 'results', 'evidence', 'report', 'comparison',
		'progress', 'command', 'performance_checkpoint', 'performance_result'))`, id).Scan(&unknownEvents); err != nil {
		return false, fmt.Errorf("read remote node event namespace: %w", err)
	}
	if unknownEvents {
		return true, nil
	}
	if submissionErr == nil {
		digest, err := mirror.Spec.Digest()
		if err != nil || digest != record.SpecSHA256 {
			return false, storageCorrupt("remote node submission mirror spec")
		}
		view, err := jobs.ProjectSubmittedJob(record, &mirror.Snapshot, nil, false)
		if err != nil {
			return false, storageCorrupt("remote node submission mirror state")
		}
		if view.Execution == nil {
			return true, nil
		}
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_control_outbox
		WHERE job_id = ? AND state = 'pending')`, id).Scan(&pending); err != nil {
		return false, fmt.Errorf("read remote node pending controls: %w", err)
	}
	if pending {
		return true, nil
	}
	var incomplete bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_job_report_refs
		WHERE job_id = ? AND imported != 1)`, id).Scan(&incomplete); err != nil {
		return false, fmt.Errorf("read remote node pending documents: %w", err)
	}
	if incomplete {
		return true, nil
	}
	var referenceCount, unknownReferences int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE
		WHEN reference_type NOT IN ('report', 'performance_result') THEN 1 ELSE 0 END), 0)
		FROM remote_job_report_refs WHERE job_id = ?`, id).Scan(&referenceCount, &unknownReferences)
	if err != nil {
		return false, fmt.Errorf("read remote node document namespace: %w", err)
	}
	if unknownReferences != 0 {
		return true, nil
	}
	if mirror.Spec.Kind == jobcontract.KindPerformance {
		var unknown bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_job_report_refs
			WHERE job_id = ? AND report_id != ?)`, id, mirror.Spec.Spec.Performance.ReportID).Scan(&unknown); err != nil {
			return false, fmt.Errorf("read remote performance document namespace: %w", err)
		}
		if unknown {
			return true, nil
		}
		return remoteNodePerformanceBlocked(ctx, tx, mirror)
	}
	if referenceCount != len(mirror.Spec.PreparedRuns()) {
		return true, nil
	}
	if mirror.Spec.Kind == jobcontract.KindComparison {
		comparison, err := queryCurrentComparison(ctx, tx, mirror.Spec.Spec.Comparison.Meta.ID)
		if err != nil {
			return false, err
		}
		if comparison.Status() == domain.ComparisonRunning {
			return true, nil
		}
	}
	for _, prepared := range mirror.Spec.PreparedRuns() {
		blocked, err := remoteNodeRunBlocked(ctx, tx, mirror, prepared.Meta.ID)
		if err != nil || blocked {
			return blocked, err
		}
	}
	return false, nil
}

func remoteNodeRunBlocked(ctx context.Context, tx *sql.Tx, mirror remoteresults.Mirror, runID string) (bool, error) {
	run, err := queryCurrentRun(ctx, tx, runID)
	if err != nil {
		return false, err
	}
	if !jobcontract.Terminal(run.Status()) {
		return true, nil
	}
	var sealed, artifacts bool
	err = tx.QueryRowContext(ctx, `SELECT sealed,
		EXISTS(SELECT 1 FROM artifacts WHERE run_id = ?
		UNION ALL SELECT 1 FROM evidence WHERE run_id = ?) FROM execution_runs WHERE id = ?`, runID, runID, runID).
		Scan(&sealed, &artifacts)
	if err != nil {
		return false, fmt.Errorf("read remote run settlement: %w", err)
	}
	// Artifact rows and report attachments are metadata, not proof of a durable
	// local file download. Until that fact exists, retaining the node is required.
	if !sealed || artifacts {
		return true, nil
	}
	var row storedReportRow
	var referenceRaw []byte
	var imported bool
	err = tx.QueryRowContext(ctx, `SELECT report.id, report.schema_version, report.run_id, report.generated_at,
		CASE WHEN length(CAST(report.document_json AS BLOB)) <= ? THEN report.document_json ELSE '' END,
		ref.document_json, ref.imported FROM reports report JOIN remote_job_report_refs ref
		ON ref.report_id = report.id AND ref.reference_type = 'report' AND ref.job_id = ?
		WHERE report.run_id = ?`, jobcontract.MaxRunReportBytes, mirror.Spec.JobID, runID).
		Scan(&row.id, &row.schemaVersion, &row.runID, &row.generatedAt, &row.document, &referenceRaw, &imported)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read remote run report settlement: %w", err)
	}
	report, err := row.decode()
	if err != nil {
		return false, err
	}
	var reference jobcontract.ReportReference
	if err := decodeCanonical(referenceRaw, &reference, nil); err != nil {
		return false, storageCorrupt("remote node run report reference")
	}
	digest := sha256.Sum256(row.document)
	validReport := imported && report.RunID == runID && reference.ID == report.ID &&
		reference.Kind == jobcontract.KindRun && reference.RunID == runID
	validBytes := reference.Size == int64(len(row.document)) && reference.SHA256 == hex.EncodeToString(digest[:])
	if !validReport || !validBytes || report.ExecutionOrigin == nil || *report.ExecutionOrigin != mirror.Snapshot.Receipt.Origin {
		return false, storageCorrupt("remote node imported report identity")
	}
	if len(report.Attachments) != 0 || len(report.Evidence) != 0 {
		return true, nil
	}
	if err := validateReportStorage(ctx, tx, report); err != nil {
		return false, err
	}
	return false, nil
}

func remoteNodePerformanceBlocked(ctx context.Context, tx *sql.Tx, mirror remoteresults.Mirror) (bool, error) {
	result, err := queryRemotePerformanceResult(ctx, tx, mirror)
	if errors.Is(err, remoteresults.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	expected, eligible, err := result.StatisticalArchive()
	if err != nil {
		return false, err
	}
	var document, rawReference []byte
	var imported bool
	err = tx.QueryRowContext(ctx, `SELECT
		CASE WHEN length(CAST(report.document_json AS BLOB)) <= ? THEN report.document_json ELSE '' END,
		ref.document_json, ref.imported FROM remote_job_report_refs ref
		LEFT JOIN quick_performance_reports report ON report.id = ref.report_id
		WHERE ref.job_id = ? AND ref.reference_type = 'report' AND ref.report_id = ?`,
		maxQuickPerformanceReportBytes, mirror.Spec.JobID, result.ReportID).Scan(&document, &rawReference, &imported)
	if errors.Is(err, sql.ErrNoRows) {
		return eligible, nil
	}
	if err != nil {
		return false, fmt.Errorf("read remote performance archive settlement: %w", err)
	}
	if !imported {
		return true, nil
	}
	report, _, err := decodeQuickPerformanceReport(document)
	if err != nil || !eligible || !equalCanonicalDocuments(expected, report) {
		return false, storageCorrupt("remote node performance archive")
	}
	var reference jobcontract.ReportReference
	if err := decodeCanonical(rawReference, &reference, nil); err != nil {
		return false, storageCorrupt("remote node performance archive reference")
	}
	digest := sha256.Sum256(document)
	validOwner := reference.ID == result.ReportID && reference.Kind == jobcontract.KindPerformance && reference.RunID == ""
	if !validOwner || reference.Size != int64(len(document)) || reference.SHA256 != hex.EncodeToString(digest[:]) {
		return false, storageCorrupt("remote node performance archive bytes")
	}
	return false, nil
}
