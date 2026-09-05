package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/caseimport"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const (
	legacyAuthoredCatalogMigrationSchemaVersion           = 3
	legacyAuthoredCatalogMigrationPersistentSchemaVersion = 2
	legacyAuthoredCatalogMigrationPreviousSchemaVersion   = 1
)

var (
	errLegacyAuthoredCatalogMigrationInvalid  = errors.New("legacy authored catalog migration: invalid input")
	errLegacyAuthoredCatalogMigrationConflict = errors.New("legacy authored catalog migration: file catalog conflict")
	errLegacyAuthoredCatalogMigrationMarker   = errors.New("legacy authored catalog migration: invalid completion marker")
)

// Credential references are deliberately excluded: Channel.CredentialID is
// sufficient to retain the keyring association without reading or copying any
// secret.
type legacyAuthoredCatalogSource interface {
	ListModels(context.Context) ([]domain.Model, error)
	ListChannels(context.Context) ([]domain.Channel, error)
	ListChannelModels(context.Context) ([]domain.ChannelModel, error)
	ListTestCases(context.Context) ([]domain.TestCase, error)
	ListSuites(context.Context) ([]domain.Suite, error)
	ListPlans(context.Context) ([]domain.Plan, error)
	ResolvePlanTargetSelection(context.Context, domain.Plan, string, string) (domain.Model, domain.Channel, domain.ChannelModel, error)
	GetTestCaseRevision(context.Context, string, uint64) (domain.TestCase, error)
	GetSuiteRevision(context.Context, string, uint64) (domain.Suite, error)
}

type legacyAuthoredCatalogMigrationTarget interface {
	ListModels(context.Context) ([]domain.Model, error)
	ListChannels(context.Context) ([]domain.Channel, error)
	ListChannelModels(context.Context) ([]domain.ChannelModel, error)
	ListTestCases(context.Context) ([]domain.TestCase, error)
	ListSuites(context.Context) ([]domain.Suite, error)
	ListPlanDocuments(context.Context) ([]plancatalog.Document, error)
	GetTestCaseRevision(context.Context, string, uint64) (domain.TestCase, error)
	GetSuiteRevision(context.Context, string, uint64) (domain.Suite, error)

	CreateModel(context.Context, domain.Model) error
	CreateChannel(context.Context, domain.Channel) error
	CreateChannelModel(context.Context, domain.ChannelModel) error
	CreateTestCase(context.Context, domain.TestCase) error
	CreateSuite(context.Context, domain.Suite) error
	CreatePlanDocument(context.Context, plancatalog.Document) error

	UpdateModel(context.Context, uint64, domain.Model) error
	UpdateChannel(context.Context, uint64, domain.Channel) error
	UpdateChannelModel(context.Context, uint64, domain.ChannelModel) error
	UpdatePlanDocument(context.Context, uint64, plancatalog.Document) error
	StoreTestCaseRevision(context.Context, domain.TestCase) error
	StoreSuiteRevision(context.Context, domain.Suite) error
	WithAuthoredCatalogRetirementLock(context.Context, func(context.Context) error) error
}

func (repository filesystemCatalogRepository) WithAuthoredCatalogRetirementLock(
	ctx context.Context,
	action func(context.Context) error,
) error {
	if ctx == nil || action == nil || !filepath.IsAbs(repository.lockPath) {
		return errLegacyAuthoredCatalogMigrationInvalid
	}
	return repository.WithCredentialMutation(ctx, action)
}

type legacyAuthoredCatalogMigrationMarker struct {
	SchemaVersion          int               `json:"schema_version"`
	Completed              bool              `json:"completed"`
	ProcessedSourceDigests []string          `json:"processed_source_digests"`
	TargetDigestsBySource  map[string]string `json:"target_digests_by_source,omitempty"`
}

type legacyAuthoredCatalogSnapshot struct {
	Models       []domain.Model         `json:"models"`
	Channels     []domain.Channel       `json:"channels"`
	Mappings     []domain.ChannelModel  `json:"mappings"`
	Cases        []domain.TestCase      `json:"cases"`
	Suites       []domain.Suite         `json:"suites"`
	Plans        []plancatalog.Document `json:"plans"`
	PinnedCases  []domain.TestCase      `json:"pinned_cases"`
	PinnedSuites []domain.Suite         `json:"pinned_suites"`
}

func migrateLegacyAuthoredCatalog(
	ctx context.Context,
	source legacyAuthoredCatalogSource,
	target legacyAuthoredCatalogMigrationTarget,
	markerPath string,
) error {
	if ctx == nil || strings.TrimSpace(markerPath) == "" || !filepath.IsAbs(markerPath) {
		return errLegacyAuthoredCatalogMigrationInvalid
	}
	marker, complete, err := legacyAuthoredCatalogMigrationComplete(ctx, markerPath)
	if err != nil {
		return err
	}
	if source == nil || target == nil {
		return errLegacyAuthoredCatalogMigrationInvalid
	}

	snapshot, err := loadLegacyAuthoredCatalogSnapshot(ctx, source)
	if err != nil {
		return err
	}
	sourceDigest, err := digestLegacyAuthoredCatalogSnapshot(snapshot)
	if err != nil {
		return err
	}
	if marker.SchemaVersion == legacyAuthoredCatalogMigrationPreviousSchemaVersion {
		v1SourceDigest, err := digestLegacyAuthoredCatalogV1Snapshot(snapshot)
		if err != nil {
			return err
		}
		if containsLegacySourceDigest(marker.ProcessedSourceDigests, v1SourceDigest) {
			// Schema v1 completed Models, Channels, Mappings, Plans, and Plan-pinned
			// Cases but did not cover bulk Cases and Suites. Only when the legacy
			// v1-shaped source digest matches may its completion checkpoint protect
			// those writes. A marker from another database must not block this
			// source's normal export.
			target = legacyAuthoredCatalogV1UpgradeTarget{legacyAuthoredCatalogMigrationTarget: target}
		}
		// The v1 source digest covered a different snapshot shape, so do not
		// carry it into the complete v3 source-to-target receipt.
		marker = legacyAuthoredCatalogMigrationMarker{}
	}
	if complete && containsLegacySourceDigest(marker.ProcessedSourceDigests, sourceDigest) {
		expectedTargetDigest := marker.TargetDigestsBySource[sourceDigest]
		actualTargetDigest, digestErr := digestMigratedFileCatalog(ctx, target)
		if digestErr != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("%w: verify completed target manifest: %v", errLegacyAuthoredCatalogMigrationConflict, digestErr)
		}
		if actualTargetDigest == expectedTargetDigest {
			// This branch is only reachable while the unchanged v10 source still
			// exists. Once schema retirement reaches v11 the production bootstrap
			// no longer calls this migration, so later user-authored file edits are
			// not compared with this crash-window receipt.
			return nil
		}
		// The target may have gained unrelated authored files after the export
		// receipt was written but before SQLite retirement acquired the global
		// authored lock. Verify the already-exported source footprint without
		// permitting any repair: an additive, non-conflicting change reaches the
		// end and refreshes the receipt, while a deleted or edited exported
		// entity attempts a mutation and fails closed.
		target = legacyAuthoredCatalogReadOnlyTarget{legacyAuthoredCatalogMigrationTarget: target}
	}
	currentModels, err := target.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("list file models: %w", err)
	}
	if err := migrateLegacyEntities(ctx, "model", snapshot.Models, currentModels, modelMigrationAdapter(target)); err != nil {
		return err
	}

	currentChannels, err := target.ListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list file channels: %w", err)
	}
	if err := migrateLegacyEntities(ctx, "channel", snapshot.Channels, currentChannels, channelMigrationAdapter(target)); err != nil {
		return err
	}

	currentMappings, err := target.ListChannelModels(ctx)
	if err != nil {
		return fmt.Errorf("list file channel mappings: %w", err)
	}
	if err := migrateLegacyEntities(ctx, "channel mapping", snapshot.Mappings, currentMappings, mappingMigrationAdapter(target)); err != nil {
		return err
	}
	if err := migrateLegacyAuthoredCases(ctx, target, snapshot.Cases); err != nil {
		return err
	}
	if err := migrateLegacyAuthoredSuites(ctx, source, target, snapshot.Suites); err != nil {
		return err
	}
	migrationSnapshot, err := remapLegacyPlanReferences(ctx, target, snapshot)
	if err != nil {
		return err
	}

	// Plan creation validates every exact Case revision under the authored
	// catalog lock, so materialize legacy pinned revisions before the Plan
	// document that references them.
	if err := migrateLegacyPinnedCaseRevisions(ctx, target, migrationSnapshot, planProjections(migrationSnapshot.Plans)); err != nil {
		return err
	}
	if err := migrateLegacyPinnedSuiteRevisions(ctx, target, migrationSnapshot, planProjections(migrationSnapshot.Plans)); err != nil {
		return err
	}

	currentPlans, err := target.ListPlanDocuments(ctx)
	if err != nil {
		return fmt.Errorf("list file plans: %w", err)
	}
	if err := migrateLegacyEntities(ctx, "plan", migrationSnapshot.Plans, currentPlans, planMigrationAdapter(target)); err != nil {
		return err
	}
	migratedPlans, err := target.ListPlanDocuments(ctx)
	if err != nil {
		return fmt.Errorf("list migrated file plans: %w", err)
	}
	if err := migrateLegacyPinnedCaseRevisions(ctx, target, migrationSnapshot, planProjections(migratedPlans)); err != nil {
		return err
	}
	if err := migrateLegacyPinnedSuiteRevisions(ctx, target, migrationSnapshot, planProjections(migratedPlans)); err != nil {
		return err
	}

	targetDigest, err := digestMigratedFileCatalog(ctx, target)
	if err != nil {
		return fmt.Errorf("fingerprint migrated file catalog: %w", err)
	}
	targetDigestsBySource := make(map[string]string, len(marker.TargetDigestsBySource)+1)
	for processedSource, processedTarget := range marker.TargetDigestsBySource {
		targetDigestsBySource[processedSource] = processedTarget
	}
	targetDigestsBySource[sourceDigest] = targetDigest
	processedSourceDigests := append([]string(nil), marker.ProcessedSourceDigests...)
	if !containsLegacySourceDigest(processedSourceDigests, sourceDigest) {
		processedSourceDigests = append(processedSourceDigests, sourceDigest)
	}
	marker = legacyAuthoredCatalogMigrationMarker{
		SchemaVersion:          legacyAuthoredCatalogMigrationSchemaVersion,
		Completed:              true,
		ProcessedSourceDigests: processedSourceDigests,
		TargetDigestsBySource:  targetDigestsBySource,
	}
	sort.Strings(marker.ProcessedSourceDigests)
	payload, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode legacy authored catalog migration marker: %w", err)
	}
	if err := fileconfig.WriteAtomically(ctx, markerPath, append(payload, '\n')); err != nil {
		return fmt.Errorf("write legacy authored catalog migration marker: %w", err)
	}
	return nil
}

