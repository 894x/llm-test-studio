package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func contentManifestFixture(t *testing.T) (FinalManifest, JobSpec, Snapshot) {
	t.Helper()
	spec := performanceSpec(t)
	spec.Assets = []Asset{{Ref: "input-a", SHA256: strings.Repeat("b", 64), Size: 1, MediaType: "image/png"}}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	terminal, err := spec.InitialSnapshot(at, originFixture(spec))
	if err != nil {
		t.Fatal(err)
	}
	terminal.Receipt.Status, terminal.Receipt.Revision, terminal.LatestSequence = domain.RunCompleted, 4, 9
	terminal.UpdatedAt = at.Add(time.Second)
	digest, err := spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	manifest := FinalManifest{
		Contract: CurrentContract, NodeID: terminal.Receipt.Origin.NodeID,
		NodeInstallationID: spec.NodeInstallationID, JobID: spec.JobID, SpecSHA256: digest,
		ExecutionOrigin: terminal.Receipt.Origin, InventoryRevision: 7, TerminalRevision: 4,
		SealedSequence: 10, CreatedAt: at.Add(2 * time.Second),
		Items: []ManifestItem{{
			ItemID: "11111111-1111-4111-8111-111111111111", Role: ContentInput,
			Ref: "input-a", SHA256: strings.Repeat("b", 64), Size: 1, MediaType: "image/png",
		}, {
			ItemID: "22222222-2222-4222-8222-222222222222", Role: ContentArtifact,
			Ref: "output-a", SHA256: strings.Repeat("c", 64), Size: 2, MediaType: "image/jpeg",
		}},
		Documents: []ManifestDocument{{
			Kind: DocumentPerformanceResult, ID: testReportID, SHA256: strings.Repeat("d", 64), Size: 3,
		}},
		CaptureFailures: []CaptureFailure{{
			ItemID: "33333333-3333-4333-8333-333333333333", Ref: "output-b", ErrorCode: CaptureReadFailed,
		}},
	}
	if err := manifest.ValidateFor(spec, terminal); err != nil {
		t.Fatal(err)
	}
	return manifest, spec, terminal
}

func TestFinalManifestCanonicalOrderingInventoryAndOwnership(t *testing.T) {
	manifest, spec, terminal := contentManifestFixture(t)
	canonical, err := manifest.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFinalManifest(bytes.NewReader(canonical))
	if err != nil || decoded.ValidateFor(spec, terminal) != nil {
		t.Fatalf("valid current manifest rejected: %v", err)
	}
	digest, _ := manifest.Digest()
	if digest != contentDocumentDigest(canonical) {
		t.Fatal("manifest digest does not identify its canonical bytes")
	}
	inventory, _ := manifest.InventoryDigest()
	slices.Reverse(manifest.Items)
	sameDigest, _ := manifest.Digest()
	sameInventory, _ := manifest.InventoryDigest()
	if sameDigest != digest || sameInventory != inventory {
		t.Fatal("caller order changed canonical identity")
	}
	manifest.Documents[0].SHA256 = strings.Repeat("e", 64)
	changedDigest, _ := manifest.Digest()
	unchangedInventory, _ := manifest.InventoryDigest()
	if changedDigest == digest || unchangedInventory != inventory {
		t.Fatal("document binding is not separate from item inventory")
	}
	frozen, err := manifest.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(struct {
		Items []ManifestItem `json:"items"`
	}{Items: frozen.Items})
	if inventory != contentDocumentDigest(expected) {
		t.Fatal("inventory hash projection changed")
	}
	manifest.Items[0].Size++
	changedInventory, _ := manifest.InventoryDigest()
	if changedInventory == inventory {
		t.Fatal("inventory did not bind item byte count")
	}
}

