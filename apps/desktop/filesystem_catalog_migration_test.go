package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestMigrateLegacyAuthoredCatalogExportsSafeEntitiesInDependencyOrder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models:   []domain.Model{model},
		channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping},
		plans:    []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrateLegacyAuthoredCatalog() error = %v", err)
	}

	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy source call order = %v, want %v", source.calls, want)
	}
	// The exact source call order is a tripwire that every authored collection
	// is read before the v10 source tables are retired.
	assertLegacyCatalogMigrationContents(t, ctx, target, model, channel, mapping, plan)
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read migration marker: %v", err)
	}
	for _, forbidden := range []string{"credential_store", "store_ref", "fingerprint", "api_key", "secret"} {
		if strings.Contains(strings.ToLower(string(marker)), forbidden) {
			t.Fatalf("migration marker contains sensitive field %q: %s", forbidden, marker)
		}
	}
	if info, err := os.Stat(markerPath); err != nil || info.Size() == 0 {
		t.Fatalf("migration marker stat = %#v, %v", info, err)
	}
}

func TestLoadLegacyAuthoredCatalogSnapshotResolvesPlanTargetsChannelThenModel(t *testing.T) {
	ctx := context.Background()
	modelA, channelA, mappingAA, plan := legacyCatalogMigrationFixtures()
	modelB := modelA
	modelB.ID = "51000000-0000-4000-8000-000000000011"
	modelB.Name = "second model"
	channelB := channelA
	channelB.ID = "51000000-0000-4000-8000-000000000012"
	channelB.Name = "second channel"
	channelB.CredentialID = "51000000-0000-4000-8000-000000000013"
	mappingAB := mappingAA
	mappingAB.ID = "51000000-0000-4000-8000-000000000014"
	mappingAB.ModelID = modelB.ID
	mappingAB.UpstreamModelName = "model-b-on-channel-a"
	mappingBA := mappingAA
	mappingBA.ID = "51000000-0000-4000-8000-000000000015"
	mappingBA.ChannelID = channelB.ID
	mappingBA.UpstreamModelName = "model-a-on-channel-b"
	mappingBB := mappingAA
	mappingBB.ID = "51000000-0000-4000-8000-000000000016"
	mappingBB.ModelID = modelB.ID
	mappingBB.ChannelID = channelB.ID
	mappingBB.UpstreamModelName = "model-b-on-channel-b"
	plan.ModelIDs = []string{modelA.ID, modelB.ID}
	plan.ChannelIDs = []string{channelA.ID, channelB.ID}
	_, pinned := legacyCatalogMigrationCaseFixtures(plan.Cases[0])
	source := &recordingLegacyCatalogSource{
		models:   []domain.Model{modelA, modelB},
		channels: []domain.Channel{channelA, channelB},
		mappings: []domain.ChannelModel{mappingAA, mappingAB, mappingBA, mappingBB},
		plans:    []domain.Plan{plan},
		caseRevisions: map[recordedCaseRevisionKey]domain.TestCase{
			{caseID: pinned.ID, revision: pinned.Revision}: pinned,
		},
	}

	snapshot, err := loadLegacyAuthoredCatalogSnapshot(ctx, source)
	if err != nil {
		t.Fatalf("load legacy snapshot: %v", err)
	}
	wantOrder := []recordedPlanTargetKey{
		{planID: plan.ID, modelID: modelA.ID, channelID: channelA.ID},
		{planID: plan.ID, modelID: modelB.ID, channelID: channelA.ID},
		{planID: plan.ID, modelID: modelA.ID, channelID: channelB.ID},
		{planID: plan.ID, modelID: modelB.ID, channelID: channelB.ID},
	}
	if !reflect.DeepEqual(source.planTargetResolutions, wantOrder) {
		t.Fatalf("Plan target resolution order = %#v, want channel-then-model %#v", source.planTargetResolutions, wantOrder)
	}
	if len(snapshot.Plans) != 1 || snapshot.Plans[0].Validate() != nil {
		t.Fatalf("snapshot Plan documents = %#v, want one valid document", snapshot.Plans)
	}
	wantMappings := []domain.ChannelModel{mappingAA, mappingAB, mappingBA, mappingBB}
	for index, binding := range snapshot.Plans[0].TargetBindings {
		if !reflect.DeepEqual(binding.Mapping, wantMappings[index]) {
			t.Fatalf("Plan binding %d mapping = %#v, want %#v", index, binding.Mapping, wantMappings[index])
		}
	}
}

func TestMigrateLegacyAuthoredCatalogPreservesExactPlanBindingsAfterCatalogEvolution(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	currentModel, currentChannel, currentMapping, plan := legacyCatalogMigrationFixtures()
	plan.Revision = 1
	plan.UpdatedAt = plan.CreatedAt
	exactModel := currentModel
	exactModel.Revision = 1
	exactModel.UpdatedAt = exactModel.CreatedAt
	exactModel.Name = "model at Plan revision one"
	exactChannel := currentChannel
	exactChannel.Revision = 1
	exactChannel.UpdatedAt = exactChannel.CreatedAt
	exactChannel.Name = "channel at Plan revision one"
	exactMapping := currentMapping
	exactMapping.Revision = 1
	exactMapping.UpdatedAt = exactMapping.CreatedAt
	exactMapping.UpstreamModelName = "upstream-at-plan-revision-one"
	key := recordedPlanTargetKey{planID: plan.ID, modelID: plan.ModelIDs[0], channelID: plan.ChannelIDs[0]}
	source := &recordingLegacyCatalogSource{
		models:   []domain.Model{currentModel},
		channels: []domain.Channel{currentChannel},
		mappings: []domain.ChannelModel{currentMapping},
		plans:    []domain.Plan{plan},
		planTargets: map[recordedPlanTargetKey]plancatalog.TargetBinding{
			key: {Model: exactModel, Channel: exactChannel, Mapping: exactMapping},
		},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate exact Plan target: %v", err)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil || len(documents) != 1 {
		t.Fatalf("migrated Plan documents = %#v, %v", documents, err)
	}
	wantDocument := plancatalog.Document{
		FileSchemaVersion: plancatalog.CurrentFileSchemaVersion,
		Plan:              plan,
		TargetBindings: []plancatalog.TargetBinding{{
			Model: exactModel, Channel: exactChannel, Mapping: exactMapping,
		}},
	}
	if !reflect.DeepEqual(documents[0], wantDocument) {
		t.Fatalf("migrated exact Plan document = %#v, want %#v", documents[0], wantDocument)
	}
	models, _ := target.ListModels(ctx)
	channels, _ := target.ListChannels(ctx)
	mappings, _ := target.ListChannelModels(ctx)
	if !reflect.DeepEqual(models, []domain.Model{currentModel}) ||
		!reflect.DeepEqual(channels, []domain.Channel{currentChannel}) ||
		!reflect.DeepEqual(mappings, []domain.ChannelModel{currentMapping}) {
		t.Fatalf("current catalogs were not migrated independently: models=%#v channels=%#v mappings=%#v", models, channels, mappings)
	}
}

func TestDigestLegacyAuthoredCatalogSnapshotIncludesExactPlanBindings(t *testing.T) {
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	document := plancatalog.Document{
		FileSchemaVersion: plancatalog.CurrentFileSchemaVersion,
		Plan:              plan,
		TargetBindings: []plancatalog.TargetBinding{{
			Model: model, Channel: channel, Mapping: mapping,
		}},
	}
	first := legacyAuthoredCatalogSnapshot{Plans: []plancatalog.Document{document}}
	second := first
	second.Plans = append([]plancatalog.Document(nil), first.Plans...)
	second.Plans[0].TargetBindings = append([]plancatalog.TargetBinding(nil), first.Plans[0].TargetBindings...)
	second.Plans[0].TargetBindings[0].Mapping.UpstreamModelName = "different-exact-binding"

	firstDigest, firstErr := digestLegacyAuthoredCatalogSnapshot(first)
	secondDigest, secondErr := digestLegacyAuthoredCatalogSnapshot(second)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("digest exact Plan bindings: first=%v second=%v", firstErr, secondErr)
	}
	if firstDigest == secondDigest {
		t.Fatalf("different exact Plan bindings produced the same source digest %q", firstDigest)
	}
}

