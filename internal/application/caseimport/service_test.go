package caseimport

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestImportCreatesVersionedCasesAndIsIdempotent(t *testing.T) {
	t.Parallel()

	store := &memoryImportStore{}
	service := newImportService(t, store)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "sync", "chat_sync", false))},
		"openai-chat/T010/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T010", "manual", "manual_unknown", false))},
		"kimi-k3/F025/case.json":     &fstest.MapFile{Data: []byte(kimiDisabledFixture())},
	}

	first, err := service.Import(context.Background(), bundle)
	if err != nil {
		t.Fatalf("first Import() error = %v", err)
	}
	if first.Discovered != 3 || first.Created != 3 || first.Runnable != 1 || first.Manual != 1 || first.Disabled != 1 || len(first.Conflicts) != 0 {
		t.Fatalf("first Import() result = %#v", first)
	}
	if len(store.state.Cases) != 3 || len(store.state.Sources) != 3 {
		t.Fatalf("stored state = %#v", store.state)
	}
	for _, source := range store.state.Sources {
		if source.SourceBytesSHA256 == "" || source.SemanticSHA256 == "" || source.MaterializedSHA256 == "" || source.BundleManifestSHA256 != first.BundleManifestSHA256 || source.ImportedRevision != 1 {
			t.Fatalf("source metadata = %#v", source)
		}
	}

	second, err := service.Import(context.Background(), bundle)
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if second.Created != 0 || second.Updated != 0 || second.Unchanged != 3 || second.BundleManifestSHA256 != first.BundleManifestSHA256 {
		t.Fatalf("second Import() result = %#v", second)
	}
	for _, testCase := range store.state.Cases {
		if testCase.Revision != 1 {
			t.Fatalf("idempotent import created revision %d for %s", testCase.Revision, testCase.Key)
		}
	}
}

func TestImportUpdatesAnUneditedCaseButPreservesAUserRevisionAsConflict(t *testing.T) {
	t.Parallel()

	store := &memoryImportStore{}
	service := newImportService(t, store)
	original := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "original", "chat_sync", false))}}
	if _, err := service.Import(context.Background(), original); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}

	changed := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "bundle update", "chat_sync", false))}}
	result, err := service.Import(context.Background(), changed)
	if err != nil {
		t.Fatalf("update Import() error = %v", err)
	}
	if result.Updated != 1 || store.state.Cases[0].Revision != 2 || store.state.Cases[0].Name != "bundle update" || store.state.Sources[0].ImportedRevision != 2 {
		t.Fatalf("safe update result=%#v state=%#v", result, store.state)
	}

	userCase := store.state.Cases[0]
	userCase.EntityMeta, _ = userCase.EntityMeta.NextRevision(userCase.UpdatedAt.Add(time.Second))
	userCase.Name = "user customization"
	store.state.Cases[0] = userCase
	conflicting := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "second bundle update", "chat_sync", false))}}
	result, err = service.Import(context.Background(), conflicting)
	if err != nil {
		t.Fatalf("conflicting Import() error = %v", err)
	}
	if len(result.Conflicts) != 1 || result.Updated != 0 || store.state.Cases[0].Name != "user customization" || store.state.Cases[0].Revision != 3 || store.state.Sources[0].ImportedRevision != 2 {
		t.Fatalf("conflict result=%#v state=%#v", result, store.state)
	}
}

func TestImportWithUnchangedSemanticsDoesNotAdoptAUserRevision(t *testing.T) {
	t.Parallel()

	store := &memoryImportStore{}
	service := newImportService(t, store)
	bundle := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "imported", "chat_sync", false))}}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	baseline := store.state.Sources[0]

	userCase := store.state.Cases[0]
	userCase.EntityMeta, _ = userCase.EntityMeta.NextRevision(userCase.UpdatedAt.Add(time.Second))
	userCase.Name = "user customization"
	store.state.Cases[0] = userCase

	result, err := service.Import(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Unchanged != 1 || result.Updated != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("Import() result = %#v", result)
	}
	stored := store.state.Sources[0]
	if stored.ImportedRevision != baseline.ImportedRevision || stored.MaterializedSHA256 != baseline.MaterializedSHA256 {
		t.Fatalf("unchanged import adopted user revision: before=%#v after=%#v", baseline, stored)
	}
}

func TestImportPreflightsEverySourceAndRetiresRemovedCases(t *testing.T) {
	t.Parallel()

	store := &memoryImportStore{}
	service := newImportService(t, store)
	initial := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "one", "chat_sync", false))},
		"openai-chat/T002/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T002", "two", "chat_sync", false))},
	}
	if _, err := service.Import(context.Background(), initial); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}

	invalid := fstest.MapFS{
		"openai-chat/T003/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T003", "three", "chat_sync", false))},
		"openai-chat/T004/case.json": &fstest.MapFile{Data: []byte(`{"id":"T004","unknown":true}`)},
	}
	applyBefore := store.applyCalls
	if _, err := service.Import(context.Background(), invalid); err == nil {
		t.Fatal("Import() accepted an invalid source")
	}
	if store.applyCalls != applyBefore || len(store.state.Cases) != 2 {
		t.Fatalf("invalid preflight mutated store: calls=%d state=%#v", store.applyCalls, store.state)
	}

	remaining := fstest.MapFS{"openai-chat/T001/case.json": initial["openai-chat/T001/case.json"]}
	result, err := service.Import(context.Background(), remaining)
	if err != nil {
		t.Fatalf("retire Import() error = %v", err)
	}
	if result.Retired != 1 || len(store.state.Cases) != 2 {
		t.Fatalf("retire result=%#v state=%#v", result, store.state)
	}
	retired := sourceByKey(t, store.state.Sources, "openai-chat/T002")
	if retired.RetiredAt == nil {
		t.Fatal("removed source was not marked retired")
	}
}