func TestFinalManifestFreezeOwnsSlicesAndNullableValues(t *testing.T) {
	manifest, _, _ := contentManifestFixture(t)
	runID := "44444444-4444-4444-8444-444444444444"
	manifest.Items[1].RunID = &runID
	manifest.Documents = []ManifestDocument{{
		Kind: DocumentRunReport, ID: testReportID, RunID: &runID, SHA256: strings.Repeat("d", 64), Size: 1,
	}}
	manifest.CaptureFailures[0].RunID = &runID
	frozen, err := manifest.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	runID = "55555555-5555-4555-8555-555555555555"
	manifest.Items[1].Size = 99
	manifest.Documents[0].Size = 99
	manifest.CaptureFailures[0].Ref = "changed"
	if *frozen.Items[0].RunID == runID || *frozen.Documents[0].RunID == runID {
		t.Fatal("frozen manifest retained caller nullable pointers")
	}
	if *frozen.CaptureFailures[0].RunID == runID || frozen.CaptureFailures[0].Ref == "changed" {
		t.Fatal("frozen manifest retained caller capture slice")
	}
	if frozen.Items[0].Size != 2 || frozen.Documents[0].Size != 1 {
		t.Fatal("frozen manifest retained caller collections")
	}
}

func TestFinalManifestRejectsInvalidFactsAndBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*FinalManifest)
	}{
		{name: "node", change: func(m *FinalManifest) { m.NodeID = "" }},
		{name: "foreign installation", change: func(m *FinalManifest) { m.NodeInstallationID = m.JobID }},
		{name: "missing origin", change: func(m *FinalManifest) { m.ExecutionOrigin = domain.ExecutionOrigin{} }},
		{name: "inventory zero", change: func(m *FinalManifest) { m.InventoryRevision = 0 }},
		{name: "inventory overflow", change: func(m *FinalManifest) { m.InventoryRevision = math.MaxInt64 + 1 }},
		{name: "terminal initial", change: func(m *FinalManifest) { m.TerminalRevision = 1 }},
		{name: "no seal event", change: func(m *FinalManifest) { m.SealedSequence = m.TerminalRevision }},
		{name: "non UTC", change: func(m *FinalManifest) { m.CreatedAt = m.CreatedAt.In(time.FixedZone("x", 3600)) }},
		{name: "nil items", change: func(m *FinalManifest) { m.Items = nil }},
		{name: "nil docs", change: func(m *FinalManifest) { m.Documents = nil }},
		{name: "nil failures", change: func(m *FinalManifest) { m.CaptureFailures = nil }},
		{name: "duplicate item", change: func(m *FinalManifest) { m.Items = append(m.Items, m.Items[0]) }},
		{name: "duplicate artifact ref", change: func(m *FinalManifest) {
			extra := m.Items[1]
			extra.ItemID = "44444444-4444-4444-8444-444444444444"
			m.Items = append(m.Items, extra)
		}},
		{name: "artifact and failure share ref", change: func(m *FinalManifest) {
			m.CaptureFailures[0].Ref = m.Items[1].Ref
		}},
		{name: "duplicate failure ref", change: func(m *FinalManifest) {
			extra := m.CaptureFailures[0]
			extra.ItemID = "44444444-4444-4444-8444-444444444444"
			m.CaptureFailures = append(m.CaptureFailures, extra)
		}},
		{name: "failure claims real item", change: func(m *FinalManifest) { m.CaptureFailures[0].ItemID = m.Items[0].ItemID }},
		{name: "duplicate doc", change: func(m *FinalManifest) { m.Documents = append(m.Documents, m.Documents[0]) }},
		{name: "unknown failure", change: func(m *FinalManifest) { m.CaptureFailures[0].ErrorCode = "other" }},
		{name: "unknown role", change: func(m *FinalManifest) { m.Items[0].Role = "evidence_url" }},
		{name: "zero size", change: func(m *FinalManifest) { m.Items[0].Size = 0 }},
		{name: "oversize file", change: func(m *FinalManifest) { m.Items[0].Size = MaxAssetBytes + 1 }},
		{name: "mime parameters", change: func(m *FinalManifest) { m.Items[0].MediaType = "image/png; charset=utf-8" }},
		{name: "uppercase sha", change: func(m *FinalManifest) { m.Items[0].SHA256 = strings.Repeat("B", 64) }},
		{name: "same sha wrong size", change: func(m *FinalManifest) { m.Items[1].SHA256 = m.Items[0].SHA256 }},
		{name: "doc count", change: func(m *FinalManifest) { m.Documents = make([]ManifestDocument, MaxManifestDocuments+1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, _, _ := contentManifestFixture(t)
			test.change(&manifest)
			if !errors.Is(manifest.Validate(), ErrInvalid) {
				t.Fatal("invalid current manifest accepted")
			}
		})
	}
	for _, role := range []ContentRole{ContentInput, ContentArtifact} {
		limit := MaxAssets
		if role == ContentArtifact {
			limit = MaxManifestArtifacts
		}
		for _, count := range []int{0, 1, limit, limit + 1} {
			manifest, _, _ := contentManifestFixture(t)
			manifest.Items = make([]ManifestItem, count)
			manifest.CaptureFailures = []CaptureFailure{}
			for index := range count {
				manifest.Items[index] = ManifestItem{
					ItemID: fmt.Sprintf("%08x-1111-4111-8111-111111111111", index+1), Role: role,
					Ref: fmt.Sprintf("ref-%d", index), SHA256: fmt.Sprintf("%064x", index),
					Size: 1, MediaType: "application/octet-stream",
				}
			}
			if (manifest.Validate() == nil) != (count <= limit) {
				t.Fatalf("role %s count %d has wrong boundary", role, count)
			}
		}
	}
	manifest, _, _ := contentManifestFixture(t)
	manifest.Items = []ManifestItem{}
	manifest.Documents = []ManifestDocument{}
	manifest.CaptureFailures = []CaptureFailure{}
	manifest.InventoryRevision, manifest.TerminalRevision, manifest.SealedSequence = math.MaxInt64, math.MaxInt64-1, math.MaxInt64
	if err := manifest.Validate(); err != nil {
		t.Fatal("valid maximum revisions/explicit empty collections rejected", err)
	}
	manifest.Contract++
	if !errors.Is(manifest.Validate(), ErrUnsupported) {
		t.Fatal("unsupported contract accepted")
	}
}

