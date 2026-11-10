package workspace

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	builtinbundle "github.com/894x/llm-test-studio/data"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestSharedCatalogDiscoversFilesAndPreservesExistingPlanOverrides(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	files, err := fs.ReadDir(builtinbundle.Bundle, "plans")
	if err != nil {
		t.Fatal(err)
	}
	var id string
	var original []byte
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		id = file.Name()[:len(file.Name())-len(".json")]
		original, err = fs.ReadFile(builtinbundle.Bundle, "plans/"+file.Name())
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if id == "" {
		t.Fatal("no built-in plan discovered")
	}
	if err := os.Mkdir(filepath.Join(root, "plans"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plans", id+".json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenCatalog(root, CatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		plan, err := repository.GetPlan(ctx, id)
		if err != nil || plan.Builtin {
			t.Fatalf("existing user plan no longer wins: %#v, %v", plan, err)
		}
		plans, err := repository.ListPlans(ctx)
		if err != nil || len(plans) <= len(files) {
			t.Fatalf("default plans were not merged: %d, %v", len(plans), err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("opening the shared catalog modified a user plan: %v", err)
	}
}

func TestBuiltinKimiProRegressionPlanSchedulesEverySuiteCaseOnce(t *testing.T) {
	repository, err := OpenCatalog(t.TempDir(), CatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := repository.GetPlan(ctx, "28875399-9c8a-5358-b02c-ebceef9e6c0b")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Builtin || len(plan.Entries) != 2 {
		t.Fatalf("unexpected built-in regression plan: %+v", plan)
	}
	suites, err := repository.ListSuites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]domain.Suite, len(suites))
	for _, suite := range suites {
		byID[suite.ID] = suite
	}
	for _, entry := range plan.Entries {
		suite, found := byID[entry.TargetID]
		if entry.TargetKind != domain.PlanTargetSuite || !found || len(suite.Cases) < 2 {
			t.Fatalf("regression entry does not resolve to a multi-case suite: %+v", entry)
		}
		request := runs.ExecutionRequest{
			Entry: domain.RunEntrySnapshot{Load: entry.Load},
			Cases: make([]domain.TestCase, len(suite.Cases)),
		}
		profile := request.LoadProfile()
		if entry.Load.Mode != domain.LoadSingle || profile.RequestCount != uint64(len(suite.Cases)) {
			t.Fatalf("suite %s schedules %d requests for %d cases with mode %s",
				suite.Key, profile.RequestCount, len(suite.Cases), entry.Load.Mode)
		}
		// An explicit request budget retains its published meaning for load tests.
		request.Entry.Load.Mode = domain.LoadFixedConcurrency
		request.Entry.Load.RequestCount = 1
		if request.LoadProfile().RequestCount != 1 {
			t.Fatal("fixed-concurrency request budget was expanded into a suite pass")
		}
	}
}
