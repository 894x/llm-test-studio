package bridge

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

type recordingWorkspaceQuery struct {
	snapshot workspace.Snapshot
	err      error
	calls    int
	ctx      context.Context
	before   func() error
}

func (query *recordingWorkspaceQuery) Snapshot(ctx context.Context) (workspace.Snapshot, error) {
	query.calls++
	query.ctx = ctx
	if query.before != nil {
		if err := query.before(); err != nil {
			return workspace.Snapshot{}, err
		}
	}
	return query.snapshot, query.err
}

type recordingCatalogQuery struct {
	snapshot catalog.Snapshot
	err      error
	calls    int
	ctx      context.Context
}

func (query *recordingCatalogQuery) Snapshot(ctx context.Context, _ catalog.Scope) (catalog.Snapshot, error) {
	query.calls++
	query.ctx = ctx
	return query.snapshot, query.err
}

type recordingReportingQuery struct {
	snapshot reporting.Snapshot
	detail   reporting.Detail
	exported reporting.ExportedDocument
	err      error
	calls    int
	ctx      context.Context
	locale   string
}

type recordingCatalogCommands struct {
	calls []string
	err   error
}

func (commands *recordingCatalogCommands) record(name string) (catalog.MutationResult, error) {
	commands.calls = append(commands.calls, name)
	return catalog.MutationResult{ID: "11111111-1111-4111-8111-111111111111", Revision: 1}, commands.err
}

func (commands *recordingCatalogCommands) CreateModel(context.Context, catalog.CreateModelCommand) (catalog.MutationResult, error) {
	return commands.record("create_model")
}

func (commands *recordingCatalogCommands) UpdateModel(context.Context, catalog.UpdateModelCommand) (catalog.MutationResult, error) {
	return commands.record("update_model")
}

func (commands *recordingCatalogCommands) DeleteModel(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_model")
	return err
}

func (commands *recordingCatalogCommands) CreateChannel(context.Context, catalog.CreateChannelCommand) (catalog.MutationResult, error) {
	return commands.record("create_channel")
}

func (commands *recordingCatalogCommands) UpdateChannel(context.Context, catalog.UpdateChannelCommand) (catalog.MutationResult, error) {
	return commands.record("update_channel")
}

func (commands *recordingCatalogCommands) DeleteChannel(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_channel")
	return err
}

func (commands *recordingCatalogCommands) CreateChannelModel(context.Context, catalog.CreateChannelModelCommand) (catalog.MutationResult, error) {
	return commands.record("create_channel_model")
}

func (commands *recordingCatalogCommands) UpdateChannelModel(context.Context, catalog.UpdateChannelModelCommand) (catalog.MutationResult, error) {
	return commands.record("update_channel_model")
}

func (commands *recordingCatalogCommands) DeleteChannelModel(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_channel_model")
	return err
}

func (commands *recordingCatalogCommands) CreateTestCase(context.Context, catalog.CreateTestCaseCommand) (catalog.MutationResult, error) {
	return commands.record("create_test_case")
}

func (commands *recordingCatalogCommands) UpdateTestCase(context.Context, catalog.UpdateTestCaseCommand) (catalog.MutationResult, error) {
	return commands.record("update_test_case")
}

func (commands *recordingCatalogCommands) DeleteTestCase(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_test_case")
	return err
}

func (commands *recordingCatalogCommands) CreateSuite(context.Context, catalog.CreateSuiteCommand) (catalog.MutationResult, error) {
	return commands.record("create_suite")
}

func (commands *recordingCatalogCommands) UpdateSuite(context.Context, catalog.UpdateSuiteCommand) (catalog.MutationResult, error) {
	return commands.record("update_suite")
}

func (commands *recordingCatalogCommands) DeleteSuite(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_suite")
	return err
}

func (commands *recordingCatalogCommands) CreatePlan(context.Context, catalog.CreatePlanCommand) (catalog.MutationResult, error) {
	return commands.record("create_plan")
}

func (commands *recordingCatalogCommands) UpdatePlan(context.Context, catalog.UpdatePlanCommand) (catalog.MutationResult, error) {
	return commands.record("update_plan")
}

func (commands *recordingCatalogCommands) DeletePlan(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_plan")
	return err
}

func (query *recordingReportingQuery) Snapshot(ctx context.Context) (reporting.Snapshot, error) {
	query.calls++
	query.ctx = ctx
	return query.snapshot, query.err
}