func TestFinalManifestValidatesActualOwnerAndCompleteInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*FinalManifest, *Snapshot)
	}{
		{name: "nonterminal", change: func(_ *FinalManifest, s *Snapshot) { s.Receipt.Status = domain.RunRunning }},
		{name: "wrong spec", change: func(m *FinalManifest, _ *Snapshot) { m.SpecSHA256 = strings.Repeat("e", 64) }},
		{name: "wrong build", change: func(m *FinalManifest, _ *Snapshot) { m.ExecutionOrigin.EngineBuildID = strings.Repeat("e", 64) }},
		{name: "wrong terminal revision", change: func(m *FinalManifest, _ *Snapshot) { m.TerminalRevision++ }},
		{name: "late data", change: func(_ *FinalManifest, s *Snapshot) { s.LatestSequence = 11 }},
		{name: "seal before terminal", change: func(m *FinalManifest, s *Snapshot) { m.CreatedAt = s.UpdatedAt.Add(-time.Second) }},
		{name: "missing input", change: func(m *FinalManifest, _ *Snapshot) { m.Items = m.Items[1:] }},
		{name: "changed input", change: func(m *FinalManifest, _ *Snapshot) { m.Items[0].MediaType = "image/jpeg" }},
		{name: "foreign run", change: func(m *FinalManifest, _ *Snapshot) { m.Items[1].RunID = &m.Items[1].ItemID }},
		{name: "foreign report", change: func(m *FinalManifest, _ *Snapshot) { m.Documents[0].ID = m.JobID }},
		{name: "wrong doc kind", change: func(m *FinalManifest, _ *Snapshot) { m.Documents[0].Kind = DocumentComparisonReport }},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, spec, terminal := contentManifestFixture(t)
			test.change(&manifest, &terminal)
			if manifest.ValidateFor(spec, terminal) == nil {
				t.Fatal("foreign or incomplete manifest accepted")
			}
		})
	}
	manifest, spec, terminal := contentManifestFixture(t)
	terminal.LatestSequence = manifest.SealedSequence
	if err := manifest.ValidateFor(spec, terminal); err != nil {
		t.Fatal("snapshot at the seal event rejected", err)
	}
	spec.Target = ExecutionTarget{Kind: TargetRemote, NodeID: manifest.NodeID}
	terminal.Receipt.Origin.Kind, manifest.ExecutionOrigin.Kind = "remote", "remote"
	manifest.SpecSHA256, _ = spec.Digest()
	if err := manifest.ValidateFor(spec, terminal); err != nil {
		t.Fatal("explicit remote owner rejected", err)
	}
}

