package plancatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/data/performanceplans"
)

func TestBuiltinPerformancePlansShareTheCatalogWithoutWritingUserFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	service, err := New(root, WithBuiltin(performanceplans.Bundle))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plans, err := service.ListDocuments(ctx)
	if err != nil || len(plans) != 18 {
		t.Fatalf("built-in plans = %d, %v; want 6 per text protocol", len(plans), err)
	}
	for _, plan := range plans {
		if !plan.Builtin || plan.Entries[0].Performance == nil || plan.Validate() != nil {
			t.Fatalf("invalid built-in plan: %#v", plan)
		}
		got, err := service.Get(ctx, plan.ID)
		if err != nil || !got.Builtin || got.Entries[0].Performance.RequestCount != plan.Entries[0].Performance.RequestCount {
			t.Fatalf("Get() = %#v, %v", got, err)
		}
		got.Entries[0].Performance.RequestCount++
		again, _ := service.Get(ctx, plan.ID)
		if again.Entries[0].Performance.RequestCount != plan.Entries[0].Performance.RequestCount {
			t.Fatal("built-in reads share mutable parameters")
		}
		updated := cloneDocument(plan)
		updated.Revision++
		updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
		if err := service.UpdateDocument(ctx, 1, updated); !errors.Is(err, ErrInvalid) {
			t.Fatalf("updating built-in plan = %v", err)
		}
		if err := service.Delete(ctx, plan.ID, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("deleting built-in plan = %v", err)
		}
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading built-ins wrote user data: %v", err)
	}
	custom := validTargetedDocument("60000000-0000-4000-8000-000000000001", time.Now().UTC())
	custom.Entries = plans[0].Entries
	custom.Entries[0].Performance = plans[0].Entries[0].Performance
	if err := service.CreateDocument(ctx, custom); err != nil {
		t.Fatal(err)
	}
	all, err := service.ListDocuments(ctx)
	if err != nil || len(all) != 19 {
		t.Fatalf("merged plans = %d, %v", len(all), err)
	}
}
