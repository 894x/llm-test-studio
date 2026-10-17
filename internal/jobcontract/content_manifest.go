package jobcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"mime"
	"slices"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxFinalManifestBytes = 4 << 20
	MaxManifestArtifacts  = 4096
	MaxManifestItems      = MaxAssets + MaxManifestArtifacts
	MaxManifestDocuments  = MaxSecrets + 2
	MaxJobArtifactBytes   = int64(4 << 30)
)

type ContentRole string

const (
	ContentInput    ContentRole = "input"
	ContentArtifact ContentRole = "artifact"
)

// ManifestItem describes owned bytes. Physical store identities stay private.
type ManifestItem struct {
	ItemID    string      `json:"item_id"`
	Role      ContentRole `json:"role"`
	Ref       string      `json:"ref"`
	RunID     *string     `json:"run_id"`
	SHA256    string      `json:"sha256"`
	Size      int64       `json:"size"`
	MediaType string      `json:"media_type"`
}

type ManifestDocumentKind string

const (
	DocumentRunReport          ManifestDocumentKind = "run_report"
	DocumentComparisonReport   ManifestDocumentKind = "comparison_report"
	DocumentPerformanceResult  ManifestDocumentKind = "performance_result"
	DocumentPerformanceArchive ManifestDocumentKind = "performance_archive"
)

// ManifestDocument binds a typed, persisted document, not a binary attachment.
// A comparison report's ID is its comparison ID; performance IDs are report IDs.
type ManifestDocument struct {
	Kind   ManifestDocumentKind `json:"kind"`
	ID     string               `json:"id"`
	RunID  *string              `json:"run_id"`
	SHA256 string               `json:"sha256"`
	Size   int64                `json:"size"`
}

type CaptureFailureCode string

const (
	CaptureReadFailed         CaptureFailureCode = "read_failed"
	CaptureIntegrityMismatch  CaptureFailureCode = "integrity_mismatch"
	CaptureStorageUnavailable CaptureFailureCode = "storage_unavailable"
	CaptureCancelled          CaptureFailureCode = "cancelled"
)

// CaptureFailure records a failed owned capture without inventing file bytes.
type CaptureFailure struct {
	ItemID    string             `json:"item_id"`
	Ref       string             `json:"ref"`
	RunID     *string            `json:"run_id"`
	ErrorCode CaptureFailureCode `json:"error_code"`
}

type FinalManifest struct {
	Contract           int                    `json:"contract"`
	NodeID             string                 `json:"node_id"`
	NodeInstallationID string                 `json:"node_installation_id"`
	JobID              string                 `json:"job_id"`
	SpecSHA256         string                 `json:"spec_sha256"`
	ExecutionOrigin    domain.ExecutionOrigin `json:"execution_origin"`
	InventoryRevision  uint64                 `json:"inventory_revision"`
	TerminalRevision   uint64                 `json:"terminal_revision"`
	SealedSequence     uint64                 `json:"sealed_sequence"`
	CreatedAt          time.Time              `json:"created_at"`
	Items              []ManifestItem         `json:"items"`
	Documents          []ManifestDocument     `json:"documents"`
	CaptureFailures    []CaptureFailure       `json:"capture_failures"`
}

func (item ManifestItem) Validate() error {
	identity := ContentIdentity{SHA256: item.SHA256, Size: item.Size}
	validIdentity := domain.IsUUID(item.ItemID) && identity.Validate() == nil
	if !validIdentity || !assetRef.MatchString(item.Ref) {
		return invalid("manifest.item")
	}
	if !validContentRunID(item.RunID) || !validContentMediaType(item.MediaType) {
		return invalid("manifest.item")
	}
	switch item.Role {
	case ContentInput:
		if item.RunID != nil {
			return invalid("manifest.item.input_run")
		}
	case ContentArtifact:
	default:
		return invalid("manifest.item.role")
	}
	return nil
}

func (document ManifestDocument) Validate() error {
	validIdentity := domain.IsUUID(document.ID) && IsSHA256(document.SHA256)
	if !validIdentity || !validContentRunID(document.RunID) {
		return invalid("manifest.document")
	}
	limit := int64(MaxRunReportBytes)
	switch document.Kind {
	case DocumentRunReport:
		if document.RunID == nil {
			return invalid("manifest.document.run")
		}
	case DocumentComparisonReport, DocumentPerformanceArchive:
		if document.RunID != nil {
			return invalid("manifest.document.run")
		}
	case DocumentPerformanceResult:
		limit = MaxPerformanceResultBytes
		if document.RunID != nil {
			return invalid("manifest.document.run")
		}
	default:
		return invalid("manifest.document.kind")
	}
	if document.Size < 1 || document.Size > limit {
		return invalid("manifest.document.size")
	}
	return nil
}

func (failure CaptureFailure) Validate() error {
	validIdentity := domain.IsUUID(failure.ItemID) && assetRef.MatchString(failure.Ref)
	if !validIdentity || !validContentRunID(failure.RunID) {
		return invalid("manifest.capture_failure")
	}
	switch failure.ErrorCode {
	case CaptureReadFailed, CaptureIntegrityMismatch, CaptureStorageUnavailable, CaptureCancelled:
		return nil
	default:
		return invalid("manifest.capture_failure.error_code")
	}
}

