// Package jobcontent owns content publication and durable job references.
package jobcontent

import (
	"errors"
	"math"
	"mime"
	"regexp"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrInvalid  = errors.New("invalid_content")
	ErrNotFound = errors.New("content_not_found")
	ErrConflict = errors.New("content_conflict")
	ErrCorrupt  = errors.New("corrupt_content")
	contentRef  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)
)

type StoreKind string

const (
	StoreKindExecutor StoreKind = "executor"
	StoreKindCache    StoreKind = "cache"
)

type Store struct {
	ID             string
	Kind           StoreKind
	InstallationID string
	CreatedAt      time.Time
}

func (store Store) Validate() error {
	validKind := store.Kind == StoreKindExecutor || store.Kind == StoreKindCache
	validIDs := domain.IsUUID(store.ID) && domain.IsUUID(store.InstallationID)
	validMetadata := validIDs && !store.CreatedAt.IsZero()
	if !validKind || !validMetadata {
		return ErrInvalid
	}
	return nil
}

// ObjectIdentity binds verified bytes to one private physical file incarnation.
// It is internal metadata, not proof that an untrusted caller owns a live pin.
type ObjectIdentity struct {
	StoreID       string
	Content       jobcontract.ContentIdentity
	IncarnationID string
	WitnessSHA256 string
	ChangeToken   string
}

func (object ObjectIdentity) Validate() error {
	validIDs := domain.IsUUID(object.StoreID) && domain.IsUUID(object.IncarnationID)
	validWitness := jobcontract.IsSHA256(object.WitnessSHA256) && jobcontract.IsSHA256(object.ChangeToken)
	validPhysicalIdentity := validIDs && validWitness
	if !validPhysicalIdentity || object.Content.Validate() != nil {
		return ErrInvalid
	}
	return nil
}

type Publication struct {
	Object ObjectIdentity
	At     time.Time
}

func (publication Publication) Validate() error {
	if publication.Object.Validate() != nil || publication.At.IsZero() {
		return ErrInvalid
	}
	return nil
}

type Generation struct {
	Object    ObjectIdentity
	Number    uint64
	CreatedAt time.Time
}

func (generation Generation) Validate() error {
	validNumber := generation.Number > 0 && generation.Number <= math.MaxInt64
	validMetadata := validNumber && !generation.CreatedAt.IsZero()
	if generation.Object.Validate() != nil || !validMetadata {
		return ErrInvalid
	}
	return nil
}

type Claim struct {
	Ref       string
	Object    ObjectIdentity
	MediaType string
}

func (claim Claim) Validate() error {
	validDescriptor := contentRef.MatchString(claim.Ref) && validMediaType(claim.MediaType)
	if !validDescriptor || claim.Object.Validate() != nil {
		return ErrInvalid
	}
	return nil
}

// ValidateClaims checks metadata correspondence only. Admission still requires
// manager-owned physical pins; callers cannot turn this validation into a pin.
func ValidateClaims(spec jobcontract.JobSpec, claims []Claim) error {
	validCount := len(claims) == len(spec.Assets) && len(claims) <= jobcontract.MaxAssets
	missingCollections := spec.Assets == nil || claims == nil
	if missingCollections || !validCount {
		return ErrInvalid
	}
	assets := make(map[string]jobcontract.Asset, len(spec.Assets))
	for _, asset := range spec.Assets {
		assets[asset.Ref] = asset
	}
	for _, claim := range claims {
		if claim.Validate() != nil {
			return ErrInvalid
		}
		asset, exists := assets[claim.Ref]
		if !exists {
			return ErrInvalid
		}
		sameBytes := asset.SHA256 == claim.Object.Content.SHA256 && asset.Size == claim.Object.Content.Size
		if !sameBytes || asset.MediaType != claim.MediaType {
			return ErrInvalid
		}
		delete(assets, claim.Ref)
	}
	if len(assets) != 0 {
		return ErrInvalid
	}
	return nil
}

type EntryState string

const (
	EntryCapturing     EntryState = "capturing"
	EntryLive          EntryState = "live"
	EntryReleased      EntryState = "released"
	EntryCaptureFailed EntryState = "capture_failed"
)

type Entry struct {
	ID                string
	JobID             string
	Role              jobcontract.ContentRole
	Ref               string
	RunID             *string
	StoreID           string
	SHA256            string
	Size              int64
	MediaType         string
	Generation        *uint64
	State             EntryState
	FailureCode       *string
	LinkedAt          time.Time
	ReleasedByCommand *string
}

type Inventory struct {
	JobID                string
	Revision             uint64
	SealedManifestSHA256 *string
	Entries              []Entry
}

func validMediaType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	canonical := mediaType == value && len(parameters) == 0
	validShape := strings.Count(mediaType, "/") == 1 && len(value) <= 127
	return err == nil && canonical && validShape
}