func (query *recordingReportingQuery) Detail(ctx context.Context, _ string) (reporting.Detail, error) {
	query.calls++
	query.ctx = ctx
	return query.detail, query.err
}

func (query *recordingReportingQuery) Export(ctx context.Context, _ string, _ reporting.ExportFormat, _ string) (reporting.ExportedDocument, error) {
	query.calls++
	query.ctx = ctx
	return query.exported, query.err
}

func (query *recordingReportingQuery) ExportLocalized(ctx context.Context, _ string, _ reporting.ExportFormat, _ string, locale string) (reporting.ExportedDocument, error) {
	query.calls++
	query.ctx = ctx
	query.locale = locale
	return query.exported, query.err
}

func TestDesktopAppExposesStartupInitializationFailure(t *testing.T) {
	initializationFailure := errors.New("cannot open application database")
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, initializationFailure
	})
	var reported error
	app.setErrorReporter(func(err error) { reported = err })

	app.onStartup(context.Background())
	if !errors.Is(reported, initializationFailure) {
		t.Fatalf("startup error was not reported immediately: %v", reported)
	}
	_, err := app.GetWorkspace()

	assertBindingErrorCode(t, err, "desktop_startup_failed")
	if !errors.Is(reported, initializationFailure) {
		t.Fatalf("locally reported error = %v, want startup failure", reported)
	}
}

func TestDesktopAppExposesSafeDiagnosticsStatusAndOpensOwnedDirectory(t *testing.T) {
	app := New(nil)
	want := DesktopDiagnosticsSnapshot{
		SchemaVersion: 1, Available: true, Format: "jsonl",
		MaxFileBytes: 10 << 20, BackupFiles: 5,
		RunCorrelation: true, RequestCorrelation: true,
	}
	opened := 0
	app.setDiagnostics(want, func() error {
		opened++
		return nil
	})

	if got := app.GetDiagnostics(); got != want {
		t.Fatalf("GetDiagnostics() = %#v, want %#v", got, want)
	}
	if err := app.OpenDiagnosticsDirectory(); err != nil {
		t.Fatalf("OpenDiagnosticsDirectory() error = %v", err)
	}
	if opened != 1 {
		t.Fatalf("diagnostics directory open calls = %d, want 1", opened)
	}
}

func TestDesktopAppRejectsUnavailableDiagnosticsDirectory(t *testing.T) {
	app := New(nil)
	app.setDiagnostics(DesktopDiagnosticsSnapshot{SchemaVersion: 1}, nil)

	err := app.OpenDiagnosticsDirectory()

	assertBindingErrorCode(t, err, "diagnostics_unavailable")
}

func TestDesktopAppGetWorkspaceDelegatesToApplicationQuery(t *testing.T) {
	want := workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}
	query := &recordingWorkspaceQuery{snapshot: want}
	app := NewDesktopApp(query, nil)
	startupContext := context.WithValue(context.Background(), struct{}{}, "desktop")
	app.onStartup(startupContext)

	got, err := app.GetWorkspace()

	if err != nil {
		t.Fatalf("GetWorkspace() error = %v", err)
	}
	if got.SchemaVersion != want.SchemaVersion {
		t.Fatalf("GetWorkspace() schema version = %d, want %d", got.SchemaVersion, want.SchemaVersion)
	}
	if query.calls != 1 {
		t.Fatalf("workspace query calls = %d, want 1", query.calls)
	}
	if query.ctx == nil || query.ctx.Value(struct{}{}) != "desktop" {
		t.Fatal("workspace query did not receive the desktop lifecycle context")
	}
}

