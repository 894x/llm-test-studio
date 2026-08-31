package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/runs"
	"github.com/894x/llm-studio/internal/application/workspace"
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

func (query *recordingCatalogQuery) Snapshot(ctx context.Context) (catalog.Snapshot, error) {
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

type recordingRunCommands struct {
	startIDs     []string
	startTargets []runs.StartCommand
	stopIDs      []string
	cancelIDs    []string
	startErr     error
	stopErr      error
	cancelErr    error
	commandEnded *bool
}

func (commands *recordingRunCommands) StartTarget(_ context.Context, command runs.StartCommand) (string, error) {
	commands.startTargets = append(commands.startTargets, command)
	if commands.commandEnded != nil {
		*commands.commandEnded = true
	}
	return "", commands.startErr
}

func (commands *recordingRunCommands) StartRun(_ context.Context, id string) error {
	commands.startIDs = append(commands.startIDs, id)
	if commands.commandEnded != nil {
		*commands.commandEnded = true
	}
	return commands.startErr
}

func (commands *recordingRunCommands) StopSending(_ context.Context, id string) error {
	commands.stopIDs = append(commands.stopIDs, id)
	if commands.commandEnded != nil {
		*commands.commandEnded = true
	}
	return commands.stopErr
}

func (commands *recordingRunCommands) CancelRun(_ context.Context, id string) error {
	commands.cancelIDs = append(commands.cancelIDs, id)
	if commands.commandEnded != nil {
		*commands.commandEnded = true
	}
	return commands.cancelErr
}

func TestDesktopAppExposesStartupInitializationFailure(t *testing.T) {
	initializationFailure := errors.New("cannot open application database")
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, initializationFailure
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
	app := newDesktopApp(nil)
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
	app := newDesktopApp(nil)
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
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{
			catalog: catalogQuery,
			reports: reportingQuery,
		}, nil
	})
	type contextKey struct{}
	startupContext := context.WithValue(context.Background(), contextKey{}, "desktop")
	app.onStartup(startupContext)

	gotCatalog, err := app.GetCatalog()
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
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{reports: query}, nil
	})
	app.onStartup(context.Background())

	detail, err := app.GetReportDetail(id)
	if err != nil || detail.SchemaVersion != reporting.CurrentSchemaVersion {
		t.Fatalf("GetReportDetail() = %#v, %v", detail, err)
	}
	exported, err := app.ExportReport(id, "json", reporting.DefaultWatermark)
	if err != nil || exported.Filename != "report.json" || exported.DataBase64 != "e30=" {
		t.Fatalf("ExportReport() = %#v, %v", exported, err)
	}
	if query.calls != 2 {
		t.Fatalf("report document calls = %d, want 2", query.calls)
	}
	if _, err := app.ExportReport(id, "exe", reporting.DefaultWatermark); !errors.As(err, new(DesktopBindingError)) {
		t.Fatalf("invalid export error = %v", err)
	}
}

func TestDesktopAppCatalogCommandsDelegateAndReturnAuthoritativeCatalog(t *testing.T) {
	query := &recordingCatalogQuery{snapshot: catalog.Snapshot{SchemaVersion: catalog.CurrentSnapshotSchemaVersion}}
	commands := &recordingCatalogCommands{}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: query, catalogCommands: commands}, nil
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
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{catalog: query, catalogCommands: commands}, nil
			})
			app.onStartup(context.Background())
			_, err := app.CreateModel(catalog.CreateModelCommand{})
			assertBindingErrorCode(t, err, test.code)
			if query.calls != 0 {
				t.Fatalf("catalog query calls = %d, want 0", query.calls)
			}
		})
	}
}

