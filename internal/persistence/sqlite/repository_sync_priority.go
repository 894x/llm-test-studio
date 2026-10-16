package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// ListRemoteSyncPriorityIDs returns active shared-job or unimported-document
// scheduling hints. Terminal jobs with all known documents imported leave this
// queue; history synchronization still revisits them for late documents.
// Go validates bounded rows until limit+1 eligible IDs. SQLite may scan and sort
// the scoped small headers/snapshots first; total work depends on scoped history
// and respects ctx. Full spec/submission blobs stay out of the sort and Go.
// Actual synchronization still authenticates full specs and the event journal.
func (repository *Repository) ListRemoteSyncPriorityIDs(
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
		remoteSyncPriorityHeadersSQL,
		origin.ConfigurationID,
		origin.NodeID,
		origin.InstallationID,
		after,
		maxSubmissionRecordBytes,
		jobcontract.MaxSubmissionBytes,
		jobcontract.MaxStateBytes,
		jobcontract.MaxStateBytes,
	)
	if err != nil {
		return remoteresults.PriorityPage{}, fmt.Errorf("read remote sync priority identities: %w", err)
	}
	defer rows.Close()
	page := remoteresults.PriorityPage{JobIDs: []string{}, NextCursor: after}
	previous := after
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return remoteresults.PriorityPage{}, err
		}
		var id, ownState, ownDigest, mirrorDigest string
		var ownOrigin, mirrorOrigin remoteresults.Origin
		var own, mirror, badReference, pendingReference bool
		var ownRevision, sequence int64
		var ownMetadata, ownKeys, preparedKeys, receiptDocument []byte
		var specMetadata, specKeys, snapshotDocument []byte
		if err := rows.Scan(
			&id,
			&own,
			&mirror,
			&ownOrigin.ConfigurationID,
			&ownOrigin.NodeID,
			&ownOrigin.InstallationID,
			&ownRevision,
			&ownState,
			&ownDigest,
			&ownMetadata,
			&ownKeys,
			&preparedKeys,
			&receiptDocument,
			&mirrorOrigin.ConfigurationID,
			&mirrorOrigin.NodeID,
			&mirrorOrigin.InstallationID,
			&mirrorDigest,
			&specMetadata,
			&specKeys,
			&snapshotDocument,
			&sequence,
			&badReference,
			&pendingReference,
		); err != nil {
			return remoteresults.PriorityPage{}, fmt.Errorf("scan remote sync priority identity: %w", err)
		}
		if !domain.IsUUID(id) || id <= previous || badReference {
			return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority identity or reference state")
		}
		previous = id
		var receipt *jobcontract.Receipt
		if own {
			validEnvelope := priorityEnvelopeKeys(ownKeys, []string{
				"created_at", "observation", "prepared", "receipt", "revision", "spec_sha256", "state", "updated_at",
			}) && priorityEnvelopeKeys(preparedKeys, []string{"configuration_id", "runner", "spec"})
			identity := prioritySubmissionIdentity{ID: id, Origin: ownOrigin, Revision: ownRevision,
				State: ownState, Digest: ownDigest}
			if !validEnvelope || ownOrigin != origin || !identity.valid(ownMetadata) {
				return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority submission identity")
			}
			if ownState == string(jobs.SubmissionAccepted) {
				value, err := jobcontract.DecodeReceipt(bytes.NewReader(receiptDocument))
				if err != nil || !priorityReceiptMatches(value, identity, ownMetadata) {
					return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority submission receipt")
				}
				receipt = &value
			} else if string(receiptDocument) != "null" {
				return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority unexpected submission receipt")
			}
		}
		eligible := ownState != string(jobs.SubmissionPrepared)
		if mirror {
			snapshot, err := jobcontract.DecodeSnapshot(bytes.NewReader(snapshotDocument))
			validSnapshot := err == nil && snapshot.Receipt.JobID == id && snapshot.LatestSequence == uint64(sequence)
			if !validSnapshot || mirrorOrigin != origin || !jobcontract.IsSHA256(mirrorDigest) {
				return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority mirror snapshot")
			}
			if !validPrioritySpec(
				specMetadata,
				specKeys,
				snapshot,
				mirrorOrigin,
			) {
				return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority mirror identity")
			}
			if own {
				if ownState == string(jobs.SubmissionPrepared) || ownDigest != mirrorDigest {
					return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority conflicting sources")
				}
				if receipt != nil && !samePriorityAcceptance(*receipt, snapshot.Receipt) {
					return remoteresults.PriorityPage{}, storageCorrupt("remote sync priority conflicting acceptance")
				}
			}
			eligible = !jobcontract.Terminal(snapshot.Receipt.Status) || pendingReference
		}
		if !eligible {
			continue
		}
		if len(page.JobIDs) == int(limit) {
			page.HasMore = true
			break
		}
		page.JobIDs = append(page.JobIDs, id)
		page.NextCursor = id
	}
	if err := rows.Err(); err != nil {
		return remoteresults.PriorityPage{}, fmt.Errorf("iterate remote sync priority identities: %w", err)
	}
	if err := rows.Close(); err != nil {
		return remoteresults.PriorityPage{}, fmt.Errorf("close remote sync priority identities: %w", err)
	}
	return page, nil
}

