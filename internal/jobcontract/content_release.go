package jobcontract

import (
	"encoding/json"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxHoldingAttestationBytes     = 8 << 10
	MaxHoldingAcknowledgementBytes = 4 << 10
	MaxReleaseCommandBytes         = 4 << 10
	MaxReleaseTombstoneBytes       = 4 << 10
	MaxReleaseStatusBytes          = 8 << 10
)

// HoldingAttestation is a node-authenticated, self-asserted complete holding.
// HolderID is an audit label, not a separate authorization or disk proof.
type HoldingAttestation struct {
	Contract           int    `json:"contract"`
	ReceiptID          string `json:"receipt_id"`
	NodeID             string `json:"node_id"`
	NodeInstallationID string `json:"node_installation_id"`
	JobID              string `json:"job_id"`
	SpecSHA256         string `json:"spec_sha256"`
	ManifestSHA256     string `json:"manifest_sha256"`
	HolderID           string `json:"holder_id"`
	ItemCount          uint32 `json:"item_count"`
	TotalBytes         int64  `json:"total_bytes"`
	InventorySHA256    string `json:"inventory_sha256"`
}

type HoldingAcknowledgement struct {
	Contract       int       `json:"contract"`
	ReceiptID      string    `json:"receipt_id"`
	JobID          string    `json:"job_id"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	ReceivedAt     time.Time `json:"received_at"`
}

// ExpectedRevision identifies only FinalManifest.InventoryRevision.
type ReleaseCommand struct {
	Contract         int    `json:"contract"`
	CommandID        string `json:"command_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
	ManifestSHA256   string `json:"manifest_sha256"`
	HoldReceiptID    string `json:"hold_receipt_id"`
}

// ReleaseTombstone is immutable logical authorization. Physical cleanup is a
// separately mutable projection and never changes these bytes.
type ReleaseTombstone struct {
	Contract            int       `json:"contract"`
	NodeID              string    `json:"node_id"`
	NodeInstallationID  string    `json:"node_installation_id"`
	JobID               string    `json:"job_id"`
	CommandID           string    `json:"command_id"`
	ExpectedRevision    uint64    `json:"expected_revision"`
	ManifestSHA256      string    `json:"manifest_sha256"`
	HoldReceiptID       string    `json:"hold_receipt_id"`
	ReleasedItemCount   uint32    `json:"released_item_count"`
	ReleasedItemsSHA256 string    `json:"released_items_sha256"`
	ReleasedAt          time.Time `json:"released_at"`
}

type ReleaseState string

const (
	ReleaseNone      ReleaseState = "none"
	ReleasePending   ReleaseState = "pending"
	ReleaseCompleted ReleaseState = "completed"
	ReleaseBlocked   ReleaseState = "blocked"
)

// CleanupSummary counts manifest entries, including shared bytes once per entry.
// Completed logical release may still have pending or retained physical bytes.
type CleanupSummary struct {
	Pending               uint32 `json:"pending"`
	SharedRetained        uint32 `json:"shared_retained"`
	NewGenerationRetained uint32 `json:"new_generation_retained"`
	Deleting              uint32 `json:"deleting"`
	Deleted               uint32 `json:"deleted"`
	AbsentConfirmed       uint32 `json:"absent_confirmed"`
	Blocked               uint32 `json:"blocked"`
}

type ReleaseStatus struct {
	Contract  int               `json:"contract"`
	JobID     string            `json:"job_id"`
	State     ReleaseState      `json:"state"`
	CommandID *string           `json:"command_id"`
	Tombstone *ReleaseTombstone `json:"tombstone"`
	Cleanup   CleanupSummary    `json:"cleanup"`
}

