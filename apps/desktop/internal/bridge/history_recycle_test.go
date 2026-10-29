package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
)

type recordingHistoryRecycle struct {
	calls   []string
	ctx     context.Context
	target  historyrecycle.Target
	after   string
	preview historyrecycle.Preview
	entry   historyrecycle.Entry
	page    historyrecycle.Page
	err     error
}

func (service *recordingHistoryRecycle) Preview(
	ctx context.Context,
	target historyrecycle.Target,
) (historyrecycle.Preview, error) {
	service.calls = append(service.calls, "preview")
	service.ctx, service.target = ctx, target
	return service.preview, service.err
}

func (service *recordingHistoryRecycle) Recycle(
	ctx context.Context,
	target historyrecycle.Target,
) (historyrecycle.Entry, error) {
	service.calls = append(service.calls, "recycle")
	service.ctx, service.target = ctx, target
	return service.entry, service.err
}

func (service *recordingHistoryRecycle) Restore(
	ctx context.Context,
	target historyrecycle.Target,
) (historyrecycle.Entry, error) {
	service.calls = append(service.calls, "restore")
	service.ctx, service.target = ctx, target
	return service.entry, service.err
}

func (service *recordingHistoryRecycle) Page(ctx context.Context, after string) (historyrecycle.Page, error) {
	service.calls = append(service.calls, "page")
	service.ctx, service.after = ctx, after
	return service.page, service.err
}

func newHistoryRecycleTestApp(t *testing.T, service HistoryRecycleService) *DesktopApp {
	t.Helper()
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{HistoryRecycle: service}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() {
		if err := app.shutdown(); err != nil {
			t.Error(err)
		}
	})
	return app
}

func historyRecycleBindingOperations(target historyrecycle.Target) []struct {
	name   string
	invoke func(*DesktopApp) error
} {
	return []struct {
		name   string
		invoke func(*DesktopApp) error
	}{
		{name: "page", invoke: func(app *DesktopApp) error {
			_, err := app.GetRecycledHistory("")
			return err
		}},
		{name: "preview", invoke: func(app *DesktopApp) error {
			_, err := app.PreviewHistoryRecycle(target)
			return err
		}},
		{name: "recycle", invoke: func(app *DesktopApp) error {
			_, err := app.RecycleHistory(target)
			return err
		}},
		{name: "restore", invoke: func(app *DesktopApp) error {
			_, err := app.RestoreHistory(target)
			return err
		}},
	}
}

func TestHistoryRecycleBindingsForwardDependencyInputsResultsAndLifecycle(t *testing.T) {
	target := historyrecycle.Target{Kind: "run", ID: "70000000-0000-4000-8000-000000000001"}
	entry := historyrecycle.Entry{
		Kind: target.Kind, ID: target.ID, Title: "saved run", RemovedAt: "2026-10-30T00:00:00Z",
		RunIDs: []string{target.ID}, ReportIDs: []string{"70000000-0000-4000-8000-000000000002"},
		ComparisonIDs: []string{}, JobIDs: []string{}, Remote: true,
	}
	cursor := entry.RemovedAt + "|" + target.Kind + "|" + target.ID
	service := &recordingHistoryRecycle{
		calls: []string{}, entry: entry,
		preview: historyrecycle.Preview{SchemaVersion: 1, Entry: entry, CanRecycle: true},
		page: historyrecycle.Page{SchemaVersion: 1, Entries: []historyrecycle.Entry{entry},
			NextCursor: cursor, HasMore: true},
	}
	var initializerContext context.Context
	app := New(func(ctx context.Context) (Dependencies, error) {
		initializerContext = ctx
		return Dependencies{HistoryRecycle: service}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app.onStartup(ctx)
	defer app.shutdown()
	if initializerContext == nil {
		t.Fatal("startup did not initialize the history recycle dependency")
	}
	if page, err := app.GetRecycledHistory(cursor); err != nil || !reflect.DeepEqual(page, service.page) {
		t.Fatalf("page = %+v, error = %v", page, err)
	}
	if service.after != cursor {
		t.Fatal("pagination cursor was not forwarded")
	}
	if preview, err := app.PreviewHistoryRecycle(target); err != nil || !reflect.DeepEqual(preview, service.preview) {
		t.Fatalf("preview = %+v, error = %v", preview, err)
	}
	if service.target != target {
		t.Fatal("preview target was not forwarded")
	}
	if result, err := app.RecycleHistory(target); err != nil || !reflect.DeepEqual(result, entry) {
		t.Fatalf("recycle = %+v, error = %v", result, err)
	}
	if service.target != target {
		t.Fatal("recycle target was not forwarded")
	}
	if result, err := app.RestoreHistory(target); err != nil || !reflect.DeepEqual(result, entry) {
		t.Fatalf("restore = %+v, error = %v", result, err)
	}
	if service.target != target {
		t.Fatal("restore target was not forwarded")
	}
	if !reflect.DeepEqual(service.calls, []string{"page", "preview", "recycle", "restore"}) {
		t.Fatalf("dependency calls = %v", service.calls)
	}
	if service.ctx != initializerContext {
		t.Fatal("binding did not use the initialized lifecycle context")
	}
	cancel()
	if !errors.Is(service.ctx.Err(), context.Canceled) {
		t.Fatal("history recycle service lost lifecycle cancellation")
	}
}

func TestHistoryRecycleBindingsSanitizeEveryServiceFailure(t *testing.T) {
	target := historyrecycle.Target{Kind: "run", ID: "70000000-0000-4000-8000-000000000001"}
	const sensitiveDetail = "private-database.sqlite provider-token=secret-test-value"
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "invalid", err: historyrecycle.ErrInvalid, code: "history_recycle_invalid"},
		{name: "not found", err: historyrecycle.ErrNotFound, code: "history_recycle_not_found"},
		{name: "active", err: historyrecycle.ErrActive, code: "history_recycle_active"},
		{name: "unconfirmed", err: historyrecycle.ErrUnconfirmed, code: "history_recycle_unconfirmed"},
		{name: "unavailable", err: ErrHistoryRecycleUnavailable, code: "history_recycle_unavailable"},
		{name: "unexpected", err: errors.New("database read failed"), code: "history_recycle_unavailable"},
	} {
		for _, operation := range historyRecycleBindingOperations(target) {
			t.Run(test.name+"/"+operation.name, func(t *testing.T) {
				internal := fmt.Errorf("%w: %s", test.err, sensitiveDetail)
				service := &recordingHistoryRecycle{calls: []string{}, err: internal}
				app := newHistoryRecycleTestApp(t, service)
				reported := []error{}
				app.setErrorReporter(func(err error) { reported = append(reported, err) })
				err := operation.invoke(app)
				assertBindingErrorCode(t, err, test.code)
				if err.Error() != test.code || errors.Is(err, internal) {
					t.Fatalf("binding leaked its internal error: %v", err)
				}
				raw, encodeErr := json.Marshal(err)
				if encodeErr != nil || strings.Contains(string(raw), sensitiveDetail) {
					t.Fatalf("binding error serialization leaked details: %s, %v", raw, encodeErr)
				}
				if len(reported) != 1 || !errors.Is(reported[0], internal) {
					t.Fatal("internal failure was not retained for diagnostics")
				}
			})
		}
	}
}