func TestDesktopAppCommandsReturnAuthoritativeWorkspaceAfterSuccess(t *testing.T) {
	const planID = "11111111-1111-4111-8111-111111111111"
	const runID = "22222222-2222-4222-8222-222222222222"
	tests := []struct {
		name       string
		id         string
		invoke     func(*DesktopApp, string) (workspace.Snapshot, error)
		calledWith func(*recordingRunCommands) []string
	}{
		{name: "start run", id: planID, invoke: (*DesktopApp).StartRun, calledWith: func(commands *recordingRunCommands) []string { return commands.startIDs }},
		{name: "stop sending", id: runID, invoke: (*DesktopApp).StopSending, calledWith: func(commands *recordingRunCommands) []string { return commands.stopIDs }},
		{name: "cancel run", id: runID, invoke: (*DesktopApp).CancelRun, calledWith: func(commands *recordingRunCommands) []string { return commands.cancelIDs }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commandEnded := false
			commands := &recordingRunCommands{commandEnded: &commandEnded}
			want := workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion, ActiveRunID: runID}
			query := &recordingWorkspaceQuery{
				snapshot: want,
				before: func() error {
					if !commandEnded {
						return errors.New("workspace queried before command completed")
					}
					return nil
				},
			}
			app := NewDesktopApp(query, commands)
			app.onStartup(context.Background())

			got, err := test.invoke(app, test.id)

			if err != nil {
				t.Fatalf("command error = %v", err)
			}
			if got.ActiveRunID != want.ActiveRunID {
				t.Fatalf("command snapshot active run = %q, want %q", got.ActiveRunID, want.ActiveRunID)
			}
			if query.calls != 1 {
				t.Fatalf("workspace query calls = %d, want 1", query.calls)
			}
			calls := test.calledWith(commands)
			if len(calls) != 1 || calls[0] != test.id {
				t.Fatalf("command IDs = %v, want [%s]", calls, test.id)
			}
		})
	}
}

func TestDesktopAppStartsOneExplicitRuntimeTarget(t *testing.T) {
	command := runs.StartCommand{
		PlanID:    "11111111-1111-4111-8111-111111111111",
		ModelID:   "22222222-2222-4222-8222-222222222222",
		ChannelID: "33333333-3333-4333-8333-333333333333",
	}
	commands := &recordingRunCommands{}
	want := workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion, ActiveRunID: "44444444-4444-4444-8444-444444444444"}
	app := NewDesktopApp(&recordingWorkspaceQuery{snapshot: want}, commands)
	app.onStartup(context.Background())

	got, err := app.StartRunTarget(command)
	if err != nil {
		t.Fatalf("StartRunTarget() error = %v", err)
	}
	if got.ActiveRunID != want.ActiveRunID || len(commands.startTargets) != 1 || commands.startTargets[0] != command {
		t.Fatalf("StartRunTarget() = snapshot:%+v commands:%+v", got, commands.startTargets)
	}

	_, err = app.StartRunTarget(runs.StartCommand{PlanID: command.PlanID, ModelID: "invalid", ChannelID: command.ChannelID})
	assertBindingErrorCode(t, err, desktopCodeInvalidIdentifier)
	if len(commands.startTargets) != 1 {
		t.Fatalf("invalid target reached commands: %+v", commands.startTargets)
	}
}

func TestDesktopAppCommandErrorsDoNotQueryOrInventWorkspace(t *testing.T) {
	const planID = "11111111-1111-4111-8111-111111111111"
	const runID = "22222222-2222-4222-8222-222222222222"
	commandFailure := errors.New("application command rejected")
	tests := []struct {
		name   string
		id     string
		invoke func(*DesktopApp, string) (workspace.Snapshot, error)
		setup  func(*recordingRunCommands)
	}{
		{name: "start run", id: planID, invoke: (*DesktopApp).StartRun, setup: func(commands *recordingRunCommands) { commands.startErr = commandFailure }},
		{name: "stop sending", id: runID, invoke: (*DesktopApp).StopSending, setup: func(commands *recordingRunCommands) { commands.stopErr = commandFailure }},
		{name: "cancel run", id: runID, invoke: (*DesktopApp).CancelRun, setup: func(commands *recordingRunCommands) { commands.cancelErr = commandFailure }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &recordingWorkspaceQuery{snapshot: workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}}
			commands := &recordingRunCommands{}
			test.setup(commands)
			app := NewDesktopApp(query, commands)
			var reported error
			app.setErrorReporter(func(err error) { reported = err })
			app.onStartup(context.Background())

			_, err := test.invoke(app, test.id)

			assertBindingErrorCode(t, err, "operation_failed")
			if query.calls != 0 {
				t.Fatalf("workspace query calls = %d, want 0", query.calls)
			}
			if !errors.Is(reported, commandFailure) {
				t.Fatalf("locally reported error = %v, want application failure", reported)
			}
		})
	}
}

