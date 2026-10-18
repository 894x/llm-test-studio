package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/overview"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

type recordingHistory struct {
	calls int
	ctx   context.Context
	after string
	ids   []string
}

func (query *recordingHistory) Page(ctx context.Context, after string) (workspace.HistoryPage, error) {
	query.calls++
	query.ctx, query.after = ctx, after
	return workspace.HistoryPage{SchemaVersion: 1}, nil
}
func (query *recordingHistory) Refresh(ctx context.Context, ids []string) (workspace.HistoryPage, error) {
	query.calls++
	query.ctx, query.ids = ctx, ids
	return workspace.HistoryPage{SchemaVersion: 1}, nil
}
func (query *recordingHistory) AvailableReports(ctx context.Context, ids []string) ([]string, error) {
	query.calls++
	query.ctx, query.ids = ctx, ids
	return ids, nil
}
func (query *recordingHistory) ComparisonHistory(ctx context.Context, after string, ids []string) (comparisons.HistoryPage, error) {
	query.calls++
	query.ctx, query.after, query.ids = ctx, after, ids
	return comparisons.HistoryPage{SchemaVersion: 1}, nil
}
func (query *recordingHistory) Snapshot(ctx context.Context) (overview.Snapshot, error) {
	query.calls++
	query.ctx = ctx
	return overview.Snapshot{SchemaVersion: 1}, nil
}

func TestBoundedHistoryBindingsValidateBeforeQueryAndUseLifecycle(t *testing.T) {
	query := &recordingHistory{}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{RunHistory: query, ComparisonHistory: query, Overview: query}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app.onStartup(ctx)
	defer app.shutdown()
	const id = "70000000-0000-4000-8000-000000000001"
	const cursor = "2026-01-01T00:00:00.000000000Z|run|" + id
	if _, err := app.GetRunHistory(cursor); err != nil || query.after != cursor {
		t.Fatalf("run page: %v", err)
	}
	document, _ := json.Marshal([]string{id})
	if _, err := app.GetRunSummaries(string(document)); err != nil || len(query.ids) != 1 {
		t.Fatalf("run refresh: %v", err)
	}
	if _, err := app.GetAvailableReports(string(document)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetComparisonHistory(cursor, string(document)); err != nil || query.after != cursor {
		t.Fatalf("comparison page: %v", err)
	}
	if _, err := app.GetOverview(); err != nil {
		t.Fatal(err)
	}
	calls := query.calls
	for _, invalid := range []string{"invalid", strings.Replace(cursor, "|run|", "|performance|", 1)} {
		if _, err := app.GetRunHistory(invalid); err == nil {
			t.Fatal("accepted invalid run cursor")
		}
		if _, err := app.GetComparisonHistory(invalid, "[]"); err == nil {
			t.Fatal("accepted invalid comparison cursor")
		}
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = id
	}
	oversized, _ := json.Marshal(tooMany)
	for _, invalid := range []string{`["bad-id"]`, `{}`, string(oversized), strings.Repeat(" ", 4097)} {
		if _, err := app.GetRunSummaries(invalid); err == nil {
			t.Fatal("accepted invalid run IDs")
		}
		if _, err := app.GetAvailableReports(invalid); err == nil {
			t.Fatal("accepted invalid report IDs")
		}
		if _, err := app.GetComparisonHistory("", invalid); err == nil {
			t.Fatal("accepted invalid comparison IDs")
		}
	}
	if query.calls != calls {
		t.Fatal("invalid request reached query")
	}
	cancel()
	if query.ctx.Err() != context.Canceled {
		t.Fatal("query lost lifecycle context")
	}
}
