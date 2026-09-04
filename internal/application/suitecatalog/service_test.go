package suitecatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func caseDocument(key, name, modelTarget string) string {
	return `{"schema_version":2,"key":"` + key + `","name":"` + name + `","dimension":"compatibility","protocol":"openai-chat","model_targets":["` + modelTarget + `"],"enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"request.single","type_version":1,"spec":{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}}}`
}

func suiteDocument(key, name, modelTarget, caseKey string) string {
	return `{"schema_version":1,"key":"` + key + `","name":"` + name + `","protocol":"openai-chat","model_target":"` + modelTarget + `","case_keys":["` + caseKey + `"]}`
}
