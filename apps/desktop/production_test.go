package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestProductionStoragePathsStayWithinInjectedConfigurationRoot(t *testing.T) {
	configurationRoot := t.TempDir()
	directory, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatalf("productionStoragePaths() error = %v", err)
	}
	wantDirectory := filepath.Join(configurationRoot, "llm-test-studio")
	wantDatabase := filepath.Join(wantDirectory, "llm-test-studio.db")
	if directory != wantDirectory || database != wantDatabase {
		t.Fatalf("storage paths = (%q, %q), want (%q, %q)", directory, database, wantDirectory, wantDatabase)
	}
	relative, err := filepath.Rel(configurationRoot, database)
	if err != nil {
		t.Fatalf("relative database path: %v", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		t.Fatalf("database path escaped injected configuration root: %q", relative)
	}
}

func TestProductionStoragePathsRejectUnsafeRoots(t *testing.T) {
	for _, root := range []string{"", "   ", "relative-config"} {
		t.Run(root, func(t *testing.T) {
			if _, _, err := productionStoragePaths(root); err == nil {
				t.Fatalf("productionStoragePaths(%q) error = nil", root)
			}
		})
	}
}

func TestProductionInitializerMigratesAndOpensReadModelsOnlyUnderInjectedRoot(t *testing.T) {
	configurationRoot := t.TempDir()
	configurationCalls := 0
	initialize := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) {
			configurationCalls++
			return configurationRoot, nil
		},
		appVersion: "desktop-test",
	})

	dependencies, err := initialize(context.Background())
	if err != nil {
		t.Fatalf("production initializer error = %v", err)
	}
	if dependencies.close == nil {
		t.Fatal("production initializer did not return repository closer")
	}
	defer func() {
		if err := dependencies.close(); err != nil {
			t.Errorf("close production dependencies: %v", err)
		}
	}()
	if configurationCalls != 1 {
		t.Fatalf("configuration directory calls = %d, want 1", configurationCalls)
	}
	if isNilInterface(dependencies.query) {
		t.Fatal("production initializer did not return workspace query")
	}
	if isNilInterface(dependencies.catalog) {
		t.Fatal("production initializer did not return catalog query")
	}
	if isNilInterface(dependencies.reports) {
		t.Fatal("production initializer did not return reporting query")
	}
	if isNilInterface(dependencies.commands) {
		t.Fatal("production initializer did not wire Go run commands")
	}
	if isNilInterface(dependencies.comparisons) {
		t.Fatal("production initializer did not wire channel comparisons")
	}
	if isNilInterface(dependencies.quickTests) {
		t.Fatal("production initializer did not wire quick tests")
	}

	directory := filepath.Join(configurationRoot, "llm-test-studio")
	database := filepath.Join(directory, "llm-test-studio.db")
	if _, err := os.Stat(database); err != nil {
		t.Fatalf("stat production database: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat production directory: %v", err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o700 {
			t.Fatalf("production directory permissions = %#o, want 0700", permissions)
		}
	}
	snapshot, err := dependencies.query.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("query initialized workspace: %v", err)
	}
	if snapshot.SchemaVersion != workspace.CurrentSchemaVersion || len(snapshot.Plans) != 0 || len(snapshot.Runs) != 0 {
		t.Fatalf("initialized workspace = %+v, want no plans or runs in schema v%d", snapshot, workspace.CurrentSchemaVersion)
	}
	if _, err := dependencies.query.Snapshot(context.Background()); err != nil {
		t.Fatalf("query initialized workspace repeatedly: %v", err)
	}
	catalogSnapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("query initialized catalog: %v", err)
	}
	if catalogSnapshot.SchemaVersion != catalog.CurrentSnapshotSchemaVersion ||
		len(catalogSnapshot.Models)+len(catalogSnapshot.Channels)+len(catalogSnapshot.ChannelModels)+
			len(catalogSnapshot.Suites)+len(catalogSnapshot.Plans) != 0 || len(catalogSnapshot.TestCases) != 89 {
		t.Fatalf("initialized catalog cardinalities = models:%d channels:%d mappings:%d cases:%d suites:%d plans:%d, want only 89 file-backed cases",
			len(catalogSnapshot.Models), len(catalogSnapshot.Channels), len(catalogSnapshot.ChannelModels),
			len(catalogSnapshot.TestCases), len(catalogSnapshot.Suites), len(catalogSnapshot.Plans))
	}
	runnable, disabled, manual := 0, 0, 0
	for _, testCase := range catalogSnapshot.TestCases {
		switch {
		case !testCase.Enabled:
			disabled++
		case testCase.ExecutionMode == "manual":
			manual++
		default:
			runnable++
		}
	}
	if runnable != 57 || disabled != 26 || manual != 6 {
		t.Fatalf("built-in case policy counts = runnable:%d disabled:%d manual:%d, want 57/26/6", runnable, disabled, manual)
	}
	reportSnapshot, err := dependencies.reports.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("query initialized reports: %v", err)
	}
	if reportSnapshot.SchemaVersion != reporting.CurrentSchemaVersion || len(reportSnapshot.Reports) != 0 {
		t.Fatalf("initialized reports = %+v, want empty schema v%d", reportSnapshot, reporting.CurrentSchemaVersion)
	}
	comparisonSnapshot, err := dependencies.comparisons.Snapshot(context.Background())
	if err != nil || len(comparisonSnapshot.Comparisons) != 0 {
		t.Fatalf("initialized comparisons = %+v, %v, want empty", comparisonSnapshot, err)
	}
}

