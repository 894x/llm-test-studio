package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/caseimport"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestCaseImportStorePersistsAnIdempotentImportAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
		"openai-chat/T002/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T002", "two"))},
	}
	service := newSQLiteImportService(t, repository)

	first, err := service.Import(context.Background(), bundle)
	if err != nil {
		t.Fatalf("first Import() error = %v", err)
	}
	if first.Created != 2 || first.Updated != 0 || first.Unchanged != 0 {
		t.Fatalf("first Import() result = %#v", first)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}
	if len(state.Cases) != 2 || len(state.Sources) != 2 {
		t.Fatalf("stored state = %#v", state)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	repository = openExistingCaseImportRepository(t, path)
	defer repository.Close()
	service = newSQLiteImportService(t, repository)
	second, err := service.Import(context.Background(), bundle)
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if second.Created != 0 || second.Updated != 0 || second.Unchanged != 2 {
		t.Fatalf("second Import() result = %#v", second)
	}
	state, err = repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() after reopen error = %v", err)
	}
	for _, testCase := range state.Cases {
		if testCase.Revision != 1 {
			t.Fatalf("test case %s revision = %d, want 1", testCase.Key, testCase.Revision)
		}
	}
}

func TestCaseImportStoreUpdatesSourceBytesWithoutAdoptingAUserRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-user-revision.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	originalJSON := caseImportLegacyFixture("T001", "imported")
	original := fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(originalJSON)}}
	if _, err := service.Import(context.Background(), original); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	baseline, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}

	userCase := baseline.Cases[0]
	userCase.EntityMeta, err = userCase.EntityMeta.NextRevision(userCase.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	userCase.Name = "user customization"
	if err := repository.UpdateTestCase(context.Background(), 1, userCase); err != nil {
		t.Fatalf("UpdateTestCase() error = %v", err)
	}

	reformatted := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte("\n  " + originalJSON + "\n")},
	}
	result, err := service.Import(context.Background(), reformatted)
	if err != nil {
		t.Fatalf("Import(reformatted) error = %v", err)
	}
	if result.Unchanged != 1 || result.Updated != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("Import(reformatted) result = %#v", result)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() after import error = %v", err)
	}
	if state.Cases[0].Revision != 2 || state.Cases[0].Name != "user customization" {
		t.Fatalf("current user case = %#v", state.Cases[0])
	}
	if state.Sources[0].ImportedRevision != baseline.Sources[0].ImportedRevision ||
		state.Sources[0].MaterializedSHA256 != baseline.Sources[0].MaterializedSHA256 ||
		state.Sources[0].SourceBytesSHA256 == baseline.Sources[0].SourceBytesSHA256 ||
		strings.TrimSpace(state.Sources[0].SourcePath) != state.Sources[0].SourcePath {
		t.Fatalf("updated source = %#v, baseline = %#v", state.Sources[0], baseline.Sources[0])
	}
}

