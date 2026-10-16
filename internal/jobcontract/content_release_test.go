package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func contentHoldingFixture(t *testing.T, manifest FinalManifest) HoldingAttestation {
	t.Helper()
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := manifest.InventoryDigest()
	if err != nil {
		t.Fatal(err)
	}
	return HoldingAttestation{
		Contract: CurrentContract, ReceiptID: "44444444-4444-4444-8444-444444444444",
		NodeID: manifest.NodeID, NodeInstallationID: manifest.NodeInstallationID,
		JobID: manifest.JobID, SpecSHA256: manifest.SpecSHA256, ManifestSHA256: digest,
		HolderID: "55555555-5555-4555-8555-555555555555", ItemCount: uint32(len(manifest.Items)),
		TotalBytes: manifest.TotalBytes(), InventorySHA256: inventory,
	}
}

func contentReleaseFixture(t *testing.T) (FinalManifest, HoldingAttestation, ReleaseCommand, ReleaseTombstone) {
	t.Helper()
	manifest, _, _ := contentManifestFixture(t)
	holding := contentHoldingFixture(t, manifest)
	command := ReleaseCommand{
		Contract: CurrentContract, CommandID: "66666666-6666-4666-8666-666666666666",
		ExpectedRevision: manifest.InventoryRevision, ManifestSHA256: holding.ManifestSHA256,
		HoldReceiptID: holding.ReceiptID,
	}
	tombstone := ReleaseTombstone{
		Contract: CurrentContract, NodeID: manifest.NodeID, NodeInstallationID: manifest.NodeInstallationID,
		JobID: manifest.JobID, CommandID: command.CommandID, ExpectedRevision: command.ExpectedRevision,
		ManifestSHA256: command.ManifestSHA256, HoldReceiptID: command.HoldReceiptID,
		ReleasedItemCount: holding.ItemCount, ReleasedItemsSHA256: holding.InventorySHA256,
		ReleasedAt: manifest.CreatedAt.Add(time.Second),
	}
	return manifest, holding, command, tombstone
}

func TestContentHoldingBindsCompleteManifestAndAllowsExplicitEmpty(t *testing.T) {
	manifest, holding, _, _ := contentReleaseFixture(t)
	if err := holding.ValidateFor(manifest); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*HoldingAttestation)
	}{
		{name: "node", change: func(h *HoldingAttestation) { h.NodeID = h.JobID }},
		{name: "installation", change: func(h *HoldingAttestation) { h.NodeInstallationID = h.JobID }},
		{name: "job", change: func(h *HoldingAttestation) { h.JobID = h.ReceiptID }},
		{name: "spec", change: func(h *HoldingAttestation) { h.SpecSHA256 = strings.Repeat("e", 64) }},
		{name: "manifest", change: func(h *HoldingAttestation) { h.ManifestSHA256 = strings.Repeat("e", 64) }},
		{name: "inventory", change: func(h *HoldingAttestation) { h.InventorySHA256 = strings.Repeat("e", 64) }},
		{name: "one item missing", change: func(h *HoldingAttestation) { h.ItemCount-- }},
		{name: "one byte missing", change: func(h *HoldingAttestation) { h.TotalBytes-- }},
		{name: "holder not identity", change: func(h *HoldingAttestation) { h.HolderID = "familiar-client" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := holding
			test.change(&changed)
			if !errors.Is(changed.ValidateFor(manifest), ErrInvalid) {
				t.Fatal("incomplete or foreign holding accepted")
			}
		})
	}
	digest, _ := holding.Digest()
	raw, _ := json.Marshal(holding)
	if digest != contentDocumentDigest(raw) {
		t.Fatal("holding idempotency digest changed")
	}
	manifest.Items = []ManifestItem{}
	manifest.Documents = []ManifestDocument{}
	manifest.CaptureFailures = []CaptureFailure{}
	empty := contentHoldingFixture(t, manifest)
	if err := empty.ValidateFor(manifest); err != nil || empty.ItemCount != 0 || empty.TotalBytes != 0 {
		t.Fatal("explicit complete empty holding rejected", err)
	}
	for _, test := range []struct {
		count uint32
		size  int64
		valid bool
	}{
		{count: 0, size: 0, valid: true}, {count: 0, size: 1}, {count: 1, size: 0},
		{count: 1, size: 1, valid: true}, {count: 1, size: MaxAssetBytes, valid: true},
		{count: 1, size: MaxAssetBytes + 1}, {count: 8, size: MaxJobAssetBytes + MaxJobArtifactBytes, valid: true},
		{count: 9, size: MaxJobAssetBytes + MaxJobArtifactBytes + 1},
		{count: MaxManifestItems, size: MaxManifestItems, valid: true},
		{count: MaxManifestItems + 1, size: MaxManifestItems + 1},
		{count: 1, size: -1},
	} {
		changed := holding
		changed.ItemCount, changed.TotalBytes = test.count, test.size
		if (changed.Validate() == nil) != test.valid {
			t.Fatalf("holding count %d bytes %d boundary wrong", test.count, test.size)
		}
	}
}