func TestMigrateLegacyAuthoredCatalogTargetlessPlanDoesNotResolveTargets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	_, _, _, plan := legacyCatalogMigrationFixtures()
	plan.ModelIDs = []string{}
	plan.ChannelIDs = []string{}
	source := &recordingLegacyCatalogSource{plans: []domain.Plan{plan}}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate targetless Plan: %v", err)
	}
	if len(source.planTargetResolutions) != 0 {
		t.Fatalf("targetless Plan target resolutions = %#v, want none", source.planTargetResolutions)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil || len(documents) != 1 || documents[0].TargetBindings == nil || len(documents[0].TargetBindings) != 0 {
		t.Fatalf("targetless Plan document = %#v, %v; want explicit empty bindings", documents, err)
	}
}

func TestMigrateLegacyAuthoredCatalogRejectsMissingOrInconsistentExactPlanTargetsWithoutMarker(t *testing.T) {
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	key := recordedPlanTargetKey{planID: plan.ID, modelID: model.ID, channelID: channel.ID}
	inconsistent := mapping
	inconsistent.ModelID = "51000000-0000-4000-8000-000000000017"
	tests := []struct {
		name    string
		targets map[recordedPlanTargetKey]plancatalog.TargetBinding
	}{
		{name: "missing", targets: map[recordedPlanTargetKey]plancatalog.TargetBinding{}},
		{name: "inconsistent", targets: map[recordedPlanTargetKey]plancatalog.TargetBinding{
			key: {Model: model, Channel: channel, Mapping: inconsistent},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			target := newLegacyCatalogMigrationTarget(t, root)
			source := &recordingLegacyCatalogSource{
				models: []domain.Model{model}, channels: []domain.Channel{channel},
				mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
				planTargets: test.targets,
			}
			markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

			if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err == nil {
				t.Fatal("migration error = nil, want exact Plan target failure")
			}
			if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("marker after exact Plan target failure stat error = %v, want not exist", err)
			}
			documents, err := target.ListPlanDocuments(ctx)
			if err != nil || len(documents) != 0 {
				t.Fatalf("Plan documents after exact target failure = %#v, %v; want none", documents, err)
			}
		})
	}
}

func TestMigrateLegacyAuthoredCatalogValidMarkerRevalidatesIdenticalSourceIdempotently(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	source.calls = nil
	source.caseRevisionReads = nil

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("idempotent migration with valid marker error = %v", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy source calls with valid marker = %v, want %v", source.calls, want)
	}
	ref := plan.Cases[0]
	key := recordedCaseRevisionKey{caseID: ref.CaseID, revision: ref.Revision}
	if source.caseRevisionReads[key] != 1 {
		t.Fatalf("legacy pinned Case reads with valid marker = %d, want 1", source.caseRevisionReads[key])
	}
	assertLegacyCatalogMigrationContents(t, ctx, target, model, channel, mapping, plan)
}

func TestMigrateLegacyAuthoredCatalogValidMarkerFailsClosedWhenTargetChangesBeforeRetirement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}

	fileModel := model
	fileModel.Revision++
	fileModel.Name = "file catalog edit after cutover"
	fileModel.UpdatedAt = fileModel.UpdatedAt.Add(time.Minute)
	if err := target.UpdateModel(ctx, model.Revision, fileModel); err != nil {
		t.Fatalf("update authoritative file model: %v", err)
	}
	source.calls = nil

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("restart migration after target edit error = %v, want migration conflict", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy fingerprint source calls = %v, want %v", source.calls, want)
	}
	models, err := target.ListModels(ctx)
	if err != nil || !reflect.DeepEqual(models, []domain.Model{fileModel}) {
		t.Fatalf("target edit after failed-closed restart = %#v, %v; want preserved %#v", models, err, fileModel)
	}
}

func TestMigrateLegacyAuthoredCatalogValidMarkerRefreshesReceiptForUnrelatedTargetAddition(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}

	independent := model
	independent.ID = "51000000-0000-4000-8000-000000000009"
	independent.Revision = 1
	independent.UpdatedAt = independent.CreatedAt
	independent.Name = "independent file model"
	if err := target.CreateModel(ctx, independent); err != nil {
		t.Fatalf("create unrelated file model: %v", err)
	}

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("refresh crash-window receipt after unrelated target addition: %v", err)
	}
	snapshot, err := loadLegacyAuthoredCatalogSnapshot(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest, err := digestLegacyAuthoredCatalogSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCompletedLegacyAuthoredCatalogMigrationForSource(ctx, target, markerPath, sourceDigest); err != nil {
		t.Fatalf("refreshed receipt validation: %v", err)
	}
	models, err := target.ListModels(ctx)
	if err != nil || len(models) != 2 || models[0].ID != model.ID || models[1].ID != independent.ID {
		t.Fatalf("models after receipt refresh = %#v, %v; want legacy and unrelated models", models, err)
	}
}

func TestMigrateLegacyAuthoredCatalogValidMarkerDetectsDeletedOrphanEntity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, _, _, _ := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{models: []domain.Model{model}}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	markerBefore, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.models.Delete(ctx, model.ID, model.Revision); err != nil {
		t.Fatalf("delete orphan file model: %v", err)
	}
	source.calls = nil

	err = migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("restart migration after orphan deletion error = %v, want migration conflict", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy fingerprint source calls = %v, want %v", source.calls, want)
	}
	models, listErr := target.ListModels(ctx)
	if listErr != nil || len(models) != 0 {
		t.Fatalf("deleted target model was replayed: models=%#v error=%v", models, listErr)
	}
	markerAfter, readErr := os.ReadFile(markerPath)
	if readErr != nil || !reflect.DeepEqual(markerAfter, markerBefore) {
		t.Fatalf("failed-closed retry changed marker: before=%s after=%s error=%v", markerBefore, markerAfter, readErr)
	}
}

func TestMigrateLegacyAuthoredCatalogValidMarkerExportsDifferentLegacyDatabaseEntity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	different := model
	different.ID = "51000000-0000-4000-8000-000000000009"
	different.Name = "model from another legacy database"
	differentSource := &recordingLegacyCatalogSource{models: []domain.Model{different}}

	if err := migrateLegacyAuthoredCatalog(ctx, differentSource, target, markerPath); err != nil {
		t.Fatalf("migrate different legacy database with valid marker: %v", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(differentSource.calls, want) {
		t.Fatalf("different legacy source calls = %v, want %v", differentSource.calls, want)
	}
	models, err := target.ListModels(ctx)
	if err != nil || len(models) != 2 || models[0].ID != model.ID || models[1].ID != different.ID {
		t.Fatalf("models after different legacy database = %#v, %v", models, err)
	}

	fileModel := model
	fileModel.Revision++
	fileModel.Name = "file edit after switching legacy databases"
	fileModel.UpdatedAt = fileModel.UpdatedAt.Add(time.Minute)
	if err := target.UpdateModel(ctx, model.Revision, fileModel); err != nil {
		t.Fatalf("update file model after switching source: %v", err)
	}
	err = migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("return to an older target receipt error = %v, want migration conflict", err)
	}
	models, err = target.ListModels(ctx)
	if err != nil || len(models) != 2 || !reflect.DeepEqual(models[0], fileModel) {
		t.Fatalf("models after rejecting older target receipt = %#v, %v; want preserved file edit", models, err)
	}
}

func TestMigrateLegacyAuthoredCatalogValidMarkerDetectsChangedLegacyEntityContent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	source.calls = nil
	source.models[0].Name = "same identity with changed legacy content"

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("changed legacy entity error = %v, want migration conflict", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy source calls after changed entity = %v, want %v", source.calls, want)
	}
	models, listErr := target.ListModels(ctx)
	if listErr != nil || !reflect.DeepEqual(models, []domain.Model{model}) {
		t.Fatalf("file model changed after legacy conflict: %#v, %v", models, listErr)
	}
}

func TestMigrateLegacyAuthoredCatalogValidMarkerPropagatesSourceReadError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	source.calls = nil
	source.failAt = "models"

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if err == nil || !strings.Contains(err.Error(), "injected legacy model read") {
		t.Fatalf("source read error with valid marker = %v", err)
	}
	if !reflect.DeepEqual(source.calls, []string{"models"}) {
		t.Fatalf("legacy source calls after read error = %v, want models only", source.calls)
	}
}

