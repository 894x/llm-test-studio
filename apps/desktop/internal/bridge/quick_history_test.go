package bridge

import (
	"context"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quickhistory"
)

type recordingQuickHistory struct {
	calls int
	ctx   context.Context
	after string
}

func (query *recordingQuickHistory) Page(ctx context.Context, after string) (quickhistory.Page, error) {
	query.calls++
	query.ctx, query.after = ctx, after
	return quickhistory.Page{SchemaVersion: 1}, nil
}

func TestQuickHistoryBindingUsesIndependentQueryAndLifecycle(t *testing.T) {
	query := &recordingQuickHistory{}
	app := New(func(context.Context) (Dependencies, error) { return Dependencies{QuickHistory: query}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.onStartup(ctx)
	defer app.shutdown()
	after := "2026-01-01T00:00:00.000000000Z|run|70000000-0000-4000-8000-000000000001"
	page, err := app.GetQuickTestHistory(after)
	if err != nil || page.SchemaVersion != 1 {
		t.Fatalf("page: %+v, %v", page, err)
	}
	if query.calls != 1 || query.after != after {
		t.Fatal("pagination query was not forwarded")
	}
	if _, err := app.GetQuickTestHistory("invalid"); err == nil {
		t.Fatal("accepted malformed cursor")
	}
	if query.calls != 1 {
		t.Fatal("invalid cursor reached repository")
	}
	cancel()
	if query.ctx.Err() != context.Canceled {
		t.Fatal("query did not use lifecycle context")
	}
}
