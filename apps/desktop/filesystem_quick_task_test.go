package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func filesystemQuickTaskFixture(t *testing.T) (filesystemCatalogRepository, domain.Suite, casecatalog.Entry, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	caseRoot := filepath.Join(root, "cases")
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	testCase := filesystemCatalogTestCase(1)
	testCase.Definition.Spec = json.RawMessage(`{"inputs":{"prompt":{"type":"string","default":"hi"}},"request":{"body":{"messages":[{"role":"user","content":{"$input":"prompt"}}]}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`)
	if err := cases.SaveCase(ctx, string(testCase.Protocol), testCase.Key, testCase); err != nil {
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
	return filesystemCatalogRepository{lockPath: filepath.Join(root, "catalog.lock"), cases: cases, suites: suites}, entries[0].Suite, entry, filepath.Join(caseRoot, entry.Group, entry.Directory, "case.json")
}

type failQuickTaskSaveCatalog struct{ *suitecatalog.Service }

func (failQuickTaskSaveCatalog) SaveSuite(context.Context, string, string, domain.Suite) error {
	return fs.ErrPermission
}

func TestQuickTaskMetadataWriteFailureIsNotReportedAsSuccess(t *testing.T) {
	repository, original, _, _ := filesystemQuickTaskFixture(t)
	repository.suites = failQuickTaskSaveCatalog{repository.suites.(*suitecatalog.Service)}
	changed := original
	changed.Description = "changed description"
	if err := repository.UpdateSuite(context.Background(), original.Revision, changed); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("metadata-only save failure = %v; want permission error", err)
	}
	stored, err := repository.GetSuite(context.Background(), original.ID)
	if err != nil || stored.Description != original.Description {
		t.Fatalf("failed save changed task: %+v, %v", stored, err)
	}
}

func TestCaseUpdateDoesNotCascadeIntoSuite(t *testing.T) {
	for _, scenario := range []string{"disable", "manual", "remove field", "change type"} {
		t.Run(scenario, func(t *testing.T) {
			repository, _, entry, casePath := filesystemQuickTaskFixture(t)
			originalBytes, err := os.ReadFile(casePath)
			if err != nil {
				t.Fatal(err)
			}
			changed := entry.TestCase
			changed.Revision++
			switch scenario {
			case "disable":
				changed.Enabled = false
			case "manual":
				changed.ExecutionMode = domain.CaseExecutionManual
			case "remove field", "change type":
				var spec map[string]any
				if err := json.Unmarshal(changed.Definition.Spec, &spec); err != nil {
					t.Fatal(err)
				}
				message := spec["request"].(map[string]any)["body"].(map[string]any)["messages"].([]any)[0].(map[string]any)
				if scenario == "remove field" {
					delete(message, "content")
				} else {
					message["content"] = 42
				}
				changed.Definition.Spec, err = json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}

			}
			if err := repository.UpdateTestCase(context.Background(), entry.TestCase.Revision, changed); err != nil {
				t.Fatalf("incompatible update = %v; want ErrInvalid", err)
			}
			currentBytes, err := os.ReadFile(casePath)
			if err != nil || bytes.Equal(currentBytes, originalBytes) {
				t.Fatal("case update did not persist")
			}
			if _, err := repository.ListSuites(context.Background()); err != nil {
				t.Fatalf("catalog no longer readable: %v", err)
			}
		})
	}
}

func TestCompatibleCaseUpdateKeepsQuickTaskHistoryReadable(t *testing.T) {
	repository, original, entry, _ := filesystemQuickTaskFixture(t)
	changed := entry.TestCase
	changed.Revision++
	changed.Name = "Renamed Case"
	if err := repository.UpdateTestCase(context.Background(), entry.TestCase.Revision, changed); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetSuite(context.Background(), original.ID)
	if err != nil || current.Revision != original.Revision {
		t.Fatalf("current task not updated: %+v, %v", current, err)
	}
	if current.Cases[0] != original.Cases[0] {
		t.Fatal("case rename changed suite reference")
	}
}
