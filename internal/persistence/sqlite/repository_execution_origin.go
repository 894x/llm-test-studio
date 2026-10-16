package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// RunExecutionOrigin returns only the executor frozen by durable admission.
// A current run without a Job owner is explicitly unmanaged and returns nil.
func (repository *Repository) RunExecutionOrigin(ctx context.Context, runID string) (*domain.ExecutionOrigin, error) {
	if ctx == nil || !domain.IsUUID(runID) {
		return nil, errors.New("run execution origin requires a context and canonical run UUID")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin run execution origin read: %w", err)
	}
	defer tx.Rollback()
	origin, err := queryRunExecutionOrigin(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit run execution origin read: %w", err)
	}
	return origin, nil
}

func queryRunExecutionOrigin(ctx context.Context, queryer relationQueryer, runID string) (*domain.ExecutionOrigin, error) {
	var bounded bool
	err := queryer.QueryRowContext(ctx, `SELECT
		length(CAST(revision.document_json AS BLOB)) BETWEEN 1 AND ? AND
		length(CAST(revision.snapshot_json AS BLOB)) BETWEEN 1 AND ?
		FROM execution_runs AS root JOIN execution_run_revisions AS revision
		ON revision.run_id = root.id AND revision.revision = root.current_revision WHERE root.id = ?`,
		MaxReportProjectionDocumentBytes, MaxReportProjectionDocumentBytes, runID).Scan(&bounded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("inspect run origin document budget: %w", err)
	}
	if err == nil && !bounded {
		return nil, storageCorrupt("run execution origin document budget")
	}
	run, err := queryCurrentRun(ctx, queryer, runID)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, `SELECT 'owned', job_id, position FROM execution_job_runs WHERE run_id = ?
		UNION ALL SELECT 'mirror', job_id, position FROM remote_job_runs WHERE run_id = ? LIMIT 3`, runID, runID)
	if err != nil {
		return nil, fmt.Errorf("read run execution owner: %w", err)
	}
	defer rows.Close()
	type owner struct {
		namespace, jobID string
		position         int
	}
	owners := []owner{}
	for rows.Next() {
		var item owner
		if err := rows.Scan(&item.namespace, &item.jobID, &item.position); err != nil {
			return nil, fmt.Errorf("scan run execution owner: %w", err)
		}
		owners = append(owners, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run execution owners: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close run execution owners: %w", err)
	}
	if len(owners) == 0 {
		return nil, nil
	}
	if len(owners) != 1 {
		return nil, storageCorrupt("duplicate run execution namespace")
	}
	owned := owners[0]
	var spec jobcontract.JobSpec
	var snapshot jobcontract.Snapshot
	if owned.namespace == "owned" {
		spec, err = queryJobSpec(ctx, queryer, owned.jobID)
		if err == nil {
			var row storedJobRow
			row, err = queryJobRow(ctx, queryer, owned.jobID)
			if err == nil {
				snapshot, err = row.decode()
			}
		}
	} else {
		mirror, readErr := queryRemoteMirror(ctx, queryer, owned.jobID)
		err, spec, snapshot = readErr, mirror.Spec, mirror.Snapshot
	}
	if err != nil {
		return nil, relationStorageError(ctx, "run execution owner", err)
	}
	var duplicate bool
	if err := queryer.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM execution_jobs WHERE id = ?) AND
		EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id = ?)`, owned.jobID, owned.jobID).Scan(&duplicate); err != nil {
		return nil, fmt.Errorf("inspect run execution namespace: %w", err)
	}
	inputs := spec.PreparedRuns()
	validPosition := owned.position >= 0 && owned.position < len(inputs)
	if duplicate || !validPosition || snapshot.Validate(spec) != nil {
		return nil, storageCorrupt("run execution ownership")
	}
	input := inputs[owned.position]
	if input.Meta.ID != runID || input.PlanID != run.PlanID() || !equalCanonicalDocuments(input.Snapshot, run.Snapshot()) {
		return nil, storageCorrupt("run execution frozen input")
	}
	origin := snapshot.Receipt.Origin
	return &origin, nil
}

func validateRunReportOrigin(ctx context.Context, queryer relationQueryer, runID string, actual *domain.ExecutionOrigin) error {
	expected, err := queryRunExecutionOrigin(ctx, queryer, runID)
	if err != nil {
		return err
	}
	return validateStoredOrigin(expected, actual)
}

func validateStoredOrigin(expected, actual *domain.ExecutionOrigin) error {
	if expected == nil && actual == nil {
		return nil
	}
	if expected == nil || actual == nil || *expected != *actual || actual.Validate() != nil {
		return storageCorrupt("report execution origin differs from durable admission")
	}
	return nil
}

func decodeAcceptedOrigin(document []byte, snapshot jobcontract.Snapshot) (validatedAdmission, error) {
	accepted, err := admissionDocuments.resolve(document)
	if err != nil {
		return validatedAdmission{}, err
	}
	got, want := snapshot.Receipt, accepted.receipt
	sameIdentity := got.Contract == want.Contract && got.JobID == want.JobID && got.Kind == want.Kind
	sameExecutor := got.Origin == want.Origin && got.AcceptedAt.Equal(want.AcceptedAt)
	sameChildren := slices.Equal(got.RunIDs, want.RunIDs) && slices.Equal(got.ReportIDs, want.ReportIDs)
	if snapshot.ValidateState() != nil || !sameIdentity || !sameExecutor || !sameChildren || got.ComparisonID != want.ComparisonID {
		return validatedAdmission{}, storageCorrupt("execution origin changed after acceptance")
	}
	return accepted, nil
}

func validateEventExecutionOrigin(event jobcontract.Event, expected domain.ExecutionOrigin) error {
	var origin domain.ExecutionOrigin
	switch event.Kind {
	case jobcontract.EventAccepted:
		var accepted jobcontract.AcceptedEvent
		if err := decodeCanonical(event.Payload, &accepted, nil); err != nil {
			return storageCorrupt("accepted event execution origin")
		}
		origin = accepted.Snapshot.Receipt.Origin
	case jobcontract.EventState:
		var snapshot jobcontract.Snapshot
		if err := decodeCanonical(event.Payload, &snapshot, func() error { return snapshot.ValidateState() }); err != nil {
			return storageCorrupt("state event execution origin")
		}
		origin = snapshot.Receipt.Origin
	case jobcontract.EventCommand:
		var confirmation jobcontract.CommandConfirmation
		if err := decodeCanonical(event.Payload, &confirmation, func() error { return confirmation.Validate() }); err != nil {
			return storageCorrupt("command event execution origin")
		}
		origin = confirmation.Snapshot.Receipt.Origin
	default:
		return nil
	}
	if origin != expected {
		return storageCorrupt("event execution origin differs from durable admission")
	}
	return nil
}
