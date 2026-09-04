package suitecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCatalogMergesBuiltinAndUserSuitesAndResolvesCaseKeys(t *testing.T) {
	caseRoot := t.TempDir()
	cases, err := casecatalog.New(casecatalog.Options{
		Builtin: fstest.MapFS{
			"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseDocument("T001", "Case one", "gpt-5.2"))},
		},
		UserRoot: caseRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	suiteRoot := t.TempDir()
	userDirectory := filepath.Join(suiteRoot, "openai-chat", "gpt-5.2-smoke")
	if err := os.MkdirAll(userDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDirectory, "suite.json"), []byte(suiteDocument("gpt-5.2-smoke", "User override", "gpt-5.2", "T001")), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		Builtin: fstest.MapFS{
			"openai-chat/gpt-5.2-smoke/suite.json": &fstest.MapFile{Data: []byte(suiteDocument("gpt-5.2-smoke", "Built-in", "gpt-5.2", "T001"))},
		},
		UserRoot: suiteRoot,
		Cases:    cases,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].Source != SourceUser || first[0].Suite.Name != "User override" {
		t.Fatalf("merged suites = %#v / %#v", first, second)
	}
	if first[0].Suite.ID != second[0].Suite.ID || first[0].Suite.Revision == 0 || first[0].Suite.Revision != second[0].Suite.Revision || len(first[0].Suite.Cases) != 1 {
		t.Fatalf("resolved suite identity = %#v / %#v", first[0].Suite, second[0].Suite)
	}
	caseEntries, err := cases.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Suite.Cases[0] != (domain.CaseRevisionRef{CaseID: caseEntries[0].TestCase.ID, Revision: caseEntries[0].TestCase.Revision}) {
		t.Fatalf("resolved suite case = %#v, want %#v", first[0].Suite.Cases[0], caseEntries[0].TestCase)
	}
}

func TestSaveSuiteWritesShareableKeysAndReloadsTheFile(t *testing.T) {
	cases, err := casecatalog.New(casecatalog.Options{
		Builtin: fstest.MapFS{
			"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseDocument("T001", "Case one", "gpt-5.2"))},
		},
		UserRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	service, err := New(Options{Builtin: fstest.MapFS{}, UserRoot: root, Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	suite := domain.Suite{
		EntityMeta: domain.EntityMeta{ID: "10000000-0000-4000-8000-000000000001", SchemaVersion: 1, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp},
		Key:        "gpt-5.2-smoke", Name: "User suite", Protocol: domain.ProtocolOpenAIChat, ModelTarget: "gpt-5.2",
		Cases: []domain.CaseRevisionRef{{CaseID: caseEntries[0].TestCase.ID, Revision: caseEntries[0].TestCase.Revision}},
	}
	if err := service.SaveSuite(context.Background(), "openai-chat", "gpt-5.2-smoke", suite); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "openai-chat", "gpt-5.2-smoke", "suite.json"))
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"case_keys"`) || strings.Contains(string(raw), `"case_id"`) {
		t.Fatalf("suite file = %q, %v", raw, err)
	}
	entries, err := service.Entries(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Source != SourceUser || entries[0].Suite.Key != suite.Key || entries[0].Suite.ModelTarget != suite.ModelTarget {
		t.Fatalf("reloaded suites = %#v, %v", entries, err)
	}
}

func TestStoreRevisionRoundTripsTheExactSuiteWithoutChangingTheActiveCatalog(t *testing.T) {
	ctx := context.Background()
	service, current := newSuiteRevisionCatalog(t, ctx)
	historical := current
	historical.Revision = current.Revision + 7
	historical.Name = "historical pinned suite"
	historical.UpdatedAt = historical.CreatedAt.Add(time.Minute)
	historical.Cases = []domain.CaseRevisionRef{
		{CaseID: "20000000-0000-4000-8000-000000000001", Revision: 41},
		{CaseID: "20000000-0000-4000-8000-000000000002", Revision: 3},
	}
	if err := historical.Validate(); err != nil {
		t.Fatalf("historical Suite fixture is invalid: %v", err)
	}

	if err := service.StoreRevision(ctx, historical); err != nil {
		t.Fatalf("StoreRevision() error = %v", err)
	}
	if err := service.StoreRevision(ctx, historical); err != nil {
		t.Fatalf("StoreRevision(idempotent retry) error = %v", err)
	}
	stored, err := service.FindRevision(ctx, historical.ID, historical.Revision)
	if err != nil {
		t.Fatalf("FindRevision() error = %v", err)
	}
	if !reflect.DeepEqual(stored.Suite, historical) {
		t.Fatalf("stored historical Suite = %#v, want exact %#v", stored.Suite, historical)
	}

	active, err := service.Find(ctx, current.ID)
	if err != nil || !reflect.DeepEqual(active.Suite, current) {
		t.Fatalf("active Suite changed = %#v, %v", active.Suite, err)
	}
	entries, err := service.Entries(ctx)
	if err != nil || len(entries) != 1 || !reflect.DeepEqual(entries[0].Suite, current) {
		t.Fatalf("Entries() enumerated a sidecar or changed the active Suite: %#v, %v", entries, err)
	}
}

func TestStoreRevisionRejectsDifferentSuiteContentAtTheSameExactRevision(t *testing.T) {
	ctx := context.Background()
	service, current := newSuiteRevisionCatalog(t, ctx)
	historical := current
	historical.Revision = current.Revision + 1
	historical.Name = "first historical payload"
	if err := service.StoreRevision(ctx, historical); err != nil {
		t.Fatalf("StoreRevision() error = %v", err)
	}

	conflict := historical
	conflict.Name = "different payload under the same revision"
	if err := service.StoreRevision(ctx, conflict); !errors.Is(err, ErrCollision) {
		t.Fatalf("StoreRevision(conflict) error = %v, want ErrCollision", err)
	}
	stored, err := service.FindRevision(ctx, historical.ID, historical.Revision)
	if err != nil || !reflect.DeepEqual(stored.Suite, historical) {
		t.Fatalf("conflicting store changed the historical Suite: %#v, %v", stored.Suite, err)
	}

	currentConflict := current
	currentConflict.Name = "different active payload under the active revision"
	if err := service.StoreRevision(ctx, currentConflict); !errors.Is(err, ErrCollision) {
		t.Fatalf("StoreRevision(active conflict) error = %v, want ErrCollision", err)
	}
}

func TestStoreRevisionMaterializesTheCurrentRevisionBeforeAnExternalEdit(t *testing.T) {
	ctx := context.Background()
	service, pinned := newSuiteRevisionCatalog(t, ctx)
	if err := service.StoreRevision(ctx, pinned); err != nil {
		t.Fatalf("StoreRevision(current) error = %v", err)
	}

	// Simulate a user editing the shareable suite.json without going through
	// Save, which cannot archive the previous active document for us.
	target := filepath.Join(service.userRoot, "openai-chat", "history", "suite.json")
	if err := os.WriteFile(target, []byte(suiteDocument("history", "externally edited suite", "gpt-5.2", "T001")), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := service.Find(ctx, pinned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Suite.Revision == pinned.Revision {
		t.Fatal("external edit did not produce a distinct active Suite revision")
	}
	exact, err := service.FindRevision(ctx, pinned.ID, pinned.Revision)
	if err != nil {
		t.Fatalf("FindRevision(pinned after external edit) error = %v", err)
	}
	if !reflect.DeepEqual(exact.Suite, pinned) {
		t.Fatalf("FindRevision() = %#v, want exact pinned %#v", exact.Suite, pinned)
	}
}

func TestStoreRevisionDoesNotOrderContentHashRevisionsNumerically(t *testing.T) {
	ctx := context.Background()
	service, current := newSuiteRevisionCatalog(t, ctx)
	if current.Revision == 1 {
		t.Fatal("fixture active revision unexpectedly equals the lower exact revision")
	}
	fixtures := []domain.Suite{current, current}
	fixtures[0].Revision = 1
	fixtures[0].Name = "numerically lower content hash"
	fixtures[1].Revision = current.Revision + 1
	fixtures[1].Name = "numerically higher content hash"

	for _, fixture := range fixtures {
		if err := service.StoreRevision(ctx, fixture); err != nil {
			t.Fatalf("StoreRevision(%d) error = %v", fixture.Revision, err)
		}
		stored, err := service.FindRevision(ctx, fixture.ID, fixture.Revision)
		if err != nil || !reflect.DeepEqual(stored.Suite, fixture) {
			t.Fatalf("FindRevision(%d) = %#v, %v; want exact %#v", fixture.Revision, stored.Suite, err, fixture)
		}
	}
}

func TestFindRevisionRequiresAnExistingExactRevision(t *testing.T) {
	ctx := context.Background()
	service, current := newSuiteRevisionCatalog(t, ctx)

	if _, err := service.FindRevision(ctx, current.ID, current.Revision+1); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("FindRevision(missing) error = %v, want fs.ErrNotExist", err)
	}
	if _, err := service.FindRevision(ctx, current.ID, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("FindRevision(zero) error = %v, want ErrInvalid", err)
	}
}

func TestSavePreservesThePreviousExactSuiteRevision(t *testing.T) {
	ctx := context.Background()
	service, first := newSuiteRevisionCatalog(t, ctx)

	if err := service.Save(ctx, "openai-chat", "history", []byte(suiteDocument("history", "second active suite", "gpt-5.2", "T001"))); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	current, err := service.Find(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Suite.Revision == first.Revision || current.Suite.Name != "second active suite" {
		t.Fatalf("current Suite = %#v, want the second active document", current.Suite)
	}
	pinned, err := service.FindRevision(ctx, first.ID, first.Revision)
	if err != nil {
		t.Fatalf("FindRevision(previous) error = %v", err)
	}
	if !reflect.DeepEqual(pinned.Suite, first) {
		t.Fatalf("previous Suite = %#v, want exact %#v", pinned.Suite, first)
	}
}

func newSuiteRevisionCatalog(t *testing.T, ctx context.Context) (*Service, domain.Suite) {
	t.Helper()
	cases, err := casecatalog.New(casecatalog.Options{
		Builtin: fstest.MapFS{
			"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseDocument("T001", "Case one", "gpt-5.2"))},
		},
		UserRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{Builtin: fstest.MapFS{}, UserRoot: t.TempDir(), Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Save(ctx, "openai-chat", "history", []byte(suiteDocument("history", "current active suite", "gpt-5.2", "T001"))); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries() = %#v, %v", entries, err)
	}
	return service, entries[0].Suite
}

func caseDocument(key, name, modelTarget string) string {
	return `{"schema_version":2,"key":"` + key + `","name":"` + name + `","dimension":"compatibility","protocol":"openai-chat","model_targets":["` + modelTarget + `"],"enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"request.single","type_version":1,"spec":{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}}}`
}

func suiteDocument(key, name, modelTarget, caseKey string) string {
	return `{"schema_version":1,"key":"` + key + `","name":"` + name + `","protocol":"openai-chat","model_target":"` + modelTarget + `","case_keys":["` + caseKey + `"]}`
}