func TestDesktopAppCatalogAndReportsDelegateWithLifecycleContext(t *testing.T) {
	catalogQuery := &recordingCatalogQuery{snapshot: catalog.Snapshot{SchemaVersion: catalog.CurrentSnapshotSchemaVersion}}
	reportingQuery := &recordingReportingQuery{snapshot: reporting.Snapshot{SchemaVersion: reporting.CurrentSchemaVersion}}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{
			Catalog: catalogQuery,
			Reports: reportingQuery,
		}, nil
	})
	type contextKey struct{}
	startupContext := context.WithValue(context.Background(), contextKey{}, "desktop")
	app.onStartup(startupContext)

	gotCatalog, err := app.GetCatalog(catalog.ScopeAll)
	if err != nil {
		t.Fatalf("GetCatalog() error = %v", err)
	}
	gotReports, err := app.GetReports()
	if err != nil {
		t.Fatalf("GetReports() error = %v", err)
	}

	if gotCatalog.SchemaVersion != catalog.CurrentSnapshotSchemaVersion {
		t.Fatalf("GetCatalog() schema version = %d, want %d", gotCatalog.SchemaVersion, catalog.CurrentSnapshotSchemaVersion)
	}
	if gotReports.SchemaVersion != reporting.CurrentSchemaVersion {
		t.Fatalf("GetReports() schema version = %d, want %d", gotReports.SchemaVersion, reporting.CurrentSchemaVersion)
	}
	if catalogQuery.calls != 1 || reportingQuery.calls != 1 {
		t.Fatalf("query calls = catalog %d, reports %d; want 1 each", catalogQuery.calls, reportingQuery.calls)
	}
	if catalogQuery.ctx == nil || catalogQuery.ctx.Value(contextKey{}) != "desktop" {
		t.Fatal("catalog query did not receive the desktop lifecycle context")
	}
	if reportingQuery.ctx == nil || reportingQuery.ctx.Value(contextKey{}) != "desktop" {
		t.Fatal("reporting query did not receive the desktop lifecycle context")
	}
}

func TestDesktopAppReadsAndExportsCompleteReports(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	query := &recordingReportingQuery{
		detail:   reporting.Detail{SchemaVersion: reporting.CurrentSchemaVersion},
		exported: reporting.ExportedDocument{Filename: "report.json", MediaType: "application/json", DataBase64: "e30="},
	}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{Reports: query}, nil
	})
	app.onStartup(context.Background())

	detail, err := app.GetReportDetail(id)
	if err != nil || detail.SchemaVersion != reporting.CurrentSchemaVersion {
		t.Fatalf("GetReportDetail() = %#v, %v", detail, err)
	}
	exported, err := app.ExportReport(id, "json", reporting.DefaultWatermark, "en-US")
	if err != nil || exported.Filename != "report.json" || exported.DataBase64 != "e30=" {
		t.Fatalf("ExportReport() = %#v, %v", exported, err)
	}
	if query.calls != 2 {
		t.Fatalf("report document calls = %d, want 2", query.calls)
	}
	if query.locale != "en-US" {
		t.Fatalf("report export locale = %q, want en-US", query.locale)
	}
	if _, err := app.ExportReport(id, "exe", reporting.DefaultWatermark, "en-US"); !errors.As(err, new(DesktopBindingError)) {
		t.Fatalf("invalid export error = %v", err)
	}
}

func TestDesktopAppCatalogCommandsDelegateAndReturnAuthoritativeCatalog(t *testing.T) {
	query := &recordingCatalogQuery{snapshot: catalog.Snapshot{SchemaVersion: catalog.CurrentSnapshotSchemaVersion}}
	commands := &recordingCatalogCommands{}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{Catalog: query, CatalogCommands: commands}, nil
	})
	app.onStartup(context.Background())

	tests := []struct {
		name   string
		invoke func(*DesktopApp) (catalog.Snapshot, error)
	}{
		{name: "create_model", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.CreateModel(catalog.CreateModelCommand{}) }},
		{name: "update_model", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.UpdateModel(catalog.UpdateModelCommand{}) }},
		{name: "delete_model", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.DeleteModel(catalog.DeleteCommand{}) }},
		{name: "create_channel", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.CreateChannel(catalog.CreateChannelCommand{})
		}},
		{name: "update_channel", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.UpdateChannel(catalog.UpdateChannelCommand{})
		}},
		{name: "delete_channel", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.DeleteChannel(catalog.DeleteCommand{}) }},
		{name: "create_channel_model", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.CreateChannelModel(catalog.CreateChannelModelCommand{})
		}},
		{name: "update_channel_model", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.UpdateChannelModel(catalog.UpdateChannelModelCommand{})
		}},
		{name: "delete_channel_model", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.DeleteChannelModel(catalog.DeleteCommand{})
		}},
		{name: "create_test_case", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.CreateTestCase(catalog.CreateTestCaseCommand{})
		}},
		{name: "update_test_case", invoke: func(app *DesktopApp) (catalog.Snapshot, error) {
			return app.UpdateTestCase(catalog.UpdateTestCaseCommand{})
		}},
		{name: "delete_test_case", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.DeleteTestCase(catalog.DeleteCommand{}) }},
		{name: "create_suite", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.CreateSuite(catalog.CreateSuiteCommand{}) }},
		{name: "update_suite", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.UpdateSuite(catalog.UpdateSuiteCommand{}) }},
		{name: "delete_suite", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.DeleteSuite(catalog.DeleteCommand{}) }},
		{name: "create_plan", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.CreatePlan(catalog.CreatePlanCommand{}) }},
		{name: "update_plan", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.UpdatePlan(catalog.UpdatePlanCommand{}) }},
		{name: "delete_plan", invoke: func(app *DesktopApp) (catalog.Snapshot, error) { return app.DeletePlan(catalog.DeleteCommand{}) }},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.invoke(app)
			if err != nil {
				t.Fatalf("catalog command error = %v", err)
			}
			if got.SchemaVersion != catalog.CurrentSnapshotSchemaVersion {
				t.Fatalf("catalog schema version = %d", got.SchemaVersion)
			}
			if commands.calls[index] != test.name {
				t.Fatalf("catalog command call = %q, want %q", commands.calls[index], test.name)
			}
		})
	}
	if query.calls != len(tests) {
		t.Fatalf("catalog snapshot calls = %d, want %d", query.calls, len(tests))
	}
}