func TestMigrateLegacyAuthoredCatalogDifferentExistingEntityFailsWithoutMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	conflicting := model
	conflicting.Revision = 1
	conflicting.Name = "user-owned file model"
	if err := target.CreateModel(ctx, conflicting); err != nil {
		t.Fatal(err)
	}
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("migration conflict error = %v, want errLegacyAuthoredCatalogMigrationConflict", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy source calls after model conflict = %v, want %v", source.calls, want)
	}
	if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker after conflict stat error = %v, want not exist", statErr)
	}
	models, listErr := target.ListModels(ctx)
	if listErr != nil || !reflect.DeepEqual(models, []domain.Model{conflicting}) {
		t.Fatalf("conflicting target was modified: %#v, %v", models, listErr)
	}
}

func TestMigrateLegacyAuthoredCatalogRetriesFromPartialRevisionWithoutMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	baseTarget := newLegacyCatalogMigrationTarget(t, root)
	target := &failingLegacyCatalogMigrationTarget{
		legacyCatalogMigrationTestTarget: baseTarget,
		failModelUpdates:                 1,
	}
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, baseTarget, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err == nil {
		t.Fatal("first migration error = nil, want injected update failure")
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker after partial migration stat error = %v, want not exist", err)
	}
	partial, err := baseTarget.ListModels(ctx)
	if err != nil || len(partial) != 1 || partial[0].Revision != 1 {
		t.Fatalf("partial models = %#v, %v; want revision 1 checkpoint", partial, err)
	}

	source.calls = nil
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("retry migration error = %v", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("retry legacy source call order = %v, want %v", source.calls, want)
	}
	assertLegacyCatalogMigrationContents(t, ctx, baseTarget, model, channel, mapping, plan)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker after successful retry: %v", err)
	}
}

func TestMigrateLegacyAuthoredCatalogReplaysValidV1MarkerAndUpgradesCaseCoverage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	legacyCase, _ := legacyCatalogMigrationCaseFixtures(domain.CaseRevisionRef{
		CaseID:   "51000000-0000-4000-8000-000000000021",
		Revision: 4,
	})
	legacyCase.Key = "T982"
	source := &recordingLegacyCatalogSource{cases: []domain.TestCase{legacyCase}}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	v1Snapshot, err := loadLegacyAuthoredCatalogSnapshot(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	v1Digest, err := digestLegacyAuthoredCatalogV1Snapshot(v1Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	source.calls = nil
	if err := os.WriteFile(markerPath, []byte(
		"{\"schema_version\":1,\"completed\":true,\"processed_source_digests\":[\""+v1Digest+"\"]}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("replay v1 migration marker: %v", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy source calls while upgrading v1 marker = %v, want %v", source.calls, want)
	}
	cases, err := target.ListTestCases(ctx)
	if err != nil || len(cases) != 1 {
		t.Fatalf("Cases after v1 marker upgrade = %#v, %v", cases, err)
	}
	assertSameCaseBusinessSemantics(t, cases[0], legacyCase)

	payload, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker legacyAuthoredCatalogMigrationMarker
	if err := json.Unmarshal(payload, &marker); err != nil {
		t.Fatalf("decode upgraded marker: %v", err)
	}
	if marker.SchemaVersion != legacyAuthoredCatalogMigrationSchemaVersion || !marker.Completed ||
		len(marker.ProcessedSourceDigests) != 1 || marker.ProcessedSourceDigests[0] == v1Digest {
		t.Fatalf("upgraded marker = %#v, want fresh schema-v%d digest", marker, legacyAuthoredCatalogMigrationSchemaVersion)
	}
}

func TestMigrateLegacyAuthoredCatalogV1CheckpointDoesNotRestoreDeletedCoveredEntity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, _, _, _ := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{models: []domain.Model{model}}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker legacyAuthoredCatalogMigrationMarker
	if err := json.Unmarshal(payload, &marker); err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadLegacyAuthoredCatalogSnapshot(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	v1SourceDigest, err := digestLegacyAuthoredCatalogV1Snapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	marker.SchemaVersion = legacyAuthoredCatalogMigrationPreviousSchemaVersion
	marker.ProcessedSourceDigests = []string{v1SourceDigest}
	marker.TargetDigestsBySource = nil
	payload, err = json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := target.models.Delete(ctx, model.ID, model.Revision); err != nil {
		t.Fatal(err)
	}

	err = migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("v1 restart after deleting exported model error = %v, want migration conflict", err)
	}
	models, listErr := target.ListModels(ctx)
	if listErr != nil || len(models) != 0 {
		t.Fatalf("v1 restart restored deleted model: models=%#v error=%v", models, listErr)
	}
}

func TestMigrateLegacyAuthoredCatalogV1CheckpointFromDifferentSourceDoesNotBlockExport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, _, _, _ := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{models: []domain.Model{model}}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	marker := `{"schema_version":1,"completed":true,"processed_source_digests":["` +
		strings.Repeat("0", 64) + `"]}`
	if err := os.WriteFile(markerPath, []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("export with unrelated v1 checkpoint: %v", err)
	}
	models, err := target.ListModels(ctx)
	if err != nil || !reflect.DeepEqual(models, []domain.Model{model}) {
		t.Fatalf("models after unrelated v1 checkpoint = %#v, %v; want %#v", models, err, model)
	}
}

func TestMigrateLegacyAuthoredCatalogReplaysPersistentV2ReceiptAfterFileEdits(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, _, _, _ := legacyCatalogMigrationFixtures()
	model.Revision = 1
	model.UpdatedAt = model.CreatedAt
	if err := target.CreateModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	oldSourceDigest := strings.Repeat("0", 64)
	oldTargetDigest := strings.Repeat("1", 64)
	marker := `{"schema_version":2,"completed":true,"processed_source_digests":["` + oldSourceDigest +
		`"],"target_digests_by_source":{"` + oldSourceDigest + `":"` + oldTargetDigest + `"}}`
	if err := os.WriteFile(markerPath, []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &recordingLegacyCatalogSource{}

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("replay persistent v2 receipt: %v", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("legacy source calls while replacing v2 receipt = %v, want %v", source.calls, want)
	}
	models, err := target.ListModels(ctx)
	if err != nil || len(models) != 1 || models[0].ID != model.ID {
		t.Fatalf("edited target models after v2 replay = %#v, %v", models, err)
	}
	payload, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var upgraded legacyAuthoredCatalogMigrationMarker
	if err := json.Unmarshal(payload, &upgraded); err != nil {
		t.Fatal(err)
	}
	if upgraded.SchemaVersion != legacyAuthoredCatalogMigrationSchemaVersion {
		t.Fatalf("upgraded marker schema = %d, want %d", upgraded.SchemaVersion, legacyAuthoredCatalogMigrationSchemaVersion)
	}
}

func TestMigrateLegacyAuthoredCatalogPersistentV2ReceiptDoesNotRestoreDeletedEntity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, _, _, _ := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{models: []domain.Model{model}}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker legacyAuthoredCatalogMigrationMarker
	if err := json.Unmarshal(payload, &marker); err != nil {
		t.Fatal(err)
	}
	marker.SchemaVersion = legacyAuthoredCatalogMigrationPersistentSchemaVersion
	payload, err = json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := target.models.Delete(ctx, model.ID, model.Revision); err != nil {
		t.Fatal(err)
	}

	err = migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("v2 restart after deleting exported model error = %v, want migration conflict", err)
	}
	models, listErr := target.ListModels(ctx)
	if listErr != nil || len(models) != 0 {
		t.Fatalf("v2 restart restored deleted model: models=%#v error=%v", models, listErr)
	}
}

func TestMigrateLegacyAuthoredCatalogRejectsUnknownMarkerSchemaWithoutReadingLegacyData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := os.WriteFile(markerPath, []byte("{\"schema_version\":4,\"completed\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &recordingLegacyCatalogSource{failAt: "models"}

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationMarker) {
		t.Fatalf("unknown marker schema error = %v, want errLegacyAuthoredCatalogMigrationMarker", err)
	}
	if len(source.calls) != 0 {
		t.Fatalf("legacy source was consulted for unknown marker schema: %v", source.calls)
	}
}

