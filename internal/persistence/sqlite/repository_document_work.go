package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ remoteresults.WorkRepository = (*Repository)(nil)

// ListRemoteDocumentWork reads only bounded headers and immutable references.
// One statement keeps sources, references and control anchors in one snapshot.
// Eligibility is checked after validation, so malformed terminal history cannot
// disappear through a filter. SQL may scan scoped history; ctx bounds that work.
func (repository *Repository) ListRemoteDocumentWork(
	ctx context.Context,
	query remoteresults.DocumentQuery,
) (remoteresults.DocumentPage, error) {
	if ctx == nil || query.Validate() != nil {
		return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
	}
	rows, err := repository.db.QueryContext(
		ctx,
		remoteDocumentWorkSQL,
		query.Origin.ConfigurationID,
		query.Origin.NodeID,
		query.Origin.InstallationID,
		query.After.JobID,
		maxSubmissionRecordBytes,
		jobcontract.MaxSubmissionBytes,
		jobcontract.MaxStateBytes,
		jobcontract.MaxStateBytes,
		maxPerformanceReferenceBytes,
		maxPerformanceReferenceBytes,
		jobs.MaxControlTargetBytes,
		jobs.MaxControlRecordBytes,
		query.After.JobID,
		query.After.ReferenceType,
		query.After.ReportID,
	)
	if err != nil {
		return remoteresults.DocumentPage{}, fmt.Errorf("read remote document work: %w", err)
	}
	defer rows.Close()
	page := remoteresults.DocumentPage{Items: []remoteresults.DocumentWork{}, NextCursor: query.After}
	previous := query.After
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return remoteresults.DocumentPage{}, err
		}
		var candidate remoteDocumentCandidate
		if err := rows.Scan(candidate.destinations()...); err != nil {
			return remoteresults.DocumentPage{}, fmt.Errorf("scan remote document work: %w", err)
		}
		cursor := candidate.cursor()
		if cursor.Validate() != nil || !documentCursorAfter(cursor, previous) {
			return remoteresults.DocumentPage{}, storageCorrupt("remote document cursor")
		}
		previous = cursor
		focus, eligible, err := candidate.validate(query.Origin)
		if err != nil {
			return remoteresults.DocumentPage{}, err
		}
		if !eligible || focus != (query.Class == remoteresults.DocumentFocus) {
			continue
		}
		if len(page.Items) == int(query.Limit) {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, remoteresults.DocumentWork{
			JobID: candidate.header.id, Document: candidate.document,
		})
		page.NextCursor = cursor
	}
	if err := rows.Err(); err != nil {
		return remoteresults.DocumentPage{}, fmt.Errorf("iterate remote document work: %w", err)
	}
	if err := rows.Close(); err != nil {
		return remoteresults.DocumentPage{}, fmt.Errorf("close remote document work: %w", err)
	}
	return page, nil
}

func documentCursorAfter(left, right remoteresults.DocumentCursor) bool {
	if left.JobID != right.JobID {
		return left.JobID > right.JobID
	}
	if left.ReferenceType != right.ReferenceType {
		return left.ReferenceType > right.ReferenceType
	}
	return left.ReportID > right.ReportID
}

type remoteDocumentCandidate struct {
	header          remoteSyncHeader
	kind            jobcontract.EventKind
	reportID        string
	raw             []byte
	imported, count int64
	runMember       bool
	sealedRaw       []byte
	sealedImported  sql.NullInt64
	sealedReportID  sql.NullString
	performanceID   sql.NullString
	performanceSpec sql.NullString
	anchor          documentControlAnchor
	document        remoteresults.DocumentReference
}

func (candidate *remoteDocumentCandidate) destinations() []any {
	values := append(
		candidate.header.destinations(),
		&candidate.kind,
		&candidate.reportID,
		&candidate.raw,
		&candidate.imported,
		&candidate.count,
		&candidate.runMember,
		&candidate.sealedRaw,
		&candidate.sealedImported,
		&candidate.sealedReportID,
		&candidate.performanceID,
		&candidate.performanceSpec,
	)
	return append(values, candidate.anchor.destinations()...)
}

func (candidate remoteDocumentCandidate) cursor() remoteresults.DocumentCursor {
	return remoteresults.DocumentCursor{
		JobID: candidate.header.id, ReferenceType: candidate.kind, ReportID: candidate.reportID,
	}
}

