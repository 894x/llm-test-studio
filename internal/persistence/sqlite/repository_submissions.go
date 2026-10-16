package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ jobs.SubmissionRepository = (*Repository)(nil)

const submissionColumns = `job_id, configuration_id, node_id, installation_id,
	revision, state, spec_sha256, document_json`

const submissionReadColumns = `
	CASE WHEN length(CAST(job_id AS BLOB)) <= 36 THEN job_id ELSE '!' END,
	CASE WHEN length(CAST(configuration_id AS BLOB)) <= 36 THEN configuration_id ELSE '!' END,
	CASE WHEN length(CAST(node_id AS BLOB)) <= 36 THEN node_id ELSE '!' END,
	CASE WHEN length(CAST(installation_id AS BLOB)) <= 36 THEN installation_id ELSE '!' END,
	CASE WHEN length(CAST(revision AS BLOB)) <= 20 THEN revision ELSE -1 END,
	CASE WHEN length(CAST(state AS BLOB)) <= 32 THEN state ELSE '!' END,
	CASE WHEN length(CAST(spec_sha256 AS BLOB)) <= 64 THEN spec_sha256 ELSE '!' END,
	CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE NULL END`

const maxSubmissionRecordBytes = jobcontract.MaxSubmissionBytes + jobcontract.MaxRunnerInfoBytes +
	jobcontract.MaxStateBytes + 4096

