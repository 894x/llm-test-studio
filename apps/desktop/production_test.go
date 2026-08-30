package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/workspace"
	"github.com/894x/llm-studio/internal/persistence/sqlite"
)

func TestProductionStoragePathsStayWithinInjectedConfigurationRoot(t *testing.T) {
	configurationRoot := t.TempDir()
	directory, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatalf("productionStoragePaths() error = %v", err)
	}
	wantDirectory := filepath.Join(configurationRoot, "llm-studio")
	wantDatabase := filepath.Join(wantDirectory, "llm-studio.db")
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
	if !isNilInterface(dependencies.commands) {
		t.Fatal("production initializer invented run commands")
	}

	directory := filepath.Join(configurationRoot, "llm-studio")
	database := filepath.Join(directory, "llm-studio.db")
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
		t.Fatalf("initialized workspace = %+v, want empty schema v%d", snapshot, workspace.CurrentSchemaVersion)
	}
	catalogSnapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("query initialized catalog: %v", err)
	}
	if catalogSnapshot.SchemaVersion != catalog.CurrentSnapshotSchemaVersion ||
		len(catalogSnapshot.Models)+len(catalogSnapshot.Channels)+len(catalogSnapshot.ChannelModels)+
			len(catalogSnapshot.Suites)+len(catalogSnapshot.Plans) != 0 || len(catalogSnapshot.TestCases) != 89 {
		t.Fatalf("initialized catalog cardinalities = models:%d channels:%d mappings:%d cases:%d suites:%d plans:%d, want only 89 built-in cases",
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

func TestProductionInitializerReimportsBuiltInCasesIdempotently(t *testing.T) {
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
	for index, firstCase := range firstSnapshot.TestCases {
		secondCase := secondSnapshot.TestCases[index]
		if firstCase.ID != secondCase.ID || firstCase.Key != secondCase.Key || firstCase.Revision != 1 || secondCase.Revision != 1 {
			t.Fatalf("case %d changed across idempotent restart: first=%+v second=%+v", index, firstCase, secondCase)
		}
	}
}

func TestProductionInitializerKeepsUserEditedBuiltInCaseAvailableOnBundleConflict(t *testing.T) {
	configurationRoot := t.TempDir()
	casePath := "openai-chat/T001/case.json"
	firstBundle := fstest.MapFS{casePath: &fstest.MapFile{Data: []byte(productionLegacyCase("first bundle"))}}
	initialize := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return configurationRoot, nil },
		appVersion:    "desktop-test",
		caseBundle:    firstBundle,
	})
	first, err := initialize(context.Background())
	if err != nil {
		t.Fatalf("first production initialization: %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close first production initialization: %v", err)
	}

	database := filepath.Join(configurationRoot, "llm-studio", "llm-studio.db")
	repository, err := sqlite.OpenRepository(context.Background(), database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatalf("open repository for user edit: %v", err)
	}
	testCases, err := repository.ListTestCases(context.Background())
	if err != nil || len(testCases) != 1 {
		_ = repository.Close()
		t.Fatalf("list imported case before user edit: cases=%d err=%v", len(testCases), err)
	}
	userCase := testCases[0]
	userCase.EntityMeta, err = userCase.EntityMeta.NextRevision(userCase.UpdatedAt.Add(time.Second))
	if err != nil {
		_ = repository.Close()
		t.Fatalf("advance user case revision: %v", err)
	}
	userCase.Name = "user customization"
	if err := repository.UpdateTestCase(context.Background(), 1, userCase); err != nil {
		_ = repository.Close()
		t.Fatalf("write user case revision: %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("close user-edit repository: %v", err)
	}

	conflictCount := 0
	secondBundle := fstest.MapFS{casePath: &fstest.MapFile{Data: []byte(productionLegacyCase("second bundle"))}}
	second, err := newProductionInitializer(productionOptions{
		userConfigDir:       func() (string, error) { return configurationRoot, nil },
		appVersion:          "desktop-test",
		caseBundle:          secondBundle,
		reportCaseConflicts: func(count int) { conflictCount = count },
	})(context.Background())
	if err != nil {
		t.Fatalf("production initialization with user conflict: %v", err)
	}
	defer second.close()
	if conflictCount != 1 {
		t.Fatalf("reported conflict count = %d, want 1", conflictCount)
	}
	snapshot, err := second.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Name != "user customization" || snapshot.TestCases[0].Revision != 2 {
		t.Fatalf("catalog after conflict = %+v, err=%v", snapshot.TestCases, err)
	}
}

func productionLegacyCase(name string) string {
	return `{"id":"T001","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","kind":"chat_sync","default":false,"severity":"normal","request":{"method":"POST","path":"/v1/chat/completions","body":{"messages":[{"role":"user","content":"hello"}]}}}`
}