func TestFinalManifestByteAndCaptureBudgets(t *testing.T) {
	for _, role := range []ContentRole{ContentInput, ContentArtifact} {
		for _, count := range []int{4, 5} {
			manifest, _, _ := contentManifestFixture(t)
			manifest.CaptureFailures = []CaptureFailure{}
			manifest.Items = make([]ManifestItem, count)
			for index := range count {
				manifest.Items[index] = ManifestItem{
					ItemID: fmt.Sprintf("%08x-1111-4111-8111-111111111111", index+1), Role: role,
					Ref: fmt.Sprintf("ref-%d", index), SHA256: fmt.Sprintf("%064x", index),
					Size: MaxAssetBytes, MediaType: "application/octet-stream",
				}
			}
			if (manifest.Validate() == nil) != (count == 4) {
				t.Fatalf("role %s total byte budget not enforced", role)
			}
		}
	}
	manifest, _, _ := contentManifestFixture(t)
	manifest.Items = []ManifestItem{}
	manifest.CaptureFailures = make([]CaptureFailure, MaxManifestArtifacts)
	for index := range manifest.CaptureFailures {
		manifest.CaptureFailures[index] = CaptureFailure{
			ItemID: fmt.Sprintf("%08x-1111-4111-8111-111111111111", index+1),
			Ref:    fmt.Sprintf("failed-%d", index), ErrorCode: CaptureReadFailed,
		}
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal("capture-failure maximum rejected", err)
	}
	manifest.Items = []ManifestItem{{
		ItemID: "ffffffff-1111-4111-8111-111111111111", Role: ContentArtifact,
		Ref: "extra", SHA256: strings.Repeat("e", 64), Size: 1, MediaType: "image/png",
	}}
	if manifest.Validate() == nil {
		t.Fatal("successful and failed captures exceeded their shared count budget")
	}
	for _, code := range []CaptureFailureCode{
		CaptureReadFailed, CaptureIntegrityMismatch, CaptureStorageUnavailable, CaptureCancelled,
	} {
		failure := manifest.CaptureFailures[0]
		failure.ErrorCode = code
		if err := failure.Validate(); err != nil {
			t.Fatal("current capture failure enum rejected", err)
		}
	}
}

func TestManifestDocumentKindsHaveExactOwnerShapeAndByteBudget(t *testing.T) {
	runID := "44444444-4444-4444-8444-444444444444"
	for _, kind := range []ManifestDocumentKind{
		DocumentRunReport, DocumentComparisonReport, DocumentPerformanceResult, DocumentPerformanceArchive,
	} {
		document := ManifestDocument{Kind: kind, ID: testReportID, SHA256: strings.Repeat("a", 64), Size: 1}
		limit := int64(MaxRunReportBytes)
		if kind == DocumentRunReport {
			document.RunID = &runID
		}
		if kind == DocumentPerformanceResult {
			limit = MaxPerformanceResultBytes
		}
		for _, size := range []int64{0, 1, limit, limit + 1} {
			document.Size = size
			if (document.Validate() == nil) != (size >= 1 && size <= limit) {
				t.Fatalf("document %s byte boundary %d wrong", kind, size)
			}
		}
		document.Size = 1
		if document.RunID == nil {
			document.RunID = &runID
		} else {
			document.RunID = nil
		}
		if document.Validate() == nil {
			t.Fatal("document accepted the wrong nullable run owner shape")
		}
	}
}