func TestMigrateLegacyAuthoredCatalogRejectsV2MarkerWithoutCompleteTargetReceipt(t *testing.T) {
	sourceDigest := strings.Repeat("0", 64)
	targetDigest := strings.Repeat("1", 64)
	otherSourceDigest := strings.Repeat("2", 64)
	tests := []struct {
		name   string
		marker string
	}{
		{
			name: "missing target digest map",
			marker: `{"schema_version":2,"completed":true,"processed_source_digests":["` +
				sourceDigest + `"]}`,
		},
		{
			name: "invalid target digest",
			marker: `{"schema_version":2,"completed":true,"processed_source_digests":["` + sourceDigest +
				`"],"target_digests_by_source":{"` + sourceDigest + `":"not-a-sha256"}}`,
		},
		{
			name: "target digest belongs to another source",
			marker: `{"schema_version":2,"completed":true,"processed_source_digests":["` + sourceDigest +
				`"],"target_digests_by_source":{"` + otherSourceDigest + `":"` + targetDigest + `"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			target := newLegacyCatalogMigrationTarget(t, root)
			markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
			if err := os.WriteFile(markerPath, []byte(test.marker+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			source := &recordingLegacyCatalogSource{failAt: "models"}

			err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
			if !errors.Is(err, errLegacyAuthoredCatalogMigrationMarker) {
				t.Fatalf("incomplete v2 marker error = %v, want marker error", err)
			}
			if len(source.calls) != 0 {
				t.Fatalf("legacy source was consulted for incomplete v2 marker: %v", source.calls)
			}
		})
	}
}

func TestMigrateLegacyAuthoredCatalogIdenticalExistingEntitiesAreIdempotentWithoutMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	source.calls = nil

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("idempotent retry error = %v", err)
	}
	if want := []string{"models", "channels", "mappings", "cases", "suites", "plans"}; !reflect.DeepEqual(source.calls, want) {
		t.Fatalf("idempotent retry source order = %v, want %v", source.calls, want)
	}
	assertLegacyCatalogMigrationContents(t, ctx, target, model, channel, mapping, plan)
}

func TestMigrateLegacyAuthoredCatalogStoresPinnedHistoricalCaseRevisionWithoutChangingCurrentCase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	current, pinned := seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrateLegacyAuthoredCatalog() error = %v", err)
	}
	stored, err := target.GetTestCaseRevision(ctx, pinned.ID, pinned.Revision)
	if err != nil || !samePinnedCaseDocument(stored, pinned) {
		t.Fatalf("stored pinned Case revision %q@%d did not match legacy snapshot: %v", pinned.ID, pinned.Revision, err)
	}
	active, err := target.cases.Find(ctx, current.ID)
	if err != nil || !reflect.DeepEqual(active.TestCase, current) {
		t.Fatalf("current case after migration = %#v, %v; want unchanged %#v", active.TestCase, err, current)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker after pinned Case migration: %v", err)
	}
}

func TestMigrateLegacyAuthoredCatalogMissingPinnedCaseRevisionFailsWithoutMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	current, pinned := seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	delete(source.caseRevisions, recordedCaseRevisionKey{caseID: pinned.ID, revision: pinned.Revision})
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if err == nil {
		t.Fatal("migration error = nil, want missing exact Case revision failure")
	}
	if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker after missing pinned Case stat error = %v, want not exist", statErr)
	}
	if strings.Contains(err.Error(), string(current.Definition.Spec)) || strings.Contains(err.Error(), current.Name) {
		t.Fatalf("migration error leaked Case document content: %v", err)
	}
}

func TestMigrateLegacyAuthoredCatalogConflictingPinnedCaseRevisionFailsWithoutMarkerOrDocumentLeak(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	_, pinned := seedLegacyPinnedCaseRevision(t, ctx, target, source, &plan)
	conflicting := pinned
	conflicting.Name = "target-only-drift-must-not-leak"
	conflicting.Definition.Spec = json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"target-only-secret-drift"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`)
	if err := target.StoreTestCaseRevision(ctx, conflicting); err != nil {
		t.Fatalf("seed conflicting target Case revision: %v", err)
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("migration conflict error = %v, want errLegacyAuthoredCatalogMigrationConflict", err)
	}
	if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker after pinned Case drift stat error = %v, want not exist", statErr)
	}
	for _, documentContent := range []string{pinned.Name, string(pinned.Definition.Spec), conflicting.Name, string(conflicting.Definition.Spec)} {
		if strings.Contains(err.Error(), documentContent) {
			t.Fatalf("migration conflict leaked Case document content: %v", err)
		}
	}
}

func TestMigrateLegacyAuthoredCatalogRetryAfterStoredCaseInterruptionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	baseTarget := newLegacyCatalogMigrationTarget(t, root)
	target := &interruptingCaseStoreMigrationTarget{
		legacyCatalogMigrationTestTarget: baseTarget,
		failAfterStore:                   true,
		storeCalls:                       map[recordedCaseRevisionKey]int{},
	}
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{plan},
	}
	_, pinned := seedLegacyPinnedCaseRevision(t, ctx, baseTarget, source, &plan)
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err == nil {
		t.Fatal("first migration error = nil, want injected post-store interruption")
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker after interrupted Case migration stat error = %v, want not exist", err)
	}
	if _, err := baseTarget.GetTestCaseRevision(ctx, pinned.ID, pinned.Revision); err != nil {
		t.Fatalf("stored Case checkpoint after interruption: %v", err)
	}

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("retry migration error = %v", err)
	}
	key := recordedCaseRevisionKey{caseID: pinned.ID, revision: pinned.Revision}
	if target.storeCalls[key] != 1 {
		t.Fatalf("Case revision store calls across retry = %d, want 1", target.storeCalls[key])
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker after successful retry: %v", err)
	}
}

func TestMigrateLegacyAuthoredCatalogDeduplicatesPinnedCaseRefsAndSkipsUnreferencedCases(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	baseTarget := newLegacyCatalogMigrationTarget(t, root)
	target := &rejectingDuplicateCaseStoreMigrationTarget{
		legacyCatalogMigrationTestTarget: baseTarget,
		storeCalls:                       map[recordedCaseRevisionKey]int{},
	}
	model, channel, mapping, firstPlan := legacyCatalogMigrationFixtures()
	secondPlan := firstPlan
	secondPlan.ID = "51000000-0000-4000-8000-000000000007"
	secondPlan.Name = "second legacy plan"
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel},
		mappings: []domain.ChannelModel{mapping}, plans: []domain.Plan{firstPlan, secondPlan},
	}
	_, pinned := seedLegacyPinnedCaseRevision(t, ctx, baseTarget, source, &firstPlan)
	unreferenced := pinned
	unreferenced.ID = "51000000-0000-4000-8000-000000000008"
	unreferenced.Name = "unreferenced legacy snapshot"
	unreferencedKey := recordedCaseRevisionKey{caseID: unreferenced.ID, revision: unreferenced.Revision}
	source.caseRevisions[unreferencedKey] = unreferenced
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate duplicate Case refs: %v", err)
	}
	pinnedKey := recordedCaseRevisionKey{caseID: pinned.ID, revision: pinned.Revision}
	if source.caseRevisionReads[pinnedKey] != 1 || target.storeCalls[pinnedKey] != 1 {
		t.Fatalf("deduplicated pinned Case reads/stores = %d/%d, want 1/1", source.caseRevisionReads[pinnedKey], target.storeCalls[pinnedKey])
	}
	if source.caseRevisionReads[unreferencedKey] != 0 || target.storeCalls[unreferencedKey] != 0 {
		t.Fatalf("unreferenced Case reads/stores = %d/%d, want 0/0", source.caseRevisionReads[unreferencedKey], target.storeCalls[unreferencedKey])
	}
}

