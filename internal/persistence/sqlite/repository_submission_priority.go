package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// ListRemoteSubmissionIDs returns bounded scheduling hints, not submission or
// execution projections. Only selected rows' small identity fields are decoded;
// the synchronizer still validates the complete remote spec and event journal.
func (repository *Repository) ListRemoteSubmissionIDs(
	ctx context.Context,
	origin remoteresults.Origin,
	after string,
	limit uint32,
) (remoteresults.PriorityPage, error) {
	if !validJobPage(ctx, after, limit) || origin.Validate() != nil {
		return remoteresults.PriorityPage{}, jobs.ErrInvalid
	}
	rows, err := repository.db.QueryContext(
		ctx,
		`WITH candidates AS MATERIALIZED (
		SELECT job_id, configuration_id, node_id, installation_id, revision, state, spec_sha256,
		CASE WHEN length(CAST(document_json AS BLOB)) BETWEEN 1 AND ? THEN document_json ELSE NULL END AS document_json
		FROM job_submissions
		WHERE configuration_id = ? AND node_id = ? AND installation_id = ?
		AND state IN ('pending', 'accepted') AND job_id > ? ORDER BY job_id LIMIT ?
		)
		SELECT CASE WHEN length(CAST(job_id AS BLOB)) = 36 THEN job_id ELSE '' END,
		configuration_id, node_id, installation_id, revision, state,
		CASE WHEN length(CAST(spec_sha256 AS BLOB)) = 64 THEN spec_sha256 ELSE '' END,
		json_extract(document_json, '$.prepared.configuration_id', '$.prepared.runner.node_id',
		'$.prepared.runner.node_installation_id', '$.prepared.spec.job_id', '$.prepared.spec.contract',
		'$.prepared.spec.target.kind', '$.prepared.spec.target.node_id', '$.prepared.spec.node_installation_id',
		'$.revision', '$.state', '$.spec_sha256'),
		(SELECT json_group_array(key) FROM json_each(candidates.document_json)),
		(SELECT json_group_array(key) FROM json_each(candidates.document_json, '$.prepared'))
		FROM candidates ORDER BY job_id`,
		maxSubmissionRecordBytes,
		origin.ConfigurationID,
		origin.NodeID,
		origin.InstallationID,
		after,
		limit+1,
	)
	if err != nil {
		return remoteresults.PriorityPage{}, fmt.Errorf("list remote submission ids: %w", err)
	}
	defer rows.Close()
	page := remoteresults.PriorityPage{JobIDs: []string{}, NextCursor: after}
	for rows.Next() {
		var id, configurationID, nodeID, installationID, state, digest string
		var revision uint64
		var metadata, rootKeys, preparedKeys []byte
		if err := rows.Scan(
			&id,
			&configurationID,
			&nodeID,
			&installationID,
			&revision,
			&state,
			&digest,
			&metadata,
			&rootKeys,
			&preparedKeys,
		); err != nil {
			return remoteresults.PriorityPage{}, fmt.Errorf("scan remote submission id: %w", err)
		}
		owner := remoteresults.Origin{ConfigurationID: configurationID, NodeID: nodeID, InstallationID: installationID}
		validEnvelope := priorityEnvelopeKeys(rootKeys, []string{
			"created_at", "observation", "prepared", "receipt", "revision", "spec_sha256", "state", "updated_at",
		}) && priorityEnvelopeKeys(preparedKeys, []string{"configuration_id", "runner", "spec"})
		validIdentity := validPriorityIdentity(
			metadata,
			id,
			owner,
			revision,
			state,
			digest,
		)
		if !validEnvelope || owner != origin || !validIdentity {
			return remoteresults.PriorityPage{}, storageCorrupt("remote submission identity")
		}
		if len(page.JobIDs) == int(limit) {
			page.HasMore = true
			break
		}
		page.JobIDs = append(page.JobIDs, id)
		page.NextCursor = id
	}
	if err := rows.Err(); err != nil {
		return remoteresults.PriorityPage{}, fmt.Errorf("iterate remote submission ids: %w", err)
	}
	return page, nil
}

func priorityEnvelopeKeys(document []byte, expected []string) bool {
	keys := []string{}
	if len(document) > 4096 || json.Unmarshal(document, &keys) != nil {
		return false
	}
	slices.Sort(keys)
	return slices.Equal(keys, expected)
}

func validPriorityIdentity(
	document []byte,
	id string,
	origin remoteresults.Origin,
	revision uint64,
	state string,
	digest string,
) bool {
	var fields []json.RawMessage
	if len(document) > 4096 || json.Unmarshal(document, &fields) != nil || len(fields) != 11 {
		return false
	}
	var configurationID, runnerNodeID, runnerInstallationID, jobID, targetKind, targetNodeID string
	var installationID, recordState, specDigest string
	var contract int
	var recordRevision uint64
	destinations := []any{&configurationID, &runnerNodeID, &runnerInstallationID, &jobID, &contract,
		&targetKind, &targetNodeID, &installationID, &recordRevision, &recordState, &specDigest}
	for index, destination := range destinations {
		if string(fields[index]) == "null" || json.Unmarshal(fields[index], destination) != nil {
			return false
		}
	}
	validRecord := domain.IsUUID(id) && revision > 0 && jobcontract.IsSHA256(digest) &&
		recordRevision == revision && recordState == state && specDigest == digest &&
		(state == string(jobs.SubmissionPending) || state == string(jobs.SubmissionAccepted))
	sameOwner := configurationID == origin.ConfigurationID && runnerNodeID == origin.NodeID &&
		runnerInstallationID == origin.InstallationID && targetNodeID == origin.NodeID && installationID == origin.InstallationID
	return validRecord && sameOwner && jobID == id && contract == jobcontract.CurrentContract &&
		targetKind == string(jobcontract.TargetRemote)
}