func TestCaseImportStoreRollsBackTheWholeBatchWhenEntityCASFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-entity-cas.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
		"openai-chat/T002/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T002", "two"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	stale, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}

	caseByKey := make(map[string]int, len(stale.Cases))
	for index, testCase := range stale.Cases {
		caseByKey[testCase.Key] = index
	}
	userCase := stale.Cases[caseByKey["T002"]]
	userCase.EntityMeta, err = userCase.EntityMeta.NextRevision(userCase.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NextRevision(user) error = %v", err)
	}
	userCase.Name = "user edit won the race"
	if err := repository.UpdateTestCase(context.Background(), 1, userCase); err != nil {
		t.Fatalf("UpdateTestCase(user) error = %v", err)
	}

	batch := caseimport.Batch{Namespace: caseimport.Namespace}
	for _, source := range stale.Sources {
		current := stale.Cases[caseByKey[strings.TrimPrefix(source.SourceKey, "openai-chat/")]]
		updated := current
		updated.EntityMeta, err = current.EntityMeta.NextRevision(current.UpdatedAt.Add(2 * time.Second))
		if err != nil {
			t.Fatalf("NextRevision(import) error = %v", err)
		}
		updated.Name += " bundle update"
		materialized, hashErr := caseimport.MaterializedSHA256(updated)
		if hashErr != nil {
			t.Fatalf("MaterializedSHA256() error = %v", hashErr)
		}
		nextSource := source
		nextSource.SourceBytesSHA256 = strings.Repeat("a", 64)
		nextSource.SemanticSHA256 = strings.Repeat("b", 64)
		nextSource.MaterializedSHA256 = materialized
		nextSource.ImportedRevision = updated.Revision
		nextSource.BundleManifestSHA256 = strings.Repeat("c", 64)
		nextSource.ImportedAt = source.ImportedAt.Add(2 * time.Second)
		batch.Changes = append(batch.Changes, caseimport.Change{
			Source:                  nextSource,
			TestCase:                updated,
			WriteEntity:             true,
			ExpectedCurrentRevision: current.Revision,
			ExpectedRecord: &caseimport.RecordExpectation{
				SourceBytesSHA256: source.SourceBytesSHA256,
				ImportedRevision:  source.ImportedRevision,
			},
		})
	}

	err = repository.ApplyCaseImportBatch(context.Background(), batch)
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("ApplyCaseImportBatch() error = %v, want ErrConflict", err)
	}
	first, err := repository.GetTestCase(context.Background(), stale.Cases[caseByKey["T001"]].ID)
	if err != nil {
		t.Fatalf("GetTestCase(T001) error = %v", err)
	}
	if first.Revision != 1 || first.Name != "one" {
		t.Fatalf("first case escaped rolled-back batch: %#v", first)
	}
	second, err := repository.GetTestCase(context.Background(), userCase.ID)
	if err != nil {
		t.Fatalf("GetTestCase(T002) error = %v", err)
	}
	if second.Revision != 2 || second.Name != "user edit won the race" {
		t.Fatalf("user case was overwritten: %#v", second)
	}
}

func TestCaseImportStoreRollsBackTheWholeBatchWhenSourceCASFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-source-cas.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
		"openai-chat/T002/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T002", "two"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}
	caseByID := make(map[string]int, len(state.Cases))
	for index, testCase := range state.Cases {
		caseByID[testCase.ID] = index
	}

	batch := caseimport.Batch{Namespace: caseimport.Namespace}
	for index, source := range state.Sources {
		updatedSource := source
		updatedSource.SourceBytesSHA256 = strings.Repeat(string(rune('d'+index)), 64)
		updatedSource.BundleManifestSHA256 = strings.Repeat("f", 64)
		expectedHash := source.SourceBytesSHA256
		if index == 1 {
			expectedHash = strings.Repeat("0", 64)
		}
		batch.Changes = append(batch.Changes, caseimport.Change{
			Source:                  updatedSource,
			TestCase:                state.Cases[caseByID[source.EntityID]],
			ExpectedCurrentRevision: state.Cases[caseByID[source.EntityID]].Revision,
			ExpectedRecord: &caseimport.RecordExpectation{
				SourceBytesSHA256: expectedHash,
				ImportedRevision:  source.ImportedRevision,
			},
		})
	}

	err = repository.ApplyCaseImportBatch(context.Background(), batch)
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("ApplyCaseImportBatch() error = %v, want ErrConflict", err)
	}
	got, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() after rollback error = %v", err)
	}
	for index := range got.Sources {
		if got.Sources[index].SourceBytesSHA256 != state.Sources[index].SourceBytesSHA256 {
			t.Fatalf("source %s escaped rolled-back batch", got.Sources[index].SourceKey)
		}
	}
}

