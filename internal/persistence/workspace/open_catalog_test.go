package workspace

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	builtinbundle "github.com/894x/llm-test-studio/data"
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