func (candidate *remoteDocumentCandidate) validate(origin remoteresults.Origin) (bool, bool, error) {
	if err := candidate.header.validate(origin); err != nil {
		return false, false, err
	}
	validState := candidate.header.mirror && candidate.count > 0 && candidate.count <= jobcontract.MaxSecrets+2 &&
		(candidate.imported == 0 || candidate.imported == 1)
	if !validState || len(candidate.raw) == 0 || len(candidate.raw) > maxPerformanceReferenceBytes {
		return false, false, storageCorrupt("remote document reference state or bounds")
	}
	document := remoteresults.DocumentReference{Kind: candidate.kind, Imported: candidate.imported == 1}
	switch candidate.kind {
	case jobcontract.EventReport:
		if err := decodeCanonical(candidate.raw, &document.Reference, nil); err != nil {
			return false, false, storageCorrupt("remote report work reference")
		}
	case jobcontract.EventPerformanceResult:
		var reference jobcontract.PerformanceResultReference
		if err := decodeCanonical(candidate.raw, &reference, func() error { return reference.Validate() }); err != nil {
			return false, false, storageCorrupt("remote performance work reference")
		}
		if reference.JobID != candidate.header.id || reference.ReportID != candidate.reportID {
			return false, false, storageCorrupt("remote performance work owner")
		}
		document.Reference = jobcontract.ReportReference{ID: reference.ReportID,
			Kind: jobcontract.KindPerformance, SHA256: reference.SHA256, Size: reference.Size}
	default:
		return false, false, storageCorrupt("remote document work kind")
	}
	reference := document.Reference
	validIdentity := domain.IsUUID(reference.ID) && reference.ID == candidate.reportID &&
		jobcontract.IsSHA256(reference.SHA256) && reference.Size > 0
	limit := int64(jobcontract.MaxRunReportBytes)
	if candidate.kind == jobcontract.EventPerformanceResult {
		limit = jobcontract.MaxPerformanceResultBytes
	} else if reference.Kind == jobcontract.KindPerformance {
		limit = maxQuickPerformanceReportBytes
	}
	if !validIdentity || reference.Size > limit {
		return false, false, storageCorrupt("remote document work identity or bytes")
	}
	performance := candidate.header.snapshot.Receipt.Kind == jobcontract.KindPerformance
	if reference.Kind == jobcontract.KindRun {
		validRun := domain.IsUUID(reference.RunID) && candidate.runMember &&
			slices.Contains(candidate.header.snapshot.Receipt.RunIDs, reference.RunID)
		if performance || !validRun {
			return false, false, storageCorrupt("remote document work run owner")
		}
	} else if reference.Kind == jobcontract.KindPerformance {
		reports := candidate.header.snapshot.Receipt.ReportIDs
		matching := candidate.performanceID.Valid && candidate.performanceID.String == reference.ID &&
			candidate.performanceSpec.Valid && candidate.performanceSpec.String == reference.ID
		validOwner := performance && len(reports) == 1 && reference.ID == reports[0] && reference.RunID == "" && matching
		if !validOwner {
			return false, false, storageCorrupt("remote document work performance owner")
		}
	} else {
		return false, false, storageCorrupt("remote document work report kind")
	}
	focus, err := candidate.anchor.focus(candidate.header, origin)
	if err != nil {
		return false, false, err
	}
	candidate.document = document
	eligible := !document.Imported
	if performance && candidate.kind == jobcontract.EventReport {
		ready, err := candidate.sealedReady()
		if err != nil {
			return false, false, err
		}
		eligible = eligible && ready
	}
	return focus, eligible, nil
}

func (candidate remoteDocumentCandidate) sealedReady() (bool, error) {
	if !candidate.sealedImported.Valid {
		if len(candidate.sealedRaw) != 0 || candidate.sealedReportID.Valid {
			return false, storageCorrupt("remote archive result reference missing")
		}
		return false, nil
	}
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(candidate.sealedRaw, &reference, func() error { return reference.Validate() }); err != nil {
		return false, storageCorrupt("remote archive result reference")
	}
	validOwner := reference.JobID == candidate.header.id && reference.ReportID == candidate.reportID
	validState := candidate.sealedImported.Int64 == 0 || candidate.sealedImported.Int64 == 1
	if !validOwner || !validState {
		return false, storageCorrupt("remote archive result owner or state")
	}
	if candidate.sealedImported.Int64 == 0 {
		if candidate.sealedReportID.Valid {
			return false, storageCorrupt("remote archive result import state")
		}
		return false, nil
	}
	if !candidate.sealedReportID.Valid || candidate.sealedReportID.String != candidate.reportID {
		return false, storageCorrupt("remote archive imported result missing")
	}
	return true, nil
}

