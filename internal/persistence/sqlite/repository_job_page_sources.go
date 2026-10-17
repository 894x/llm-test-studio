package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type storedSubmissionRow struct {
	id              string
	configurationID string
	nodeID          string
	installationID  string
	revision        uint64
	state           string
	digest          string
	document        []byte
}

func (stored *storedSubmissionRow) scan(row interface{ Scan(...any) error }) error {
	err := row.Scan(
		&stored.id,
		&stored.configurationID,
		&stored.nodeID,
		&stored.installationID,
		&stored.revision,
		&stored.state,
		&stored.digest,
		&stored.document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read submission record: %w", err)
	}
	return nil
}

func (stored storedSubmissionRow) decode() (jobs.SubmissionRecord, error) {
	record, err := decodeSubmissionRecord(stored.document)
	if err != nil {
		return jobs.SubmissionRecord{}, err
	}
	prepared := record.Prepared
	validOwner := prepared.ConfigurationID == stored.configurationID && prepared.Runner.NodeID == stored.nodeID &&
		prepared.Runner.NodeInstallationID == stored.installationID
	validRecord := prepared.Spec.JobID == stored.id && record.Revision == stored.revision &&
		string(record.State) == stored.state && record.SpecSHA256 == stored.digest
	if !validOwner || !validRecord {
		return jobs.SubmissionRecord{}, fmt.Errorf("%w: submission identity", ErrCorrupt)
	}
	return record, nil
}

func (stored storedSubmissionRow) byteSize() int64 {
	return int64(len(stored.id)) + int64(len(stored.configurationID)) + int64(len(stored.nodeID)) +
		int64(len(stored.installationID)) + int64(len(stored.state)) + int64(len(stored.digest)) +
		int64(len(stored.document)) + 8
}

type storedRemoteMirrorRow struct {
	configurationID  string
	nodeID           string
	installationID   string
	digest           string
	specDocument     []byte
	snapshotDocument []byte
	sequence         uint64
	acceptedDocument []byte
	kind             string
}

func (stored *storedRemoteMirrorRow) scan(row interface{ Scan(...any) error }) error {
	err := row.Scan(
		&stored.configurationID,
		&stored.nodeID,
		&stored.installationID,
		&stored.digest,
		&stored.specDocument,
		&stored.snapshotDocument,
		&stored.sequence,
		&stored.acceptedDocument,
		&stored.kind,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteresults.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read remote mirror: %w", err)
	}
	return nil
}

func (stored storedRemoteMirrorRow) decode(id string) (remoteresults.Mirror, error) {
	mirror := remoteresults.Mirror{Origin: remoteresults.Origin{
		ConfigurationID: stored.configurationID,
		NodeID:          stored.nodeID,
		InstallationID:  stored.installationID,
	}}
	if err := decodeCanonical(
		stored.specDocument,
		&mirror.Spec,
		func() error { return mirror.Spec.Validate() },
	); err != nil {
		return remoteresults.Mirror{}, fmt.Errorf("%w: remote spec", ErrCorrupt)
	}
	if err := decodeCanonical(stored.snapshotDocument, &mirror.Snapshot, nil); err != nil {
		return remoteresults.Mirror{}, fmt.Errorf("%w: remote snapshot", ErrCorrupt)
	}
	actualDigest, err := mirror.Spec.Digest()
	validOrigin := mirror.Origin.Validate() == nil && mirror.Spec.Target.Kind == jobcontract.TargetRemote &&
		mirror.Spec.Target.NodeID == mirror.Origin.NodeID && mirror.Spec.NodeInstallationID == mirror.Origin.InstallationID
	validState := mirror.Spec.JobID == id && mirror.Snapshot.LatestSequence == stored.sequence &&
		mirror.Snapshot.Validate(mirror.Spec) == nil && string(mirror.Snapshot.Receipt.Kind) == stored.kind
	validMirror := err == nil && actualDigest == stored.digest && validOrigin && validState
	if !validMirror {
		return remoteresults.Mirror{}, fmt.Errorf("%w: remote mirror identity", ErrCorrupt)
	}
	accepted, err := decodeAcceptedOrigin(stored.acceptedDocument, mirror.Snapshot)
	if err != nil {
		return remoteresults.Mirror{}, storageCorrupt("remote accepted origin")
	}
	validAcceptedSpec := accepted.specSHA256 == actualDigest &&
		accepted.specDocumentSHA256 == sha256.Sum256(stored.specDocument)
	if !validAcceptedSpec {
		return remoteresults.Mirror{}, storageCorrupt("remote accepted origin")
	}
	executor := mirror.Snapshot.Receipt.Origin
	validExecutor := executor.Kind == "remote" && executor.NodeID == mirror.Origin.NodeID &&
		executor.InstallationID == mirror.Origin.InstallationID
	if !validExecutor {
		return remoteresults.Mirror{}, storageCorrupt("remote executor origin")
	}
	return mirror, nil
}

func (stored storedRemoteMirrorRow) byteSize() int64 {
	return int64(len(stored.configurationID)) + int64(len(stored.nodeID)) + int64(len(stored.installationID)) +
		int64(len(stored.digest)) + int64(len(stored.kind)) + int64(len(stored.specDocument)) +
		int64(len(stored.snapshotDocument)) + int64(len(stored.acceptedDocument)) + 8
}
