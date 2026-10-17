package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
)

// LocalExecutorIdentity initializes only an unused execution/outbox namespace.
// Missing or damaged identity beside existing work requires explicit recovery;
// startup must never mint another owner and abandon its pending jobs.
func (repository *Repository) LocalExecutorIdentity(ctx context.Context) (jobhost.LocalIdentity, error) {
	if ctx == nil {
		return jobhost.LocalIdentity{}, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobhost.LocalIdentity{}, fmt.Errorf("begin local executor identity: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_executor_identity`).Scan(&count); err != nil {
		return jobhost.LocalIdentity{}, fmt.Errorf("count local executor identity: %w", err)
	}
	if count > 1 {
		return jobhost.LocalIdentity{}, fmt.Errorf("%w: multiple local executor identities", ErrCorrupt)
	}
	var identity jobhost.LocalIdentity
	var nodeID, installationID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT
		CASE WHEN typeof(node_id) = 'text' AND length(CAST(node_id AS BLOB)) = 36 THEN node_id ELSE NULL END,
		CASE WHEN typeof(installation_id) = 'text' AND length(CAST(installation_id AS BLOB)) = 36 THEN installation_id ELSE NULL END,
		CASE WHEN typeof(commitment_key) = 'blob' AND length(commitment_key) = 32 THEN commitment_key ELSE NULL END
		FROM local_executor_identity WHERE singleton = 1`).Scan(
		&nodeID, &installationID, &identity.CommitmentKey,
	)
	if err == nil {
		identity.NodeID, identity.InstallationID = nodeID.String, installationID.String
		if identity.Validate() != nil {
			identity.Clear()
			return jobhost.LocalIdentity{}, fmt.Errorf("%w: invalid local executor identity", ErrCorrupt)
		}
		return identity, nil
	}
	identity.Clear()
	if !errors.Is(err, sql.ErrNoRows) {
		return jobhost.LocalIdentity{}, fmt.Errorf("read local executor identity: %w", err)
	}
	if count != 0 {
		return jobhost.LocalIdentity{}, fmt.Errorf("%w: invalid local executor singleton", ErrCorrupt)
	}
	var used bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_jobs)
		OR EXISTS(SELECT 1 FROM job_submissions)`).Scan(&used)
	if err != nil {
		return jobhost.LocalIdentity{}, fmt.Errorf("check local executor namespace: %w", err)
	}
	if used {
		return jobhost.LocalIdentity{}, fmt.Errorf("%w: restore missing local executor identity before startup", ErrCorrupt)
	}
	node, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		return jobhost.LocalIdentity{}, err
	}
	installation, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		return jobhost.LocalIdentity{}, err
	}
	identity = jobhost.LocalIdentity{NodeID: node.ID, InstallationID: installation.ID, CommitmentKey: make([]byte, 32)}
	if _, err := rand.Read(identity.CommitmentKey); err != nil {
		identity.Clear()
		return jobhost.LocalIdentity{}, err
	}
	if err := identity.Validate(); err != nil {
		identity.Clear()
		return jobhost.LocalIdentity{}, err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO local_executor_identity(singleton, node_id, installation_id, commitment_key)
		VALUES (1, ?, ?, ?)`,
		identity.NodeID,
		identity.InstallationID,
		identity.CommitmentKey,
	)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		identity.Clear()
		return jobhost.LocalIdentity{}, fmt.Errorf("save local executor identity: %w", err)
	}
	return identity, nil
}