func TestProductionInitializationFailureRemainsVisibleThroughBinding(t *testing.T) {
	configurationFailure := errors.New("configuration directory unavailable")
	app := newDesktopApp(newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return "", configurationFailure },
		appVersion:    "desktop-test",
	}))
	var reported error
	app.setErrorReporter(func(err error) { reported = err })

	app.onStartup(context.Background())
	_, err := app.GetWorkspace()

	assertBindingErrorCode(t, err, "desktop_startup_failed")
	if !errors.Is(reported, configurationFailure) {
		t.Fatalf("locally reported error = %v, want configuration failure", reported)
	}
}

func TestProductionInitializerLoadsBuiltInCasesFromFilesWithoutDatabaseImport(t *testing.T) {
	configurationRoot := t.TempDir()
	initialize := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return configurationRoot, nil },
		appVersion:    "desktop-test",
	})

	first, err := initialize(context.Background())
	if err != nil {
		t.Fatalf("first production initialization: %v", err)
	}
	firstSnapshot, err := first.catalog.Snapshot(context.Background())
	if err != nil {
		_ = first.close()
		t.Fatalf("first catalog snapshot: %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close first production initialization: %v", err)
	}
	database := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	repository, err := persistence.OpenRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	storedCases, err := repository.ListTestCases(context.Background())
	_ = repository.Close()
	if err != nil || len(storedCases) != 0 {
		t.Fatalf("database case rows = %d, %v, want none", len(storedCases), err)
	}

	second, err := initialize(context.Background())
	if err != nil {
		t.Fatalf("second production initialization: %v", err)
	}
	defer func() {
		if err := second.close(); err != nil {
			t.Errorf("close second production initialization: %v", err)
		}
	}()
	secondSnapshot, err := second.catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("second catalog snapshot: %v", err)
	}

	if len(firstSnapshot.TestCases) != 89 || len(secondSnapshot.TestCases) != 89 {
		t.Fatalf("case counts across restart = %d/%d, want 89/89", len(firstSnapshot.TestCases), len(secondSnapshot.TestCases))
	}
	if len(firstSnapshot.Suites) != 0 || len(secondSnapshot.Suites) != 0 || len(firstSnapshot.Plans) != 0 || len(secondSnapshot.Plans) != 0 {
		t.Fatalf("suite/plan counts across restart = %d/%d suites, %d/%d plans, want all empty",
			len(firstSnapshot.Suites), len(secondSnapshot.Suites), len(firstSnapshot.Plans), len(secondSnapshot.Plans))
	}
	for index, firstCase := range firstSnapshot.TestCases {
		secondCase := secondSnapshot.TestCases[index]
		if firstCase.ID != secondCase.ID || firstCase.Key != secondCase.Key || firstCase.Revision == 0 || firstCase.Revision != secondCase.Revision {
			t.Fatalf("case %d changed across idempotent restart: first=%+v second=%+v", index, firstCase, secondCase)
		}
	}
}