func (repository *Repository) CreateSubmission(
	ctx context.Context,
	record jobs.SubmissionRecord,
) (jobs.SubmissionRecord, error) {
	if record.Validate() != nil || record.State != jobs.SubmissionPrepared {
		return jobs.SubmissionRecord{}, jobs.ErrInvalid
	}
	document, err := marshalCanonical(record)
	if err != nil || len(document) > maxSubmissionRecordBytes {
		return jobs.SubmissionRecord{}, jobs.ErrInvalid
	}
	// Decode our owned canonical bytes before holding the transaction, so the
	// caller cannot change mutable prepared values during admission.
	record, err = decodeSubmissionRecord(document)
	if err != nil {
		return jobs.SubmissionRecord{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.SubmissionRecord{}, fmt.Errorf("begin submission record: %w", err)
	}
	defer tx.Rollback()
	previous, err := querySubmission(ctx, tx, record.Prepared.Spec.JobID)
	if err == nil {
		if !record.SamePreparation(previous) {
			return jobs.SubmissionRecord{}, jobs.ErrConflict
		}
		return previous, nil
	}
	if !errors.Is(err, jobs.ErrNotFound) {
		return jobs.SubmissionRecord{}, err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO job_submissions (`+submissionColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.Prepared.Spec.JobID,
		record.Prepared.ConfigurationID,
		record.Prepared.Runner.NodeID,
		record.Prepared.Runner.NodeInstallationID,
		record.Revision,
		record.State,
		record.SpecSHA256,
		document,
	)
	if err != nil {
		return jobs.SubmissionRecord{}, classifySubmissionWrite("create submission record", err)
	}
	if err := tx.Commit(); err != nil {
		return jobs.SubmissionRecord{}, classifySubmissionWrite("commit submission record", err)
	}
	return record, nil
}

func (repository *Repository) GetSubmission(ctx context.Context, id string) (jobs.SubmissionRecord, error) {
	if !domain.IsUUID(id) {
		return jobs.SubmissionRecord{}, jobs.ErrInvalid
	}
	return querySubmission(ctx, repository.db, id)
}

func (repository *Repository) UpdateSubmission(
	ctx context.Context,
	next jobs.SubmissionRecord,
	expected uint64,
) error {
	validRevision := expected > 0 && next.Revision == expected+1
	if next.Validate() != nil || !validRevision {
		return jobs.ErrInvalid
	}
	document, err := marshalCanonical(next)
	if err != nil || len(document) > maxSubmissionRecordBytes {
		return jobs.ErrInvalid
	}
	next, err = decodeSubmissionRecord(document)
	if err != nil {
		return err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin submission update: %w", err)
	}
	defer tx.Rollback()
	previous, err := querySubmission(ctx, tx, next.Prepared.Spec.JobID)
	if err != nil {
		return err
	}
	if previous.Revision != expected {
		return jobs.ErrConflict
	}
	if err := previous.ValidateSuccessor(next); err != nil {
		return err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE job_submissions
		SET revision = ?, state = ?, document_json = ? WHERE job_id = ? AND revision = ?`,
		next.Revision,
		next.State,
		document,
		next.Prepared.Spec.JobID,
		expected,
	)
	if err != nil {
		return classifySubmissionWrite("update submission record", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read submission update count: %w", err)
	}
	if count != 1 {
		return jobs.ErrConflict
	}
	return classifySubmissionWrite("commit submission update", tx.Commit())
}

func (repository *Repository) ListPendingSubmissions(
	ctx context.Context,
	after string,
	limit uint32,
) (jobs.SubmissionPage, error) {
	validCursor := after == "" || domain.IsUUID(after)
	if !validCursor || limit == 0 || limit > jobs.MaxSubmissionPageSize {
		return jobs.SubmissionPage{}, jobs.ErrInvalid
	}
	rows, err := repository.db.QueryContext(
		ctx,
		`SELECT `+submissionReadColumns+`
		FROM job_submissions WHERE state IN ('prepared', 'pending') AND job_id > ?
		ORDER BY job_id LIMIT ?`,
		maxSubmissionRecordBytes,
		after,
		limit+1,
	)
	if err != nil {
		return jobs.SubmissionPage{}, fmt.Errorf("list pending submissions: %w", err)
	}
	defer rows.Close()
	page := jobs.SubmissionPage{Records: []jobs.SubmissionRecord{}, NextCursor: after}
	var used int
	for rows.Next() {
		var stored storedSubmissionRow
		if err := stored.scan(rows); err != nil {
			return jobs.SubmissionPage{}, err
		}
		record, err := stored.decode()
		if err != nil {
			return jobs.SubmissionPage{}, err
		}
		size := len(stored.document)
		if len(page.Records) == int(limit) || used+size > jobs.MaxSubmissionPageBytes {
			page.HasMore = true
			break
		}
		page.Records = append(page.Records, record)
		page.NextCursor = record.Prepared.Spec.JobID
		used += size
	}
	if err := rows.Err(); err != nil {
		return jobs.SubmissionPage{}, fmt.Errorf("iterate pending submissions: %w", err)
	}
	return page, nil
}

func querySubmission(ctx context.Context, queryer rowQueryer, id string) (jobs.SubmissionRecord, error) {
	stored, err := queryStoredSubmission(ctx, queryer, id)
	if err != nil {
		return jobs.SubmissionRecord{}, err
	}
	return stored.decode()
}

func queryStoredSubmission(ctx context.Context, queryer rowQueryer, id string) (storedSubmissionRow, error) {
	var stored storedSubmissionRow
	err := stored.scan(queryer.QueryRowContext(
		ctx,
		`SELECT `+submissionReadColumns+` FROM job_submissions WHERE job_id = ?`,
		maxSubmissionRecordBytes,
		id,
	))
	if err != nil {
		return storedSubmissionRow{}, err
	}
	return stored, nil
}

func decodeSubmissionRecord(document []byte) (jobs.SubmissionRecord, error) {
	corrupt := fmt.Errorf("%w: submission document", ErrCorrupt)
	if len(document) > maxSubmissionRecordBytes {
		return jobs.SubmissionRecord{}, corrupt
	}
	// Reject duplicate and noncanonical envelope fields before typed decoding.
	// Construct the record from the individually checked documents, so a second
	// unmarshal cannot allocate an earlier, unchecked duplicate value.
	fields, err := submissionFields(document, []string{
		"prepared", "spec_sha256", "revision", "state", "observation", "created_at", "updated_at", "receipt",
	}, "receipt")
	if err != nil {
		return jobs.SubmissionRecord{}, corrupt
	}
	prepared, err := submissionFields(fields["prepared"], []string{"configuration_id", "runner", "spec"}, "")
	if err != nil {
		return jobs.SubmissionRecord{}, corrupt
	}
	var record jobs.SubmissionRecord
	record.Prepared.Spec, err = jobcontract.DecodeSpec(bytes.NewReader(prepared["spec"]))
	if err != nil {
		return jobs.SubmissionRecord{}, corrupt
	}
	record.Prepared.Runner, err = jobcontract.DecodeRunnerInfo(bytes.NewReader(prepared["runner"]))
	if err != nil {
		return jobs.SubmissionRecord{}, corrupt
	}
	if !bytes.Equal(bytes.TrimSpace(fields["receipt"]), []byte("null")) {
		receipt, err := jobcontract.DecodeReceipt(bytes.NewReader(fields["receipt"]))
		if err != nil {
			return jobs.SubmissionRecord{}, corrupt
		}
		record.Receipt = &receipt
	}
	if json.Unmarshal(prepared["configuration_id"], &record.Prepared.ConfigurationID) != nil {
		return jobs.SubmissionRecord{}, corrupt
	}
	for name, destination := range map[string]any{
		"spec_sha256": &record.SpecSHA256, "revision": &record.Revision, "state": &record.State,
		"observation": &record.Observation, "created_at": &record.CreatedAt, "updated_at": &record.UpdatedAt,
	} {
		if json.Unmarshal(fields[name], destination) != nil {
			return jobs.SubmissionRecord{}, corrupt
		}
	}
	if record.Validate() != nil {
		return jobs.SubmissionRecord{}, corrupt
	}
	canonical, err := marshalCanonical(record)
	if err != nil || !bytes.Equal(canonical, document) {
		return jobs.SubmissionRecord{}, corrupt
	}
	return record, nil
}

func submissionFields(document []byte, allowed []string, nullable string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, ErrCorrupt
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		validName := err == nil && ok && slices.Contains(allowed, name) && fields[name] == nil
		if !validName {
			return nil, ErrCorrupt
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, ErrCorrupt
		}
		if name != nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrCorrupt
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrCorrupt
	}
	if decoder.Decode(new(any)) != io.EOF || len(fields) != len(allowed) {
		return nil, ErrCorrupt
	}
	return fields, nil
}

func classifySubmissionWrite(operation string, err error) error {
	err = classifyWriteError(operation, err)
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: %s", jobs.ErrConflict, operation)
	}
	return err
}