func TestImportPreflightsDeferredLoadProfilesBeforeWritingCases(t *testing.T) {
	t.Parallel()

	store := &memoryImportStore{}
	service := newImportService(t, store)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json":    &fstest.MapFile{Data: []byte(legacyFixture("T001", "one", "chat_sync", false))},
		"kimi-k3/load-profile-32k.json": &fstest.MapFile{Data: []byte(`{"name":"broken","unexpected":true}`)},
	}

	if _, err := service.Import(context.Background(), bundle); err == nil {
		t.Fatal("Import() accepted a malformed deferred load profile")
	}
	if store.applyCalls != 0 || len(store.state.Cases) != 0 || len(store.state.Sources) != 0 {
		t.Fatalf("malformed deferred profile mutated store: calls=%d state=%#v", store.applyCalls, store.state)
	}
}

func TestImportFailsClosedWhenAStoredSourceHasNoCurrentCase(t *testing.T) {
	t.Parallel()

	store := &memoryImportStore{}
	service := newImportService(t, store)
	bundle := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "one", "chat_sync", false))}}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	retiredAt := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)
	store.state.Sources[0].RetiredAt = &retiredAt
	store.state.Cases = nil

	_, err := service.Import(context.Background(), fstest.MapFS{})
	if !errors.Is(err, ErrInconsistent) {
		t.Fatalf("Import() error = %v, want ErrInconsistent", err)
	}
}

func TestImportUsesStableIDsAcrossIndependentDatabases(t *testing.T) {
	t.Parallel()

	bundle := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(legacyFixture("T001", "stable", "chat_sync", false))}}
	stores := []*memoryImportStore{{}, {}}
	ids := make([]string, len(stores))
	for index, store := range stores {
		service := newImportService(t, store)
		if _, err := service.Import(context.Background(), bundle); err != nil {
			t.Fatalf("Import(%d) error = %v", index, err)
		}
		ids[index] = store.state.Cases[0].ID
		if !domain.IsUUID(ids[index]) {
			t.Fatalf("imported id %q is not a canonical UUID", ids[index])
		}
	}
	if ids[0] != ids[1] {
		t.Fatalf("stable IDs = %v", ids)
	}
	const expectedT001ID = "062b66ef-8d2e-59a1-95cb-dadf03dfe264"
	if ids[0] != expectedT001ID {
		t.Fatalf("stable T001 id = %q, want %q", ids[0], expectedT001ID)
	}
}

func TestImportConvertsTheCompleteRepositoryBundle(t *testing.T) {
	t.Parallel()

	casesDirectory := filepath.Join(testRepositoryRoot(t), "cases")
	service := newImportService(t, &memoryImportStore{})
	result, err := service.Import(context.Background(), os.DirFS(casesDirectory))
	if err != nil {
		t.Fatalf("Import(repository cases) error = %v", err)
	}
	if result.Discovered != 90 || result.Created != 90 || result.Runnable != 58 || result.Disabled != 26 || result.Manual != 6 || result.DeferredPlanTemplates != 1 {
		t.Fatalf("repository import result = %#v", result)
	}
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type memoryImportStore struct {
	state      State
	applyCalls int
}

func (store *memoryImportStore) LoadCaseImportState(context.Context, string) (State, error) {
	return store.state, nil
}

func (store *memoryImportStore) ApplyCaseImportBatch(_ context.Context, batch Batch) error {
	store.applyCalls++
	for _, change := range batch.Changes {
		if change.WriteEntity {
			replaced := false
			for index := range store.state.Cases {
				if store.state.Cases[index].ID == change.TestCase.ID {
					store.state.Cases[index] = change.TestCase
					replaced = true
				}
			}
			if !replaced {
				store.state.Cases = append(store.state.Cases, change.TestCase)
			}
		}
		replaced := false
		for index := range store.state.Sources {
			if store.state.Sources[index].Namespace == change.Source.Namespace && store.state.Sources[index].SourceKey == change.Source.SourceKey {
				store.state.Sources[index] = change.Source
				replaced = true
			}
		}
		if !replaced {
			store.state.Sources = append(store.state.Sources, change.Source)
		}
	}
	for _, retirement := range batch.Retirements {
		for index := range store.state.Sources {
			if store.state.Sources[index].Namespace == batch.Namespace && store.state.Sources[index].SourceKey == retirement.SourceKey {
				stamp := retirement.RetiredAt
				store.state.Sources[index].RetiredAt = &stamp
			}
		}
	}
	return nil
}

func newImportService(t *testing.T, store Store) *Service {
	t.Helper()
	service, err := New(Dependencies{Store: store, Clock: fixedClock{now: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func sourceByKey(t *testing.T, sources []SourceRecord, key string) SourceRecord {
	t.Helper()
	for _, source := range sources {
		if source.SourceKey == key {
			return source
		}
	}
	t.Fatalf("source %q not found", key)
	return SourceRecord{}
}

func legacyFixture(id, name, kind string, disabled bool) string {
	enabled := "true"
	if disabled {
		enabled = "false"
	}
	execution := "automatic"
	if kind == "manual_unknown" {
		execution = "manual"
	}
	return `{"schema_version":2,"key":"` + id + `","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","enabled":` + enabled + `,"default":false,"severity":"normal","execution_mode":"` + execution + `","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"` + kind + `","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}}}`
}

func kimiDisabledFixture() string {
	return `{"schema_version":2,"key":"must.multimodal_type_required","name":"disabled","dimension":"multimodal","protocol":"kimi-k3","enabled":false,"default":false,"severity":"critical","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"kimi_error_400","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[]}},"options":{}}}}`
}

var _ fs.FS = fstest.MapFS{}