type recordingComparisons struct {
	snapshot comparisons.Snapshot
	ctx      context.Context
}

func (service *recordingComparisons) Snapshot(ctx context.Context) (comparisons.Snapshot, error) {
	service.ctx = ctx
	return service.snapshot, nil
}

func TestDesktopComparisonsUsesReadOnlyLifecycleQuery(t *testing.T) {
	service := &recordingComparisons{
		snapshot: comparisons.Snapshot{SchemaVersion: comparisons.CurrentSnapshotSchemaVersion},
	}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{Comparisons: service}, nil
	})
	type contextKey struct{}
	app.onStartup(context.WithValue(context.Background(), contextKey{}, "desktop"))

	got, err := app.GetComparisons()
	if err != nil || got.SchemaVersion != comparisons.CurrentSnapshotSchemaVersion {
		t.Fatalf("GetComparisons() = %+v, %v", got, err)
	}
	if service.ctx == nil || service.ctx.Value(contextKey{}) != "desktop" {
		t.Fatal("comparison query did not receive the desktop lifecycle context")
	}
}

func TestDesktopAppCatalogCommandErrorsKeepStablePublicMeaning(t *testing.T) {
	tests := []struct {
		name    string
		failure error
		code    string
	}{
		{name: "invalid", failure: catalog.ErrInvalid, code: "catalog_invalid"},
		{name: "conflict", failure: catalog.ErrConflict, code: "catalog_revision_conflict"},
		{name: "not found", failure: catalog.ErrNotFound, code: "catalog_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &recordingCatalogQuery{snapshot: catalog.Snapshot{SchemaVersion: catalog.CurrentSnapshotSchemaVersion}}
			commands := &recordingCatalogCommands{err: test.failure}
			app := New(func(context.Context) (Dependencies, error) {
				return Dependencies{Catalog: query, CatalogCommands: commands}, nil
			})
			var reported error
			app.setErrorReporter(func(err error) { reported = err })
			app.onStartup(context.Background())
			_, err := app.CreateModel(catalog.CreateModelCommand{})
			assertBindingErrorCode(t, err, test.code)
			var diagnostic catalogCommandDiagnosticError
			if !errors.As(reported, &diagnostic) || diagnostic.operation != "create model" || !errors.Is(reported, test.failure) {
				t.Fatalf("reported catalog diagnostic = %#v, want create model wrapping %v", reported, test.failure)
			}
			if query.calls != 0 {
				t.Fatalf("catalog query calls = %d, want 0", query.calls)
			}
		})
	}
}