func TestMigrateLegacyAuthoredCatalogExportsUnreferencedLegacyUserCase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	_, legacy := legacyCatalogMigrationCaseFixtures(domain.CaseRevisionRef{
		CaseID:   "51000000-0000-4000-8000-000000000018",
		Revision: 3,
	})
	legacy.Name = "unreferenced legacy user Case"
	source := &recordingLegacyCatalogSource{cases: []domain.TestCase{legacy}}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate unreferenced legacy Case: %v", err)
	}
	cases, err := target.ListTestCases(ctx)
	if err != nil {
		t.Fatalf("list migrated file Cases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("migrated file Cases = %#v, want native file Case with legacy semantics", cases)
	}
	assertSameCaseBusinessSemantics(t, cases[0], legacy)
	if cases[0].ID == legacy.ID || cases[0].Revision == legacy.Revision {
		t.Fatalf("migrated Case retained legacy derived metadata: got %#v legacy %#v", cases[0].EntityMeta, legacy.EntityMeta)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("migration marker after Case export: %v", err)
	}
}

func TestMigrateLegacyAuthoredCatalogExportsLegacySuiteThroughNativeCaseKeys(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	_, legacyCase := legacyCatalogMigrationCaseFixtures(domain.CaseRevisionRef{
		CaseID:   "51000000-0000-4000-8000-000000000019",
		Revision: 4,
	})
	legacyCase.Key = "T981"
	legacyCase.ModelTargets = []string{"legacy-upstream"}
	legacySuite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "51000000-0000-4000-8000-000000000020", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 2, CreatedAt: legacyCase.CreatedAt, UpdatedAt: legacyCase.UpdatedAt,
		},
		Key: "legacy-suite", Name: "legacy Suite", Protocol: legacyCase.Protocol,
		ModelTarget: "legacy-upstream",
		Cases:       []domain.CaseRevisionRef{{CaseID: legacyCase.ID, Revision: legacyCase.Revision}},
	}
	source := &recordingLegacyCatalogSource{
		cases:  []domain.TestCase{legacyCase},
		suites: []domain.Suite{legacySuite},
		caseRevisions: map[recordedCaseRevisionKey]domain.TestCase{
			{caseID: legacyCase.ID, revision: legacyCase.Revision}: legacyCase,
		},
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate legacy Suite: %v", err)
	}
	fileCases, err := target.ListTestCases(ctx)
	if err != nil || len(fileCases) != 1 {
		t.Fatalf("migrated file Cases = %#v, %v", fileCases, err)
	}
	fileSuites, err := target.ListSuites(ctx)
	if err != nil || len(fileSuites) != 1 {
		t.Fatalf("migrated file Suites = %#v, %v", fileSuites, err)
	}
	wantRef := domain.CaseRevisionRef{CaseID: fileCases[0].ID, Revision: fileCases[0].Revision}
	if fileSuites[0].ID == legacySuite.ID || fileSuites[0].Revision == legacySuite.Revision ||
		fileSuites[0].Key != legacySuite.Key || fileSuites[0].Name != legacySuite.Name ||
		fileSuites[0].Protocol != legacySuite.Protocol || fileSuites[0].ModelTarget != legacySuite.ModelTarget ||
		!reflect.DeepEqual(fileSuites[0].Cases, []domain.CaseRevisionRef{wantRef}) {
		t.Fatalf("migrated native file Suite = %#v, want legacy semantics with Case ref %#v", fileSuites[0], wantRef)
	}
	if fileSuites[0].Cases[0].CaseID == legacySuite.Cases[0].CaseID {
		t.Fatalf("migrated Suite retained legacy Case UUID %#v", fileSuites[0].Cases[0])
	}
}

func TestMigrateLegacyAuthoredCatalogRemapsPlanSuiteToNativeFileIdentity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	legacyCurrent, legacyPinned := legacyCatalogMigrationCaseFixtures(plan.Cases[0])
	legacyCurrent.ModelTargets = []string{mapping.UpstreamModelName}
	legacyPinned.ModelTargets = append([]string(nil), legacyCurrent.ModelTargets...)
	legacySuite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "51000000-0000-4000-8000-000000000022", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 3, CreatedAt: legacyCurrent.CreatedAt, UpdatedAt: legacyCurrent.UpdatedAt,
		},
		Key: "legacy-plan-suite", Name: "legacy Plan Suite", Protocol: legacyCurrent.Protocol,
		ModelTarget: mapping.UpstreamModelName,
		Cases:       []domain.CaseRevisionRef{{CaseID: legacyCurrent.ID, Revision: legacyCurrent.Revision}},
	}
	plan.SuiteID = legacySuite.ID
	plan.SuiteRevision = legacySuite.Revision
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel}, mappings: []domain.ChannelModel{mapping},
		cases: []domain.TestCase{legacyCurrent}, suites: []domain.Suite{legacySuite}, plans: []domain.Plan{plan},
		caseRevisions: map[recordedCaseRevisionKey]domain.TestCase{
			{caseID: legacyCurrent.ID, revision: legacyCurrent.Revision}: legacyCurrent,
			{caseID: legacyPinned.ID, revision: legacyPinned.Revision}:   legacyPinned,
		},
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate Plan with legacy Suite: %v", err)
	}
	fileCases, err := target.ListTestCases(ctx)
	if err != nil || len(fileCases) != 1 {
		t.Fatalf("migrated file Cases = %#v, %v", fileCases, err)
	}
	fileSuites, err := target.ListSuites(ctx)
	if err != nil || len(fileSuites) != 1 {
		t.Fatalf("migrated file Suites = %#v, %v", fileSuites, err)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil || len(documents) != 1 {
		t.Fatalf("migrated Plan documents = %#v, %v", documents, err)
	}
	if documents[0].SuiteID != fileSuites[0].ID || documents[0].SuiteID == legacySuite.ID ||
		documents[0].SuiteRevision != legacySuite.Revision {
		t.Fatalf("migrated Plan Suite ref = %q/%d, want native ID %q and exact legacy revision %d", documents[0].SuiteID, documents[0].SuiteRevision, fileSuites[0].ID, legacySuite.Revision)
	}
	pinnedSuite, err := target.GetSuiteRevision(ctx, documents[0].SuiteID, documents[0].SuiteRevision)
	if err != nil {
		t.Fatalf("resolve remapped pinned Suite sidecar: %v", err)
	}
	if pinnedSuite.ID != fileSuites[0].ID || pinnedSuite.Revision != legacySuite.Revision ||
		len(pinnedSuite.Cases) != 1 || pinnedSuite.Cases[0].CaseID != fileCases[0].ID ||
		pinnedSuite.Cases[0].Revision != legacyCurrent.Revision {
		t.Fatalf("remapped pinned Suite = %#v, want exact legacy Suite with native Case identity", pinnedSuite)
	}
	caseEntries, err := target.cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("list Case entries before sidecar removal = %#v, %v", caseEntries, err)
	}
	innerCaseRevisionPath := filepath.Join(
		root, "cases", caseEntries[0].Group, caseEntries[0].Directory, "revisions",
		strconv.FormatUint(pinnedSuite.Cases[0].Revision, 10)+".json",
	)
	if err := os.Remove(innerCaseRevisionPath); err != nil {
		t.Fatalf("remove Suite-only pinned Case sidecar: %v", err)
	}

	err = migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("restart after Suite-only Case sidecar loss error = %v, want migration conflict", err)
	}
}

