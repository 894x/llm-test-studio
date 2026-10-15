package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (repository *Repository) ListJobs(ctx context.Context, query jobhost.JobListQuery) (jobhost.JobPage, error) {
	if err := query.Validate(); err != nil {
		return jobhost.JobPage{}, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobhost.JobPage{}, fmt.Errorf("begin job list: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(
		ctx,
		`
		SELECT accepted_sequence, `+jobColumns+` FROM execution_jobs
		WHERE accepted_sequence > ? AND installation_id = ?
		  AND json_extract(spec_json, '$.target.kind') = ?
		  AND COALESCE(json_extract(spec_json, '$.target.node_id'), '') = ?
		ORDER BY accepted_sequence LIMIT ?
	`,
		query.After,
		query.InstallationID,
		query.Target.Kind,
		query.Target.NodeID,
		query.Limit+1,
	)
	if err != nil {
		return jobhost.JobPage{}, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	page := jobhost.JobPage{
		Contract: jobcontract.CurrentContract, Jobs: []jobcontract.Snapshot{},
		NextCursor: strconv.FormatUint(query.After, 10),
	}
	for rows.Next() {
		var sequence int64
		var row storedJobRow
		if err := rows.Scan(
			&sequence,
			&row.id,
			&row.installation,
			&row.build,
			&row.kind,
			&row.revision,
			&row.status,
			&row.acceptedAt,
			&row.specSHA256,
			&row.commitment,
			&row.document,
			&row.sequence,
		); err != nil {
			return jobhost.JobPage{}, fmt.Errorf("scan job list: %w", err)
		}
		if sequence <= 0 || uint64(sequence) <= query.After {
			return jobhost.JobPage{}, storageCorrupt("job acceptance sequence")
		}
		if len(page.Jobs) == int(query.Limit) {
			page.HasMore = true
			break
		}
		snapshot, err := row.decode()
		if err != nil {
			return jobhost.JobPage{}, err
		}
		page.Jobs = append(page.Jobs, snapshot)
		page.NextCursor = strconv.FormatInt(sequence, 10)
	}
	if err := rows.Err(); err != nil {
		return jobhost.JobPage{}, fmt.Errorf("iterate job list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return jobhost.JobPage{}, fmt.Errorf("close job list: %w", err)
	}
	for _, snapshot := range page.Jobs {
		spec, err := queryJobSpec(ctx, tx, snapshot.Receipt.JobID)
		if err != nil {
			return jobhost.JobPage{}, err
		}
		sameOwner := spec.NodeInstallationID == query.InstallationID && spec.Target == query.Target
		if !sameOwner || snapshot.Validate(spec) != nil {
			return jobhost.JobPage{}, storageCorrupt("job list ownership")
		}
	}
	if err := tx.Commit(); err != nil {
		return jobhost.JobPage{}, fmt.Errorf("commit job list: %w", err)
	}
	return page, nil
}

func (repository *Repository) ReadRunReport(
	ctx context.Context,
	jobID string,
	reportID string,
) (jobhost.RunReportDocument, error) {
	if !domain.IsUUID(jobID) || !domain.IsUUID(reportID) {
		return jobhost.RunReportDocument{}, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobhost.RunReportDocument{}, fmt.Errorf("begin job report read: %w", err)
	}
	defer tx.Rollback()
	var row storedReportRow
	err = row.scan(tx.QueryRowContext(
		ctx,
		`
		SELECT report.id, report.schema_version, report.run_id, report.generated_at, report.document_json
		FROM reports AS report JOIN execution_job_runs AS owned ON owned.run_id = report.run_id
		WHERE owned.job_id = ? AND report.id = ?
	`,
		jobID,
		reportID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return jobhost.RunReportDocument{}, jobhost.ErrNotFound
	}
	if err != nil {
		return jobhost.RunReportDocument{}, fmt.Errorf("read owned report: %w", err)
	}
	if len(row.document) > jobcontract.MaxRunReportBytes {
		return jobhost.RunReportDocument{}, storageCorrupt("oversized job report")
	}
	report, err := row.decode()
	if err != nil {
		return jobhost.RunReportDocument{}, err
	}
	if err := validateReportStorage(ctx, tx, report); err != nil {
		return jobhost.RunReportDocument{}, err
	}
	spec, err := queryJobSpec(ctx, tx, jobID)
	if err != nil {
		return jobhost.RunReportDocument{}, err
	}
	jobRow, err := queryJobRow(ctx, tx, jobID)
	if err != nil {
		return jobhost.RunReportDocument{}, err
	}
	snapshot, err := jobRow.decode()
	if err != nil || snapshot.Validate(spec) != nil {
		return jobhost.RunReportDocument{}, storageCorrupt("job report owner")
	}
	matchedInput := false
	for _, input := range spec.PreparedRuns() {
		if input.Meta.ID == report.RunID && equalCanonicalDocuments(input.Snapshot, report.PlanSnapshot) {
			matchedInput = true
			break
		}
	}
	if !matchedInput {
		return jobhost.RunReportDocument{}, storageCorrupt("job report input")
	}
	reference := reportReference(
		report.ID,
		jobcontract.KindRun,
		report.RunID,
		row.document,
	)
	if err := validateReportJournal(
		ctx,
		tx,
		snapshot,
		reference,
	); err != nil {
		return jobhost.RunReportDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobhost.RunReportDocument{}, fmt.Errorf("commit job report read: %w", err)
	}
	return jobhost.RunReportDocument{Report: report, Reference: reference, Document: row.document}, nil
}

func validateReportJournal(
	ctx context.Context,
	tx *sql.Tx,
	snapshot jobcontract.Snapshot,
	reference jobcontract.ReportReference,
) error {
	rows, err := tx.QueryContext(
		ctx,
		`
		SELECT sequence, created_at, document_json FROM execution_job_events
		WHERE job_id = ? AND kind = 'report' AND json_extract(document_json, '$.payload.id') = ? LIMIT 2
	`,
		snapshot.Receipt.JobID,
		reference.ID,
	)
	if err != nil {
		return fmt.Errorf("read report journal: %w", err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var sequence int64
		var createdAt string
		var document []byte
		if err := rows.Scan(&sequence, &createdAt, &document); err != nil {
			return fmt.Errorf("scan report journal: %w", err)
		}
		event, err := decodeStoredJobEvent(document)
		if err != nil {
			return err
		}
		matchingIdentity := event.JobID == snapshot.Receipt.JobID && event.Kind == jobcontract.EventReport
		matchingSequence := int64(event.Sequence) == sequence && event.Sequence <= snapshot.LatestSequence
		matching := matchingIdentity && matchingSequence && formatTime(event.CreatedAt) == createdAt
		var saved jobcontract.ReportReference
		if err := json.Unmarshal(event.Payload, &saved); err != nil {
			return storageCorrupt("job report journal")
		}
		if !matching || saved != reference {
			return storageCorrupt("job report journal")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate report journal: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close report journal: %w", err)
	}
	if count != 1 {
		return storageCorrupt("missing or duplicate report journal")
	}
	return nil
}
