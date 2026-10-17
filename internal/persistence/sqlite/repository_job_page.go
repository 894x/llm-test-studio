package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// A page owns these raw facts from one read transaction. Projection never
// consults the repository, so releasing the connection cannot mix revisions.
type storedJobView struct {
	execution   storedExecution
	progress    storedJobProgress
	performance storedJobPerformance
}

func queryStoredJobView(ctx context.Context, tx *sql.Tx, id string) (storedJobView, error) {
	execution, err := queryStoredExecution(ctx, tx, id)
	if err != nil {
		return storedJobView{}, err
	}
	stored := storedJobView{execution: execution}
	if execution.kind() != string(jobcontract.KindPerformance) {
		return stored, nil
	}
	remote := execution.remote != nil
	stored.progress, err = queryStoredJobProgress(
		ctx,
		tx,
		id,
		remote,
	)
	if err == nil {
		stored.performance, err = queryStoredJobPerformance(
			ctx,
			tx,
			id,
			remote,
		)
	}
	return stored, err
}

func projectStoredJobView(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
	if err := ctx.Err(); err != nil {
		return jobs.JobView{}, err
	}
	read, err := stored.execution.decode()
	if err != nil {
		return jobs.JobView{}, err
	}
	var progress *jobs.ProgressView
	var ready bool
	if read.Execution != nil && read.Spec.Kind == jobcontract.KindPerformance {
		progress, err = stored.progress.decode(read)
		if err == nil {
			ready, err = stored.performance.decode(&read)
		}
		if err != nil {
			return jobs.JobView{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return jobs.JobView{}, err
	}
	view, err := jobs.ProjectJob(read.JobRead, progress, ready)
	if err != nil {
		return jobs.JobView{}, storageCorrupt("job execution projection")
	}
	return view, nil
}

func (stored storedJobView) byteSize() int64 {
	return stored.execution.byteSize() + int64(len(stored.progress.document)+len(stored.progress.kind)+
		len(stored.progress.createdAt)+8) + stored.performance.byteSize()
}

type storedExecution struct {
	id         string
	submission *storedSubmissionRow
	local      *storedLocalExecution
	remote     *storedRemoteMirrorRow
}

func queryStoredExecution(ctx context.Context, tx *sql.Tx, id string) (storedExecution, error) {
	stored := storedExecution{id: id}
	submission, err := queryStoredSubmission(ctx, tx, id)
	if err != nil && !errors.Is(err, jobs.ErrNotFound) {
		return storedExecution{}, err
	}
	if err == nil {
		stored.submission = &submission
	}
	var local, remote bool
	err = tx.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM execution_jobs WHERE id = ?),
		EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id = ?)`,
		id,
		id,
	).Scan(&local, &remote)
	if err != nil {
		return storedExecution{}, fmt.Errorf("read execution namespace: %w", err)
	}
	if local && remote {
		return storedExecution{}, storageCorrupt("conflicting execution namespaces")
	}
	if !local && !remote {
		return stored, nil
	}
	if err := boundExecutionDocuments(
		ctx,
		tx,
		id,
		local,
	); err != nil {
		return storedExecution{}, err
	}
	if local {
		row, err := queryStoredLocalExecution(ctx, tx, id)
		if err != nil {
			return storedExecution{}, err
		}
		stored.local = &row
		return stored, nil
	}
	row, err := queryStoredRemoteMirror(ctx, tx, id)
	if err != nil {
		return storedExecution{}, err
	}
	stored.remote = &row
	return stored, nil
}

func (stored storedExecution) kind() string {
	if stored.local != nil {
		return stored.local.row.kind
	}
	if stored.remote != nil {
		return stored.remote.kind
	}
	return ""
}

func (stored storedExecution) byteSize() int64 {
	size := int64(len(stored.id))
	if stored.submission != nil {
		size += stored.submission.byteSize()
	}
	if stored.local != nil {
		size += stored.local.byteSize()
	}
	if stored.remote != nil {
		size += stored.remote.byteSize()
	}
	return size
}

func (stored storedExecution) decode() (executionRead, error) {
	read := executionRead{}
	if stored.submission != nil {
		record, err := stored.submission.decode()
		if err != nil {
			return executionRead{}, err
		}
		read.Spec, read.Submission = record.Prepared.Spec, &record
		read.Location = jobs.Location{
			Kind: record.Prepared.Spec.Target.Kind, ConfigurationID: record.Prepared.ConfigurationID,
		}
	}
	local, remote := stored.local != nil, stored.remote != nil
	if read.Submission == nil && !remote {
		return executionRead{}, jobs.ErrNotFound
	}
	wantsLocal := read.Spec.Target.Kind == jobcontract.TargetLocal
	if read.Submission != nil {
		wrongNamespace := wantsLocal && remote || !wantsLocal && local
		executedBeforeSending := read.Submission.State == jobs.SubmissionPrepared && (local || remote)
		if wrongNamespace || executedBeforeSending {
			return executionRead{}, storageCorrupt("submission execution namespace")
		}
	}
	if !local && !remote {
		if wantsLocal && read.Submission.State == jobs.SubmissionAccepted {
			return executionRead{}, storageCorrupt("accepted local execution missing")
		}
		return read, nil
	}
	if wantsLocal {
		spec, snapshot, err := stored.local.decode(stored.id)
		if err != nil {
			return executionRead{}, err
		}
		read.Spec, read.Execution = spec, &snapshot
	} else {
		mirror, err := stored.remote.decode(stored.id)
		if err != nil {
			return executionRead{}, err
		}
		origin := mirror.Origin
		if read.Submission != nil {
			record := read.Submission
			matchingOrigin := origin.ConfigurationID == record.Prepared.ConfigurationID &&
				origin.NodeID == record.Prepared.Runner.NodeID && origin.InstallationID == record.Prepared.Runner.NodeInstallationID
			if !matchingOrigin {
				return executionRead{}, storageCorrupt("submission mirror origin")
			}
		}
		read.Location = jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: origin.ConfigurationID}
		read.Spec, read.Execution, read.mirror = mirror.Spec, &mirror.Snapshot, &mirror
	}
	if read.JobRead.Validate() != nil {
		return executionRead{}, storageCorrupt("execution differs from immutable source")
	}
	return read, nil
}

type storedLocalExecution struct {
	row  storedJobRow
	spec storedJobSpecRow
}

func queryStoredLocalExecution(ctx context.Context, tx *sql.Tx, id string) (storedLocalExecution, error) {
	stored := storedLocalExecution{}
	err := stored.row.scan(tx.QueryRowContext(
		ctx,
		`SELECT
		CASE WHEN length(CAST(id AS BLOB)) <= 36 THEN id ELSE '!' END,
		CASE WHEN length(CAST(installation_id AS BLOB)) <= 36 THEN installation_id ELSE '!' END,
		CASE WHEN length(CAST(engine_build_id AS BLOB)) <= 64 THEN engine_build_id ELSE '!' END,
		CASE WHEN length(CAST(kind AS BLOB)) <= 16 THEN kind ELSE '!' END,
		CASE WHEN typeof(revision) = 'integer' THEN revision ELSE -1 END,
		CASE WHEN length(CAST(status AS BLOB)) <= 16 THEN status ELSE '!' END,
		CASE WHEN length(CAST(accepted_at AS BLOB)) <= 64 THEN accepted_at ELSE '!' END,
		CASE WHEN length(CAST(spec_sha256 AS BLOB)) <= 64 THEN spec_sha256 ELSE '!' END,
		CASE WHEN length(CAST(secret_commitment AS BLOB)) <= 64 THEN secret_commitment ELSE '!' END,
		CASE WHEN length(CAST(snapshot_json AS BLOB)) <= ? THEN snapshot_json ELSE '' END,
		CASE WHEN typeof(last_sequence) = 'integer' THEN last_sequence ELSE -1 END,
		(SELECT CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM execution_job_events WHERE job_id = execution_jobs.id AND sequence = 1)
		FROM execution_jobs WHERE id = ?`,
		jobcontract.MaxStateBytes,
		jobcontract.MaxEventBytes,
		id,
	))
	if err != nil {
		return storedLocalExecution{}, fmt.Errorf("read job: %w", err)
	}
	stored.spec, err = queryStoredJobSpec(ctx, tx, id)
	if err != nil {
		return storedLocalExecution{}, err
	}
	return stored, nil
}

func (stored storedLocalExecution) byteSize() int64 {
	row, spec := stored.row, stored.spec
	return int64(len(spec.document) + len(row.document) + len(row.acceptedDocument) + len(spec.installation) +
		len(spec.build) + len(spec.kind) + len(spec.digest) + len(row.id) + len(row.installation) + len(row.build) +
		len(row.kind) + len(row.status) + len(row.acceptedAt) + len(row.specSHA256) + len(row.commitment) + 16)
}

func (stored storedLocalExecution) decode(id string) (jobcontract.JobSpec, jobcontract.Snapshot, error) {
	spec, err := stored.spec.decode(id)
	if err != nil {
		return spec, jobcontract.Snapshot{}, err
	}
	snapshot, err := stored.row.decode()
	return spec, snapshot, err
}

type storedJobProgress struct {
	present   bool
	sequence  uint64
	kind      string
	createdAt string
	document  []byte
}

func queryStoredJobProgress(
	ctx context.Context,
	tx *sql.Tx,
	id string,
	remote bool,
) (storedJobProgress, error) {
	statement := `SELECT CASE WHEN typeof(sequence) = 'integer' THEN sequence ELSE -1 END,
		CASE WHEN length(CAST(kind AS BLOB)) <= 32 THEN kind ELSE '!' END, '',
		CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM remote_job_events WHERE job_id = ? AND kind = 'progress' ORDER BY sequence DESC LIMIT 1`
	if !remote {
		statement = `SELECT CASE WHEN typeof(sequence) = 'integer' THEN sequence ELSE -1 END,
			CASE WHEN length(CAST(kind AS BLOB)) <= 32 THEN kind ELSE '!' END,
			CASE WHEN length(CAST(created_at AS BLOB)) <= 64 THEN created_at ELSE '!' END,
			CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
			FROM execution_job_events WHERE job_id = ? AND kind = 'progress' ORDER BY sequence DESC LIMIT 1`
	}
	var stored storedJobProgress
	err := tx.QueryRowContext(
		ctx,
		statement,
		jobs.MaxProgressViewBytes,
		id,
	).Scan(
		&stored.sequence,
		&stored.kind,
		&stored.createdAt,
		&stored.document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, nil
	}
	if err != nil {
		return storedJobProgress{}, fmt.Errorf("read job progress: %w", err)
	}
	stored.present = true
	return stored, nil
}

func (stored storedJobProgress) decode(read executionRead) (*jobs.ProgressView, error) {
	if !stored.present {
		return nil, nil
	}
	event, err := decodeStoredJobEvent(stored.document)
	if err != nil {
		return nil, err
	}
	matching := event.JobID == read.Spec.JobID && event.Kind == jobcontract.EventProgress &&
		event.Sequence == stored.sequence && string(event.Kind) == stored.kind
	if !matching || read.mirror == nil && formatTime(event.CreatedAt) != stored.createdAt {
		return nil, storageCorrupt("job progress envelope")
	}
	var progress quicktest.PerformanceProgress
	if err := decodeCanonical(event.Payload, &progress, nil); err != nil {
		return nil, storageCorrupt("job progress payload")
	}
	view := &jobs.ProgressView{Sequence: stored.sequence,
		RecordedAt: event.CreatedAt.Format(time.RFC3339Nano), Progress: progress}
	if view.Validate(*read.Execution) != nil {
		return nil, storageCorrupt("job progress state")
	}
	return view, nil
}