func (holding HoldingAttestation) Validate() error {
	if holding.Contract != CurrentContract {
		return ErrUnsupported
	}
	validNode := domain.IsUUID(holding.NodeID) && domain.IsUUID(holding.NodeInstallationID)
	validIDs := domain.IsUUID(holding.ReceiptID) && domain.IsUUID(holding.JobID) && domain.IsUUID(holding.HolderID)
	validDigests := IsSHA256(holding.SpecSHA256) && IsSHA256(holding.ManifestSHA256) && IsSHA256(holding.InventorySHA256)
	if !validNode || !validIDs || !validDigests {
		return invalid("content_holding.identity")
	}
	validCount := holding.ItemCount <= MaxManifestItems
	validBytes := holding.TotalBytes >= 0 && holding.TotalBytes <= MaxJobAssetBytes+MaxJobArtifactBytes
	consistentEmpty := (holding.ItemCount == 0) == (holding.TotalBytes == 0)
	if !validCount || !validBytes || !consistentEmpty {
		return invalid("content_holding.inventory")
	}
	validPerItemBytes := holding.TotalBytes >= int64(holding.ItemCount) &&
		holding.TotalBytes <= int64(holding.ItemCount)*MaxAssetBytes
	if !validPerItemBytes {
		return invalid("content_holding.inventory")
	}
	return nil
}

func (holding HoldingAttestation) ValidateFor(manifest FinalManifest) error {
	if err := holding.Validate(); err != nil {
		return err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return err
	}
	inventory, err := manifest.InventoryDigest()
	if err != nil {
		return err
	}
	validNode := holding.NodeID == manifest.NodeID && holding.NodeInstallationID == manifest.NodeInstallationID
	validJob := holding.JobID == manifest.JobID && holding.SpecSHA256 == manifest.SpecSHA256
	validManifest := holding.ManifestSHA256 == digest && holding.InventorySHA256 == inventory
	validInventory := holding.ItemCount == uint32(len(manifest.Items)) && holding.TotalBytes == manifest.TotalBytes()
	if !validNode || !validJob || !validManifest || !validInventory {
		return invalid("content_holding.owner")
	}
	return nil
}

func (holding HoldingAttestation) Digest() (string, error) {
	if err := holding.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(holding)
	if err != nil {
		return "", invalid("content_holding")
	}
	return contentDocumentDigest(raw), nil
}

func (ack HoldingAcknowledgement) Validate() error {
	if ack.Contract != CurrentContract {
		return ErrUnsupported
	}
	validIDs := domain.IsUUID(ack.ReceiptID) && domain.IsUUID(ack.JobID)
	if !validIDs || !IsSHA256(ack.ManifestSHA256) || !validPerformanceTime(ack.ReceivedAt) {
		return invalid("content_holding.acknowledgement")
	}
	return nil
}

func (ack HoldingAcknowledgement) ValidateFor(holding HoldingAttestation) error {
	if err := ack.Validate(); err != nil {
		return err
	}
	if err := holding.Validate(); err != nil {
		return err
	}
	validIDs := ack.ReceiptID == holding.ReceiptID && ack.JobID == holding.JobID
	if !validIDs || ack.ManifestSHA256 != holding.ManifestSHA256 {
		return invalid("content_holding.acknowledgement.owner")
	}
	return nil
}

func (command ReleaseCommand) Validate() error {
	if command.Contract != CurrentContract {
		return ErrUnsupported
	}
	validIDs := domain.IsUUID(command.CommandID) && domain.IsUUID(command.HoldReceiptID)
	validRevision := command.ExpectedRevision > 0 && command.ExpectedRevision <= math.MaxInt64
	if !validIDs || !validRevision || !IsSHA256(command.ManifestSHA256) {
		return invalid("content_release.command")
	}
	return nil
}

func (command ReleaseCommand) ValidateFor(manifest FinalManifest, holding HoldingAttestation) error {
	if err := command.Validate(); err != nil {
		return err
	}
	if err := holding.ValidateFor(manifest); err != nil {
		return err
	}
	validManifest := command.ManifestSHA256 == holding.ManifestSHA256 &&
		command.ExpectedRevision == manifest.InventoryRevision
	if !validManifest || command.HoldReceiptID != holding.ReceiptID {
		return invalid("content_release.command.owner")
	}
	return nil
}

func (command ReleaseCommand) Digest() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return "", invalid("content_release.command")
	}
	return contentDocumentDigest(raw), nil
}

