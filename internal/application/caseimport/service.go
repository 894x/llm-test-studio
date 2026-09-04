// Package caseimport imports the repository's v2 JSON case catalog into
// versioned domain entities and coordinates idempotent persistence.
package caseimport

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	Namespace        = "builtin.cases/v2"
	ConverterVersion = 2
)

var (
	ErrInvalid      = errors.New("case import: invalid input")
	ErrInconsistent = errors.New("case import: inconsistent stored state")
	ErrUnavailable  = errors.New("case import: storage unavailable")
)

type Clock interface{ Now() time.Time }

type Store interface {
	LoadCaseImportState(context.Context, string) (State, error)
	ApplyCaseImportBatch(context.Context, Batch) error
}

type Dependencies struct {
	Store Store
	Clock Clock
}

type Service struct {
	store Store
	clock Clock
}

type State struct {
	Sources []SourceRecord
	Cases   []domain.TestCase
}

type SourceRecord struct {
	Namespace            string
	SourceKey            string
	SourcePath           string
	SourceBytesSHA256    string
	SemanticSHA256       string
	MaterializedSHA256   string
	ConverterVersion     uint64
	EntityID             string
	ImportedRevision     uint64
	BundleManifestSHA256 string
	ImportedAt           time.Time
	RetiredAt            *time.Time
}

type RecordExpectation struct {
	SourceBytesSHA256 string
	ImportedRevision  uint64
}

type Change struct {
	Source                  SourceRecord
	TestCase                domain.TestCase
	WriteEntity             bool
	ExpectedCurrentRevision uint64
	ExpectedRecord          *RecordExpectation
}

type Retirement struct {
	SourceKey string
	Expected  RecordExpectation
	RetiredAt time.Time
}

type Batch struct {
	Namespace   string
	Changes     []Change
	Retirements []Retirement
}

type Conflict struct {
	SourceKey        string `json:"source_key"`
	EntityID         string `json:"entity_id"`
	ImportedRevision uint64 `json:"imported_revision"`
	CurrentRevision  uint64 `json:"current_revision"`
}

type Result struct {
	Discovered            int        `json:"discovered"`
	Created               int        `json:"created"`
	Updated               int        `json:"updated"`
	Unchanged             int        `json:"unchanged"`
	Retired               int        `json:"retired"`
	Runnable              int        `json:"runnable"`
	Disabled              int        `json:"disabled"`
	Manual                int        `json:"manual"`
	DeferredPlanTemplates int        `json:"deferred_plan_templates"`
	BundleManifestSHA256  string     `json:"bundle_manifest_sha256"`
	Conflicts             []Conflict `json:"conflicts"`
}

type discoveredCase struct {
	convertedCase
	SourceKey string
}

func New(dependencies Dependencies) (*Service, error) {
	if isNil(dependencies.Store) || isNil(dependencies.Clock) {
		return nil, ErrInvalid
	}
	return &Service{store: dependencies.Store, clock: dependencies.Clock}, nil
}