func TestDesktopAppCatalogCommandReportsCommittedRefreshFailureWithoutRepeatingMutation(t *testing.T) {
	refreshFailure := errors.New("catalog file became temporarily unreadable")
	query := &recordingCatalogQuery{err: refreshFailure}
	commands := &recordingCatalogCommands{}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{Catalog: query, CatalogCommands: commands}, nil
	})
	var reported error
	app.setErrorReporter(func(err error) { reported = err })
	app.onStartup(context.Background())

	_, err := app.CreateModel(catalog.CreateModelCommand{})

	assertBindingErrorCode(t, err, desktopCodeCatalogSavedRefreshFailed)
	if len(commands.calls) != 1 || commands.calls[0] != "create_model" {
		t.Fatalf("catalog mutation calls = %v, want one create_model", commands.calls)
	}
	if query.calls != 1 {
		t.Fatalf("catalog snapshot calls = %d, want 1", query.calls)
	}
	var diagnostic catalogCommandDiagnosticError
	if !errors.As(reported, &diagnostic) || diagnostic.operation != "create model" {
		t.Fatalf("reported catalog diagnostic = %#v, want create model refresh diagnostic", reported)
	}
	if !errors.Is(reported, ErrCatalogSavedRefreshFailed) || !errors.Is(reported, refreshFailure) {
		t.Fatalf("reported catalog diagnostic = %v, want committed marker and refresh cause", reported)
	}
}

func TestDesktopAppRejectsCallsBeforeStartup(t *testing.T) {
	query := &recordingWorkspaceQuery{}
	app := NewDesktopApp(query, nil)

	_, err := app.GetWorkspace()
	assertBindingErrorCode(t, err, "desktop_not_started")
	if query.calls != 0 {
		t.Fatal("call before startup reached an application dependency")
	}
}

func TestDesktopAppWaitsForStartupInProgressBeforeServingBindings(t *testing.T) {
	want := workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}
	startupEntered := make(chan struct{})
	allowStartup := make(chan struct{})
	app := New(func(context.Context) (Dependencies, error) {
		close(startupEntered)
		<-allowStartup
		return Dependencies{Query: &recordingWorkspaceQuery{snapshot: want}}, nil
	})

	startupDone := make(chan struct{})
	go func() {
		app.onStartup(context.Background())
		close(startupDone)
	}()
	<-startupEntered

	type bindingResult struct {
		snapshot workspace.Snapshot
		err      error
	}
	result := make(chan bindingResult, 1)
	go func() {
		snapshot, err := app.GetWorkspace()
		result <- bindingResult{snapshot: snapshot, err: err}
	}()

	select {
	case early := <-result:
		close(allowStartup)
		<-startupDone
		t.Fatalf("binding completed before startup: snapshot=%+v error=%v", early.snapshot, early.err)
	case <-time.After(100 * time.Millisecond):
	}

	close(allowStartup)
	<-startupDone
	got := <-result
	if got.err != nil {
		t.Fatalf("GetWorkspace() error = %v", got.err)
	}
	if got.snapshot.SchemaVersion != want.SchemaVersion {
		t.Fatalf("schema version = %q, want %q", got.snapshot.SchemaVersion, want.SchemaVersion)
	}
}

func TestDesktopAppCatalogAndReportsRejectCallsBeforeStartup(t *testing.T) {
	catalogQuery := &recordingCatalogQuery{}
	reportingQuery := &recordingReportingQuery{}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{Catalog: catalogQuery, Reports: reportingQuery}, nil
	})

	_, err := app.GetCatalog(catalog.ScopeAll)
	assertBindingErrorCode(t, err, "desktop_not_started")
	_, err = app.GetReports()
	assertBindingErrorCode(t, err, "desktop_not_started")
	if catalogQuery.calls != 0 || reportingQuery.calls != 0 {
		t.Fatalf("calls before startup reached queries: catalog=%d reports=%d", catalogQuery.calls, reportingQuery.calls)
	}
}

func TestDesktopAppTreatsTypedNilDependenciesAsUnavailable(t *testing.T) {
	t.Run("workspace query", func(t *testing.T) {
		var query *recordingWorkspaceQuery
		app := NewDesktopApp(query, nil)
		app.onStartup(context.Background())

		_, err := app.GetWorkspace()
		assertBindingErrorCode(t, err, "workspace_unavailable")
	})

	t.Run("saved quick task service", func(t *testing.T) {
		var service *recordingSavedQuickTaskService
		app := NewDesktopApp(nil, service)
		app.onStartup(context.Background())

		_, err := app.GetQuickTask("11111111-1111-4111-8111-111111111111")
		assertBindingErrorCode(t, err, desktopCodeCommandsMissing)
	})

	t.Run("catalog query", func(t *testing.T) {
		var query *recordingCatalogQuery
		app := New(func(context.Context) (Dependencies, error) {
			return Dependencies{Catalog: query}, nil
		})
		app.onStartup(context.Background())

		_, err := app.GetCatalog(catalog.ScopeAll)
		assertBindingErrorCode(t, err, "catalog_unavailable")
	})

	t.Run("reporting query", func(t *testing.T) {
		var query *recordingReportingQuery
		app := New(func(context.Context) (Dependencies, error) {
			return Dependencies{Reports: query}, nil
		})
		app.onStartup(context.Background())

		_, err := app.GetReports()
		assertBindingErrorCode(t, err, "reports_unavailable")
	})
}