func TestMigrateLegacyAuthoredCatalogPreservesExactHistoricalSuiteRevision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	legacyCurrentCase, legacyPinnedCase := legacyCatalogMigrationCaseFixtures(plan.Cases[0])
	legacyCurrentCase.ModelTargets = []string{mapping.UpstreamModelName}
	legacyPinnedCase.ModelTargets = append([]string(nil), legacyCurrentCase.ModelTargets...)

	pinnedSuite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "51000000-0000-4000-8000-000000000023", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 3, CreatedAt: legacyPinnedCase.CreatedAt, UpdatedAt: legacyPinnedCase.UpdatedAt,
		},
		Key: "historical-plan-suite", Name: "historical Suite revision", Protocol: legacyPinnedCase.Protocol,
		ModelTarget: mapping.UpstreamModelName,
		Cases:       []domain.CaseRevisionRef{{CaseID: legacyPinnedCase.ID, Revision: legacyPinnedCase.Revision}},
	}
	currentSuite := pinnedSuite
	currentSuite.Revision = 4
	currentSuite.UpdatedAt = legacyCurrentCase.UpdatedAt
	currentSuite.Name = "current Suite revision"
	currentSuite.Cases = []domain.CaseRevisionRef{{CaseID: legacyCurrentCase.ID, Revision: legacyCurrentCase.Revision}}
	plan.SuiteID = pinnedSuite.ID
	plan.SuiteRevision = pinnedSuite.Revision

	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel}, mappings: []domain.ChannelModel{mapping},
		cases: []domain.TestCase{legacyCurrentCase}, suites: []domain.Suite{currentSuite}, plans: []domain.Plan{plan},
		caseRevisions: map[recordedCaseRevisionKey]domain.TestCase{
			{caseID: legacyCurrentCase.ID, revision: legacyCurrentCase.Revision}: legacyCurrentCase,
			{caseID: legacyPinnedCase.ID, revision: legacyPinnedCase.Revision}:   legacyPinnedCase,
		},
		suiteRevisions: map[recordedSuiteRevisionKey]domain.Suite{
			{suiteID: pinnedSuite.ID, revision: pinnedSuite.Revision}: pinnedSuite,
		},
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate Plan with historical Suite: %v", err)
	}
	fileCases, err := target.ListTestCases(ctx)
	if err != nil || len(fileCases) != 1 {
		t.Fatalf("migrated file Cases = %#v, %v", fileCases, err)
	}
	fileSuites, err := target.ListSuites(ctx)
	if err != nil || len(fileSuites) != 1 {
		t.Fatalf("migrated file Suites = %#v, %v", fileSuites, err)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil || len(documents) != 1 {
		t.Fatalf("migrated Plan documents = %#v, %v", documents, err)
	}
	if documents[0].SuiteID != fileSuites[0].ID || documents[0].SuiteRevision != pinnedSuite.Revision {
		t.Fatalf("migrated Plan Suite ref = %q/%d, want %q/%d", documents[0].SuiteID, documents[0].SuiteRevision, fileSuites[0].ID, pinnedSuite.Revision)
	}
	stored, err := target.GetSuiteRevision(ctx, fileSuites[0].ID, pinnedSuite.Revision)
	if err != nil {
		t.Fatalf("GetSuiteRevision(historical) error = %v", err)
	}
	if stored.Name != pinnedSuite.Name || stored.Revision != pinnedSuite.Revision ||
		len(stored.Cases) != 1 || stored.Cases[0] != (domain.CaseRevisionRef{CaseID: fileCases[0].ID, Revision: legacyPinnedCase.Revision}) {
		t.Fatalf("historical Suite sidecar = %#v, want exact pinned semantics with native Case identity", stored)
	}
	if fileSuites[0].Name != currentSuite.Name || fileSuites[0].Revision == stored.Revision {
		t.Fatalf("active Suite = %#v, historical sidecar = %#v; current and pinned revisions must remain distinct", fileSuites[0], stored)
	}
	suiteEntries, err := target.suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("list Suite entries before sidecar removal = %#v, %v", suiteEntries, err)
	}
	suiteRevisionPath := filepath.Join(
		root, "suites", suiteEntries[0].Group, suiteEntries[0].Directory, "revisions",
		strconv.FormatUint(stored.Revision, 10)+".json",
	)
	if err := os.Remove(suiteRevisionPath); err != nil {
		t.Fatalf("remove pinned Suite sidecar: %v", err)
	}

	err = migrateLegacyAuthoredCatalog(ctx, source, target, markerPath)
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("restart after pinned Suite sidecar loss error = %v, want migration conflict", err)
	}
}