func TestContentHoldingAcknowledgementOnlyAcknowledgesTheSameAttestation(t *testing.T) {
	manifest, holding, _, _ := contentReleaseFixture(t)
	ack := HoldingAcknowledgement{
		Contract: CurrentContract, ReceiptID: holding.ReceiptID, JobID: holding.JobID,
		ManifestSHA256: holding.ManifestSHA256, ReceivedAt: manifest.CreatedAt.Add(time.Second),
	}
	if err := ack.ValidateFor(holding); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*HoldingAcknowledgement){
		func(a *HoldingAcknowledgement) { a.ReceiptID = a.JobID },
		func(a *HoldingAcknowledgement) { a.JobID = a.ReceiptID },
		func(a *HoldingAcknowledgement) { a.ManifestSHA256 = strings.Repeat("e", 64) },
		func(a *HoldingAcknowledgement) { a.ReceivedAt = time.Time{} },
	} {
		changed := ack
		change(&changed)
		if changed.ValidateFor(holding) == nil {
			t.Fatal("foreign or malformed acknowledgement accepted")
		}
	}
}

func TestContentReleaseRevisionIsOnlyTheManifestInventoryRevision(t *testing.T) {
	manifest, holding, command, tombstone := contentReleaseFixture(t)
	if err := command.ValidateFor(manifest, holding); err != nil {
		t.Fatal(err)
	}
	if err := tombstone.ValidateFor(manifest, command); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []uint64{0, manifest.TerminalRevision, manifest.SealedSequence, math.MaxInt64 + 1} {
		changed := command
		changed.ExpectedRevision = revision
		if changed.ValidateFor(manifest, holding) == nil {
			t.Fatal("non-inventory revision authorized release")
		}
	}
	for _, change := range []func(*ReleaseCommand){
		func(c *ReleaseCommand) { c.HoldReceiptID = c.CommandID },
		func(c *ReleaseCommand) { c.ManifestSHA256 = strings.Repeat("e", 64) },
		func(c *ReleaseCommand) { c.CommandID = "command" },
	} {
		changed := command
		change(&changed)
		if changed.ValidateFor(manifest, holding) == nil {
			t.Fatal("unbound command accepted")
		}
	}
	digest, _ := command.Digest()
	raw, _ := json.Marshal(command)
	if digest != contentDocumentDigest(raw) {
		t.Fatal("release idempotency digest changed")
	}
	for _, change := range []func(*ReleaseTombstone){
		func(c *ReleaseTombstone) { c.JobID = c.CommandID },
		func(c *ReleaseTombstone) { c.NodeID = c.CommandID },
		func(c *ReleaseTombstone) { c.NodeInstallationID = c.CommandID },
		func(c *ReleaseTombstone) { c.CommandID = c.JobID },
		func(c *ReleaseTombstone) { c.ExpectedRevision = manifest.TerminalRevision },
		func(c *ReleaseTombstone) { c.ManifestSHA256 = strings.Repeat("e", 64) },
		func(c *ReleaseTombstone) { c.HoldReceiptID = c.JobID },
		func(c *ReleaseTombstone) { c.ReleasedItemCount-- },
		func(c *ReleaseTombstone) { c.ReleasedItemsSHA256 = strings.Repeat("e", 64) },
		func(c *ReleaseTombstone) { c.ReleasedAt = manifest.CreatedAt.Add(-time.Second) },
	} {
		changed := tombstone
		change(&changed)
		if changed.ValidateFor(manifest, command) == nil {
			t.Fatal("unbound tombstone accepted")
		}
	}
}

func TestContentReleaseStableTombstoneAndMutableCleanupAreSeparate(t *testing.T) {
	_, _, command, tombstone := contentReleaseFixture(t)
	original, err := tombstone.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	status := ReleaseStatus{
		Contract: CurrentContract, JobID: tombstone.JobID, State: ReleaseCompleted,
		CommandID: &command.CommandID, Tombstone: &tombstone,
		Cleanup: CleanupSummary{SharedRetained: tombstone.ReleasedItemCount},
	}
	for _, summary := range []CleanupSummary{
		{SharedRetained: 2}, {NewGenerationRetained: 2}, {Pending: 2},
		{Deleting: 2}, {Deleted: 2}, {AbsentConfirmed: 2}, {Blocked: 2},
	} {
		status.Cleanup = summary
		if err := status.Validate(); err != nil {
			t.Fatal("logical completion incorrectly requires physical deletion", err)
		}
		after, _ := status.Tombstone.CanonicalBytes()
		if !bytes.Equal(original, after) {
			t.Fatal("cleanup changed immutable release authorization")
		}
	}
	for _, test := range []struct {
		state ReleaseState
		valid bool
	}{
		{state: ReleaseNone}, {state: ReleasePending}, {state: ReleaseBlocked},
		{state: ReleaseCompleted, valid: true}, {state: "done"},
	} {
		status.State = test.state
		if (status.Validate() == nil) != test.valid {
			t.Fatal("status/tombstone lifecycle combination wrong")
		}
	}
	status = ReleaseStatus{Contract: CurrentContract, JobID: tombstone.JobID, State: ReleaseNone}
	if err := status.Validate(); err != nil {
		t.Fatal("explicit no-release projection rejected", err)
	}
	status.State, status.CommandID = ReleasePending, &command.CommandID
	if err := status.Validate(); err != nil {
		t.Fatal("pending empty inventory rejected", err)
	}
	status.Cleanup.Pending = math.MaxUint32
	status.Cleanup.Deleted = math.MaxUint32
	if status.Cleanup.Total() != 2*uint64(math.MaxUint32) || status.Validate() == nil {
		t.Fatal("cleanup counter overflow accepted")
	}
}
