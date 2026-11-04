package plancatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/data/performanceplans"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestBuiltinPerformancePlansShareTheCatalogWithoutWritingUserFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	service, err := New(root, WithBuiltin(performanceplans.Bundle))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plans, err := service.ListDocuments(ctx)
	if err != nil || len(plans) != 21 {
		t.Fatalf("built-in plans = %d, %v; want 7 per text protocol", len(plans), err)
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
	if err != nil || len(all) != 22 {
		t.Fatalf("merged plans = %d, %v", len(all), err)
	}
}

func TestBuiltinInputTokenLaddersPrepareEveryPerformanceEntry(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "plans"), WithBuiltin(performanceplans.Bundle))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base, err := service.Get(ctx, "45fd3d2c-71e3-54c2-95d3-60b94386d7b8")
	if err != nil {
		t.Fatal(err)
	}
	expected := *base.Entries[0].Performance
	expected.Concurrency = 0
	expected.RandomSeed = 0
	expected.RampRequestCap = 0
	expected.CapacityStart = 0
	expected.CapacityStep = 0
	inputs := []uint32{1000, 2000, 4000, 8000, 16000, 32000, 64000, 128000, 256000, 512000}
	counts := []uint64{300, 240, 180, 120, 80, 50, 30, 20, 10, 5}
	rates := []float64{15, 12, 9, 6, 4, 2.5, 1.5, 1, 0.5, 0.25}
	plans := map[domain.Protocol]string{
		"openai-chat":        "2f98dba5-67ee-5bbc-95b5-0bcdd8c75d0a",
		"openai-responses":   "2c30cbba-8d0e-5bed-a240-7fc6963417f9",
		"anthropic-messages": "daac8735-ce54-5aec-b739-2e3aa2bbe54c",
	}
	for protocol, id := range plans {
		t.Run(string(protocol), func(t *testing.T) {
			plan, err := service.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Protocol != protocol || len(plan.Entries) != len(inputs) {
				t.Fatalf("unexpected ladder plan: %#v", plan)
			}
			var measured, warmup uint64
			for index, entry := range plan.Entries {
				expected.InputTokens = inputs[index]
				expected.RequestCount = counts[index]
				expected.RatePerSecond = rates[index]
				if entry.Performance == nil || *entry.Performance != expected {
					t.Fatalf("entry %d profile = %#v, want %#v", index, entry.Performance, expected)
				}
				if entry.OnFailure != "continue" {
					t.Fatalf("entry %d does not preserve continuation after failure", index)
				}
				_, err := quicktest.PreparePlanEntry(entry.EntryID, domain.ChannelSnapshot{
					Protocol: protocol, BaseURL: "https://example.test", UpstreamModelName: "test-model",
				}, *entry.Performance)
				if err != nil {
					t.Fatalf("prepare entry %d: %v", index, err)
				}
				measured += entry.Performance.RequestCount
				warmup += entry.Performance.WarmupRequests
			}
			if measured != 1035 || warmup != 100 {
				t.Fatalf("request budget = %d measured + %d warmup", measured, warmup)
			}
		})
	}
}
