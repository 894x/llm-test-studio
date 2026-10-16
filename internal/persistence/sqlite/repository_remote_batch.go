package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type remoteBatchRunBinding struct {
	position       int
	planID         string
	snapshotSHA256 [sha256.Size]byte
}

type remoteBatchRun struct {
	run      domain.Run
	snapshot domain.RunSnapshot
	sealed   int
}

// This state belongs to one ApplyRemoteBatch transaction. Only runs declared
// in its already validated Spec can enter the cache. Their total frozen input
// is bounded by MaxSubmissionBytes, and no state survives commit or rollback.
type remoteBatchState struct {
	jobID    string
	bindings map[string]remoteBatchRunBinding
	runs     map[string]remoteBatchRun
}

func newRemoteBatchState(spec jobcontract.JobSpec) (*remoteBatchState, error) {
	inputs := spec.PreparedRuns()
	state := &remoteBatchState{
		jobID: spec.JobID, bindings: make(map[string]remoteBatchRunBinding, len(inputs)),
		runs: make(map[string]remoteBatchRun, len(inputs)),
	}
	for position, input := range inputs {
		document, err := marshalCanonical(input.Snapshot)
		if err != nil {
			return nil, storageCorrupt("remote batch frozen snapshot")
		}
		state.bindings[input.Meta.ID] = remoteBatchRunBinding{
			position: position, planID: input.PlanID, snapshotSHA256: sha256.Sum256(document),
		}
	}
	return state, nil
}

func (state *remoteBatchState) currentRun(ctx context.Context, tx *sql.Tx, runID string) (remoteBatchRun, error) {
	binding, declared := state.bindings[runID]
	if !declared {
		return remoteBatchRun{}, remoteresults.ErrConflict
	}
	var owner string
	var position, sealed, reportCount int
	var revision uint64
	var local bool
	err := tx.QueryRowContext(ctx, `SELECT owned.job_id, owned.position, root.current_revision, root.sealed,
		EXISTS(SELECT 1 FROM execution_job_runs WHERE run_id = owned.run_id),
		(SELECT COUNT(*) FROM reports WHERE run_id = owned.run_id)
		FROM remote_job_runs AS owned JOIN execution_runs AS root ON root.id = owned.run_id
		WHERE owned.run_id = ?`, runID).Scan(&owner, &position, &revision, &sealed, &local, &reportCount)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteBatchRun{}, remoteresults.ErrConflict
	}
	if err != nil {
		return remoteBatchRun{}, fmt.Errorf("read remote batch run owner: %w", err)
	}
	validOwner := owner == state.jobID && position == binding.position && !local
	validRoot := revision > 0 && (sealed == 0 || sealed == 1) && reportCount == sealed
	if !validOwner || !validRoot {
		return remoteBatchRun{}, storageCorrupt("remote batch run ownership")
	}
	if cached, exists := state.runs[runID]; exists && cached.run.Meta().Revision == revision && cached.sealed == sealed {
		return cached, nil
	}
	run, err := queryBoundedCurrentRun(ctx, tx, runID)
	if err != nil {
		return remoteBatchRun{}, err
	}
	snapshot := run.Snapshot()
	document, err := marshalCanonical(snapshot)
	if err != nil || run.PlanID() != binding.planID || sha256.Sum256(document) != binding.snapshotSHA256 {
		return remoteBatchRun{}, storageCorrupt("remote batch run frozen snapshot")
	}
	cached := remoteBatchRun{run: run, snapshot: snapshot, sealed: sealed}
	state.runs[runID] = cached
	return cached, nil
}

func (state *remoteBatchState) invalidateRuns() {
	clear(state.runs)
}

func sameRemoteReceiptIdentity(left, right jobcontract.Receipt) bool {
	sameJob := left.Contract == right.Contract && left.JobID == right.JobID && left.Kind == right.Kind
	sameSource := left.Origin == right.Origin && left.AcceptedAt.Equal(right.AcceptedAt)
	sameChildren := slices.Equal(left.RunIDs, right.RunIDs) && slices.Equal(left.ReportIDs, right.ReportIDs)
	return sameJob && sameSource && sameChildren && left.ComparisonID == right.ComparisonID
}