func TestDesktopAppShutdownIsIdempotent(t *testing.T) {
	closeFailure := errors.New("close failed")
	closeCalls := 0
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{
			Query: &recordingWorkspaceQuery{},
			Close: func() error {
				closeCalls++
				return closeFailure
			},
		}, nil
	})
	app.onStartup(context.Background())

	first := app.shutdown()
	second := app.shutdown()

	if !errors.Is(first, closeFailure) || !errors.Is(second, closeFailure) {
		t.Fatalf("shutdown errors = (%v, %v), want close failure", first, second)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	_, err := app.GetWorkspace()
	assertBindingErrorCode(t, err, "desktop_stopped")
}

func TestDesktopAppShutdownBeforeStartupPreventsInitialization(t *testing.T) {
	initializeCalls := 0
	app := New(func(context.Context) (Dependencies, error) {
		initializeCalls++
		return Dependencies{Query: &recordingWorkspaceQuery{}}, nil
	})

	if err := app.shutdown(); err != nil {
		t.Fatalf("shutdown before startup error = %v", err)
	}
	app.onStartup(context.Background())

	if initializeCalls != 0 {
		t.Fatalf("initializer calls = %d, want 0", initializeCalls)
	}
	_, err := app.GetWorkspace()
	assertBindingErrorCode(t, err, "desktop_stopped")
}

type cancelableWorkspaceQuery struct {
	started  chan struct{}
	returned atomic.Bool
}

type cancelableCatalogQuery struct {
	started  chan struct{}
	returned atomic.Bool
}

func (query *cancelableCatalogQuery) Snapshot(ctx context.Context, _ catalog.Scope) (catalog.Snapshot, error) {
	close(query.started)
	<-ctx.Done()
	query.returned.Store(true)
	return catalog.Snapshot{}, ctx.Err()
}

type cancelableReportingQuery struct {
	started  chan struct{}
	returned atomic.Bool
}

func (query *cancelableReportingQuery) Snapshot(ctx context.Context) (reporting.Snapshot, error) {
	close(query.started)
	<-ctx.Done()
	query.returned.Store(true)
	return reporting.Snapshot{}, ctx.Err()
}

func (query *cancelableWorkspaceQuery) Snapshot(ctx context.Context) (workspace.Snapshot, error) {
	close(query.started)
	<-ctx.Done()
	query.returned.Store(true)
	return workspace.Snapshot{}, ctx.Err()
}

func TestDesktopAppShutdownCancelsAndDrainsActiveQueriesBeforeClose(t *testing.T) {
	query := &cancelableWorkspaceQuery{started: make(chan struct{})}
	closeCalls := 0
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{
			Query: query,
			Close: func() error {
				closeCalls++
				if !query.returned.Load() {
					return errors.New("resource closed before active query returned")
				}
				return nil
			},
		}, nil
	})
	app.onStartup(context.Background())
	queryResult := make(chan error, 1)
	go func() {
		_, err := app.GetWorkspace()
		queryResult <- err
	}()

	select {
	case <-query.started:
	case <-time.After(2 * time.Second):
		t.Fatal("workspace query did not start")
	}
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- app.shutdown() }()

	select {
	case err := <-queryResult:
		assertBindingErrorCode(t, err, "operation_cancelled")
	case <-time.After(2 * time.Second):
		t.Fatal("active workspace query was not canceled")
	}
	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not drain active query")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
}