type documentControlAnchor struct {
	id, jobID, submissionID, mirrorID sql.NullString
	document                          []byte
	outboxID, outboxJobID             sql.NullString
	revision                          sql.NullInt64
	state                             sql.NullString
	record                            []byte
}

func (anchor *documentControlAnchor) destinations() []any {
	return []any{&anchor.id, &anchor.jobID, &anchor.submissionID, &anchor.mirrorID, &anchor.document,
		&anchor.outboxID, &anchor.outboxJobID, &anchor.revision, &anchor.state, &anchor.record}
}

func (anchor documentControlAnchor) focus(header remoteSyncHeader, origin remoteresults.Origin) (bool, error) {
	if !anchor.id.Valid {
		return header.own, nil
	}
	submission := anchor.submissionID.Valid && !anchor.mirrorID.Valid && anchor.submissionID.String == header.id
	mirror := !anchor.submissionID.Valid && anchor.mirrorID.Valid && anchor.mirrorID.String == header.id
	validSource := submission && header.own || mirror && header.mirror
	validIDs := domain.IsUUID(anchor.id.String) && anchor.jobID.Valid && anchor.jobID.String == header.id &&
		anchor.outboxID.Valid && anchor.outboxID.String == anchor.id.String &&
		anchor.outboxJobID.Valid && anchor.outboxJobID.String == header.id
	validState := anchor.revision.Valid && anchor.revision.Int64 > 0 && anchor.state.Valid
	if !validSource || !validIDs || !validState {
		return false, storageCorrupt("remote document control source")
	}
	var target jobs.ControlTarget
	if len(anchor.document) > jobs.MaxControlTargetBytes ||
		decodeCanonical(anchor.document, &target, func() error { return target.Validate() }) != nil {
		return false, storageCorrupt("remote document control target")
	}
	record, err := decodeControlRecord(anchor.record)
	if err != nil {
		return false, err
	}
	matching := record.Command.CommandID == anchor.id.String && record.Command.JobID == header.id &&
		record.Revision == uint64(anchor.revision.Int64) && string(record.State) == anchor.state.String &&
		record.Target.Equal(target)
	current := jobs.ControlTarget{
		JobID: header.id, Location: jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: origin.ConfigurationID},
		NodeID: origin.NodeID, InstallationID: origin.InstallationID, SpecSHA256: header.mirrorDigest,
		Acceptance: &jobs.ControlAcceptance{
			Origin: header.snapshot.Receipt.Origin, AcceptedAt: header.snapshot.Receipt.AcceptedAt,
		},
	}
	validBinding := target.SameBinding(current) && (target.Acceptance != nil || header.own)
	if target.Acceptance != nil && !target.Equal(current) {
		validBinding = false
	}
	if !matching || !validBinding {
		return false, storageCorrupt("remote document control binding")
	}
	if record.Confirmation != nil {
		snapshot := record.Confirmation.Snapshot
		if snapshot.ValidateState() != nil || !samePriorityAcceptance(snapshot.Receipt, header.snapshot.Receipt) {
			return false, storageCorrupt("remote document control acceptance")
		}
	}
	return true, nil
}

const remoteDocumentScopeSQL = `WITH scope(configuration_id,node_id,installation_id,after_id) AS (VALUES(?,?,?,?)),
 ids AS (
 SELECT job_id FROM job_submissions, scope
 WHERE job_submissions.configuration_id = scope.configuration_id AND job_submissions.node_id = scope.node_id
 AND job_submissions.installation_id = scope.installation_id AND job_id >= after_id
 UNION
 SELECT job_id FROM remote_job_mirrors, scope
 WHERE remote_job_mirrors.configuration_id = scope.configuration_id AND remote_job_mirrors.node_id = scope.node_id
 AND remote_job_mirrors.installation_id = scope.installation_id AND job_id >= after_id
 ) `