func (service *Service) Import(ctx context.Context, bundle fs.FS) (Result, error) {
	if service == nil || isNil(bundle) {
		return Result{}, ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	cases, manifest, deferred, err := discoverBundle(ctx, bundle)
	if err != nil {
		return Result{}, err
	}
	state, err := service.store.LoadCaseImportState(ctx, Namespace)
	if err != nil {
		return Result{}, classifyStoreError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := validateState(state); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInconsistent, err)
	}
	now := service.clock.Now()
	if now.IsZero() {
		return Result{}, ErrInvalid
	}
	now = now.UTC()
	result := Result{Discovered: len(cases), DeferredPlanTemplates: deferred, BundleManifestSHA256: manifest, Conflicts: []Conflict{}}
	batch := Batch{Namespace: Namespace}
	sources := make(map[string]SourceRecord, len(state.Sources))
	currentCases := make(map[string]domain.TestCase, len(state.Cases))
	for _, source := range state.Sources {
		if source.Namespace == Namespace {
			sources[source.SourceKey] = source
		}
	}
	for _, testCase := range state.Cases {
		currentCases[testCase.ID] = testCase
	}
	seen := make(map[string]struct{}, len(cases))
	for _, candidate := range cases {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		seen[candidate.SourceKey] = struct{}{}
		switch {
		case !candidate.Enabled:
			result.Disabled++
		case candidate.ExecutionMode == domain.CaseExecutionManual:
			result.Manual++
		default:
			result.Runnable++
		}
		existing, imported := sources[candidate.SourceKey]
		if !imported {
			id := stableCaseID(candidate.SourceKey)
			if _, collision := currentCases[id]; collision {
				return Result{}, fmt.Errorf("%w: deterministic case identity collision", ErrInconsistent)
			}
			meta := domain.EntityMeta{ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: now, UpdatedAt: now}
			entity := candidate.materialize(meta)
			source := sourceRecord(candidate, entity, manifest, now)
			batch.Changes = append(batch.Changes, Change{Source: source, TestCase: entity, WriteEntity: true})
			result.Created++
			continue
		}
		current, ok := currentCases[existing.EntityID]
		if !ok || current.Revision < existing.ImportedRevision {
			return Result{}, fmt.Errorf("%w: imported case is missing", ErrInconsistent)
		}
		currentHash, err := materializedHash(current)
		if err != nil {
			return Result{}, fmt.Errorf("%w: hash current case", ErrInconsistent)
		}
		semanticChanged := existing.SemanticSHA256 != candidate.SemanticSHA256 || existing.ConverterVersion != ConverterVersion || existing.MaterializedSHA256 != candidate.MaterializedSHA256
		if semanticChanged && current.Revision > existing.ImportedRevision && currentHash != candidate.MaterializedSHA256 {
			result.Conflicts = append(result.Conflicts, Conflict{
				SourceKey: candidate.SourceKey, EntityID: current.ID,
				ImportedRevision: existing.ImportedRevision, CurrentRevision: current.Revision,
			})
			continue
		}
		writeEntity := semanticChanged && currentHash != candidate.MaterializedSHA256
		entity := current
		if writeEntity {
			meta, metaErr := current.EntityMeta.NextRevision(strictlyAfter(now, current.UpdatedAt))
			if metaErr != nil {
				return Result{}, fmt.Errorf("%w: advance imported case revision", ErrInconsistent)
			}
			entity = candidate.materialize(meta)
			result.Updated++
		} else {
			result.Unchanged++
		}
		source := sourceRecord(candidate, entity, manifest, now)
		if !semanticChanged {
			source.EntityID = existing.EntityID
			source.ImportedRevision = existing.ImportedRevision
			source.MaterializedSHA256 = existing.MaterializedSHA256
			source.ImportedAt = existing.ImportedAt
		}
		needsRecordWrite := writeEntity || existing.SourcePath != source.SourcePath || existing.SourceBytesSHA256 != source.SourceBytesSHA256 ||
			existing.SemanticSHA256 != source.SemanticSHA256 || existing.MaterializedSHA256 != source.MaterializedSHA256 ||
			existing.ConverterVersion != source.ConverterVersion || existing.ImportedRevision != source.ImportedRevision ||
			existing.BundleManifestSHA256 != source.BundleManifestSHA256 || existing.RetiredAt != nil
		if needsRecordWrite {
			expectation := &RecordExpectation{SourceBytesSHA256: existing.SourceBytesSHA256, ImportedRevision: existing.ImportedRevision}
			batch.Changes = append(batch.Changes, Change{
				Source: source, TestCase: entity, WriteEntity: writeEntity,
				ExpectedCurrentRevision: current.Revision, ExpectedRecord: expectation,
			})
		}
	}
	for _, source := range state.Sources {
		if source.Namespace != Namespace || source.RetiredAt != nil {
			continue
		}
		if _, exists := seen[source.SourceKey]; exists {
			continue
		}
		batch.Retirements = append(batch.Retirements, Retirement{
			SourceKey: source.SourceKey,
			Expected:  RecordExpectation{SourceBytesSHA256: source.SourceBytesSHA256, ImportedRevision: source.ImportedRevision},
			RetiredAt: now,
		})
		result.Retired++
	}
	if len(batch.Changes) != 0 || len(batch.Retirements) != 0 {
		if err := service.store.ApplyCaseImportBatch(ctx, batch); err != nil {
			return Result{}, classifyStoreError(ctx, err)
		}
	}
	return result, nil
}