func TestMigrateLegacyAuthoredCatalogRemapsPlanPinnedCaseToNativeRevisionSidecar(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := newLegacyCatalogMigrationTarget(t, root)
	model, channel, mapping, plan := legacyCatalogMigrationFixtures()
	legacyCurrent, legacyPinned := legacyCatalogMigrationCaseFixtures(plan.Cases[0])
	source := &recordingLegacyCatalogSource{
		models: []domain.Model{model}, channels: []domain.Channel{channel}, mappings: []domain.ChannelModel{mapping},
		cases: []domain.TestCase{legacyCurrent}, plans: []domain.Plan{plan},
		caseRevisions: map[recordedCaseRevisionKey]domain.TestCase{
			{caseID: legacyPinned.ID, revision: legacyPinned.Revision}: legacyPinned,
		},
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")

	if err := migrateLegacyAuthoredCatalog(ctx, source, target, markerPath); err != nil {
		t.Fatalf("migrate Plan with legacy pinned Case: %v", err)
	}
	fileCases, err := target.ListTestCases(ctx)
	if err != nil || len(fileCases) != 1 {
		t.Fatalf("migrated file Cases = %#v, %v", fileCases, err)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil || len(documents) != 1 || len(documents[0].Cases) != 1 {
		t.Fatalf("migrated Plan documents = %#v, %v", documents, err)
	}
	ref := documents[0].Cases[0]
	if ref.CaseID != fileCases[0].ID || ref.CaseID == legacyPinned.ID || ref.Revision != legacyPinned.Revision {
		t.Fatalf("migrated Plan Case ref = %#v, want native ID %q and legacy revision %d", ref, fileCases[0].ID, legacyPinned.Revision)
	}
	stored, err := target.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
	if err != nil {
		t.Fatalf("resolve remapped pinned Case sidecar: %v", err)
	}
	assertSameCaseBusinessSemantics(t, stored, legacyPinned)
}

type recordingLegacyCatalogSource struct {
	models                []domain.Model
	channels              []domain.Channel
	mappings              []domain.ChannelModel
	cases                 []domain.TestCase
	suites                []domain.Suite
	plans                 []domain.Plan
	planTargets           map[recordedPlanTargetKey]plancatalog.TargetBinding
	planTargetErrors      map[recordedPlanTargetKey]error
	planTargetResolutions []recordedPlanTargetKey
	caseRevisions         map[recordedCaseRevisionKey]domain.TestCase
	caseRevisionReads     map[recordedCaseRevisionKey]int
	suiteRevisions        map[recordedSuiteRevisionKey]domain.Suite
	calls                 []string
	failAt                string
}

type recordedPlanTargetKey struct {
	planID    string
	modelID   string
	channelID string
}

type recordedCaseRevisionKey struct {
	caseID   string
	revision uint64
}

type recordedSuiteRevisionKey struct {
	suiteID  string
	revision uint64
}

type failingLegacyCatalogMigrationTarget struct {
	legacyCatalogMigrationTestTarget
	failModelUpdates int
}

type interruptingCaseStoreMigrationTarget struct {
	legacyCatalogMigrationTestTarget
	failAfterStore bool
	storeCalls     map[recordedCaseRevisionKey]int
}

func (target *interruptingCaseStoreMigrationTarget) StoreTestCaseRevision(ctx context.Context, value domain.TestCase) error {
	key := recordedCaseRevisionKey{caseID: value.ID, revision: value.Revision}
	target.storeCalls[key]++
	if err := target.legacyCatalogMigrationTestTarget.StoreTestCaseRevision(ctx, value); err != nil {
		return err
	}
	if target.failAfterStore {
		target.failAfterStore = false
		return errors.New("injected interruption after Case revision store")
	}
	return nil
}

type rejectingDuplicateCaseStoreMigrationTarget struct {
	legacyCatalogMigrationTestTarget
	storeCalls map[recordedCaseRevisionKey]int
}

func (target *rejectingDuplicateCaseStoreMigrationTarget) StoreTestCaseRevision(ctx context.Context, value domain.TestCase) error {
	key := recordedCaseRevisionKey{caseID: value.ID, revision: value.Revision}
	target.storeCalls[key]++
	if target.storeCalls[key] > 1 {
		return errors.New("duplicate Case revision store")
	}
	return target.legacyCatalogMigrationTestTarget.StoreTestCaseRevision(ctx, value)
}

func (target *failingLegacyCatalogMigrationTarget) UpdateModel(ctx context.Context, revision uint64, value domain.Model) error {
	if target.failModelUpdates > 0 {
		target.failModelUpdates--
		return errors.New("injected model update failure")
	}
	return target.legacyCatalogMigrationTestTarget.UpdateModel(ctx, revision, value)
}

func (source *recordingLegacyCatalogSource) ListModels(context.Context) ([]domain.Model, error) {
	source.calls = append(source.calls, "models")
	if source.failAt == "models" {
		return nil, errors.New("injected legacy model read")
	}
	return source.models, nil
}

func (source *recordingLegacyCatalogSource) ListChannels(context.Context) ([]domain.Channel, error) {
	source.calls = append(source.calls, "channels")
	if source.failAt == "channels" {
		return nil, errors.New("injected legacy channel read")
	}
	return source.channels, nil
}

func (source *recordingLegacyCatalogSource) ListChannelModels(context.Context) ([]domain.ChannelModel, error) {
	source.calls = append(source.calls, "mappings")
	if source.failAt == "mappings" {
		return nil, errors.New("injected legacy mapping read")
	}
	return source.mappings, nil
}

func (source *recordingLegacyCatalogSource) ListPlans(context.Context) ([]domain.Plan, error) {
	source.calls = append(source.calls, "plans")
	if source.failAt == "plans" {
		return nil, errors.New("injected legacy plan read")
	}
	return source.plans, nil
}

func (source *recordingLegacyCatalogSource) ResolvePlanTargetSelection(
	_ context.Context,
	plan domain.Plan,
	modelID string,
	channelID string,
) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	key := recordedPlanTargetKey{planID: plan.ID, modelID: modelID, channelID: channelID}
	source.planTargetResolutions = append(source.planTargetResolutions, key)
	if err, exists := source.planTargetErrors[key]; exists {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	if source.planTargets != nil {
		binding, exists := source.planTargets[key]
		if !exists {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, errors.New("legacy exact Plan target is unavailable")
		}
		return binding.Model, binding.Channel, binding.Mapping, nil
	}
	var model domain.Model
	for _, candidate := range source.models {
		if candidate.ID == modelID {
			model = candidate
			break
		}
	}
	var channel domain.Channel
	for _, candidate := range source.channels {
		if candidate.ID == channelID {
			channel = candidate
			break
		}
	}
	var mapping domain.ChannelModel
	for _, candidate := range source.mappings {
		if candidate.ModelID == modelID && candidate.ChannelID == channelID {
			mapping = candidate
			break
		}
	}
	if model.ID == "" || channel.ID == "" || mapping.ID == "" {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, errors.New("legacy exact Plan target is unavailable")
	}
	return model, channel, mapping, nil
}

func (source *recordingLegacyCatalogSource) GetTestCaseRevision(_ context.Context, id string, revision uint64) (domain.TestCase, error) {
	key := recordedCaseRevisionKey{caseID: id, revision: revision}
	if source.caseRevisionReads == nil {
		source.caseRevisionReads = map[recordedCaseRevisionKey]int{}
	}
	source.caseRevisionReads[key]++
	value, ok := source.caseRevisions[key]
	if !ok {
		return domain.TestCase{}, errors.New("legacy exact Case revision is unavailable")
	}
	return value, nil
}

func (source *recordingLegacyCatalogSource) GetSuiteRevision(_ context.Context, id string, revision uint64) (domain.Suite, error) {
	key := recordedSuiteRevisionKey{suiteID: id, revision: revision}
	if value, ok := source.suiteRevisions[key]; ok {
		return value, nil
	}
	for _, value := range source.suites {
		if value.ID == id && value.Revision == revision {
			return value, nil
		}
	}
	return domain.Suite{}, errors.New("legacy exact Suite revision is unavailable")
}

func (source *recordingLegacyCatalogSource) ListTestCases(context.Context) ([]domain.TestCase, error) {
	source.calls = append(source.calls, "cases")
	if source.failAt == "cases" {
		return nil, errors.New("injected legacy Case read")
	}
	return source.cases, nil
}

func (source *recordingLegacyCatalogSource) ListSuites(context.Context) ([]domain.Suite, error) {
	source.calls = append(source.calls, "suites")
	if source.failAt == "suites" {
		return nil, errors.New("injected legacy Suite read")
	}
	return source.suites, nil
}

type legacyCatalogMigrationTestTarget struct {
	models   *modelcatalog.Service
	channels *channelcatalog.Service
	cases    *casecatalog.Service
	suites   *suitecatalog.Service
	plans    *plancatalog.Service
}

func newLegacyCatalogMigrationTarget(t *testing.T, root string) legacyCatalogMigrationTestTarget {
	t.Helper()
	models, err := modelcatalog.New(filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	suites, err := suitecatalog.New(suitecatalog.Options{
		Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites"), Cases: cases,
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	return legacyCatalogMigrationTestTarget{models: models, channels: channels, cases: cases, suites: suites, plans: plans}
}

func (target legacyCatalogMigrationTestTarget) ListModels(ctx context.Context) ([]domain.Model, error) {
	return target.models.List(ctx)
}

func (target legacyCatalogMigrationTestTarget) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	return target.channels.ListChannels(ctx)
}

func (target legacyCatalogMigrationTestTarget) ListChannelModels(ctx context.Context) ([]domain.ChannelModel, error) {
	return target.channels.ListMappings(ctx)
}

func (target legacyCatalogMigrationTestTarget) ListPlanDocuments(ctx context.Context) ([]plancatalog.Document, error) {
	return target.plans.ListDocuments(ctx)
}

func (target legacyCatalogMigrationTestTarget) GetTestCaseRevision(ctx context.Context, id string, revision uint64) (domain.TestCase, error) {
	entry, err := target.cases.FindRevision(ctx, id, revision)
	return entry.TestCase, err
}

func (target legacyCatalogMigrationTestTarget) GetSuiteRevision(ctx context.Context, id string, revision uint64) (domain.Suite, error) {
	entry, err := target.suites.FindRevision(ctx, id, revision)
	return entry.Suite, err
}

func (target legacyCatalogMigrationTestTarget) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	entries, err := target.cases.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.TestCase, len(entries))
	for index, entry := range entries {
		result[index] = entry.TestCase
	}
	return result, nil
}

func (target legacyCatalogMigrationTestTarget) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	entries, err := target.suites.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Suite, len(entries))
	for index, entry := range entries {
		result[index] = entry.Suite
	}
	return result, nil
}

func (target legacyCatalogMigrationTestTarget) StoreTestCaseRevision(ctx context.Context, value domain.TestCase) error {
	return target.cases.StoreRevision(ctx, value)
}

func (target legacyCatalogMigrationTestTarget) StoreSuiteRevision(ctx context.Context, value domain.Suite) error {
	return target.suites.StoreRevision(ctx, value)
}

func (target legacyCatalogMigrationTestTarget) WithAuthoredCatalogRetirementLock(ctx context.Context, action func(context.Context) error) error {
	if action == nil {
		return errLegacyAuthoredCatalogMigrationInvalid
	}
	return action(ctx)
}

func (target legacyCatalogMigrationTestTarget) CreateTestCase(ctx context.Context, value domain.TestCase) error {
	return target.cases.SaveCase(ctx, string(value.Protocol), value.Key, value)
}

func (target legacyCatalogMigrationTestTarget) CreateSuite(ctx context.Context, value domain.Suite) error {
	return target.suites.SaveSuite(ctx, string(value.Protocol), value.Key, value)
}

func (target legacyCatalogMigrationTestTarget) CreateModel(ctx context.Context, value domain.Model) error {
	return target.models.Create(ctx, value)
}

func (target legacyCatalogMigrationTestTarget) CreateChannel(ctx context.Context, value domain.Channel) error {
	return target.channels.CreateChannel(ctx, value)
}

func (target legacyCatalogMigrationTestTarget) CreateChannelModel(ctx context.Context, value domain.ChannelModel) error {
	return target.channels.CreateMapping(ctx, value)
}

func (target legacyCatalogMigrationTestTarget) CreatePlanDocument(ctx context.Context, value plancatalog.Document) error {
	return target.plans.CreateDocument(ctx, value)
}

func (target legacyCatalogMigrationTestTarget) UpdateModel(ctx context.Context, revision uint64, value domain.Model) error {
	return target.models.Update(ctx, revision, value)
}

func (target legacyCatalogMigrationTestTarget) UpdateChannel(ctx context.Context, revision uint64, value domain.Channel) error {
	return target.channels.UpdateChannel(ctx, revision, value)
}