func (manifest FinalManifest) Validate() error {
	if manifest.Contract != CurrentContract {
		return ErrUnsupported
	}
	validIdentity := domain.IsUUID(manifest.NodeID) && domain.IsUUID(manifest.NodeInstallationID)
	validJob := domain.IsUUID(manifest.JobID) && IsSHA256(manifest.SpecSHA256)
	if !validIdentity || !validJob {
		return invalid("manifest.identity")
	}
	origin := manifest.ExecutionOrigin
	validOrigin := origin.NodeID == manifest.NodeID && origin.InstallationID == manifest.NodeInstallationID
	if origin.Validate() != nil || !validOrigin {
		return invalid("manifest.execution_origin")
	}
	validInventory := manifest.InventoryRevision > 0 && manifest.InventoryRevision <= math.MaxInt64
	validTerminal := manifest.TerminalRevision >= 2 && manifest.TerminalRevision < math.MaxInt64
	validSequence := manifest.SealedSequence > manifest.TerminalRevision && manifest.SealedSequence <= math.MaxInt64
	if !validInventory || !validTerminal || !validSequence {
		return invalid("manifest.revision")
	}
	if !validPerformanceTime(manifest.CreatedAt) {
		return invalid("manifest.created_at")
	}
	if err := manifest.validateInventory(); err != nil {
		return err
	}
	if manifest.Documents == nil || len(manifest.Documents) > MaxManifestDocuments {
		return invalid("manifest.documents")
	}
	seen := make(map[string]struct{}, len(manifest.Documents))
	for _, document := range manifest.Documents {
		if err := document.Validate(); err != nil {
			return err
		}
		key := string(document.Kind) + ":" + document.ID
		if _, duplicate := seen[key]; duplicate {
			return invalid("manifest.document.duplicate")
		}
		seen[key] = struct{}{}
	}
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > MaxFinalManifestBytes {
		return ErrTooLarge
	}
	return nil
}

func (manifest FinalManifest) validateInventory() error {
	nullCollection := manifest.Items == nil || manifest.CaptureFailures == nil
	tooManyItems := len(manifest.Items) > MaxManifestItems || len(manifest.CaptureFailures) > MaxManifestArtifacts
	if nullCollection || tooManyItems {
		return invalid("manifest.items")
	}
	seen := make(map[string]struct{}, len(manifest.Items)+len(manifest.CaptureFailures))
	refs := make(map[string]struct{}, len(manifest.Items)+len(manifest.CaptureFailures))
	contentSizes := map[string]int64{}
	var inputCount, artifactCount int
	var inputBytes, artifactBytes int64
	for _, item := range manifest.Items {
		if err := item.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[item.ItemID]; duplicate {
			return invalid("manifest.item.duplicate")
		}
		seen[item.ItemID] = struct{}{}
		refKey := string(item.Role) + ":" + item.Ref
		if _, duplicate := refs[refKey]; duplicate {
			return invalid("manifest.item.ref")
		}
		refs[refKey] = struct{}{}
		if size, exists := contentSizes[item.SHA256]; exists && size != item.Size {
			return invalid("manifest.item.content_identity")
		}
		contentSizes[item.SHA256] = item.Size
		if item.Role == ContentInput {
			inputCount++
			inputBytes += item.Size
			continue
		}
		artifactCount++
		artifactBytes += item.Size
	}
	validCounts := inputCount <= MaxAssets && artifactCount+len(manifest.CaptureFailures) <= MaxManifestArtifacts
	validBytes := inputBytes <= MaxJobAssetBytes && artifactBytes <= MaxJobArtifactBytes
	if !validCounts || !validBytes {
		return invalid("manifest.inventory_budget")
	}
	for _, failure := range manifest.CaptureFailures {
		if err := failure.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[failure.ItemID]; duplicate {
			return invalid("manifest.capture_failure.duplicate")
		}
		seen[failure.ItemID] = struct{}{}
		refKey := string(ContentArtifact) + ":" + failure.Ref
		if _, duplicate := refs[refKey]; duplicate {
			return invalid("manifest.capture_failure.ref")
		}
		refs[refKey] = struct{}{}
	}
	return nil
}