func (tombstone ReleaseTombstone) Validate() error {
	if tombstone.Contract != CurrentContract {
		return ErrUnsupported
	}
	command := ReleaseCommand{
		Contract: tombstone.Contract, CommandID: tombstone.CommandID,
		ExpectedRevision: tombstone.ExpectedRevision, ManifestSHA256: tombstone.ManifestSHA256,
		HoldReceiptID: tombstone.HoldReceiptID,
	}
	validNode := domain.IsUUID(tombstone.NodeID) && domain.IsUUID(tombstone.NodeInstallationID)
	validItems := tombstone.ReleasedItemCount <= MaxManifestItems && IsSHA256(tombstone.ReleasedItemsSHA256)
	if command.Validate() != nil || !validNode || !domain.IsUUID(tombstone.JobID) {
		return invalid("content_release.tombstone.identity")
	}
	if !validItems || !validPerformanceTime(tombstone.ReleasedAt) {
		return invalid("content_release.tombstone")
	}
	return nil
}

func (tombstone ReleaseTombstone) ValidateFor(manifest FinalManifest, command ReleaseCommand) error {
	if err := tombstone.Validate(); err != nil {
		return err
	}
	if err := command.Validate(); err != nil {
		return err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return err
	}
	inventory, err := manifest.InventoryDigest()
	if err != nil {
		return err
	}
	validNode := tombstone.NodeID == manifest.NodeID && tombstone.NodeInstallationID == manifest.NodeInstallationID
	validOwner := tombstone.JobID == manifest.JobID && tombstone.CommandID == command.CommandID
	validRevision := tombstone.ExpectedRevision == manifest.InventoryRevision &&
		command.ExpectedRevision == manifest.InventoryRevision
	validManifest := tombstone.ManifestSHA256 == digest && command.ManifestSHA256 == digest
	validGrant := tombstone.HoldReceiptID == command.HoldReceiptID && tombstone.ReleasedItemsSHA256 == inventory
	validCount := tombstone.ReleasedItemCount == uint32(len(manifest.Items))
	if !validNode || !validOwner || !validRevision || !validManifest || !validGrant || !validCount {
		return invalid("content_release.tombstone.owner")
	}
	if tombstone.ReleasedAt.Before(manifest.CreatedAt) {
		return invalid("content_release.tombstone.time")
	}
	return nil
}

func (tombstone ReleaseTombstone) CanonicalBytes() ([]byte, error) {
	if err := tombstone.Validate(); err != nil {
		return nil, err
	}
	tombstone.ReleasedAt = tombstone.ReleasedAt.UTC()
	return json.Marshal(tombstone)
}

func (summary CleanupSummary) Total() uint64 {
	return uint64(summary.Pending) + uint64(summary.SharedRetained) + uint64(summary.NewGenerationRetained) +
		uint64(summary.Deleting) + uint64(summary.Deleted) + uint64(summary.AbsentConfirmed) + uint64(summary.Blocked)
}

func (status ReleaseStatus) Validate() error {
	if status.Contract != CurrentContract {
		return ErrUnsupported
	}
	if !domain.IsUUID(status.JobID) || status.Cleanup.Total() > MaxManifestItems {
		return invalid("content_release.status")
	}
	if status.State == ReleaseNone {
		if status.CommandID != nil || status.Tombstone != nil || status.Cleanup.Total() != 0 {
			return invalid("content_release.status.none")
		}
		return nil
	}
	if status.CommandID == nil || !domain.IsUUID(*status.CommandID) {
		return invalid("content_release.status.command")
	}
	switch status.State {
	case ReleasePending, ReleaseBlocked:
		if status.Tombstone != nil {
			return invalid("content_release.status.tombstone")
		}
	case ReleaseCompleted:
		if status.Tombstone == nil || status.Tombstone.Validate() != nil {
			return invalid("content_release.status.tombstone")
		}
		tombstone := status.Tombstone
		validOwner := tombstone.JobID == status.JobID && tombstone.CommandID == *status.CommandID
		if !validOwner || uint64(tombstone.ReleasedItemCount) != status.Cleanup.Total() {
			return invalid("content_release.status.owner")
		}
	default:
		return invalid("content_release.status.state")
	}
	return nil
}