const remoteDocumentWorkSQL = remoteDocumentScopeSQL +
	remoteSyncHeaderCTEsSQL + ` SELECT ` + remoteSyncHeaderColumnsSQL + `,
 CASE WHEN length(CAST(r.reference_type AS BLOB)) <= 32 THEN r.reference_type ELSE '' END,
 CASE WHEN length(CAST(r.report_id AS BLOB)) = 36 THEN r.report_id ELSE '' END,
 CASE WHEN length(CAST(r.document_json AS BLOB)) BETWEEN 1 AND ? THEN r.document_json ELSE '' END,
 CASE WHEN typeof(r.imported) = 'integer' AND r.imported IN (0,1) THEN r.imported ELSE -1 END,
 (SELECT count(*) FROM remote_job_report_refs counted WHERE counted.job_id = headers.job_id),
 EXISTS(SELECT 1 FROM remote_job_runs owned WHERE owned.job_id = headers.job_id AND owned.run_id =
 CASE WHEN length(CAST(r.document_json AS BLOB)) BETWEEN 1 AND 4096 THEN
 CASE WHEN json_valid(r.document_json) THEN json_extract(r.document_json,'$.run_id') END END),
 CASE WHEN length(CAST(sealed.document_json AS BLOB)) BETWEEN 1 AND ? THEN sealed.document_json ELSE '' END,
 CASE WHEN sealed.job_id IS NULL THEN NULL WHEN typeof(sealed.imported) = 'integer'
 AND sealed.imported IN (0,1) THEN sealed.imported ELSE -1 END,
 CASE WHEN length(CAST(result.report_id AS BLOB)) = 36 THEN result.report_id
 WHEN result.job_id IS NOT NULL THEN '' END,
 CASE WHEN length(CAST(headers.performance_id AS BLOB)) = 36 THEN headers.performance_id
 WHEN headers.performance_id IS NOT NULL THEN '' END,
 CASE WHEN length(CAST(json_extract(headers.spec,'$.spec.performance.report_id') AS BLOB)) = 36
 THEN json_extract(headers.spec,'$.spec.performance.report_id') END,
 CASE WHEN anchor.command_id IS NULL THEN NULL
 WHEN length(CAST(anchor.command_id AS BLOB)) <= 36 THEN anchor.command_id ELSE '' END,
 CASE WHEN length(CAST(anchor.job_id AS BLOB)) <= 36 THEN anchor.job_id ELSE '' END,
 CASE WHEN length(CAST(anchor.submission_job_id AS BLOB)) <= 36 THEN anchor.submission_job_id
 WHEN anchor.submission_job_id IS NOT NULL THEN '' END,
 CASE WHEN length(CAST(anchor.mirror_job_id AS BLOB)) <= 36 THEN anchor.mirror_job_id
 WHEN anchor.mirror_job_id IS NOT NULL THEN '' END,
 CASE WHEN length(CAST(anchor.document_json AS BLOB)) <= ? THEN anchor.document_json ELSE '' END,
 CASE WHEN length(CAST(control.command_id AS BLOB)) <= 36 THEN control.command_id ELSE '' END,
 CASE WHEN length(CAST(control.job_id AS BLOB)) <= 36 THEN control.job_id ELSE '' END,
 control.revision, CASE WHEN length(CAST(control.state AS BLOB)) <= 16 THEN control.state ELSE '' END,
 CASE WHEN length(CAST(control.document_json AS BLOB)) <= ? THEN control.document_json ELSE '' END
 FROM headers JOIN remote_job_report_refs r ON r.job_id = headers.job_id
 LEFT JOIN remote_job_report_refs sealed ON sealed.job_id = headers.job_id
 AND sealed.reference_type = 'performance_result' AND sealed.report_id = r.report_id
 LEFT JOIN remote_performance_results result ON result.job_id = headers.job_id
 LEFT JOIN job_control_targets anchor ON anchor.command_id =
 (SELECT min(target.command_id) FROM job_control_targets target WHERE target.job_id = headers.job_id)
 LEFT JOIN job_control_outbox control ON control.command_id = anchor.command_id
 WHERE (headers.job_id,r.reference_type,r.report_id) > (?,?,?)
 ORDER BY headers.job_id,r.reference_type,r.report_id`