func TestHistoryRecycleBindingsRejectUnavailableDependenciesAndLifecycleStates(t *testing.T) {
	target := historyrecycle.Target{Kind: "run", ID: "70000000-0000-4000-8000-000000000001"}
	for _, state := range []string{"nil app", "not started", "stopped", "missing service", "typed nil service"} {
		t.Run(state, func(t *testing.T) {
			service := &recordingHistoryRecycle{calls: []string{}}
			var app *DesktopApp
			code := "history_recycle_unavailable"
			switch state {
			case "nil app":
				code = "desktop_not_started"
			case "not started":
				code = "desktop_not_started"
				app = New(func(context.Context) (Dependencies, error) {
					t.Fatal("a binding initialized the desktop before startup")
					return Dependencies{}, nil
				})
			case "stopped":
				code = "desktop_stopped"
				app = newHistoryRecycleTestApp(t, service)
				if err := app.shutdown(); err != nil {
					t.Fatal(err)
				}
			case "missing service":
				app = newHistoryRecycleTestApp(t, nil)
			case "typed nil service":
				var unavailable *recordingHistoryRecycle
				app = newHistoryRecycleTestApp(t, unavailable)
			}
			for _, operation := range historyRecycleBindingOperations(target) {
				assertBindingErrorCode(t, operation.invoke(app), code)
			}
			if len(service.calls) != 0 {
				t.Fatal("unavailable lifecycle reached the history recycle service")
			}
		})
	}
}

func TestHistoryRecycleBindingsEmitVisibilityChangesOnlyAfterSuccessfulMutations(t *testing.T) {
	target := historyrecycle.Target{Kind: "run", ID: "70000000-0000-4000-8000-000000000001"}
	service := &recordingHistoryRecycle{calls: []string{}}
	app := newHistoryRecycleTestApp(t, service)
	events := []string{}
	app.setEventEmitter(func(ctx context.Context, name string, data ...interface{}) {
		if name != "history-visibility-changed" || len(data) != 0 || ctx != service.ctx {
			t.Fatalf("invalid visibility event: %s, %v", name, data)
		}
		events = append(events, name)
	})
	for _, operation := range historyRecycleBindingOperations(target) {
		before := len(events)
		if err := operation.invoke(app); err != nil {
			t.Fatal(err)
		}
		want := before
		if operation.name == "recycle" || operation.name == "restore" {
			want++
		}
		if len(events) != want {
			t.Fatalf("successful %s emitted %d events, want %d", operation.name, len(events)-before, want-before)
		}
	}
	service.err = historyrecycle.ErrActive
	for _, operation := range historyRecycleBindingOperations(target) {
		before := len(events)
		assertBindingErrorCode(t, operation.invoke(app), "history_recycle_active")
		if len(events) != before {
			t.Fatalf("failed %s emitted a visibility change", operation.name)
		}
	}
}