type legacyAuthoredCatalogReadOnlyTarget struct {
	legacyAuthoredCatalogMigrationTarget
}

type legacyAuthoredCatalogV1UpgradeTarget struct {
	legacyAuthoredCatalogMigrationTarget
}

func (legacyAuthoredCatalogReadOnlyTarget) mutation(kind, id string) error {
	return fmt.Errorf(
		"%w: completed target receipt would mutate %s %q",
		errLegacyAuthoredCatalogMigrationConflict,
		kind,
		id,
	)
}

func (target legacyAuthoredCatalogReadOnlyTarget) CreateModel(_ context.Context, value domain.Model) error {
	return target.mutation("model", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) CreateChannel(_ context.Context, value domain.Channel) error {
	return target.mutation("channel", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) CreateChannelModel(_ context.Context, value domain.ChannelModel) error {
	return target.mutation("channel mapping", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) CreateTestCase(_ context.Context, value domain.TestCase) error {
	return target.mutation("Case", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) CreateSuite(_ context.Context, value domain.Suite) error {
	return target.mutation("Suite", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) CreatePlanDocument(_ context.Context, value plancatalog.Document) error {
	return target.mutation("Plan", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) UpdateModel(_ context.Context, _ uint64, value domain.Model) error {
	return target.mutation("model", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) UpdateChannel(_ context.Context, _ uint64, value domain.Channel) error {
	return target.mutation("channel", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) UpdateChannelModel(_ context.Context, _ uint64, value domain.ChannelModel) error {
	return target.mutation("channel mapping", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) UpdatePlanDocument(_ context.Context, _ uint64, value plancatalog.Document) error {
	return target.mutation("Plan", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) StoreTestCaseRevision(_ context.Context, value domain.TestCase) error {
	return target.mutation("Case revision", value.ID)
}

func (target legacyAuthoredCatalogReadOnlyTarget) StoreSuiteRevision(_ context.Context, value domain.Suite) error {
	return target.mutation("Suite revision", value.ID)
}

func (legacyAuthoredCatalogV1UpgradeTarget) mutation(kind, id string) error {
	return fmt.Errorf(
		"%w: v1 completion checkpoint would mutate previously exported %s %q",
		errLegacyAuthoredCatalogMigrationConflict,
		kind,
		id,
	)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) CreateModel(_ context.Context, value domain.Model) error {
	return target.mutation("model", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) CreateChannel(_ context.Context, value domain.Channel) error {
	return target.mutation("channel", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) CreateChannelModel(_ context.Context, value domain.ChannelModel) error {
	return target.mutation("channel mapping", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) CreatePlanDocument(_ context.Context, value plancatalog.Document) error {
	return target.mutation("Plan", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) UpdateModel(_ context.Context, _ uint64, value domain.Model) error {
	return target.mutation("model", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) UpdateChannel(_ context.Context, _ uint64, value domain.Channel) error {
	return target.mutation("channel", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) UpdateChannelModel(_ context.Context, _ uint64, value domain.ChannelModel) error {
	return target.mutation("channel mapping", value.ID)
}

func (target legacyAuthoredCatalogV1UpgradeTarget) UpdatePlanDocument(_ context.Context, _ uint64, value plancatalog.Document) error {
	return target.mutation("Plan", value.ID)
}

func containsLegacySourceDigest(digests []string, target string) bool {
	index := sort.SearchStrings(digests, target)
	return index < len(digests) && digests[index] == target
}

func loadLegacyAuthoredCatalogSnapshot(
	ctx context.Context,
	source legacyAuthoredCatalogSource,
) (legacyAuthoredCatalogSnapshot, error) {
	models, err := source.ListModels(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list legacy models: %w", err)
	}
	channels, err := source.ListChannels(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list legacy channels: %w", err)
	}
	mappings, err := source.ListChannelModels(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list legacy channel mappings: %w", err)
	}
	cases, err := source.ListTestCases(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list legacy Cases: %w", err)
	}
	suites, err := source.ListSuites(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list legacy Suites: %w", err)
	}
	plans, err := source.ListPlans(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list legacy plans: %w", err)
	}
	planDocuments := make([]plancatalog.Document, 0, len(plans))
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		document := plancatalog.Document{
			FileSchemaVersion: plancatalog.CurrentFileSchemaVersion,
			Plan:              plan,
			TargetBindings:    []plancatalog.TargetBinding{},
		}
		for _, channelID := range plan.ChannelIDs {
			for _, modelID := range plan.ModelIDs {
				model, channel, mapping, err := source.ResolvePlanTargetSelection(ctx, plan, modelID, channelID)
				if err != nil {
					return legacyAuthoredCatalogSnapshot{}, fmt.Errorf(
						"resolve legacy plan %q target model %q channel %q: %w",
						plan.ID, modelID, channelID, err,
					)
				}
				document.TargetBindings = append(document.TargetBindings, plancatalog.TargetBinding{
					Model: model, Channel: channel, Mapping: mapping,
				})
			}
		}
		if err := document.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf(
				"%w: legacy plan %q target bindings: %v",
				errLegacyAuthoredCatalogMigrationInvalid, plan.ID, err,
			)
		}
		planDocuments = append(planDocuments, document)
	}

	suiteRefs := make(map[legacyPinnedSuiteRevisionKey]domain.EntityRevisionRef)
	for _, document := range planDocuments {
		plan := document.Plan
		if plan.SuiteID != "" {
			ref := domain.EntityRevisionRef{ID: plan.SuiteID, Revision: plan.SuiteRevision}
			suiteRefs[legacyPinnedSuiteRevisionKey{suiteID: ref.ID, revision: ref.Revision}] = ref
		}
	}
	suiteKeys := make([]legacyPinnedSuiteRevisionKey, 0, len(suiteRefs))
	for key := range suiteRefs {
		suiteKeys = append(suiteKeys, key)
	}
	sort.Slice(suiteKeys, func(left, right int) bool {
		if suiteKeys[left].suiteID == suiteKeys[right].suiteID {
			return suiteKeys[left].revision < suiteKeys[right].revision
		}
		return suiteKeys[left].suiteID < suiteKeys[right].suiteID
	})
	pinnedSuites := make([]domain.Suite, 0, len(suiteKeys))
	for _, key := range suiteKeys {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		legacy, err := source.GetSuiteRevision(ctx, key.suiteID, key.revision)
		if err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("load legacy pinned Suite %q revision %d: %w", key.suiteID, key.revision, err)
		}
		if !validPinnedSuiteRevision(legacy, suiteRefs[key]) {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: legacy pinned Suite %q revision %d", errLegacyAuthoredCatalogMigrationInvalid, key.suiteID, key.revision)
		}
		pinnedSuites = append(pinnedSuites, legacy)
	}

	refs := make(map[legacyPinnedCaseRevisionKey]domain.CaseRevisionRef)
	for _, document := range planDocuments {
		plan := document.Plan
		for _, ref := range plan.Cases {
			refs[legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}] = ref
		}
	}
	for _, suite := range pinnedSuites {
		for _, ref := range suite.Cases {
			refs[legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}] = ref
		}
	}
	keys := make([]legacyPinnedCaseRevisionKey, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].caseID == keys[right].caseID {
			return keys[left].revision < keys[right].revision
		}
		return keys[left].caseID < keys[right].caseID
	})
	pinnedCases := make([]domain.TestCase, 0, len(keys))
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		legacy, err := source.GetTestCaseRevision(ctx, key.caseID, key.revision)
		if err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("load legacy pinned Case %q revision %d: %w", key.caseID, key.revision, err)
		}
		if !validPinnedCaseRevision(legacy, refs[key]) {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: legacy pinned Case %q revision %d", errLegacyAuthoredCatalogMigrationInvalid, key.caseID, key.revision)
		}
		pinnedCases = append(pinnedCases, legacy)
	}

	snapshot := legacyAuthoredCatalogSnapshot{
		Models:       append([]domain.Model(nil), models...),
		Channels:     append([]domain.Channel(nil), channels...),
		Mappings:     append([]domain.ChannelModel(nil), mappings...),
		Cases:        append([]domain.TestCase(nil), cases...),
		Suites:       append([]domain.Suite(nil), suites...),
		Plans:        append([]plancatalog.Document(nil), planDocuments...),
		PinnedCases:  pinnedCases,
		PinnedSuites: pinnedSuites,
	}
	sortLegacyAuthoredCatalogSnapshot(&snapshot)
	return snapshot, nil
}

func sortLegacyAuthoredCatalogSnapshot(snapshot *legacyAuthoredCatalogSnapshot) {
	sort.Slice(snapshot.Models, func(left, right int) bool { return snapshot.Models[left].ID < snapshot.Models[right].ID })
	sort.Slice(snapshot.Channels, func(left, right int) bool { return snapshot.Channels[left].ID < snapshot.Channels[right].ID })
	sort.Slice(snapshot.Mappings, func(left, right int) bool { return snapshot.Mappings[left].ID < snapshot.Mappings[right].ID })
	sort.Slice(snapshot.Cases, func(left, right int) bool {
		leftIdentity := string(snapshot.Cases[left].Protocol) + "/" + snapshot.Cases[left].Key
		rightIdentity := string(snapshot.Cases[right].Protocol) + "/" + snapshot.Cases[right].Key
		if leftIdentity == rightIdentity {
			return snapshot.Cases[left].ID < snapshot.Cases[right].ID
		}
		return leftIdentity < rightIdentity
	})
	sort.Slice(snapshot.Suites, func(left, right int) bool {
		leftIdentity := string(snapshot.Suites[left].Protocol) + "/" + snapshot.Suites[left].Key
		rightIdentity := string(snapshot.Suites[right].Protocol) + "/" + snapshot.Suites[right].Key
		if leftIdentity == rightIdentity {
			return snapshot.Suites[left].ID < snapshot.Suites[right].ID
		}
		return leftIdentity < rightIdentity
	})
	sort.Slice(snapshot.Plans, func(left, right int) bool { return snapshot.Plans[left].ID < snapshot.Plans[right].ID })
	sort.Slice(snapshot.PinnedCases, func(left, right int) bool {
		if snapshot.PinnedCases[left].ID == snapshot.PinnedCases[right].ID {
			return snapshot.PinnedCases[left].Revision < snapshot.PinnedCases[right].Revision
		}
		return snapshot.PinnedCases[left].ID < snapshot.PinnedCases[right].ID
	})
	sort.Slice(snapshot.PinnedSuites, func(left, right int) bool {
		if snapshot.PinnedSuites[left].ID == snapshot.PinnedSuites[right].ID {
			return snapshot.PinnedSuites[left].Revision < snapshot.PinnedSuites[right].Revision
		}
		return snapshot.PinnedSuites[left].ID < snapshot.PinnedSuites[right].ID
	})
}

func planProjections(documents []plancatalog.Document) []domain.Plan {
	plans := make([]domain.Plan, len(documents))
	for index, document := range documents {
		plans[index] = document.Plan
	}
	return plans
}

func digestLegacyAuthoredCatalogSnapshot(snapshot legacyAuthoredCatalogSnapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode legacy authored catalog source fingerprint: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// legacyAuthoredCatalogV1Snapshot exactly preserves the source fingerprint
// shape written by schema-v1 completion markers. That version covered the
// three target catalogs, Plans, and only exact Cases pinned directly by Plans.
type legacyAuthoredCatalogV1Snapshot struct {
	Models      []domain.Model         `json:"models"`
	Channels    []domain.Channel       `json:"channels"`
	Mappings    []domain.ChannelModel  `json:"mappings"`
	Plans       []plancatalog.Document `json:"plans"`
	PinnedCases []domain.TestCase      `json:"pinned_cases"`
}

func digestLegacyAuthoredCatalogV1Snapshot(snapshot legacyAuthoredCatalogSnapshot) (string, error) {
	pinnedByKey := make(map[legacyPinnedCaseRevisionKey]domain.TestCase, len(snapshot.PinnedCases))
	for _, testCase := range snapshot.PinnedCases {
		pinnedByKey[legacyPinnedCaseRevisionKey{caseID: testCase.ID, revision: testCase.Revision}] = testCase
	}
	directRefs := make(map[legacyPinnedCaseRevisionKey]struct{})
	for _, document := range snapshot.Plans {
		for _, ref := range document.Cases {
			directRefs[legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}] = struct{}{}
		}
	}
	keys := make([]legacyPinnedCaseRevisionKey, 0, len(directRefs))
	for key := range directRefs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].caseID == keys[right].caseID {
			return keys[left].revision < keys[right].revision
		}
		return keys[left].caseID < keys[right].caseID
	})
	pinnedCases := make([]domain.TestCase, 0, len(keys))
	for _, key := range keys {
		testCase, exists := pinnedByKey[key]
		if !exists {
			return "", fmt.Errorf(
				"%w: v1 source fingerprint is missing pinned Case %q revision %d",
				errLegacyAuthoredCatalogMigrationInvalid,
				key.caseID,
				key.revision,
			)
		}
		pinnedCases = append(pinnedCases, testCase)
	}
	v1 := legacyAuthoredCatalogV1Snapshot{
		Models:      append([]domain.Model(nil), snapshot.Models...),
		Channels:    append([]domain.Channel(nil), snapshot.Channels...),
		Mappings:    append([]domain.ChannelModel(nil), snapshot.Mappings...),
		Plans:       append([]plancatalog.Document(nil), snapshot.Plans...),
		PinnedCases: pinnedCases,
	}
	payload, err := json.Marshal(v1)
	if err != nil {
		return "", fmt.Errorf("encode legacy authored catalog v1 source fingerprint: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// digestMigratedFileCatalog records the complete authored target reached by a
// successful export. The receipt closes the crash window between writing the
// marker and retiring the v10 tables: an unchanged source digest alone cannot
// prove that the already-exported files still exist or still contain the same
// data on the next startup.
func digestMigratedFileCatalog(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
) (string, error) {
	snapshot, err := loadMigratedFileCatalogSnapshot(ctx, target)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode migrated authored catalog target fingerprint: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func loadMigratedFileCatalogSnapshot(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
) (legacyAuthoredCatalogSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return legacyAuthoredCatalogSnapshot{}, err
	}
	models, err := target.ListModels(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list migrated file models: %w", err)
	}
	channels, err := target.ListChannels(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list migrated file channels: %w", err)
	}
	mappings, err := target.ListChannelModels(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list migrated file channel mappings: %w", err)
	}
	cases, err := target.ListTestCases(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list migrated file Cases: %w", err)
	}
	suites, err := target.ListSuites(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list migrated file Suites: %w", err)
	}
	plans, err := target.ListPlanDocuments(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list migrated file plans: %w", err)
	}

	for _, model := range models {
		if err := model.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file model %q is invalid", errLegacyAuthoredCatalogMigrationConflict, model.ID)
		}
	}
	for _, channel := range channels {
		if err := channel.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file channel %q is invalid", errLegacyAuthoredCatalogMigrationConflict, channel.ID)
		}
	}
	for _, mapping := range mappings {
		if err := mapping.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file channel mapping %q is invalid", errLegacyAuthoredCatalogMigrationConflict, mapping.ID)
		}
	}
	for _, testCase := range cases {
		if err := testCase.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file Case %q is invalid", errLegacyAuthoredCatalogMigrationConflict, testCase.ID)
		}
	}
	caseRefs := make(map[legacyPinnedCaseRevisionKey]domain.CaseRevisionRef)
	for _, suite := range suites {
		if err := suite.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file Suite %q is invalid", errLegacyAuthoredCatalogMigrationConflict, suite.ID)
		}
		// Current Suite documents are part of the manifest, and their exact
		// Case revisions are independently covered so a missing revision
		// sidecar cannot be hidden by an otherwise intact Suite file.
		for _, ref := range suite.Cases {
			caseRefs[legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}] = ref
		}
	}

	suiteRefs := make(map[legacyPinnedSuiteRevisionKey]domain.EntityRevisionRef)
	for _, document := range plans {
		if err := document.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file Plan %q is invalid", errLegacyAuthoredCatalogMigrationConflict, document.ID)
		}
		for _, ref := range document.Cases {
			caseRefs[legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}] = ref
		}
		if document.SuiteID != "" {
			ref := domain.EntityRevisionRef{ID: document.SuiteID, Revision: document.SuiteRevision}
			suiteRefs[legacyPinnedSuiteRevisionKey{suiteID: ref.ID, revision: ref.Revision}] = ref
		}
	}

	suiteKeys := make([]legacyPinnedSuiteRevisionKey, 0, len(suiteRefs))
	for key := range suiteRefs {
		suiteKeys = append(suiteKeys, key)
	}
	sort.Slice(suiteKeys, func(left, right int) bool {
		if suiteKeys[left].suiteID == suiteKeys[right].suiteID {
			return suiteKeys[left].revision < suiteKeys[right].revision
		}
		return suiteKeys[left].suiteID < suiteKeys[right].suiteID
	})
	pinnedSuites := make([]domain.Suite, 0, len(suiteKeys))
	for _, key := range suiteKeys {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		stored, err := target.GetSuiteRevision(ctx, key.suiteID, key.revision)
		if err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("resolve migrated file pinned Suite %q revision %d: %w", key.suiteID, key.revision, err)
		}
		if !validPinnedSuiteRevision(stored, suiteRefs[key]) {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file pinned Suite %q revision %d is not exact", errLegacyAuthoredCatalogMigrationConflict, key.suiteID, key.revision)
		}
		pinnedSuites = append(pinnedSuites, stored)
		// Plan Suite revisions are historical authored documents. Cover the
		// exact Case documents referenced inside them even when a Case is not
		// listed directly on the Plan.
		for _, ref := range stored.Cases {
			caseRefs[legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}] = ref
		}
	}

	caseKeys := make([]legacyPinnedCaseRevisionKey, 0, len(caseRefs))
	for key := range caseRefs {
		caseKeys = append(caseKeys, key)
	}
	sort.Slice(caseKeys, func(left, right int) bool {
		if caseKeys[left].caseID == caseKeys[right].caseID {
			return caseKeys[left].revision < caseKeys[right].revision
		}
		return caseKeys[left].caseID < caseKeys[right].caseID
	})
	pinnedCases := make([]domain.TestCase, 0, len(caseKeys))
	for _, key := range caseKeys {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		stored, err := target.GetTestCaseRevision(ctx, key.caseID, key.revision)
		if err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("resolve migrated file pinned Case %q revision %d: %w", key.caseID, key.revision, err)
		}
		if !validPinnedCaseRevision(stored, caseRefs[key]) {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: migrated file pinned Case %q revision %d is not exact", errLegacyAuthoredCatalogMigrationConflict, key.caseID, key.revision)
		}
		pinnedCases = append(pinnedCases, stored)
	}

	snapshot := legacyAuthoredCatalogSnapshot{
		Models:       append([]domain.Model(nil), models...),
		Channels:     append([]domain.Channel(nil), channels...),
		Mappings:     append([]domain.ChannelModel(nil), mappings...),
		Cases:        append([]domain.TestCase(nil), cases...),
		Suites:       append([]domain.Suite(nil), suites...),
		Plans:        append([]plancatalog.Document(nil), plans...),
		PinnedCases:  pinnedCases,
		PinnedSuites: pinnedSuites,
	}
	sortLegacyAuthoredCatalogSnapshot(&snapshot)
	return snapshot, nil
}