type prioritySubmissionIdentity struct {
	ID       string
	Origin   remoteresults.Origin
	Revision int64
	State    string
	Digest   string
}

func (identity prioritySubmissionIdentity) valid(document []byte) bool {
	var configurationID, nodeID, installationID, jobID, targetKind, targetNodeID, targetInstallationID string
	var state, digest, build, kind string
	var contract int
	var revision int64
	if !priorityFields(document, []any{&configurationID, &nodeID, &installationID, &jobID, &contract,
		&targetKind, &targetNodeID, &targetInstallationID, &revision, &state, &digest, &build, &kind}) {
		return false
	}
	validRecord := identity.Revision > 0 && jobcontract.IsSHA256(identity.Digest) && jobcontract.IsSHA256(build) &&
		revision == identity.Revision && state == identity.State && digest == identity.Digest
	validState := state == string(jobs.SubmissionPrepared) && revision == 1 ||
		state == string(jobs.SubmissionPending) && revision >= 2 ||
		state == string(jobs.SubmissionAccepted) && revision >= 3
	sameOwner := configurationID == identity.Origin.ConfigurationID && nodeID == identity.Origin.NodeID &&
		installationID == identity.Origin.InstallationID && targetNodeID == nodeID && targetInstallationID == installationID
	validKind := kind == string(jobcontract.KindRun) || kind == string(jobcontract.KindComparison) ||
		kind == string(jobcontract.KindPerformance)
	return validRecord && validState && sameOwner && validKind && jobID == identity.ID &&
		contract == jobcontract.CurrentContract && targetKind == string(jobcontract.TargetRemote)
}

func priorityReceiptMatches(receipt jobcontract.Receipt, identity prioritySubmissionIdentity, metadata []byte) bool {
	fields := []json.RawMessage{}
	if json.Unmarshal(metadata, &fields) != nil || len(fields) != 13 {
		return false
	}
	var build, kind string
	if json.Unmarshal(fields[11], &build) != nil || json.Unmarshal(fields[12], &kind) != nil {
		return false
	}
	origin := receipt.Origin
	return receipt.JobID == identity.ID && string(receipt.Kind) == kind && origin.Kind == "remote" &&
		origin.NodeID == identity.Origin.NodeID && origin.InstallationID == identity.Origin.InstallationID &&
		origin.EngineBuildID == build
}