func TestDesktopAppShutdownCancelsAndDrainsCatalogAndReportQueriesBeforeClose(t *testing.T) {
	catalogQuery := &cancelableCatalogQuery{started: make(chan struct{})}
	reportingQuery := &cancelableReportingQuery{started: make(chan struct{})}
	closeCalls := 0
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{
			Catalog: catalogQuery,
			Reports: reportingQuery,
			Close: func() error {
				closeCalls++
				if !catalogQuery.returned.Load() || !reportingQuery.returned.Load() {
					return errors.New("resource closed before catalog and report queries returned")
				}
				return nil
			},
		}, nil
	})
	app.onStartup(context.Background())
	catalogResult := make(chan error, 1)
	reportingResult := make(chan error, 1)
	go func() {
		_, err := app.GetCatalog(catalog.ScopeAll)
		catalogResult <- err
	}()
	go func() {
		_, err := app.GetReports()
		reportingResult <- err
	}()

	for name, started := range map[string]<-chan struct{}{
		"catalog": catalogQuery.started,
		"reports": reportingQuery.started,
	} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s query did not start", name)
		}
	}
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- app.shutdown() }()

	for name, result := range map[string]<-chan error{
		"catalog": catalogResult,
		"reports": reportingResult,
	} {
		select {
		case err := <-result:
			assertBindingErrorCode(t, err, "operation_cancelled")
		case <-time.After(2 * time.Second):
			t.Fatalf("active %s query was not canceled", name)
		}
	}
	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not drain active catalog and report queries")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
}

func assertBindingErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("binding error = nil, want code %q", want)
	}
	var bindingError DesktopBindingError
	if !errors.As(err, &bindingError) {
		t.Fatalf("binding error type = %T, want DesktopBindingError", err)
	}
	if bindingError.Code != want {
		t.Fatalf("binding error code = %q, want %q", bindingError.Code, want)
	}
}

func TestDesktopAppBindingErrorsAreStableAndDoNotLeakSensitiveDetails(t *testing.T) {
	const secret = "https://provider.example/v1 api-key=sk-sensitive-value"
	sensitiveFailure := errors.New(secret)
	tests := []struct {
		name   string
		app    func() *DesktopApp
		invoke func(*DesktopApp) error
	}{
		{
			name: "startup",
			app: func() *DesktopApp {
				return New(func(context.Context) (Dependencies, error) {
					return Dependencies{}, sensitiveFailure
				})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetWorkspace()
				return err
			},
		},
		{
			name: "workspace query",
			app: func() *DesktopApp {
				return NewDesktopApp(&recordingWorkspaceQuery{err: sensitiveFailure}, nil)
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetWorkspace()
				return err
			},
		},
		{
			name: "catalog query",
			app: func() *DesktopApp {
				return New(func(context.Context) (Dependencies, error) {
					return Dependencies{Catalog: &recordingCatalogQuery{err: sensitiveFailure}}, nil
				})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetCatalog(catalog.ScopeAll)
				return err
			},
		},
		{
			name: "reporting query",
			app: func() *DesktopApp {
				return New(func(context.Context) (Dependencies, error) {
					return Dependencies{Reports: &recordingReportingQuery{err: sensitiveFailure}}, nil
				})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetReports()
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := test.app()
			var reported error
			app.setErrorReporter(func(err error) { reported = err })
			app.onStartup(context.Background())

			err := test.invoke(app)

			if err == nil {
				t.Fatal("binding error = nil")
			}
			if strings.Contains(err.Error(), "provider.example") || strings.Contains(err.Error(), "sk-sensitive-value") {
				t.Fatalf("binding error leaked sensitive detail: %q", err)
			}
			var bindingError DesktopBindingError
			if !errors.As(err, &bindingError) {
				t.Fatalf("binding error type = %T, want DesktopBindingError", err)
			}
			if test.name == "startup" {
				if bindingError.Code != "desktop_startup_failed" {
					t.Fatalf("binding error code = %q, want desktop_startup_failed", bindingError.Code)
				}
			} else if bindingError.Code != "operation_failed" {
				t.Fatalf("binding error code = %q, want operation_failed", bindingError.Code)
			}
			if !errors.Is(reported, sensitiveFailure) {
				t.Fatalf("locally reported error = %v, want sensitive internal failure", reported)
			}
		})
	}
}

type recordingSavedQuickTaskService struct {
	detail     runs.QuickTaskDetail
	err        error
	ctx        context.Context
	historyIDs []string
	remembered []runs.RememberQuickTaskCredentialCommand
	forgotten  []string
}