func (target legacyCatalogMigrationTestTarget) UpdateChannelModel(ctx context.Context, revision uint64, value domain.ChannelModel) error {
	return target.channels.UpdateMapping(ctx, revision, value)
}

func (target legacyCatalogMigrationTestTarget) UpdatePlanDocument(ctx context.Context, revision uint64, value plancatalog.Document) error {
	return target.plans.UpdateDocument(ctx, revision, value)
}

func legacyCatalogMigrationFixtures() (domain.Model, domain.Channel, domain.ChannelModel, domain.Plan) {
	now := time.Date(2026, 9, 4, 10, 30, 0, 0, time.UTC)
	meta := func(id string, revision uint64) domain.EntityMeta {
		return domain.EntityMeta{
			ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: revision,
			CreatedAt: now, UpdatedAt: now.Add(time.Duration(revision-1) * time.Minute),
		}
	}
	model := domain.Model{
		EntityMeta: meta("51000000-0000-4000-8000-000000000001", 3),
		Name:       "legacy model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"},
	}
	channel := domain.Channel{
		EntityMeta: meta("51000000-0000-4000-8000-000000000002", 2),
		Name:       "legacy channel", BaseURL: "https://legacy.example.test/v1", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, CredentialID: "51000000-0000-4000-8000-000000000003",
	}
	mapping := domain.ChannelModel{
		EntityMeta: meta("51000000-0000-4000-8000-000000000004", 4),
		ChannelID:  channel.ID, ModelID: model.ID, UpstreamModelName: "legacy-upstream",
	}
	plan := domain.Plan{
		EntityMeta: meta("51000000-0000-4000-8000-000000000005", 5),
		Name:       "legacy plan", ModelIDs: []string{model.ID}, ChannelIDs: []string{channel.ID},
		Cases: []domain.CaseRevisionRef{{CaseID: "51000000-0000-4000-8000-000000000006", Revision: 7}},
		Load: domain.LoadProfile{
			Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
		},
		SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 3_000}},
	}
	return model, channel, mapping, plan
}

func legacyCatalogMigrationCaseFixtures(ref domain.CaseRevisionRef) (domain.TestCase, domain.TestCase) {
	now := time.Date(2026, 9, 4, 10, 30, 0, 0, time.UTC)
	current := domain.TestCase{
		EntityMeta: domain.EntityMeta{
			ID: ref.CaseID, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: ref.Revision + 1,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		},
		Key: "T980", Name: "current file definition", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          domain.CaseType("request.single"),
			TypeVersion:   1,
			Spec:          json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"current-file-definition"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`),
		},
	}
	pinned := current
	pinned.Revision = ref.Revision
	pinned.UpdatedAt = now.Add(-time.Minute)
	pinned.Name = "legacy pinned definition"
	pinned.Definition.Spec = json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"legacy-pinned-definition"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`)
	return current, pinned
}

func seedLegacyPinnedCaseRevision(
	t *testing.T,
	ctx context.Context,
	target legacyCatalogMigrationTestTarget,
	source *recordingLegacyCatalogSource,
	plan *domain.Plan,
) (domain.TestCase, domain.TestCase) {
	t.Helper()
	if plan == nil || len(plan.Cases) == 0 {
		t.Fatal("seed pinned Case revision requires a Plan Case ref")
	}
	originalRef := plan.Cases[0]
	currentCandidate, _ := legacyCatalogMigrationCaseFixtures(originalRef)
	current := seedCurrentMigrationCase(t, ctx, target, currentCandidate)
	ref := domain.CaseRevisionRef{CaseID: current.ID, Revision: originalRef.Revision}
	plan.Cases[0] = ref
	for planIndex := range source.plans {
		for caseIndex := range source.plans[planIndex].Cases {
			if source.plans[planIndex].Cases[caseIndex] == originalRef {
				source.plans[planIndex].Cases[caseIndex] = ref
			}
		}
	}
	pinned := current
	pinned.Revision = ref.Revision
	pinned.Name = "legacy pinned definition"
	pinned.Definition.Spec = json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"legacy-pinned-definition"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`)
	if source.caseRevisions == nil {
		source.caseRevisions = map[recordedCaseRevisionKey]domain.TestCase{}
	}
	source.caseRevisions[recordedCaseRevisionKey{caseID: pinned.ID, revision: pinned.Revision}] = pinned
	return current, pinned
}

func seedCurrentMigrationCase(t *testing.T, ctx context.Context, target legacyCatalogMigrationTestTarget, current domain.TestCase) domain.TestCase {
	t.Helper()
	if err := target.cases.SaveCase(ctx, string(current.Protocol), current.Key, current); err != nil {
		t.Fatalf("seed current filesystem Case: %v", err)
	}
	entries, err := target.cases.Entries(ctx)
	if err != nil {
		t.Fatalf("reload current filesystem Case: %v", err)
	}
	for _, entry := range entries {
		if entry.TestCase.Protocol == current.Protocol && entry.TestCase.Key == current.Key {
			return entry.TestCase
		}
	}
	t.Fatal("seeded current filesystem Case was not discoverable")
	return domain.TestCase{}
}

func assertLegacyCatalogMigrationContents(
	t *testing.T,
	ctx context.Context,
	target legacyCatalogMigrationTestTarget,
	model domain.Model,
	channel domain.Channel,
	mapping domain.ChannelModel,
	plan domain.Plan,
) {
	t.Helper()
	models, modelsErr := target.ListModels(ctx)
	channels, channelsErr := target.ListChannels(ctx)
	mappings, mappingsErr := target.ListChannelModels(ctx)
	planDocuments, plansErr := target.ListPlanDocuments(ctx)
	plans := planProjections(planDocuments)
	if modelsErr != nil || channelsErr != nil || mappingsErr != nil || plansErr != nil {
		t.Fatalf("list migrated catalogs: models=%v channels=%v mappings=%v plans=%v", modelsErr, channelsErr, mappingsErr, plansErr)
	}
	if !reflect.DeepEqual(models, []domain.Model{model}) ||
		!reflect.DeepEqual(channels, []domain.Channel{channel}) ||
		!reflect.DeepEqual(mappings, []domain.ChannelModel{mapping}) ||
		!reflect.DeepEqual(plans, []domain.Plan{plan}) {
		t.Fatalf("migrated catalogs = models:%#v channels:%#v mappings:%#v plans:%#v", models, channels, mappings, plans)
	}
}

func assertSameCaseBusinessSemantics(t *testing.T, got, want domain.TestCase) {
	t.Helper()
	gotDocument := struct {
		Key           string
		Name          string
		Dimension     string
		Protocol      domain.Protocol
		ModelTargets  []string
		Enabled       bool
		Default       bool
		Severity      domain.CaseSeverity
		ExecutionMode domain.CaseExecutionMode
		SchemaVersion int
		Type          domain.CaseType
		TypeVersion   uint32
	}{
		got.Key, got.Name, got.Dimension, got.Protocol, got.ModelTargets, got.Enabled, got.Default,
		got.Severity, got.ExecutionMode, got.Definition.SchemaVersion, got.Definition.Type, got.Definition.TypeVersion,
	}
	wantDocument := struct {
		Key           string
		Name          string
		Dimension     string
		Protocol      domain.Protocol
		ModelTargets  []string
		Enabled       bool
		Default       bool
		Severity      domain.CaseSeverity
		ExecutionMode domain.CaseExecutionMode
		SchemaVersion int
		Type          domain.CaseType
		TypeVersion   uint32
	}{
		want.Key, want.Name, want.Dimension, want.Protocol, want.ModelTargets, want.Enabled, want.Default,
		want.Severity, want.ExecutionMode, want.Definition.SchemaVersion, want.Definition.Type, want.Definition.TypeVersion,
	}
	var gotSpec, wantSpec any
	gotErr := json.Unmarshal(got.Definition.Spec, &gotSpec)
	wantErr := json.Unmarshal(want.Definition.Spec, &wantSpec)
	if gotErr != nil || wantErr != nil || !reflect.DeepEqual(gotDocument, wantDocument) || !reflect.DeepEqual(gotSpec, wantSpec) {
		t.Fatalf("Case business semantics = %#v/%#v, want %#v/%#v (decode errors %v/%v)", gotDocument, gotSpec, wantDocument, wantSpec, gotErr, wantErr)
	}
}