func migrateLegacyAuthoredCases(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	legacy []domain.TestCase,
) error {
	current, err := target.ListTestCases(ctx)
	if err != nil {
		return fmt.Errorf("list file Cases: %w", err)
	}
	currentByIdentity := make(map[string]domain.TestCase, len(current))
	for _, testCase := range current {
		identity := authoredCaseIdentity(testCase)
		if _, duplicate := currentByIdentity[identity]; duplicate {
			return fmt.Errorf("%w: duplicate file Case %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		currentByIdentity[identity] = testCase
	}
	seenLegacy := make(map[string]struct{}, len(legacy))
	for _, desired := range legacy {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := desired.Validate(); err != nil {
			return fmt.Errorf("%w: legacy Case %q is invalid", errLegacyAuthoredCatalogMigrationInvalid, desired.ID)
		}
		identity := authoredCaseIdentity(desired)
		if _, duplicate := seenLegacy[identity]; duplicate {
			return fmt.Errorf("%w: duplicate legacy Case identity %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		seenLegacy[identity] = struct{}{}
		if persisted, exists := currentByIdentity[identity]; exists {
			same, err := sameAuthoredCaseSemantics(persisted, desired)
			if err != nil {
				return fmt.Errorf("compare file Case %q: %w", identity, err)
			}
			if !same {
				return fmt.Errorf("%w: Case %q differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, identity)
			}
			continue
		}
		if err := target.CreateTestCase(ctx, desired); err != nil {
			return fmt.Errorf("create file Case %q: %w", identity, err)
		}
		persisted, err := findAuthoredCaseByIdentity(ctx, target, identity)
		if err != nil {
			return fmt.Errorf("verify file Case %q: %w", identity, err)
		}
		same, err := sameAuthoredCaseSemantics(persisted, desired)
		if err != nil || !same {
			return fmt.Errorf("%w: file Case %q differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		currentByIdentity[identity] = persisted
	}
	return nil
}

func authoredCaseIdentity(testCase domain.TestCase) string {
	return string(testCase.Protocol) + "/" + testCase.Key
}

func findAuthoredCaseByIdentity(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	identity string,
) (domain.TestCase, error) {
	cases, err := target.ListTestCases(ctx)
	if err != nil {
		return domain.TestCase{}, err
	}
	var result domain.TestCase
	found := false
	for _, testCase := range cases {
		if authoredCaseIdentity(testCase) != identity {
			continue
		}
		if found {
			return domain.TestCase{}, fmt.Errorf("%w: duplicate file Case %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		result, found = testCase, true
	}
	if !found {
		return domain.TestCase{}, fs.ErrNotExist
	}
	return result, nil
}

func sameAuthoredCaseSemantics(left, right domain.TestCase) (bool, error) {
	leftDigest, err := caseimport.MaterializedSHA256(left)
	if err != nil {
		return false, err
	}
	rightDigest, err := caseimport.MaterializedSHA256(right)
	if err != nil {
		return false, err
	}
	return leftDigest == rightDigest, nil
}

func migrateLegacyAuthoredSuites(
	ctx context.Context,
	source legacyAuthoredCatalogSource,
	target legacyAuthoredCatalogMigrationTarget,
	legacy []domain.Suite,
) error {
	current, err := target.ListSuites(ctx)
	if err != nil {
		return fmt.Errorf("list file Suites: %w", err)
	}
	currentByIdentity := make(map[string]domain.Suite, len(current))
	for _, suite := range current {
		identity := authoredSuiteIdentity(suite)
		if _, duplicate := currentByIdentity[identity]; duplicate {
			return fmt.Errorf("%w: duplicate file Suite %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		currentByIdentity[identity] = suite
	}
	seenLegacy := make(map[string]struct{}, len(legacy))
	for _, legacySuite := range legacy {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := legacySuite.Validate(); err != nil {
			return fmt.Errorf("%w: legacy Suite %q is invalid", errLegacyAuthoredCatalogMigrationInvalid, legacySuite.ID)
		}
		identity := authoredSuiteIdentity(legacySuite)
		if _, duplicate := seenLegacy[identity]; duplicate {
			return fmt.Errorf("%w: duplicate legacy Suite identity %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		seenLegacy[identity] = struct{}{}

		desired := legacySuite
		desired.Cases = make([]domain.CaseRevisionRef, len(legacySuite.Cases))
		seenCaseIdentities := make(map[string]struct{}, len(legacySuite.Cases))
		for index, legacyRef := range legacySuite.Cases {
			legacyCase, err := source.GetTestCaseRevision(ctx, legacyRef.CaseID, legacyRef.Revision)
			if err != nil {
				return fmt.Errorf("resolve legacy Suite %q Case %q revision %d: %w", identity, legacyRef.CaseID, legacyRef.Revision, err)
			}
			if !validPinnedCaseRevision(legacyCase, legacyRef) || legacyCase.Protocol != legacySuite.Protocol ||
				!legacyCase.AppliesToModel(legacySuite.ModelTarget) {
				return fmt.Errorf("%w: legacy Suite %q Case %q revision %d is incompatible", errLegacyAuthoredCatalogMigrationInvalid, identity, legacyRef.CaseID, legacyRef.Revision)
			}
			caseIdentity := authoredCaseIdentity(legacyCase)
			if _, duplicate := seenCaseIdentities[caseIdentity]; duplicate {
				return fmt.Errorf("%w: legacy Suite %q has duplicate Case identity %q", errLegacyAuthoredCatalogMigrationConflict, identity, caseIdentity)
			}
			seenCaseIdentities[caseIdentity] = struct{}{}
			fileCase, err := findAuthoredCaseByIdentity(ctx, target, caseIdentity)
			if err != nil {
				return fmt.Errorf("resolve file Suite %q Case %q: %w", identity, caseIdentity, err)
			}
			if fileCase.Protocol != legacySuite.Protocol || !fileCase.AppliesToModel(legacySuite.ModelTarget) {
				return fmt.Errorf("%w: file Suite %q Case %q is incompatible", errLegacyAuthoredCatalogMigrationConflict, identity, caseIdentity)
			}
			desired.Cases[index] = domain.CaseRevisionRef{CaseID: fileCase.ID, Revision: fileCase.Revision}
		}

		if persisted, exists := currentByIdentity[identity]; exists {
			if !sameAuthoredSuiteSemantics(persisted, desired) {
				return fmt.Errorf("%w: Suite %q differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, identity)
			}
			continue
		}
		if err := target.CreateSuite(ctx, desired); err != nil {
			return fmt.Errorf("create file Suite %q: %w", identity, err)
		}
		persisted, err := findAuthoredSuiteByIdentity(ctx, target, identity)
		if err != nil {
			return fmt.Errorf("verify file Suite %q: %w", identity, err)
		}
		if !sameAuthoredSuiteSemantics(persisted, desired) {
			return fmt.Errorf("%w: file Suite %q differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		currentByIdentity[identity] = persisted
	}
	return nil
}

func authoredSuiteIdentity(suite domain.Suite) string {
	return string(suite.Protocol) + "/" + suite.Key
}

func findAuthoredSuiteByIdentity(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	identity string,
) (domain.Suite, error) {
	suites, err := target.ListSuites(ctx)
	if err != nil {
		return domain.Suite{}, err
	}
	var result domain.Suite
	found := false
	for _, suite := range suites {
		if authoredSuiteIdentity(suite) != identity {
			continue
		}
		if found {
			return domain.Suite{}, fmt.Errorf("%w: duplicate file Suite %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		result, found = suite, true
	}
	if !found {
		return domain.Suite{}, fs.ErrNotExist
	}
	return result, nil
}

func sameAuthoredSuiteSemantics(left, right domain.Suite) bool {
	leftDocument := struct {
		Key, Name, ModelTarget string
		Protocol               domain.Protocol
		Cases                  []domain.CaseRevisionRef
	}{left.Key, left.Name, left.ModelTarget, left.Protocol, left.Cases}
	rightDocument := struct {
		Key, Name, ModelTarget string
		Protocol               domain.Protocol
		Cases                  []domain.CaseRevisionRef
	}{right.Key, right.Name, right.ModelTarget, right.Protocol, right.Cases}
	return reflect.DeepEqual(leftDocument, rightDocument)
}

func remapLegacyPlanReferences(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	snapshot legacyAuthoredCatalogSnapshot,
) (legacyAuthoredCatalogSnapshot, error) {
	fileCases, err := target.ListTestCases(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list file Cases for Plan migration: %w", err)
	}
	fileCasesByIdentity := make(map[string]domain.TestCase, len(fileCases))
	for _, testCase := range fileCases {
		identity := authoredCaseIdentity(testCase)
		if _, duplicate := fileCasesByIdentity[identity]; duplicate {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: duplicate file Case %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		fileCasesByIdentity[identity] = testCase
	}
	fileSuites, err := target.ListSuites(ctx)
	if err != nil {
		return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("list file Suites for Plan migration: %w", err)
	}
	fileSuitesByIdentity := make(map[string]domain.Suite, len(fileSuites))
	for _, suite := range fileSuites {
		identity := authoredSuiteIdentity(suite)
		if _, duplicate := fileSuitesByIdentity[identity]; duplicate {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: duplicate file Suite %q", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		fileSuitesByIdentity[identity] = suite
	}
	legacyPinned := make(map[legacyPinnedCaseRevisionKey]domain.TestCase, len(snapshot.PinnedCases))
	for _, testCase := range snapshot.PinnedCases {
		key := legacyPinnedCaseRevisionKey{caseID: testCase.ID, revision: testCase.Revision}
		if _, duplicate := legacyPinned[key]; duplicate {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: duplicate legacy pinned Case %q revision %d", errLegacyAuthoredCatalogMigrationConflict, testCase.ID, testCase.Revision)
		}
		legacyPinned[key] = testCase
	}
	legacyPinnedSuites := make(map[legacyPinnedSuiteRevisionKey]domain.Suite, len(snapshot.PinnedSuites))
	for _, suite := range snapshot.PinnedSuites {
		key := legacyPinnedSuiteRevisionKey{suiteID: suite.ID, revision: suite.Revision}
		if _, duplicate := legacyPinnedSuites[key]; duplicate {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: duplicate legacy pinned Suite %q revision %d", errLegacyAuthoredCatalogMigrationConflict, suite.ID, suite.Revision)
		}
		legacyPinnedSuites[key] = suite
	}

	result := snapshot
	result.Plans = append([]plancatalog.Document(nil), snapshot.Plans...)
	remappedPinned := make(map[legacyPinnedCaseRevisionKey]domain.TestCase, len(snapshot.PinnedCases))
	remappedPinnedByLegacy := make(map[legacyPinnedCaseRevisionKey]domain.TestCase, len(snapshot.PinnedCases))
	for legacyKey, legacy := range legacyPinned {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		identity := authoredCaseIdentity(legacy)
		fileCase, exists := fileCasesByIdentity[identity]
		if !exists {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: pinned Case %q has no file identity", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		remapped := legacy
		remapped.ID = fileCase.ID
		remappedKey := legacyPinnedCaseRevisionKey{caseID: remapped.ID, revision: remapped.Revision}
		if previous, duplicate := remappedPinned[remappedKey]; duplicate {
			same, err := sameAuthoredCaseSemantics(previous, remapped)
			if err != nil || !same {
				return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: remapped pinned Case %q revision %d is ambiguous", errLegacyAuthoredCatalogMigrationConflict, remapped.ID, remapped.Revision)
			}
		} else {
			remappedPinned[remappedKey] = remapped
		}
		remappedPinnedByLegacy[legacyKey] = remapped
	}

	remappedPinnedSuites := make(map[legacyPinnedSuiteRevisionKey]domain.Suite, len(snapshot.PinnedSuites))
	remappedPinnedSuitesByLegacy := make(map[legacyPinnedSuiteRevisionKey]domain.Suite, len(snapshot.PinnedSuites))
	for legacyKey, legacy := range legacyPinnedSuites {
		if err := ctx.Err(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, err
		}
		identity := authoredSuiteIdentity(legacy)
		fileSuite, exists := fileSuitesByIdentity[identity]
		if !exists {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: pinned Suite %q has no current file identity", errLegacyAuthoredCatalogMigrationConflict, identity)
		}
		remapped := legacy
		remapped.ID = fileSuite.ID
		remapped.Cases = make([]domain.CaseRevisionRef, len(legacy.Cases))
		for index, legacyRef := range legacy.Cases {
			remappedCase, exists := remappedPinnedByLegacy[legacyPinnedCaseRevisionKey{caseID: legacyRef.CaseID, revision: legacyRef.Revision}]
			if !exists || remappedCase.Protocol != remapped.Protocol || !remappedCase.AppliesToModel(remapped.ModelTarget) {
				return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: pinned Suite %q Case %q revision %d is unavailable", errLegacyAuthoredCatalogMigrationInvalid, identity, legacyRef.CaseID, legacyRef.Revision)
			}
			remapped.Cases[index] = domain.CaseRevisionRef{CaseID: remappedCase.ID, Revision: remappedCase.Revision}
		}
		if err := remapped.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: remapped pinned Suite %q revision %d is invalid", errLegacyAuthoredCatalogMigrationInvalid, remapped.ID, remapped.Revision)
		}
		remappedKey := legacyPinnedSuiteRevisionKey{suiteID: remapped.ID, revision: remapped.Revision}
		if previous, duplicate := remappedPinnedSuites[remappedKey]; duplicate && !samePinnedSuiteDocument(previous, remapped) {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: remapped pinned Suite %q revision %d is ambiguous", errLegacyAuthoredCatalogMigrationConflict, remapped.ID, remapped.Revision)
		}
		remappedPinnedSuites[remappedKey] = remapped
		remappedPinnedSuitesByLegacy[legacyKey] = remapped
	}

	for documentIndex := range result.Plans {
		document := result.Plans[documentIndex]
		document.Plan.Cases = append([]domain.CaseRevisionRef(nil), document.Plan.Cases...)
		if document.Plan.SuiteID != "" {
			legacyKey := legacyPinnedSuiteRevisionKey{suiteID: document.Plan.SuiteID, revision: document.Plan.SuiteRevision}
			remappedSuite, exists := remappedPinnedSuitesByLegacy[legacyKey]
			if !exists {
				return legacyAuthoredCatalogSnapshot{}, fmt.Errorf(
					"%w: Plan %q legacy Suite %q revision %d is unavailable",
					errLegacyAuthoredCatalogMigrationInvalid,
					document.ID,
					document.Plan.SuiteID,
					document.Plan.SuiteRevision,
				)
			}
			document.Plan.SuiteID = remappedSuite.ID
			document.Plan.SuiteRevision = remappedSuite.Revision
		}
		for caseIndex, legacyRef := range document.Plan.Cases {
			if err := ctx.Err(); err != nil {
				return legacyAuthoredCatalogSnapshot{}, err
			}
			legacyKey := legacyPinnedCaseRevisionKey{caseID: legacyRef.CaseID, revision: legacyRef.Revision}
			remapped, exists := remappedPinnedByLegacy[legacyKey]
			if !exists {
				return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: Plan %q legacy pinned Case %q revision %d is unavailable", errLegacyAuthoredCatalogMigrationInvalid, document.ID, legacyRef.CaseID, legacyRef.Revision)
			}
			document.Plan.Cases[caseIndex] = domain.CaseRevisionRef{CaseID: remapped.ID, Revision: remapped.Revision}
		}
		if err := document.Validate(); err != nil {
			return legacyAuthoredCatalogSnapshot{}, fmt.Errorf("%w: remapped Plan %q is invalid", errLegacyAuthoredCatalogMigrationInvalid, document.ID)
		}
		result.Plans[documentIndex] = document
	}
	result.PinnedCases = make([]domain.TestCase, 0, len(remappedPinned))
	for _, testCase := range remappedPinned {
		result.PinnedCases = append(result.PinnedCases, testCase)
	}
	result.PinnedSuites = make([]domain.Suite, 0, len(remappedPinnedSuites))
	for _, suite := range remappedPinnedSuites {
		result.PinnedSuites = append(result.PinnedSuites, suite)
	}
	sortLegacyAuthoredCatalogSnapshot(&result)
	return result, nil
}

func validateMigratedFileCatalog(ctx context.Context, target legacyAuthoredCatalogMigrationTarget) error {
	_, err := loadMigratedFileCatalogSnapshot(ctx, target)
	return err
}

func migrateLegacyPinnedSuiteRevisions(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	snapshot legacyAuthoredCatalogSnapshot,
	filePlans []domain.Plan,
) error {
	legacySuites := make(map[legacyPinnedSuiteRevisionKey]domain.Suite, len(snapshot.PinnedSuites))
	for _, suite := range snapshot.PinnedSuites {
		legacySuites[legacyPinnedSuiteRevisionKey{suiteID: suite.ID, revision: suite.Revision}] = suite
	}
	seen := make(map[legacyPinnedSuiteRevisionKey]struct{})
	for _, plan := range filePlans {
		if plan.SuiteID == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		key := legacyPinnedSuiteRevisionKey{suiteID: plan.SuiteID, revision: plan.SuiteRevision}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		ref := domain.EntityRevisionRef{ID: key.suiteID, Revision: key.revision}

		legacy, legacyRef := legacySuites[key]
		if !legacyRef {
			stored, err := target.GetSuiteRevision(ctx, key.suiteID, key.revision)
			if err != nil {
				return fmt.Errorf("resolve file-only pinned Suite %q revision %d: %w", key.suiteID, key.revision, err)
			}
			if !validPinnedSuiteRevision(stored, ref) {
				return fmt.Errorf("%w: file-only pinned Suite %q revision %d is not exact", errLegacyAuthoredCatalogMigrationConflict, key.suiteID, key.revision)
			}
			continue
		}
		if !validPinnedSuiteRevision(legacy, ref) {
			return fmt.Errorf("%w: legacy pinned Suite %q revision %d", errLegacyAuthoredCatalogMigrationInvalid, key.suiteID, key.revision)
		}

		stored, resolveErr := target.GetSuiteRevision(ctx, key.suiteID, key.revision)
		if resolveErr == nil && validPinnedSuiteRevision(stored, ref) {
			if !samePinnedSuiteDocument(stored, legacy) {
				return fmt.Errorf("%w: file pinned Suite %q revision %d differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, key.suiteID, key.revision)
			}
			continue
		}
		if resolveErr == nil {
			return fmt.Errorf("%w: file pinned Suite %q revision %d is not exact", errLegacyAuthoredCatalogMigrationConflict, key.suiteID, key.revision)
		}
		if !errors.Is(resolveErr, fs.ErrNotExist) && !errors.Is(resolveErr, catalog.ErrNotFound) {
			return fmt.Errorf("resolve file pinned Suite %q revision %d: %w", key.suiteID, key.revision, resolveErr)
		}
		if err := target.StoreSuiteRevision(ctx, legacy); err != nil {
			return fmt.Errorf("store file pinned Suite %q revision %d: %w", key.suiteID, key.revision, err)
		}
		persisted, err := target.GetSuiteRevision(ctx, key.suiteID, key.revision)
		if err != nil {
			return fmt.Errorf("verify file pinned Suite %q revision %d: %w", key.suiteID, key.revision, err)
		}
		if !validPinnedSuiteRevision(persisted, ref) || !samePinnedSuiteDocument(persisted, legacy) {
			return fmt.Errorf("%w: file pinned Suite %q revision %d differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, key.suiteID, key.revision)
		}
	}
	return nil
}

type legacyPinnedCaseRevisionKey struct {
	caseID   string
	revision uint64
}

type legacyPinnedSuiteRevisionKey struct {
	suiteID  string
	revision uint64
}

func migrateLegacyPinnedCaseRevisions(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	snapshot legacyAuthoredCatalogSnapshot,
	filePlans []domain.Plan,
) error {
	legacyCases := make(map[legacyPinnedCaseRevisionKey]domain.TestCase, len(snapshot.PinnedCases))
	for _, testCase := range snapshot.PinnedCases {
		legacyCases[legacyPinnedCaseRevisionKey{caseID: testCase.ID, revision: testCase.Revision}] = testCase
	}
	references := make([]domain.CaseRevisionRef, 0)
	for _, plan := range filePlans {
		references = append(references, plan.Cases...)
	}
	// A pinned Suite is itself an exact authored revision. Materialize every
	// Case it references before storing that Suite sidecar, including Cases
	// that are not repeated directly on a Plan.
	for _, suite := range snapshot.PinnedSuites {
		references = append(references, suite.Cases...)
	}
	seen := make(map[legacyPinnedCaseRevisionKey]struct{})
	for _, ref := range references {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := legacyPinnedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}

		legacy, legacyRef := legacyCases[key]
		if !legacyRef {
			stored, err := target.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
			if err != nil {
				return fmt.Errorf("resolve file-only pinned Case %q revision %d: %w", ref.CaseID, ref.Revision, err)
			}
			if !validPinnedCaseRevision(stored, ref) {
				return fmt.Errorf("%w: file-only pinned Case %q revision %d is not exact", errLegacyAuthoredCatalogMigrationConflict, ref.CaseID, ref.Revision)
			}
			continue
		}

		if !validPinnedCaseRevision(legacy, ref) {
			return fmt.Errorf("%w: legacy pinned Case %q revision %d", errLegacyAuthoredCatalogMigrationInvalid, ref.CaseID, ref.Revision)
		}

		stored, resolveErr := target.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
		if resolveErr == nil && validPinnedCaseRevision(stored, ref) {
			if !samePinnedCaseDocument(stored, legacy) {
				return fmt.Errorf("%w: file pinned Case %q revision %d differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, ref.CaseID, ref.Revision)
			}
			continue
		}
		if resolveErr == nil {
			return fmt.Errorf("%w: file pinned Case %q revision %d is not exact", errLegacyAuthoredCatalogMigrationConflict, ref.CaseID, ref.Revision)
		}
		if resolveErr != nil && !errors.Is(resolveErr, fs.ErrNotExist) && !errors.Is(resolveErr, catalog.ErrNotFound) {
			return fmt.Errorf("resolve file pinned Case %q revision %d: %w", ref.CaseID, ref.Revision, resolveErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := target.StoreTestCaseRevision(ctx, legacy); err != nil {
			return fmt.Errorf("store file pinned Case %q revision %d: %w", ref.CaseID, ref.Revision, err)
		}
		persisted, err := target.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
		if err != nil {
			return fmt.Errorf("verify file pinned Case %q revision %d: %w", ref.CaseID, ref.Revision, err)
		}
		if !validPinnedCaseRevision(persisted, ref) || !samePinnedCaseDocument(persisted, legacy) {
			return fmt.Errorf("%w: file pinned Case %q revision %d differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, ref.CaseID, ref.Revision)
		}
	}
	return nil
}

func validPinnedCaseRevision(testCase domain.TestCase, ref domain.CaseRevisionRef) bool {
	return testCase.ID == ref.CaseID && testCase.Revision == ref.Revision && testCase.Validate() == nil
}

func validPinnedSuiteRevision(suite domain.Suite, ref domain.EntityRevisionRef) bool {
	return suite.ID == ref.ID && suite.Revision == ref.Revision && suite.Validate() == nil
}

func samePinnedCaseDocument(left, right domain.TestCase) bool {
	same, err := sameAuthoredCaseSemantics(left, right)
	return err == nil && same
}

func samePinnedSuiteDocument(left, right domain.Suite) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func legacyAuthoredCatalogMigrationComplete(ctx context.Context, markerPath string) (legacyAuthoredCatalogMigrationMarker, bool, error) {
	if err := ctx.Err(); err != nil {
		return legacyAuthoredCatalogMigrationMarker{}, false, err
	}
	payload, err := os.ReadFile(filepath.Clean(markerPath))
	if errors.Is(err, os.ErrNotExist) {
		return legacyAuthoredCatalogMigrationMarker{}, false, nil
	}
	if err != nil {
		return legacyAuthoredCatalogMigrationMarker{}, false, fmt.Errorf("read legacy authored catalog migration marker: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var marker legacyAuthoredCatalogMigrationMarker
	if err := decoder.Decode(&marker); err != nil {
		return legacyAuthoredCatalogMigrationMarker{}, false, fmt.Errorf("%w: %v", errLegacyAuthoredCatalogMigrationMarker, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return legacyAuthoredCatalogMigrationMarker{}, false, fmt.Errorf("%w: marker must contain one object", errLegacyAuthoredCatalogMigrationMarker)
	}
	if marker.SchemaVersion != legacyAuthoredCatalogMigrationSchemaVersion &&
		marker.SchemaVersion != legacyAuthoredCatalogMigrationPersistentSchemaVersion &&
		marker.SchemaVersion != legacyAuthoredCatalogMigrationPreviousSchemaVersion {
		return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
	}
	if !marker.Completed || len(marker.ProcessedSourceDigests) == 0 {
		return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
	}
	for index, sourceDigest := range marker.ProcessedSourceDigests {
		digest, err := hex.DecodeString(sourceDigest)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != sourceDigest ||
			index > 0 && marker.ProcessedSourceDigests[index-1] >= sourceDigest {
			return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
		}
	}
	if marker.SchemaVersion == legacyAuthoredCatalogMigrationPreviousSchemaVersion {
		if len(marker.TargetDigestsBySource) != 0 {
			return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
		}
		// Schema v1 predated bulk Case and Suite export, and its source digest
		// therefore cannot prove that those authored entities reached files.
		// Preserve the valid checkpoint for the caller's selective upgrade. Its
		// source digest used the older snapshot shape and therefore cannot serve
		// as a current completion receipt.
		return marker, false, nil
	}
	if len(marker.TargetDigestsBySource) != len(marker.ProcessedSourceDigests) {
		return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
	}
	for _, sourceDigest := range marker.ProcessedSourceDigests {
		targetDigest, exists := marker.TargetDigestsBySource[sourceDigest]
		if !exists {
			return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
		}
		digest, err := hex.DecodeString(targetDigest)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != targetDigest {
			return legacyAuthoredCatalogMigrationMarker{}, false, errLegacyAuthoredCatalogMigrationMarker
		}
	}
	// Schema v2 is a valid receipt from the previous implementation. Keep its
	// target digest so a matching legacy source can use the same read-only
	// footprint verification as v3: unrelated additions may refresh the
	// receipt, but deleted or edited exported entities are never restored.
	return marker, true, nil
}

type legacyEntityMigrationAdapter[T any] struct {
	id           func(T) string
	revision     func(T) uint64
	withRevision func(T, uint64) T
	create       func(context.Context, T) error
	update       func(context.Context, uint64, T) error
}

func migrateLegacyEntities[T any](
	ctx context.Context,
	kind string,
	legacy []T,
	current []T,
	adapter legacyEntityMigrationAdapter[T],
) error {
	currentByID := make(map[string]T, len(current))
	for _, entity := range current {
		id := adapter.id(entity)
		if _, duplicate := currentByID[id]; duplicate {
			return fmt.Errorf("%w: duplicate file %s %q", errLegacyAuthoredCatalogMigrationConflict, kind, id)
		}
		currentByID[id] = entity
	}
	seenLegacy := make(map[string]struct{}, len(legacy))
	for _, desired := range legacy {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := adapter.id(desired)
		if _, duplicate := seenLegacy[id]; duplicate {
			return fmt.Errorf("%w: duplicate legacy %s %q", errLegacyAuthoredCatalogMigrationConflict, kind, id)
		}
		seenLegacy[id] = struct{}{}
		desiredRevision := adapter.revision(desired)
		if desiredRevision == 0 {
			return fmt.Errorf("%w: legacy %s %q has zero revision", errLegacyAuthoredCatalogMigrationInvalid, kind, id)
		}

		persisted, exists := currentByID[id]
		currentRevision := uint64(0)
		if exists {
			if reflect.DeepEqual(persisted, desired) {
				continue
			}
			currentRevision = adapter.revision(persisted)
			// A failed revision replay leaves the final payload at an earlier
			// revision. That exact state is the only non-identical state a retry
			// may advance; any content difference is a user-file conflict.
			if currentRevision >= desiredRevision ||
				!reflect.DeepEqual(adapter.withRevision(persisted, desiredRevision), desired) {
				return fmt.Errorf("%w: %s %q differs from legacy SQLite", errLegacyAuthoredCatalogMigrationConflict, kind, id)
			}
		} else {
			first := adapter.withRevision(desired, 1)
			if err := adapter.create(ctx, first); err != nil {
				return fmt.Errorf("create file %s %q: %w", kind, id, err)
			}
			currentRevision = 1
		}
		for currentRevision < desiredRevision {
			next := currentRevision + 1
			if err := adapter.update(ctx, currentRevision, adapter.withRevision(desired, next)); err != nil {
				return fmt.Errorf("advance file %s %q to revision %d: %w", kind, id, next, err)
			}
			currentRevision = next
		}
	}
	return nil
}

func modelMigrationAdapter(target legacyAuthoredCatalogMigrationTarget) legacyEntityMigrationAdapter[domain.Model] {
	return legacyEntityMigrationAdapter[domain.Model]{
		id:       func(value domain.Model) string { return value.ID },
		revision: func(value domain.Model) uint64 { return value.Revision },
		withRevision: func(value domain.Model, revision uint64) domain.Model {
			value.Revision = revision
			return value
		},
		create: target.CreateModel,
		update: target.UpdateModel,
	}
}

func channelMigrationAdapter(target legacyAuthoredCatalogMigrationTarget) legacyEntityMigrationAdapter[domain.Channel] {
	return legacyEntityMigrationAdapter[domain.Channel]{
		id:       func(value domain.Channel) string { return value.ID },
		revision: func(value domain.Channel) uint64 { return value.Revision },
		withRevision: func(value domain.Channel, revision uint64) domain.Channel {
			value.Revision = revision
			return value
		},
		create: target.CreateChannel,
		update: target.UpdateChannel,
	}
}

func mappingMigrationAdapter(target legacyAuthoredCatalogMigrationTarget) legacyEntityMigrationAdapter[domain.ChannelModel] {
	return legacyEntityMigrationAdapter[domain.ChannelModel]{
		id:       func(value domain.ChannelModel) string { return value.ID },
		revision: func(value domain.ChannelModel) uint64 { return value.Revision },
		withRevision: func(value domain.ChannelModel, revision uint64) domain.ChannelModel {
			value.Revision = revision
			return value
		},
		create: target.CreateChannelModel,
		update: target.UpdateChannelModel,
	}
}

func planMigrationAdapter(target legacyAuthoredCatalogMigrationTarget) legacyEntityMigrationAdapter[plancatalog.Document] {
	return legacyEntityMigrationAdapter[plancatalog.Document]{
		id:       func(value plancatalog.Document) string { return value.ID },
		revision: func(value plancatalog.Document) uint64 { return value.Revision },
		withRevision: func(value plancatalog.Document, revision uint64) plancatalog.Document {
			value.Plan.Revision = revision
			return value
		},
		create: target.CreatePlanDocument,
		update: target.UpdatePlanDocument,
	}
}