func TestDesktopAppCommandsRejectInvalidIdentifiersBeforeDelegation(t *testing.T) {
	invalidIDs := []string{
		"",
		"   ",
		"not-a-uuid",
		"00000000-0000-0000-0000-000000000000",
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
	}
	commands := &recordingRunCommands{}
	query := &recordingWorkspaceQuery{snapshot: workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}}
	app := NewDesktopApp(query, commands)
	app.onStartup(context.Background())
	operations := []struct {
		name   string
		invoke func(*DesktopApp, string) (workspace.Snapshot, error)
	}{
		{name: "start run", invoke: (*DesktopApp).StartRun},
		{name: "stop sending", invoke: (*DesktopApp).StopSending},
		{name: "cancel run", invoke: (*DesktopApp).CancelRun},
	}

	for _, operation := range operations {
		for _, id := range invalidIDs {
			t.Run(operation.name+"/"+id, func(t *testing.T) {
				_, err := operation.invoke(app, id)
				assertBindingErrorCode(t, err, "invalid_identifier")
			})
		}
	}
	if len(commands.startIDs)+len(commands.stopIDs)+len(commands.cancelIDs) != 0 {
		t.Fatalf("invalid identifiers reached commands: start=%v stop=%v cancel=%v", commands.startIDs, commands.stopIDs, commands.cancelIDs)
	}
	if query.calls != 0 {
		t.Fatalf("invalid identifiers queried workspace %d times, want 0", query.calls)
	}
}

func TestDesktopAppRejectsCallsBeforeStartup(t *testing.T) {
	query := &recordingWorkspaceQuery{}
	commands := &recordingRunCommands{}
	app := NewDesktopApp(query, commands)

	_, err := app.GetWorkspace()
	assertBindingErrorCode(t, err, "desktop_not_started")
	_, err = app.StartRun("11111111-1111-4111-8111-111111111111")
	assertBindingErrorCode(t, err, "desktop_not_started")
	if query.calls != 0 || len(commands.startIDs) != 0 {
		t.Fatal("call before startup reached an application dependency")
	}
}

func TestDesktopAppWaitsForStartupInProgressBeforeServingBindings(t *testing.T) {
	want := workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}
	startupEntered := make(chan struct{})
	allowStartup := make(chan struct{})
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		close(startupEntered)
		<-allowStartup
		return desktopDependencies{query: &recordingWorkspaceQuery{snapshot: want}}, nil
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
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: catalogQuery, reports: reportingQuery}, nil
	})

	_, err := app.GetCatalog()
	assertBindingErrorCode(t, err, "desktop_not_started")
	_, err = app.GetReports()
	assertBindingErrorCode(t, err, "desktop_not_started")
	if catalogQuery.calls != 0 || reportingQuery.calls != 0 {
		t.Fatalf("calls before startup reached queries: catalog=%d reports=%d", catalogQuery.calls, reportingQuery.calls)
	}
}