func validPrioritySpec(
	metadata, keys []byte,
	snapshot jobcontract.Snapshot,
	origin remoteresults.Origin,
) bool {
	validKeys := priorityEnvelopeKeys(keys, []string{
		"assets", "contract", "engine_build_id", "job_id", "kind", "node_installation_id", "spec", "target",
	})
	var contract int
	var id, targetKind, nodeID, installationID, build, kind string
	fields := []any{&contract, &id, &targetKind, &nodeID, &installationID, &build, &kind}
	if !validKeys || !priorityFields(metadata, fields) {
		return false
	}
	receipt := snapshot.Receipt
	validSpec := contract == jobcontract.CurrentContract && id == receipt.JobID && kind == string(receipt.Kind) &&
		targetKind == string(jobcontract.TargetRemote) && nodeID == origin.NodeID && installationID == origin.InstallationID
	actual := receipt.Origin
	return validSpec && actual.Kind == "remote" && actual.NodeID == nodeID &&
		actual.InstallationID == installationID && actual.EngineBuildID == build
}

func samePriorityAcceptance(left, right jobcontract.Receipt) bool {
	identity := left.Contract == right.Contract && left.JobID == right.JobID && left.Kind == right.Kind &&
		left.AcceptedAt.Equal(right.AcceptedAt) && left.Origin == right.Origin && left.ComparisonID == right.ComparisonID
	return identity && slices.Equal(left.RunIDs, right.RunIDs) && slices.Equal(left.ReportIDs, right.ReportIDs)
}

func priorityFields(document []byte, destinations []any) bool {
	fields := []json.RawMessage{}
	if len(document) > 4096 || json.Unmarshal(document, &fields) != nil || len(fields) != len(destinations) {
		return false
	}
	for index, destination := range destinations {
		if string(fields[index]) == "null" || json.Unmarshal(fields[index], destination) != nil {
			return false
		}
	}
	return true
}

func priorityEnvelopeKeys(document []byte, expected []string) bool {
	keys := []string{}
	if len(document) > 4096 || json.Unmarshal(document, &keys) != nil {
		return false
	}
	slices.Sort(keys)
	return slices.Equal(keys, expected)
}

// Scope selects IDs only. Unscoped source joins expose same-ID conflicts.
const remoteSyncPriorityScopeSQL = `WITH scope(configuration_id,node_id,installation_id,after_id) AS (VALUES(?,?,?,?)),
 ids AS (
 SELECT job_id FROM job_submissions, scope
 WHERE job_submissions.configuration_id = scope.configuration_id AND job_submissions.node_id = scope.node_id
 AND job_submissions.installation_id = scope.installation_id AND job_id > after_id
 UNION
 SELECT job_id FROM remote_job_mirrors, scope
 WHERE remote_job_mirrors.configuration_id = scope.configuration_id AND remote_job_mirrors.node_id = scope.node_id
 AND remote_job_mirrors.installation_id = scope.installation_id AND job_id > after_id
 ) `