func TestCaseImportStoreFailsClosedWhenImportedMaterializedHashIsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-corrupt-hash.db")
	repository := openCaseImportRepository(t, path)
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		repository.Close()
		t.Fatalf("seed Import() error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db := openDatabase(t, path)
	if _, err := db.Exec(`UPDATE test_case_import_sources SET materialized_sha256 = ?`, strings.Repeat("0", 64)); err != nil {
		db.Close()
		t.Fatalf("corrupt materialized hash: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close corruption fixture: %v", err)
	}

	repository, err := persistence.OpenLegacyCatalogRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenLegacyCatalogRepository() error = %v", err)
	}
	defer repository.Close()
	if _, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("LoadCaseImportState() error = %v, want ErrCorrupt", err)
	}
}

func TestCaseImportStoreFailsClosedWhenImportedRevisionIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-missing-revision.db")
	repository := openCaseImportRepository(t, path)
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		repository.Close()
		t.Fatalf("seed Import() error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db := openDatabase(t, path)
	if _, err := db.Exec(`UPDATE test_case_import_sources SET imported_revision = 99`); err != nil {
		db.Close()
		t.Fatalf("corrupt imported revision: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close corruption fixture: %v", err)
	}

	repository, err := persistence.OpenLegacyCatalogRepository(context.Background(), path, persistence.RepositoryOptions{})
	if repository != nil {
		_ = repository.Close()
	}
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("OpenLegacyCatalogRepository() error = %v, want ErrCorrupt", err)
	}
}

func TestCaseImportStoreRetiresAndReactivatesSourcesWithoutDeletingCases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-retirement.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	firstFile := &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))}
	secondFile := &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T002", "two"))}
	all := fstest.MapFS{
		"openai-chat/T001/case.json": firstFile,
		"openai-chat/T002/case.json": secondFile,
	}
	if _, err := service.Import(context.Background(), all); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}

	result, err := service.Import(context.Background(), fstest.MapFS{"openai-chat/T001/case.json": firstFile})
	if err != nil {
		t.Fatalf("retiring Import() error = %v", err)
	}
	if result.Retired != 1 {
		t.Fatalf("retiring Import() result = %#v", result)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState(retired) error = %v", err)
	}
	if len(state.Cases) != 2 || state.Sources[1].RetiredAt == nil || state.Sources[1].ImportedRevision != 1 {
		t.Fatalf("retired state = %#v", state)
	}

	result, err = service.Import(context.Background(), all)
	if err != nil {
		t.Fatalf("reactivating Import() error = %v", err)
	}
	if result.Updated != 0 || result.Unchanged != 2 {
		t.Fatalf("reactivating Import() result = %#v", result)
	}
	state, err = repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState(reactivated) error = %v", err)
	}
	if state.Sources[1].RetiredAt != nil || state.Cases[1].Revision != 1 {
		t.Fatalf("reactivated state = %#v", state)
	}
}

func TestCaseImportStoreHonorsCancelledContexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-context.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := repository.LoadCaseImportState(ctx, caseimport.Namespace); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadCaseImportState(cancelled) error = %v, want context.Canceled", err)
	}
	if err := repository.ApplyCaseImportBatch(ctx, caseimport.Batch{Namespace: caseimport.Namespace}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyCaseImportBatch(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestCaseImportStoreRefusesToAdvanceAHashCorruptedImportBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-corrupt-baseline.db")
	repository := openCaseImportRepository(t, path)
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		repository.Close()
		t.Fatalf("seed Import() error = %v", err)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		repository.Close()
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db := openDatabase(t, path)
	var document []byte
	if err := db.QueryRow(`SELECT document_json FROM test_cases WHERE id = ? AND revision = 1`, state.Cases[0].ID).Scan(&document); err != nil {
		db.Close()
		t.Fatalf("read baseline document: %v", err)
	}
	tampered := state.Cases[0]
	tampered.Name = "tampered without a revision"
	document, err = json.Marshal(tampered)
	if err != nil {
		db.Close()
		t.Fatalf("encode tampered document: %v", err)
	}
	if _, err := db.Exec(`UPDATE test_cases SET document_json = ? WHERE id = ? AND revision = 1`, document, tampered.ID); err != nil {
		db.Close()
		t.Fatalf("tamper baseline document: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close corruption fixture: %v", err)
	}

	repository = openExistingCaseImportRepository(t, path)
	defer repository.Close()
	updated := state.Cases[0]
	updated.EntityMeta, err = updated.EntityMeta.NextRevision(updated.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	updated.Name = "bundle update"
	materialized, err := caseimport.MaterializedSHA256(updated)
	if err != nil {
		t.Fatalf("MaterializedSHA256() error = %v", err)
	}
	nextSource := state.Sources[0]
	nextSource.SourceBytesSHA256 = strings.Repeat("a", 64)
	nextSource.SemanticSHA256 = strings.Repeat("b", 64)
	nextSource.MaterializedSHA256 = materialized
	nextSource.ImportedRevision = 2
	nextSource.BundleManifestSHA256 = strings.Repeat("c", 64)
	nextSource.ImportedAt = nextSource.ImportedAt.Add(time.Second)
	err = repository.ApplyCaseImportBatch(context.Background(), caseimport.Batch{
		Namespace: caseimport.Namespace,
		Changes: []caseimport.Change{{
			Source:                  nextSource,
			TestCase:                updated,
			WriteEntity:             true,
			ExpectedCurrentRevision: 1,
			ExpectedRecord: &caseimport.RecordExpectation{
				SourceBytesSHA256: state.Sources[0].SourceBytesSHA256,
				ImportedRevision:  1,
			},
		}},
	})
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("ApplyCaseImportBatch() error = %v, want ErrCorrupt", err)
	}
}

func TestCaseImportStoreLoadsUntrackedCurrentCasesForIdentityCollisionChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case-import-current-catalog.db")
	repository := openCaseImportRepository(t, path)
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "one"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}
	userCase := state.Cases[0]
	userCase.EntityMeta = domain.EntityMeta{
		ID:            "ffffffff-ffff-4fff-8fff-ffffffffffff",
		SchemaVersion: domain.CurrentEntitySchemaVersion,
		Revision:      1,
		CreatedAt:     userCase.CreatedAt.Add(time.Minute),
		UpdatedAt:     userCase.CreatedAt.Add(time.Minute),
	}
	userCase.Key = "USER1"
	userCase.Name = "user-created case"
	if err := repository.CreateTestCase(context.Background(), userCase); err != nil {
		t.Fatalf("CreateTestCase(user) error = %v", err)
	}

	state, err = repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() after user case error = %v", err)
	}
	if len(state.Sources) != 1 || len(state.Cases) != 2 || state.Cases[1].ID != userCase.ID {
		t.Fatalf("loaded state = %#v", state)
	}
}

type caseImportClock struct{ now time.Time }

func (clock caseImportClock) Now() time.Time { return clock.now }

func newSQLiteImportService(t *testing.T, store caseimport.Store) *caseimport.Service {
	t.Helper()
	service, err := caseimport.New(caseimport.Dependencies{
		Store: store,
		Clock: caseImportClock{now: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("caseimport.New() error = %v", err)
	}
	return service
}

func openCaseImportRepository(t *testing.T, path string) *persistence.Repository {
	t.Helper()
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "case-import-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return openExistingCaseImportRepository(t, path)
}

func openExistingCaseImportRepository(t *testing.T, path string) *persistence.Repository {
	t.Helper()
	repository, err := persistence.OpenLegacyCatalogRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenLegacyCatalogRepository() error = %v", err)
	}
	return repository
}

func caseImportLegacyFixture(id, name string) string {
	return `{"schema_version":2,"key":"` + id + `","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}}}`
}

var _ caseimport.Store = (*persistence.Repository)(nil)
