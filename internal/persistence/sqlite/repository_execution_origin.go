package sqlite

import (
	"context"
	"crypto/sha256"
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
	run, err := queryBoundedCurrentRun(ctx, queryer, runID)
	if err != nil {
		return nil, err
	}
	return queryValidatedRunExecutionOrigin(ctx, queryer, run)
}

func queryBoundedCurrentRun(ctx context.Context, queryer rowQueryer, runID string) (domain.Run, error) {
	var bounded bool
	err := queryer.QueryRowContext(ctx, `SELECT
		length(CAST(revision.document_json AS BLOB)) BETWEEN 1 AND ? AND
		length(CAST(revision.snapshot_json AS BLOB)) BETWEEN 1 AND ?
		FROM execution_runs AS root JOIN execution_run_revisions AS revision
		ON revision.run_id = root.id AND revision.revision = root.current_revision WHERE root.id = ?`,
		MaxReportProjectionDocumentBytes, MaxReportProjectionDocumentBytes, runID).Scan(&bounded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, fmt.Errorf("inspect run origin document budget: %w", err)
	}
	if err == nil && !bounded {
		return domain.Run{}, storageCorrupt("run execution origin document budget")
	}
	return queryCurrentRun(ctx, queryer, runID)
}

// The caller has already validated the current durable Run in this read or
// write operation. Reuse it while still checking every current owner document.
func queryValidatedRunExecutionOrigin(ctx context.Context, queryer relationQueryer, run domain.Run) (*domain.ExecutionOrigin, error) {
	runID := run.Meta().ID
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
	var admission validatedAdmission
	var snapshot jobcontract.Snapshot
	if owned.namespace == "owned" {
		admission, snapshot, err = queryOwnedRunAdmission(ctx, queryer, owned.jobID)
	} else {
		admission, snapshot, err = queryMirroredRunAdmission(ctx, queryer, owned.jobID)
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
	validPosition := owned.position >= 0 && owned.position < len(admission.runs)
	if duplicate || !validPosition {
		return nil, storageCorrupt("run execution ownership")
	}
	input := admission.runs[owned.position]
	snapshotDocument, err := marshalCanonical(run.Snapshot())
	if err != nil {
		return nil, storageCorrupt("run execution current snapshot")
	}
	validIdentity := input.runID == runID && input.planID == run.PlanID()
	if !validIdentity || input.snapshotSHA256 != sha256.Sum256(snapshotDocument) {
		return nil, storageCorrupt("run execution frozen input")
	}
	origin := snapshot.Receipt.Origin
	return &origin, nil
}

func queryOwnedRunAdmission(ctx context.Context, queryer rowQueryer, jobID string) (validatedAdmission, jobcontract.Snapshot, error) {
	row, err := queryJobRow(ctx, queryer, jobID)
	if err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, err
	}
	snapshot, err := row.decode()
	if err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, err
	}
	admission, err := decodeAcceptedOrigin(row.acceptedDocument, snapshot)
	if err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, err
	}
	var specDocument []byte
	err = queryer.QueryRowContext(ctx, `SELECT
		CASE WHEN length(CAST(spec_json AS BLOB)) <= ? THEN spec_json ELSE '' END
		FROM execution_jobs WHERE id = ?`, jobcontract.MaxSubmissionBytes, jobID).Scan(&specDocument)
	if err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, fmt.Errorf("read run execution frozen spec: %w", err)
	}
	if admission.specDocumentSHA256 != sha256.Sum256(specDocument) {
		return validatedAdmission{}, jobcontract.Snapshot{}, storageCorrupt("run execution frozen spec bytes")
	}
	return admission, snapshot, nil
}

func queryMirroredRunAdmission(ctx context.Context, queryer rowQueryer, jobID string) (validatedAdmission, jobcontract.Snapshot, error) {
	var configurationID, nodeID, installationID, digest string
	var sequence uint64
	var specDocument, snapshotDocument, acceptedDocument []byte
	err := queryer.QueryRowContext(ctx, `SELECT configuration_id, node_id, installation_id, spec_sha256,
		CASE WHEN length(CAST(spec_json AS BLOB)) <= ? THEN spec_json ELSE '' END,
		CASE WHEN length(CAST(snapshot_json AS BLOB)) <= ? THEN snapshot_json ELSE '' END, applied_sequence,
		(SELECT CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		 FROM remote_job_events WHERE job_id = remote_job_mirrors.job_id AND sequence = 1)
		FROM remote_job_mirrors WHERE job_id = ?`,
		jobcontract.MaxSubmissionBytes, jobcontract.MaxStateBytes, jobcontract.MaxEventBytes, jobID).Scan(
		&configurationID, &nodeID, &installationID, &digest,
		&specDocument, &snapshotDocument, &sequence, &acceptedDocument,
	)
	if err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, fmt.Errorf("read mirrored run execution source: %w", err)
	}
	var snapshot jobcontract.Snapshot
	if err := decodeCanonical(snapshotDocument, &snapshot, func() error { return snapshot.ValidateState() }); err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, storageCorrupt("mirrored run execution snapshot")
	}
	admission, err := decodeAcceptedOrigin(acceptedDocument, snapshot)
	if err != nil {
		return validatedAdmission{}, jobcontract.Snapshot{}, err
	}
	origin := snapshot.Receipt.Origin
	validConfiguration := domain.IsUUID(configurationID) && origin.Kind == "remote"
	matchingOrigin := origin.NodeID == nodeID && origin.InstallationID == installationID
	matchingIdentity := snapshot.Receipt.JobID == jobID && snapshot.LatestSequence == sequence
	matchingSpec := admission.specSHA256 == digest && admission.specDocumentSHA256 == sha256.Sum256(specDocument)
	if !validConfiguration || !matchingOrigin || !matchingIdentity || !matchingSpec {
		return validatedAdmission{}, jobcontract.Snapshot{}, storageCorrupt("mirrored run execution source identity")
	}
	return admission, snapshot, nil
}

func validateRunReportOrigin(ctx context.Context, queryer relationQueryer, runID string, actual *domain.ExecutionOrigin) error {
	expected, err := queryRunExecutionOrigin(ctx, queryer, runID)
	if err != nil {
		return err
	}
	return validateStoredOrigin(expected, actual)
}

func validateReportOriginForRun(ctx context.Context, queryer relationQueryer, run domain.Run, actual *domain.ExecutionOrigin) error {
	expected, err := queryValidatedRunExecutionOrigin(ctx, queryer, run)
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
