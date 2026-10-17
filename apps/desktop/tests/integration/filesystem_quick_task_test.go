package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
	workspacestore "github.com/894x/llm-test-studio/internal/persistence/workspace"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func filesystemQuickTaskFixture(t *testing.T) (workspacestore.Catalog, domain.Suite, casecatalog.Entry, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	caseRoot := filepath.Join(root, "cases")
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	testCase := filesystemCatalogTestCase(1)
	testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{"inputs":{"prompt":{"type":"string","default":"hi"}},"request":{"body":{"messages":[{"role":"user","content":{"$input":"prompt"}}]}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`)
	if err := cases.SaveCase(ctx, string(domain.ProtocolOpenAIChat), testCase.Key, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("cases=%+v, %v", caseEntries, err)
	}
	entry := caseEntries[0]
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	suite := filesystemCatalogSuiteFixture(entry.TestCase)
	suite.Inputs = []domain.SuiteInput{{Key: "prompt", Label: "Message", Input: testspec.Input{Type: "string", Default: json.RawMessage(`"hello"`)}, Bindings: []domain.SuiteInputBinding{{CaseID: entry.TestCase.ID, Input: "prompt"}}}}
	if err := suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	entries, err := suites.Entries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("suites=%+v, %v", entries, err)
	}
	return workspacestore.Catalog{LockPath: filepath.Join(root, "catalog.lock"), Cases: cases, Suites: suites}, entries[0].Suite, entry, filepath.Join(caseRoot, entry.Group, entry.Directory, "case.json")
}