func discoverBundle(ctx context.Context, bundle fs.FS) ([]discoveredCase, string, int, error) {
	var paths []string
	if err := fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("case bundle contains a symbolic link")
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return nil, "", 0, fmt.Errorf("scan case bundle: %w", err)
	}
	sort.Strings(paths)
	manifestLines := make([]string, 0, len(paths))
	cases := make([]discoveredCase, 0, len(paths))
	seenKeys := make(map[string]string)
	deferred := 0
	for _, path := range paths {
		raw, err := fs.ReadFile(bundle, path)
		if err != nil {
			return nil, "", 0, fmt.Errorf("read case bundle: %w", err)
		}
		rawHash := sha256Hex(raw)
		manifestLines = append(manifestLines, path+"\x00"+rawHash)
		if path == "kimi-k3/load-profile-32k.json" {
			if err := validateDeferredLoadProfile(path, raw); err != nil {
				return nil, "", 0, err
			}
			deferred++
			continue
		}
		if entryParts := strings.Split(path, "/"); len(entryParts) != 3 || entryParts[2] != "case.json" {
			return nil, "", 0, fmt.Errorf("case bundle contains unsupported JSON path %q", path)
		}
		candidate, err := convertFilesystemCase(path, raw)
		if err != nil {
			return nil, "", 0, err
		}
		sourceKey := string(candidate.Protocol) + "/" + candidate.Key
		if previous, duplicate := seenKeys[sourceKey]; duplicate {
			return nil, "", 0, fmt.Errorf("case source key %q is duplicated by %s and %s", sourceKey, previous, path)
		}
		seenKeys[sourceKey] = path
		cases = append(cases, discoveredCase{convertedCase: candidate, SourceKey: sourceKey})
	}
	return cases, sha256Hex([]byte(strings.Join(manifestLines, "\n"))), deferred, nil
}

func sourceRecord(candidate discoveredCase, entity domain.TestCase, manifest string, now time.Time) SourceRecord {
	return SourceRecord{
		Namespace: Namespace, SourceKey: candidate.SourceKey, SourcePath: candidate.SourcePath,
		SourceBytesSHA256: candidate.SourceBytesSHA256, SemanticSHA256: candidate.SemanticSHA256,
		MaterializedSHA256: candidate.MaterializedSHA256, ConverterVersion: ConverterVersion,
		EntityID: entity.ID, ImportedRevision: entity.Revision, BundleManifestSHA256: manifest,
		ImportedAt: now,
	}
}

func validateState(state State) error {
	sourceKeys := make(map[string]struct{}, len(state.Sources))
	paths := make(map[string]struct{}, len(state.Sources))
	entityIDs := make(map[string]struct{}, len(state.Sources))
	caseRevisions := make(map[string]uint64, len(state.Cases))
	for _, testCase := range state.Cases {
		if err := testCase.Validate(); err != nil {
			return err
		}
		if _, duplicate := caseRevisions[testCase.ID]; duplicate {
			return errors.New("duplicate current test case")
		}
		caseRevisions[testCase.ID] = testCase.Revision
	}
	for _, source := range state.Sources {
		if source.Namespace != Namespace || source.SourceKey == "" || source.SourcePath == "" || source.ConverterVersion == 0 ||
			!domain.IsUUID(source.EntityID) || source.ImportedRevision == 0 || !validSHA256(source.SourceBytesSHA256) ||
			!validSHA256(source.SemanticSHA256) || !validSHA256(source.MaterializedSHA256) || !validSHA256(source.BundleManifestSHA256) ||
			!canonicalUTC(source.ImportedAt) || (source.RetiredAt != nil && !canonicalUTC(*source.RetiredAt)) {
			return errors.New("invalid case import source metadata")
		}
		if _, duplicate := sourceKeys[source.SourceKey]; duplicate {
			return errors.New("duplicate case import source key")
		}
		if _, duplicate := paths[source.SourcePath]; duplicate {
			return errors.New("duplicate case import source path")
		}
		if _, duplicate := entityIDs[source.EntityID]; duplicate {
			return errors.New("duplicate imported entity id")
		}
		currentRevision, exists := caseRevisions[source.EntityID]
		if !exists || currentRevision < source.ImportedRevision {
			return errors.New("imported case source has no current entity")
		}
		sourceKeys[source.SourceKey] = struct{}{}
		paths[source.SourcePath] = struct{}{}
		entityIDs[source.EntityID] = struct{}{}
	}
	return nil
}

func stableCaseID(sourceKey string) string {
	namespace := [16]byte{0x76, 0x80, 0x78, 0x2d, 0x7a, 0xe8, 0x55, 0x8b, 0x9f, 0x32, 0x17, 0xd1, 0x3f, 0x31, 0xa6, 0x6b}
	hash := sha1.New()
	_, _ = hash.Write(namespace[:])
	_, _ = hash.Write([]byte(Namespace + "/" + sourceKey))
	value := hash.Sum(nil)[:16]
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func strictlyAfter(candidate, previous time.Time) time.Time {
	if candidate.After(previous) {
		return candidate
	}
	return previous.Add(time.Nanosecond)
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalUTC(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func classifyStoreError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