// Documents stay inside SQLite expressions, never in a materialized CTE or Go.
// Each returned header is <=4KiB; current snapshots/receipts are <=64KiB.
const remoteSyncPriorityHeadersSQL = remoteSyncPriorityScopeSQL + `,
 sources AS NOT MATERIALIZED (
 SELECT ids.job_id, s.job_id IS NOT NULL AS own, m.job_id IS NOT NULL AS mirror,
 s.configuration_id AS sc,s.node_id AS sn,s.installation_id AS si,s.revision AS sr,s.state AS ss,s.spec_sha256 AS sd,
 m.configuration_id AS mc,m.node_id AS mn,m.installation_id AS mi,m.spec_sha256 AS md,m.applied_sequence AS sequence,
 CASE WHEN length(CAST(s.document_json AS BLOB)) BETWEEN 1 AND ?
 THEN CASE WHEN json_valid(s.document_json) THEN s.document_json END END AS submission,
 CASE WHEN length(CAST(m.spec_json AS BLOB)) BETWEEN 1 AND ?
 THEN CASE WHEN json_valid(m.spec_json) THEN m.spec_json END END AS spec,
 CASE WHEN length(CAST(m.snapshot_json AS BLOB)) BETWEEN 1 AND ?
 THEN CASE WHEN json_valid(m.snapshot_json) THEN m.snapshot_json END END AS snapshot
 FROM ids LEFT JOIN job_submissions s ON s.job_id = ids.job_id
 LEFT JOIN remote_job_mirrors m ON m.job_id = ids.job_id
 ), headers AS NOT MATERIALIZED (
 SELECT *,
 json_extract(submission,'$.prepared.configuration_id','$.prepared.runner.node_id',
 '$.prepared.runner.node_installation_id','$.prepared.spec.job_id','$.prepared.spec.contract',
 '$.prepared.spec.target.kind','$.prepared.spec.target.node_id','$.prepared.spec.node_installation_id',
 '$.revision','$.state','$.spec_sha256','$.prepared.spec.engine_build_id','$.prepared.spec.kind') AS own_metadata,
 (SELECT json_group_array(key) FROM json_each(submission)) AS own_keys,
 (SELECT json_group_array(key) FROM json_each(submission,'$.prepared')) AS prepared_keys,
 CASE WHEN json_type(submission,'$.receipt') = 'null' THEN 'null'
 WHEN json_type(submission,'$.receipt') = 'object'
 AND length(CAST(json_extract(submission,'$.receipt') AS BLOB)) BETWEEN 1 AND ?
 THEN json_extract(submission,'$.receipt') END AS own_receipt,
 json_extract(spec,'$.contract','$.job_id','$.target.kind','$.target.node_id','$.node_installation_id',
 '$.engine_build_id','$.kind') AS spec_metadata,
 (SELECT json_group_array(key) FROM json_each(spec)) AS spec_keys
 FROM sources
 )
 SELECT CASE WHEN length(CAST(job_id AS BLOB)) = 36 THEN job_id ELSE '' END,own,mirror,
 CASE WHEN length(CAST(sc AS BLOB)) = 36 THEN sc ELSE '' END,
 CASE WHEN length(CAST(sn AS BLOB)) = 36 THEN sn ELSE '' END,
 CASE WHEN length(CAST(si AS BLOB)) = 36 THEN si ELSE '' END,
 COALESCE(sr,0),CASE WHEN length(CAST(ss AS BLOB)) <= 8 THEN ss ELSE '' END,
 CASE WHEN length(CAST(sd AS BLOB)) = 64 THEN sd ELSE '' END,
 CASE WHEN length(CAST(own_metadata AS BLOB)) <= 4096 THEN own_metadata END,
 CASE WHEN length(CAST(own_keys AS BLOB)) <= 4096 THEN own_keys END,
 CASE WHEN length(CAST(prepared_keys AS BLOB)) <= 4096 THEN prepared_keys END,own_receipt,
 CASE WHEN length(CAST(mc AS BLOB)) = 36 THEN mc ELSE '' END,
 CASE WHEN length(CAST(mn AS BLOB)) = 36 THEN mn ELSE '' END,
 CASE WHEN length(CAST(mi AS BLOB)) = 36 THEN mi ELSE '' END,
 CASE WHEN length(CAST(md AS BLOB)) = 64 THEN md ELSE '' END,
 CASE WHEN length(CAST(spec_metadata AS BLOB)) <= 4096 THEN spec_metadata END,
 CASE WHEN length(CAST(spec_keys AS BLOB)) <= 4096 THEN spec_keys END,snapshot,COALESCE(sequence,0),
 EXISTS(SELECT 1 FROM remote_job_report_refs r WHERE r.job_id = headers.job_id
 AND (r.imported IS NULL OR r.imported NOT IN (0,1) OR r.reference_type NOT IN ('report','performance_result'))),
 EXISTS(SELECT 1 FROM remote_job_report_refs r WHERE r.job_id = headers.job_id AND r.imported = 0)
 FROM headers ORDER BY job_id`