func TestProductionInitializerMergesExecutableUserCaseOverBuiltInCase(t *testing.T) {
	configurationRoot := t.TempDir()
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	casePath := "openai-chat/T001/case.json"
	firstBundle := fstest.MapFS{casePath: &fstest.MapFile{Data: []byte(productionLegacyCase("first bundle"))}}
	initialize := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		caseBundle:     firstBundle,
		executablePath: func() (string, error) { return executable, nil },
	})
	first, err := initialize(context.Background())
	if err != nil {
		t.Fatalf("first production initialization: %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close first production initialization: %v", err)
	}

	userDirectory := filepath.Join(executableDirectory, "cases", "openai-chat", "T001")
	if err := os.MkdirAll(userDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDirectory, "case.json"), []byte(productionLegacyCase("user customization")), 0o600); err != nil {
		t.Fatal(err)
	}

	secondBundle := fstest.MapFS{casePath: &fstest.MapFile{Data: []byte(productionLegacyCase("second bundle"))}}
	second, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		caseBundle:     secondBundle,
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatalf("production initialization with user conflict: %v", err)
	}
	defer second.close()
	snapshot, err := second.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Name != "user customization" {
		t.Fatalf("catalog after conflict = %+v, err=%v", snapshot.TestCases, err)
	}
}

func TestProductionCaseCreateWritesShareableFileBesideExecutable(t *testing.T) {
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return t.TempDir(), nil },
		appVersion:     "desktop-test",
		caseBundle:     fstest.MapFS{},
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T900", Name: "shareable", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Method:                  domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{"Content-Type": "application/json"},
		Body:                json.RawMessage(`{"messages":[{"role":"user","content":"hello"}]}`),
		AllowedHTTPStatuses: []int{200}, StreamCompletion: domain.StreamCompletionNotApplicable,
		Assertions: []catalog.AssertionInput{{Kind: domain.AssertionResponseSchema, Config: json.RawMessage(`{"required":true}`)}},
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	path := filepath.Join(executableDirectory, "cases", "openai-chat", "T900", "case.json")
	if raw, err := os.ReadFile(path); err != nil || !json.Valid(raw) {
		t.Fatalf("shareable case file = %q, %v", raw, err)
	}
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T900", Name: "must not replace", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Method:                  domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{"Content-Type": "application/json"},
		Body:                json.RawMessage(`{"messages":[{"role":"user","content":"replacement"}]}`),
		AllowedHTTPStatuses: []int{200}, StreamCompletion: domain.StreamCompletionNotApplicable,
		Assertions: []catalog.AssertionInput{{Kind: domain.AssertionResponseSchema, Config: json.RawMessage(`{"required":true}`)}},
	})
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("duplicate CreateTestCase() error = %v, want ErrConflict", err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Key != "T900" || snapshot.TestCases[0].Name != "shareable" {
		t.Fatalf("filesystem catalog after create = %#v, %v", snapshot.TestCases, err)
	}
}

func TestProductionCutsOverLegacyDatabaseCasesToExeRelativeFiles(t *testing.T) {
	configurationRoot := t.TempDir()
	_, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(context.Background(), database, persistence.MigrateOptions{AppVersion: "legacy-fixture"}); err != nil {
		t.Fatal(err)
	}
	repository, err := persistence.OpenRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	legacyCase := domain.TestCase{
		EntityMeta: domain.EntityMeta{ID: "71000000-0000-4000-8000-000000000001", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Key:        "T777", Name: "legacy user case", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Request:       domain.TestRequest{Method: domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"user","content":"legacy"}]}`)},
			Expected:      domain.TestExpected{AllowedHTTPStatuses: []int{200}, StreamCompletion: domain.StreamCompletionNotApplicable},
			Assertions:    []domain.TestAssertion{{Kind: domain.AssertionResponseSchema, Config: json.RawMessage(`{"required":true}`)}},
		},
	}
	if err := repository.CreateTestCase(context.Background(), legacyCase); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return configurationRoot, nil }, appVersion: "desktop-test",
		caseBundle: fstest.MapFS{}, executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Name != legacyCase.Name {
		t.Fatalf("cut-over catalog = %#v, %v", snapshot.TestCases, err)
	}
	casePath := filepath.Join(executableDirectory, "cases", "openai-chat", "T777", "case.json")
	if raw, err := os.ReadFile(casePath); err != nil || !json.Valid(raw) {
		t.Fatalf("cut-over case file = %q, %v", raw, err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}
	repository, err = persistence.OpenRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	remaining, err := repository.ListTestCases(context.Background())
	if err != nil || len(remaining) != 0 {
		t.Fatalf("legacy unreferenced database cases = %#v, %v", remaining, err)
	}
}

func productionLegacyCase(name string) string {
	return `{"id":"T001","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","kind":"chat_sync","default":false,"severity":"normal","request":{"method":"POST","path":"/v1/chat/completions","body":{"messages":[{"role":"user","content":"hello"}]}}}`
}