func (service *recordingSavedQuickTaskService) QuickTask(ctx context.Context, id string) (runs.QuickTaskDetail, error) {
	service.ctx = ctx
	service.historyIDs = append(service.historyIDs, id)
	return service.detail, service.err
}

func (service *recordingSavedQuickTaskService) RememberQuickTaskCredential(
	ctx context.Context,
	command runs.RememberQuickTaskCredentialCommand,
) error {
	service.ctx = ctx
	service.remembered = append(service.remembered, command)
	return service.err
}

func (service *recordingSavedQuickTaskService) ForgetQuickTaskCredential(ctx context.Context, id string) error {
	service.ctx = ctx
	service.forgotten = append(service.forgotten, id)
	return service.err
}

func TestDesktopSavedQuickTaskCommandsUseLifecycleAndValidateIdentity(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	service := &recordingSavedQuickTaskService{
		detail:     runs.QuickTaskDetail{SchemaVersion: 1, RunID: id},
		historyIDs: []string{}, remembered: []runs.RememberQuickTaskCredentialCommand{}, forgotten: []string{},
	}
	app := NewDesktopApp(nil, service)
	type contextKey struct{}
	app.onStartup(context.WithValue(context.Background(), contextKey{}, "desktop"))
	defer app.shutdown()

	detail, err := app.GetQuickTask(id)
	if err != nil || detail.RunID != id {
		t.Fatalf("GetQuickTask() = %+v, %v", detail, err)
	}
	command := runs.RememberQuickTaskCredentialCommand{RunID: id, APIKey: "private-remembered-key"}
	if err := app.RememberQuickTaskCredential(command); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetQuickTaskCredential(id); err != nil {
		t.Fatal(err)
	}
	historyCalled := len(service.historyIDs) == 1
	rememberCalled := len(service.remembered) == 1
	forgetCalled := len(service.forgotten) == 1
	if !historyCalled || !rememberCalled || !forgetCalled {
		t.Fatal("saved quick task command did not delegate once")
	}
	if service.remembered[0] != command {
		t.Fatal("remembered credential command changed before delegation")
	}
	if service.historyIDs[0] != id || service.forgotten[0] != id {
		t.Fatal("saved quick task identity changed before delegation")
	}
	if service.ctx == nil || service.ctx.Value(contextKey{}) != "desktop" {
		t.Fatal("saved quick task commands did not receive the lifecycle context")
	}
	for _, invalid := range []string{"", " ", "invalid", "00000000-0000-0000-0000-000000000000"} {
		_, err := app.GetQuickTask(invalid)
		assertBindingErrorCode(t, err, desktopCodeInvalidIdentifier)
		err = app.RememberQuickTaskCredential(runs.RememberQuickTaskCredentialCommand{RunID: invalid})
		assertBindingErrorCode(t, err, desktopCodeInvalidIdentifier)
		assertBindingErrorCode(t, app.ForgetQuickTaskCredential(invalid), desktopCodeInvalidIdentifier)
	}
	for _, count := range []int{len(service.historyIDs), len(service.remembered), len(service.forgotten)} {
		if count != 1 {
			t.Fatal("invalid quick task identity reached saved-task services")
		}
	}
}

func TestDesktopSavedQuickTaskErrorsStaySafeThroughLifecycle(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	for _, operation := range []struct {
		name   string
		invoke func(*DesktopApp) error
	}{
		{name: "history", invoke: func(app *DesktopApp) error { _, err := app.GetQuickTask(id); return err }},
		{name: "remember", invoke: func(app *DesktopApp) error {
			return app.RememberQuickTaskCredential(runs.RememberQuickTaskCredentialCommand{RunID: id})
		}},
		{name: "forget", invoke: func(app *DesktopApp) error { return app.ForgetQuickTaskCredential(id) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			service := &recordingSavedQuickTaskService{err: errors.New("Bearer private-test-key")}
			app := NewDesktopApp(nil, service)
			assertBindingErrorCode(t, operation.invoke(app), desktopCodeNotStarted)
			app.onStartup(context.Background())
			err := operation.invoke(app)
			assertBindingErrorCode(t, err, desktopCodeOperationFailed)
			if strings.Contains(err.Error(), "private-test-key") {
				t.Fatal("saved quick task error exposed private detail")
			}
			if err := app.shutdown(); err != nil {
				t.Fatal(err)
			}
			assertBindingErrorCode(t, operation.invoke(app), DesktopCodeStopped)
		})
	}
}