// ValidateFor verifies declared ownership. Persistence must additionally prove
// that every document and artifact exists and that the sealed inventory is exact.
func (manifest FinalManifest) ValidateFor(spec JobSpec, terminal Snapshot) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := terminal.Validate(spec); err != nil {
		return err
	}
	if !Terminal(terminal.Receipt.Status) {
		return invalid("manifest.terminal")
	}
	digest, err := spec.Digest()
	if err != nil {
		return err
	}
	validOwner := manifest.JobID == spec.JobID && manifest.SpecSHA256 == digest
	validOrigin := manifest.ExecutionOrigin == terminal.Receipt.Origin
	validRevision := manifest.TerminalRevision == terminal.Receipt.Revision &&
		manifest.SealedSequence >= terminal.LatestSequence
	if !validOwner || !validOrigin || !validRevision {
		return invalid("manifest.owner")
	}
	if manifest.CreatedAt.Before(terminal.UpdatedAt) {
		return invalid("manifest.created_at")
	}
	assets := make(map[string]Asset, len(spec.Assets))
	for _, asset := range spec.Assets {
		assets[asset.Ref] = asset
	}
	for _, item := range manifest.Items {
		if !ownedContentRun(item.RunID, terminal.Receipt.RunIDs) {
			return invalid("manifest.item.owner")
		}
		if item.Role != ContentInput {
			continue
		}
		asset, exists := assets[item.Ref]
		sameBytes := asset.SHA256 == item.SHA256 && asset.Size == item.Size && asset.MediaType == item.MediaType
		if !exists || !sameBytes {
			return invalid("manifest.item.input")
		}
		delete(assets, item.Ref)
	}
	if len(assets) != 0 {
		return invalid("manifest.item.missing_input")
	}
	for _, failure := range manifest.CaptureFailures {
		if !ownedContentRun(failure.RunID, terminal.Receipt.RunIDs) {
			return invalid("manifest.capture_failure.owner")
		}
	}
	for _, document := range manifest.Documents {
		if !manifestDocumentOwned(document, terminal.Receipt) {
			return invalid("manifest.document.owner")
		}
	}
	return nil
}

func manifestDocumentOwned(document ManifestDocument, receipt Receipt) bool {
	switch document.Kind {
	case DocumentRunReport:
		return receipt.Kind != KindPerformance && ownedContentRun(document.RunID, receipt.RunIDs)
	case DocumentComparisonReport:
		return receipt.Kind == KindComparison && document.ID == receipt.ComparisonID
	case DocumentPerformanceResult, DocumentPerformanceArchive:
		return receipt.Kind == KindPerformance && slices.Contains(receipt.ReportIDs, document.ID)
	default:
		return false
	}
}

// Freeze clones every collection and nullable value, then orders the one current
// representation. Caller-owned slices and pointers are never modified.
func (manifest FinalManifest) Freeze() (FinalManifest, error) {
	if err := manifest.Validate(); err != nil {
		return FinalManifest{}, err
	}
	frozen := manifest
	frozen.CreatedAt = frozen.CreatedAt.UTC()
	frozen.Items = slices.Clone(manifest.Items)
	for index := range frozen.Items {
		frozen.Items[index].RunID = cloneContentRunID(frozen.Items[index].RunID)
	}
	frozen.Documents = slices.Clone(manifest.Documents)
	for index := range frozen.Documents {
		frozen.Documents[index].RunID = cloneContentRunID(frozen.Documents[index].RunID)
	}
	frozen.CaptureFailures = slices.Clone(manifest.CaptureFailures)
	for index := range frozen.CaptureFailures {
		frozen.CaptureFailures[index].RunID = cloneContentRunID(frozen.CaptureFailures[index].RunID)
	}
	slices.SortFunc(frozen.Items, compareManifestItems)
	slices.SortFunc(frozen.Documents, func(left, right ManifestDocument) int {
		if order := strings.Compare(string(left.Kind), string(right.Kind)); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(frozen.CaptureFailures, func(left, right CaptureFailure) int {
		return strings.Compare(left.ItemID, right.ItemID)
	})
	return frozen, nil
}

func (manifest FinalManifest) CanonicalBytes() ([]byte, error) {
	frozen, err := manifest.Freeze()
	if err != nil {
		return nil, err
	}
	return json.Marshal(frozen)
}

func (manifest FinalManifest) Digest() (string, error) {
	raw, err := manifest.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return contentDocumentDigest(raw), nil
}

// InventoryDigest hashes {"items":[...]} with all seven item fields, ordered by
// (role, ref, item_id). Documents and failures remain bound by the manifest hash.
func (manifest FinalManifest) InventoryDigest() (string, error) {
	frozen, err := manifest.Freeze()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Items []ManifestItem `json:"items"`
	}{Items: frozen.Items})
	if err != nil {
		return "", invalid("manifest.inventory")
	}
	return contentDocumentDigest(raw), nil
}

func (manifest FinalManifest) TotalBytes() int64 {
	var total int64
	for _, item := range manifest.Items {
		total += item.Size
	}
	return total
}

func compareManifestItems(left, right ManifestItem) int {
	if order := strings.Compare(string(left.Role), string(right.Role)); order != 0 {
		return order
	}
	if order := strings.Compare(left.Ref, right.Ref); order != 0 {
		return order
	}
	return strings.Compare(left.ItemID, right.ItemID)
}

func validContentMediaType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	canonical := err == nil && mediaType == value && len(parameters) == 0
	return canonical && strings.Count(value, "/") == 1 && len(value) <= 127
}

func validContentRunID(id *string) bool { return id == nil || domain.IsUUID(*id) }

func ownedContentRun(id *string, owned []string) bool {
	return id == nil || slices.Contains(owned, *id)
}

func cloneContentRunID(id *string) *string {
	if id == nil {
		return nil
	}
	value := *id
	return &value
}

func contentDocumentDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
