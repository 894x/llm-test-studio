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
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
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
	if err := cases.SaveCase(ctx, string(testCase.Protocol), testCase.Key, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("cases=%+v, %v", caseEntries, err)
	}
	entry := caseEntries[0]
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites"), Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	suite := filesystemCatalogSuiteFixture(entry.TestCase)
	suite.ModelTarget = ""
	suite.QuickTest = &domain.SuiteQuickTest{Description: "Connection", TimeoutMS: 30000, Inputs: []domain.SuiteInput{{
		Key: "prompt", Label: "Message", Type: "text", Default: json.RawMessage(`"hello"`),
		Bindings: []domain.SuiteInputBinding{{CaseKey: testCase.Key, Pointer: "/request/body/messages/0/content"}},
	}}}
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
	changed.QuickTest = original.QuickTest.Clone()
	changed.QuickTest.TimeoutMS++
	if err := repository.UpdateSuite(context.Background(), original.Revision, changed); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("metadata-only save failure = %v; want permission error", err)
	}
	stored, err := repository.GetSuite(context.Background(), original.ID)
	if err != nil || stored.QuickTest.TimeoutMS != original.QuickTest.TimeoutMS {
		t.Fatalf("failed save changed task: %+v, %v", stored, err)
	}
}

func TestCaseUpdateCannotBreakQuickTaskCatalog(t *testing.T) {
	for _, scenario := range []string{"disable", "manual", "remove field", "change type", "scope model"} {
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
			case "scope model":
				changed.ModelTargets = []string{"gpt-test"}
			}
			if err := repository.UpdateTestCase(context.Background(), entry.TestCase.Revision, changed); !errors.Is(err, catalog.ErrInvalid) {
				t.Fatalf("incompatible update = %v; want ErrInvalid", err)
			}
			currentBytes, err := os.ReadFile(casePath)
			if err != nil || !bytes.Equal(currentBytes, originalBytes) {
				t.Fatal("rejected update changed Case bytes")
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
	if err != nil || current.Revision == original.Revision {
		t.Fatalf("current task not updated: %+v, %v", current, err)
	}
	pinned, err := repository.GetSuiteRevision(context.Background(), original.ID, original.Revision)
	if err != nil || pinned.Cases[0] != original.Cases[0] {
		t.Fatalf("pinned task lost: %+v, %v", pinned, err)
	}
}