func TestDesktopAppTreatsTypedNilDependenciesAsUnavailable(t *testing.T) {
	const planID = "11111111-1111-4111-8111-111111111111"
	t.Run("workspace query", func(t *testing.T) {
		var query *recordingWorkspaceQuery
		app := NewDesktopApp(query, &recordingRunCommands{})
		app.onStartup(context.Background())

		_, err := app.GetWorkspace()
		assertBindingErrorCode(t, err, "workspace_unavailable")
	})

	t.Run("run commands", func(t *testing.T) {
		var commands *recordingRunCommands
		query := &recordingWorkspaceQuery{snapshot: workspace.Snapshot{SchemaVersion: workspace.CurrentSchemaVersion}}
		app := NewDesktopApp(query, commands)
		app.onStartup(context.Background())

		_, err := app.StartRun(planID)
		assertBindingErrorCode(t, err, "run_commands_unavailable")
		if query.calls != 0 {
			t.Fatalf("workspace query calls = %d, want 0", query.calls)
		}
	})

	t.Run("catalog query", func(t *testing.T) {
		var query *recordingCatalogQuery
		app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
			return desktopDependencies{catalog: query}, nil
		})
		app.onStartup(context.Background())

		_, err := app.GetCatalog()
		assertBindingErrorCode(t, err, "catalog_unavailable")
	})

	t.Run("reporting query", func(t *testing.T) {
		var query *recordingReportingQuery
		app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
			return desktopDependencies{reports: query}, nil
		})
		app.onStartup(context.Background())

		_, err := app.GetReports()
		assertBindingErrorCode(t, err, "reports_unavailable")
	})
}

func TestDesktopAppShutdownIsIdempotent(t *testing.T) {
	closeFailure := errors.New("close failed")
	closeCalls := 0
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{
			query: &recordingWorkspaceQuery{},
			close: func() error {
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
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		initializeCalls++
		return desktopDependencies{query: &recordingWorkspaceQuery{}}, nil
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

func (query *cancelableCatalogQuery) Snapshot(ctx context.Context) (catalog.Snapshot, error) {
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
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{
			query: query,
			close: func() error {
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
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{
			catalog: catalogQuery,
			reports: reportingQuery,
			close: func() error {
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
		_, err := app.GetCatalog()
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
	const validID = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name   string
		app    func() *DesktopApp
		invoke func(*DesktopApp) error
	}{
		{
			name: "startup",
			app: func() *DesktopApp {
				return newDesktopApp(func(context.Context) (desktopDependencies, error) {
					return desktopDependencies{}, sensitiveFailure
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
				return NewDesktopApp(&recordingWorkspaceQuery{err: sensitiveFailure}, &recordingRunCommands{})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetWorkspace()
				return err
			},
		},
		{
			name: "catalog query",
			app: func() *DesktopApp {
				return newDesktopApp(func(context.Context) (desktopDependencies, error) {
					return desktopDependencies{catalog: &recordingCatalogQuery{err: sensitiveFailure}}, nil
				})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetCatalog()
				return err
			},
		},
		{
			name: "reporting query",
			app: func() *DesktopApp {
				return newDesktopApp(func(context.Context) (desktopDependencies, error) {
					return desktopDependencies{reports: &recordingReportingQuery{err: sensitiveFailure}}, nil
				})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.GetReports()
				return err
			},
		},
		{
			name: "start run",
			app: func() *DesktopApp {
				return NewDesktopApp(&recordingWorkspaceQuery{}, &recordingRunCommands{startErr: sensitiveFailure})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.StartRun(validID)
				return err
			},
		},
		{
			name: "stop sending",
			app: func() *DesktopApp {
				return NewDesktopApp(&recordingWorkspaceQuery{}, &recordingRunCommands{stopErr: sensitiveFailure})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.StopSending(validID)
				return err
			},
		},
		{
			name: "cancel run",
			app: func() *DesktopApp {
				return NewDesktopApp(&recordingWorkspaceQuery{}, &recordingRunCommands{cancelErr: sensitiveFailure})
			},
			invoke: func(app *DesktopApp) error {
				_, err := app.CancelRun(validID)
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
