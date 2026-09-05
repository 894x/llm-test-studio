package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
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

func TestCredentialCleanupRegistryPathIsNamespacedByAuthoredCatalogRoot(t *testing.T) {
	configurationDirectory := t.TempDir()
	rootA := filepath.Join(t.TempDir(), "copy-a")
	rootB := filepath.Join(t.TempDir(), "copy-b")
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pathA, err := credentialCleanupRegistryPath(configurationDirectory, rootA)
	if err != nil {
		t.Fatal(err)
	}
	pathAEquivalent, err := credentialCleanupRegistryPath(configurationDirectory, filepath.Join(rootA, "."))
	if err != nil {
		t.Fatal(err)
	}
	pathB, err := credentialCleanupRegistryPath(configurationDirectory, rootB)
	if err != nil {
		t.Fatal(err)
	}
	if pathA != pathAEquivalent {
		t.Fatalf("equivalent authored roots produced different registry paths: %q != %q", pathA, pathAEquivalent)
	}
	if pathA == pathB {
		t.Fatalf("distinct authored roots shared registry path %q", pathA)
	}

	queueA, err := credentials.NewFileCleanupQueue(pathA)
	if err != nil {
		t.Fatal(err)
	}
	queueB, err := credentials.NewFileCleanupQueue(pathB)
	if err != nil {
		t.Fatal(err)
	}
	credentialID := "72000000-0000-4000-8000-000000000001"
	if err := queueA.Enqueue(context.Background(), credentialID); err != nil {
		t.Fatal(err)
	}
	ids, err := queueB.List(context.Background())
	if err != nil || len(ids) != 0 {
		t.Fatalf("second authored root registry = %#v, %v; want isolated empty registry", ids, err)
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
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	configurationCalls := 0
	initialize := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) {
			configurationCalls++
			return configurationRoot, nil
		},
		appVersion:     "desktop-test",
		executablePath: func() (string, error) { return executable, nil },
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
	assertProductionDatabaseRetired(t, database)
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
			len(catalogSnapshot.Plans) != 0 || len(catalogSnapshot.TestCases) != 499 || len(catalogSnapshot.Suites) != 26 {
		t.Fatalf("initialized catalog cardinalities = models:%d channels:%d mappings:%d cases:%d suites:%d plans:%d, want 499 file-backed cases and 26 scenario suites",
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
	if runnable != 261 || disabled != 131 || manual != 107 {
		t.Fatalf("built-in case policy counts = runnable:%d disabled:%d manual:%d, want 261/131/107", runnable, disabled, manual)
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

func TestProductionInitializerExportsLegacyAuthoredCatalogBeforeServingFiles(t *testing.T) {
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	configurationRoot := t.TempDir()
	_, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(context.Background(), database, persistence.MigrateOptions{AppVersion: "legacy-test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := persistence.OpenLegacyCatalogRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	legacy := domain.Model{
		EntityMeta: domain.EntityMeta{
			ID: "71000000-0000-4000-8000-000000000001", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Name: "legacy SQLite model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"},
	}
	if err := repository.CreateModel(context.Background(), legacy); err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		caseBundle:     fstest.MapFS{},
		suiteBundle:    fstest.MapFS{},
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatalf("initialize with legacy authored catalog: %v", err)
	}
	defer dependencies.close()

	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.Models) != 1 || snapshot.Models[0].ID != legacy.ID || snapshot.Models[0].Revision != legacy.Revision {
		t.Fatalf("migrated catalog models = %#v, %v", snapshot.Models, err)
	}
	models, err := os.ReadFile(filepath.Join(executableDirectory, "models.json"))
	if err != nil || !json.Valid(models) || !strings.Contains(string(models), legacy.ID) {
		t.Fatalf("migrated models.json = %q, %v", models, err)
	}
	markerPath := filepath.Join(executableDirectory, ".llm-test-studio-authored-catalog-v1.json")
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumed catalog migration marker stat error = %v, want not exist", err)
	}
	assertProductionDatabaseRetired(t, database)
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
	assertProductionDatabaseRetired(t, database)
	if len(firstSnapshot.Suites) != 26 {
		t.Fatalf("file suites = %d, want 26", len(firstSnapshot.Suites))
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

	if len(firstSnapshot.TestCases) != 499 || len(secondSnapshot.TestCases) != 499 {
		t.Fatalf("case counts across restart = %d/%d, want 499/499", len(firstSnapshot.TestCases), len(secondSnapshot.TestCases))
	}
	if len(firstSnapshot.Suites) != 26 || len(secondSnapshot.Suites) != 26 || len(firstSnapshot.Plans) != 0 || len(secondSnapshot.Plans) != 0 {
		t.Fatalf("suite/plan counts across restart = %d/%d suites, %d/%d plans, want 26 scenario suites and no plans",
			len(firstSnapshot.Suites), len(secondSnapshot.Suites), len(firstSnapshot.Plans), len(secondSnapshot.Plans))
	}
	wantSuites := map[string]struct {
		target string
		count  int
	}{
		"Kimi K3 官方基础套件":                  {target: "kimi-k3", count: 60},
		"Kimi K2.7 Code 官方基础套件":           {target: "kimi-k2.7-code", count: 10},
		"Kimi K2.7 Code Highspeed 官方基础套件": {target: "kimi-k2.7-code-highspeed", count: 10},
		"Kimi K2.6 官方基础套件":                {target: "kimi-k2.6", count: 11},
		"Wan 3.0 标准版 · 连通性测试":             {target: "wan3.0-video", count: 1},
		"Wan 3.0 标准版 · 基本功能测试":            {target: "wan3.0-video", count: 6},
		"Wan 3.0 标准版 · 参数拒绝测试":            {target: "wan3.0-video", count: 39},
		"Wan 3.0 标准版 · 完整测试（自动可执行）":       {target: "wan3.0-video", count: 74},
		"Wan 3.0 标准版 · 完整矩阵（含禁用模板）":       {target: "wan3.0-video", count: 191},
		"Wan 3.0 Prime · 连通性测试":           {target: "wan3.0-video-prime", count: 1},
		"Wan 3.0 Prime · 基本功能测试":          {target: "wan3.0-video-prime", count: 6},
		"Wan 3.0 Prime · 参数拒绝测试":          {target: "wan3.0-video-prime", count: 39},
		"Wan 3.0 Prime · 完整测试（自动可执行）":     {target: "wan3.0-video-prime", count: 74},
		"Wan 3.0 Prime · 完整矩阵（含禁用模板）":     {target: "wan3.0-video-prime", count: 191},
		"Wan 2.7 文生视频边界套件":                {target: "wan2.7-t2v", count: 7},
		"Wan 2.7 2026-06-12 快照边界套件":       {target: "wan2.7-t2v-2026-06-12", count: 7},
		"Wan 2.6 文生视频边界套件":                {target: "wan2.6-t2v", count: 4},
		"Wan 2.5 文生视频边界套件":                {target: "wan2.5-t2v-preview", count: 2},
		"Wan 2.2 文生视频边界套件":                {target: "wan2.2-t2v-plus", count: 3},
		"Wan 2.1 Turbo 边界套件":              {target: "wanx2.1-t2v-turbo", count: 3},
		"Wan 2.1 Plus 边界套件":               {target: "wanx2.1-t2v-plus", count: 3},
		"MiniMax H3 连通性测试套件":              {target: "MiniMax-H3", count: 3},
		"MiniMax H3 基本功能测试套件":             {target: "MiniMax-H3", count: 24},
		"MiniMax H3 参数拒绝测试套件":             {target: "MiniMax-H3", count: 45},
		"MiniMax H3 自动化核心回归套件":            {target: "MiniMax-H3", count: 48},
		"MiniMax H3 视频生成完整边界套件":           {target: "MiniMax-H3", count: 149},
	}
	firstByName := make(map[string]catalog.SuiteSummary, len(firstSnapshot.Suites))
	secondByName := make(map[string]catalog.SuiteSummary, len(secondSnapshot.Suites))
	caseByID := make(map[string]catalog.TestCaseSummary, len(firstSnapshot.TestCases))
	for _, testCase := range firstSnapshot.TestCases {
		caseByID[testCase.ID] = testCase
	}
	for _, suite := range firstSnapshot.Suites {
		firstByName[suite.Name] = suite
	}
	for _, suite := range secondSnapshot.Suites {
		secondByName[suite.Name] = suite
	}
	for name, want := range wantSuites {
		firstSuite, firstFound := firstByName[name]
		secondSuite, secondFound := secondByName[name]
		if !firstFound || !secondFound || firstSuite.ID != secondSuite.ID || firstSuite.Revision != secondSuite.Revision || firstSuite.CaseCount != want.count || firstSuite.CaseCount != len(firstSuite.Cases) {
			t.Fatalf("per-model suite %q across restart = %+v / %+v, want %d cases", name, firstSuite, secondSuite, want.count)
		}
		for _, ref := range firstSuite.Cases {
			testCase, found := caseByID[ref.CaseID]
			if !found || !containsString(testCase.ModelTargets, want.target) {
				t.Fatalf("suite %q includes case %+v with targets %#v", name, ref, testCase.ModelTargets)
			}
		}
	}
	for index, firstCase := range firstSnapshot.TestCases {
		secondCase := secondSnapshot.TestCases[index]
		if firstCase.ID != secondCase.ID || firstCase.Key != secondCase.Key || firstCase.Revision == 0 || firstCase.Revision != secondCase.Revision {
			t.Fatalf("case %d changed across idempotent restart: first=%+v second=%+v", index, firstCase, secondCase)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
		suiteBundle:    fstest.MapFS{},
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
		suiteBundle:    fstest.MapFS{},
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
		suiteBundle:    fstest.MapFS{},
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T900", Name: "shareable", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		ModelTargets: []string{"gpt-5.2", "gpt-4.1-mini"},
		Enabled:      true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    casetypes.TypeRequestSingle, TypeVersion: 1,
		Spec: json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{"Content-Type":"application/json"},"body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}`),
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	path := filepath.Join(executableDirectory, "cases", "openai-chat", "T900", "case.json")
	if raw, err := os.ReadFile(path); err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"model_targets"`) || !strings.Contains(string(raw), `"gpt-5.2"`) {
		t.Fatalf("shareable case file = %q, %v", raw, err)
	}
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T900", Name: "must not replace", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    casetypes.TypeRequestSingle, TypeVersion: 1,
		Spec: json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{"Content-Type":"application/json"},"body":{"messages":[{"role":"user","content":"replacement"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}`),
	})
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("duplicate CreateTestCase() error = %v, want ErrConflict", err)
	}
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T901", Name: "reserved", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    casetypes.TypeLegacyAPIAudit, TypeVersion: 1,
		Spec: json.RawMessage(`{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}`),
	})
	if !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("reserved CreateTestCase() error = %v, want ErrInvalid", err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Key != "T900" || snapshot.TestCases[0].Name != "shareable" || len(snapshot.TestCases[0].ModelTargets) != 2 {
		t.Fatalf("filesystem catalog after create = %#v, %v", snapshot.TestCases, err)
	}
}

func TestProductionModelAndPlanCreateWriteFilesWithoutDatabaseCatalogRows(t *testing.T) {
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	configurationRoot := t.TempDir()
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		caseBundle:     fstest.MapFS{},
		suiteBundle:    fstest.MapFS{},
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()

	createdModel, err := dependencies.catalogCommands.CreateModel(context.Background(), catalog.CreateModelCommand{
		Name: "file-backed model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"},
	})
	if err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	createdCase, err := dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T950", Name: "file-backed case", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    casetypes.TypeRequestSingle, TypeVersion: 1,
		Spec: json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}`),
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	createdPlan, err := dependencies.catalogCommands.CreatePlan(context.Background(), catalog.CreatePlanCommand{
		Name:     "file-backed plan",
		Cases:    []catalog.CaseRevisionInput{{CaseID: createdCase.ID, Revision: createdCase.Revision}},
		LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
		SLAThresholds: map[string]float64{"e2e_p95_ms": 3_000},
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	modelsPath := filepath.Join(executableDirectory, "models.json")
	modelsRaw, err := os.ReadFile(modelsPath)
	if err != nil || !json.Valid(modelsRaw) || !strings.Contains(string(modelsRaw), createdModel.ID) || !strings.Contains(string(modelsRaw), "file-backed model") {
		t.Fatalf("model catalog file = %q, %v", modelsRaw, err)
	}
	planPath := filepath.Join(executableDirectory, "plans", createdPlan.ID+".json")
	planRaw, err := os.ReadFile(planPath)
	if err != nil || !json.Valid(planRaw) || !strings.Contains(string(planRaw), createdPlan.ID) || !strings.Contains(string(planRaw), createdCase.ID) {
		t.Fatalf("plan file = %q, %v", planRaw, err)
	}

	database := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	assertProductionDatabaseRetired(t, database)
}

func TestProductionChannelCreateWritesOnlyMetadataToFileAndSecretToKeyring(t *testing.T) {
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	configurationRoot := t.TempDir()
	credentialStore := credentials.NewMemoryStore()
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:   func() (string, error) { return configurationRoot, nil },
		appVersion:      "desktop-test",
		caseBundle:      fstest.MapFS{},
		suiteBundle:     fstest.MapFS{},
		executablePath:  func() (string, error) { return executable, nil },
		credentialStore: credentialStore,
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()

	model, err := dependencies.catalogCommands.CreateModel(context.Background(), catalog.CreateModelCommand{
		Name: "channel model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"},
	})
	if err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	const apiKey = "secret-must-not-enter-json-or-sqlite"
	channel, err := dependencies.catalogCommands.CreateChannel(context.Background(), catalog.CreateChannelCommand{
		Name: "file-backed channel", BaseURL: "https://api.example.test/v1", APIKey: apiKey,
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	mapping, err := dependencies.catalogCommands.CreateChannelModel(context.Background(), catalog.CreateChannelModelCommand{
		ChannelID: channel.ID, ModelID: model.ID, UpstreamModelName: "upstream-file-model",
	})
	if err != nil {
		t.Fatalf("CreateChannelModel() error = %v", err)
	}

	channelsPath := filepath.Join(executableDirectory, "channels.json")
	channelsRaw, err := os.ReadFile(channelsPath)
	if err != nil || !json.Valid(channelsRaw) {
		t.Fatalf("channel catalog file = %q, %v", channelsRaw, err)
	}
	if strings.Contains(string(channelsRaw), apiKey) || !strings.Contains(string(channelsRaw), channel.ID) ||
		!strings.Contains(string(channelsRaw), mapping.ID) || !strings.Contains(string(channelsRaw), "upstream-file-model") {
		t.Fatalf("channel catalog did not contain safe metadata only: %s", channelsRaw)
	}
	var channelDocuments []struct {
		ID            string `json:"id"`
		CredentialID  string `json:"credential_id"`
		ModelMappings []struct {
			ID string `json:"id"`
		} `json:"model_mappings"`
	}
	if err := json.Unmarshal(channelsRaw, &channelDocuments); err != nil || len(channelDocuments) != 1 ||
		channelDocuments[0].ID != channel.ID || len(channelDocuments[0].ModelMappings) != 1 {
		t.Fatalf("channel documents = %#v, %v", channelDocuments, err)
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channelDocuments[0].CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	credentialScope, err := credentialScopeForAuthoredCatalogRoot(executableDirectory)
	if err != nil {
		t.Fatal(err)
	}
	scopedStore, err := credentials.NewScopedStore(credentialStore, credentialScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := scopedStore.Test(context.Background(), storeRef); err != nil {
		t.Fatalf("keyring credential test error = %v", err)
	}
	if err := credentialStore.Test(context.Background(), storeRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("unscoped legacy keyring entry = %v, want ErrNotFound", err)
	}

	database := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	assertProductionDatabaseRetired(t, database)
}

func TestProductionCopiedChannelMetadataCannotDeleteAnotherAuthoredRootSecret(t *testing.T) {
	ctx := context.Background()
	configurationRoot := t.TempDir()
	rootA := t.TempDir()
	rootB := t.TempDir()
	baseStore := credentials.NewMemoryStore()
	optionsForRoot := func(root string) productionOptions {
		return productionOptions{
			userConfigDir:   func() (string, error) { return configurationRoot, nil },
			appVersion:      "desktop-test",
			caseBundle:      fstest.MapFS{},
			suiteBundle:     fstest.MapFS{},
			executablePath:  func() (string, error) { return filepath.Join(root, "llm-test-studio.exe"), nil },
			credentialStore: baseStore,
		}
	}
	dependenciesA, err := newProductionInitializer(optionsForRoot(rootA))(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dependenciesA.close()
	channel, err := dependenciesA.catalogCommands.CreateChannel(ctx, catalog.CreateChannelCommand{
		Name: "root A channel", BaseURL: "https://api.example.test/v1", APIKey: "root-a-secret",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	channelsRaw, err := os.ReadFile(filepath.Join(rootA, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "channels.json"), channelsRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	dependenciesB, err := newProductionInitializer(optionsForRoot(rootB))(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := dependenciesB.catalogCommands.DeleteChannel(ctx, catalog.DeleteCommand{
		ID: channel.ID, ExpectedRevision: channel.Revision,
	}); err != nil {
		_ = dependenciesB.close()
		t.Fatal(err)
	}
	if err := dependenciesB.close(); err != nil {
		t.Fatal(err)
	}

	var documents []struct {
		CredentialID string `json:"credential_id"`
	}
	if err := json.Unmarshal(channelsRaw, &documents); err != nil || len(documents) != 1 {
		t.Fatalf("channel documents = %#v, %v", documents, err)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, documents[0].CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	scopeA, err := credentialScopeForAuthoredCatalogRoot(rootA)
	if err != nil {
		t.Fatal(err)
	}
	storeA, err := credentials.NewScopedStore(baseStore, scopeA)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.Test(ctx, ref); err != nil {
		t.Fatalf("root A key was deleted through copied root B metadata: %v", err)
	}
}

func TestProductionV11StartupMigratesEarlierUnscopedChannelCredential(t *testing.T) {
	ctx := context.Background()
	configurationRoot := t.TempDir()
	executableDirectory := t.TempDir()
	baseStore := credentials.NewMemoryStore()
	options := productionOptions{
		userConfigDir:   func() (string, error) { return configurationRoot, nil },
		appVersion:      "desktop-test",
		caseBundle:      fstest.MapFS{},
		suiteBundle:     fstest.MapFS{},
		executablePath:  func() (string, error) { return filepath.Join(executableDirectory, "llm-test-studio.exe"), nil },
		credentialStore: baseStore,
	}
	dependencies, err := newProductionInitializer(options)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := dependencies.catalogCommands.CreateChannel(ctx, catalog.CreateChannelCommand{
		Name: "early v11 channel", BaseURL: "https://api.example.test/v1", APIKey: "early-v11-secret",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		_ = dependencies.close()
		t.Fatal(err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}

	channelsRaw, err := os.ReadFile(filepath.Join(executableDirectory, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	var documents []struct {
		ID           string `json:"id"`
		CredentialID string `json:"credential_id"`
	}
	if err := json.Unmarshal(channelsRaw, &documents); err != nil || len(documents) != 1 || documents[0].ID != channel.ID {
		t.Fatalf("channel documents = %#v, %v", documents, err)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, documents[0].CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := credentialScopeForAuthoredCatalogRoot(executableDirectory)
	if err != nil {
		t.Fatal(err)
	}
	scopedStore, err := credentials.NewScopedStore(baseStore, scope)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := scopedStore.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := lease.Bytes()
	if err != nil {
		_ = lease.Close()
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		clear(secret)
		t.Fatal(err)
	}
	if err := scopedStore.Delete(ctx, ref); err != nil {
		clear(secret)
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, ref, secret); err != nil {
		clear(secret)
		t.Fatal(err)
	}
	clear(secret)

	dependencies, err = newProductionInitializer(options)(ctx)
	if err != nil {
		t.Fatalf("v11 unscoped credential upgrade: %v", err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Test(ctx, ref); err != nil {
		t.Fatalf("legacy v1 migration bridge after v11 startup = %v, want retained", err)
	}
	if err := scopedStore.Test(ctx, ref); err != nil {
		t.Fatalf("scoped key after v11 startup = %v, want retained", err)
	}
}

func TestProductionV10RetirementMigratesUnscopedCredentialReferencedOnlyByExistingFiles(t *testing.T) {
	ctx := context.Background()
	executableDirectory := t.TempDir()
	baseStore := credentials.NewMemoryStore()
	optionsForConfig := func(configurationRoot string) productionOptions {
		return productionOptions{
			userConfigDir:   func() (string, error) { return configurationRoot, nil },
			appVersion:      "desktop-test",
			caseBundle:      fstest.MapFS{},
			suiteBundle:     fstest.MapFS{},
			executablePath:  func() (string, error) { return filepath.Join(executableDirectory, "llm-test-studio.exe"), nil },
			credentialStore: baseStore,
		}
	}
	firstDependencies, err := newProductionInitializer(optionsForConfig(t.TempDir()))(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = firstDependencies.catalogCommands.CreateChannel(ctx, catalog.CreateChannelCommand{
		Name: "existing file channel", BaseURL: "https://api.example.test/v1", APIKey: "existing-file-secret",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		_ = firstDependencies.close()
		t.Fatal(err)
	}
	if err := firstDependencies.close(); err != nil {
		t.Fatal(err)
	}

	channelsRaw, err := os.ReadFile(filepath.Join(executableDirectory, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	var documents []struct {
		CredentialID string `json:"credential_id"`
	}
	if err := json.Unmarshal(channelsRaw, &documents); err != nil || len(documents) != 1 {
		t.Fatalf("channel documents = %#v, %v", documents, err)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, documents[0].CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := credentialScopeForAuthoredCatalogRoot(executableDirectory)
	if err != nil {
		t.Fatal(err)
	}
	scopedStore, err := credentials.NewScopedStore(baseStore, scope)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := scopedStore.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := lease.Bytes()
	if err != nil {
		_ = lease.Close()
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		clear(secret)
		t.Fatal(err)
	}
	if err := scopedStore.Delete(ctx, ref); err != nil {
		clear(secret)
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, ref, secret); err != nil {
		clear(secret)
		t.Fatal(err)
	}
	clear(secret)

	secondConfigurationRoot := t.TempDir()
	secondDependencies, err := newProductionInitializer(optionsForConfig(secondConfigurationRoot))(ctx)
	if err != nil {
		t.Fatalf("v10 retirement with existing files: %v", err)
	}
	if err := secondDependencies.close(); err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Test(ctx, ref); err != nil {
		t.Fatalf("legacy v1 migration bridge after v10 retirement = %v, want retained", err)
	}
	if err := scopedStore.Test(ctx, ref); err != nil {
		t.Fatalf("scoped key after v10 retirement = %v, want retained", err)
	}
	database := filepath.Join(secondConfigurationRoot, "llm-test-studio", "llm-test-studio.db")
	assertProductionDatabaseRetired(t, database)
}

func TestProductionLegacyV1BridgeMigratesSameCredentialIntoTwoAuthoredRoots(t *testing.T) {
	ctx := context.Background()
	rootA := t.TempDir()
	rootB := t.TempDir()
	baseStore := credentials.NewMemoryStore()
	now := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	channel := domain.Channel{
		EntityMeta: domain.EntityMeta{
			ID: "72300000-0000-4000-8000-000000000001", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Name: "shared legacy channel", BaseURL: "https://api.example.test/v1",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
		CredentialID: "72300000-0000-4000-8000-000000000002",
	}
	filesA, err := channelcatalog.New(filepath.Join(rootA, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := filesA.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(rootA, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "channels.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, ref, []byte("shared-legacy-secret")); err != nil {
		t.Fatal(err)
	}
	initializeRoot := func(root string) {
		t.Helper()
		dependencies, err := newProductionInitializer(productionOptions{
			userConfigDir:   func() (string, error) { return t.TempDir(), nil },
			appVersion:      "desktop-test",
			caseBundle:      fstest.MapFS{},
			suiteBundle:     fstest.MapFS{},
			executablePath:  func() (string, error) { return filepath.Join(root, "llm-test-studio.exe"), nil },
			credentialStore: baseStore,
		})(ctx)
		if err != nil {
			t.Fatalf("initialize authored root %s: %v", root, err)
		}
		if err := dependencies.close(); err != nil {
			t.Fatal(err)
		}
	}
	initializeRoot(rootA)
	initializeRoot(rootB)

	for _, root := range []string{rootA, rootB} {
		scope, err := credentialScopeForAuthoredCatalogRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		store, err := credentials.NewScopedStore(baseStore, scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Test(ctx, ref); err != nil {
			t.Fatalf("scoped credential for root %s = %v, want migrated", root, err)
		}
	}
	if err := baseStore.Test(ctx, ref); err != nil {
		t.Fatalf("legacy v1 migration bridge was consumed by one root: %v", err)
	}
}

func TestProductionSuiteCreateWritesShareableFileBesideExecutableWithoutDatabaseWrites(t *testing.T) {
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	configurationRoot := t.TempDir()
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		caseBundle:     fstest.MapFS{},
		suiteBundle:    fstest.MapFS{},
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()

	createdCase, err := dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T900", Name: "shareable", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		ModelTargets: []string{"gpt-5.2"}, Enabled: true, Default: false,
		Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    casetypes.TypeRequestSingle, TypeVersion: 1,
		Spec: json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{"Content-Type":"application/json"},"body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}`),
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	createdSuite, err := dependencies.catalogCommands.CreateSuite(context.Background(), catalog.CreateSuiteCommand{
		Key: "gpt-5.2-smoke", Name: "GPT-5.2 smoke", Protocol: domain.ProtocolOpenAIChat, ModelTarget: "gpt-5.2",
		Cases: []catalog.CaseRevisionInput{{CaseID: createdCase.ID, Revision: createdCase.Revision}},
	})
	if err != nil {
		t.Fatalf("CreateSuite() error = %v", err)
	}

	path := filepath.Join(executableDirectory, "suites", "openai-chat", "gpt-5.2-smoke", "suite.json")
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"case_keys"`) || strings.Contains(string(raw), `"case_id"`) {
		t.Fatalf("shareable suite file = %q, %v", raw, err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.Suites) != 1 || snapshot.Suites[0].ID != createdSuite.ID || snapshot.Suites[0].ModelTarget != "gpt-5.2" {
		t.Fatalf("filesystem suite catalog = %#v, %v", snapshot.Suites, err)
	}

	database := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	assertProductionDatabaseRetired(t, database)
}

func TestProductionPlanCanReferenceFilesystemSuiteWithoutDatabaseSuiteRow(t *testing.T) {
	configurationRoot := t.TempDir()
	executable := filepath.Join(t.TempDir(), "llm-test-studio.exe")
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.Suites) == 0 {
		t.Fatalf("filesystem suites = %#v, %v", snapshot.Suites, err)
	}
	suite := snapshot.Suites[0]
	_, err = dependencies.catalogCommands.CreatePlan(context.Background(), catalog.CreatePlanCommand{
		Name: "filesystem suite plan", SuiteID: suite.ID, SuiteRevision: suite.Revision, Cases: suite.Cases,
		LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
		SLAThresholds: map[string]float64{"e2e_p95_ms": 3_000},
	})
	if err != nil {
		t.Fatalf("CreatePlan() with filesystem suite error = %v", err)
	}
	updated, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(updated.Plans) != 1 || updated.Plans[0].SuiteID != suite.ID || updated.Plans[0].SuiteRevision != suite.Revision {
		t.Fatalf("plan referencing filesystem suite = %#v, %v", updated.Plans, err)
	}
}

func TestProductionExportsLegacyUserCaseToFilesBeforeDatabaseRetirement(t *testing.T) {
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
	repository, err := persistence.OpenLegacyCatalogRepository(context.Background(), database, persistence.RepositoryOptions{})
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
			Type:          casetypes.TypeRequestSingle, TypeVersion: 1,
			Spec: json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"legacy"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"response_schema","config":{"required":true}}]}`),
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
		caseBundle: fstest.MapFS{}, suiteBundle: fstest.MapFS{}, executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Key != legacyCase.Key ||
		snapshot.TestCases[0].Name != legacyCase.Name || snapshot.TestCases[0].ID == legacyCase.ID {
		t.Fatalf("file-only catalog after legacy export = %#v, %v; want one native file Case with legacy semantics", snapshot.TestCases, err)
	}
	casePath := filepath.Join(executableDirectory, "cases", "openai-chat", "T777", "case.json")
	casePayload, err := os.ReadFile(casePath)
	if err != nil || !json.Valid(casePayload) || !strings.Contains(string(casePayload), `"name": "legacy user case"`) {
		t.Fatalf("exported legacy Case file %s = %q, %v", casePath, casePayload, err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}
	assertProductionDatabaseRetired(t, database)
}

func TestProductionRetirementRevalidatesSourceTargetReceiptUnderAuthoredLock(t *testing.T) {
	ctx := context.Background()
	configurationRoot := t.TempDir()
	_, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(ctx, database, persistence.MigrateOptions{AppVersion: "fixture-v10"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := persistence.OpenLegacyCatalogRepository(ctx, database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	model, _, _, _ := legacyCatalogMigrationFixtures()
	model.Revision = 1
	model.UpdatedAt = model.CreatedAt
	if err := legacy.CreateModel(ctx, model); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	baseTarget := newLegacyCatalogMigrationTarget(t, root)
	target := &tamperingAuthoredCatalogRetirementTarget{
		legacyCatalogMigrationTestTarget: baseTarget,
		tamperOnListModels:               3,
		beforeListModels: func(lockCtx context.Context) error {
			return baseTarget.models.Delete(lockCtx, model.ID, model.Revision)
		},
	}
	markerPath := filepath.Join(root, ".legacy-authored-catalog-migrated.json")
	legacyCredentialStore := credentials.NewMemoryStore()
	scopedCredentialStore, err := credentials.NewScopedStore(legacyCredentialStore, strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}

	repository, err := openProductionRepositoryAfterCatalogCutover(
		ctx, database, "desktop-test", target, markerPath,
		legacyCredentialStore, scopedCredentialStore, credentials.NewMemoryCleanupQueue(),
	)
	if repository != nil {
		_ = repository.Close()
		t.Fatal("production repository unexpectedly opened after target tamper")
	}
	if !errors.Is(err, errLegacyAuthoredCatalogMigrationConflict) {
		t.Fatalf("retirement after target tamper error = %v, want migration conflict", err)
	}
	version, versionErr := persistence.SchemaVersion(ctx, database)
	if versionErr != nil || version != persistence.CatalogExportSchemaVersion {
		t.Fatalf("database version after failed receipt revalidation = %d, %v; want v10", version, versionErr)
	}
	models, listErr := baseTarget.ListModels(ctx)
	if listErr != nil || len(models) != 0 {
		t.Fatalf("tampered target was replayed before retirement: models=%#v error=%v", models, listErr)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("completion marker after failed retirement revalidation: %v", statErr)
	}
}

func TestProductionCatalogExportRunsWhileAuthoredRetirementLockHeld(t *testing.T) {
	ctx := context.Background()
	configurationRoot := t.TempDir()
	_, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(ctx, database, persistence.MigrateOptions{AppVersion: "fixture-v10"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := persistence.OpenLegacyCatalogRepository(ctx, database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	model, _, _, _ := legacyCatalogMigrationFixtures()
	model.Revision = 1
	model.UpdatedAt = model.CreatedAt
	if err := legacy.CreateModel(ctx, model); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	baseTarget := newLegacyCatalogMigrationTarget(t, root)
	target := &authoredLockRequiredMigrationTarget{legacyCatalogMigrationTestTarget: baseTarget}
	legacyCredentialStore := credentials.NewMemoryStore()
	scopedCredentialStore, err := credentials.NewScopedStore(legacyCredentialStore, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := openProductionRepositoryAfterCatalogCutover(
		ctx, database, "desktop-test", target,
		filepath.Join(root, ".legacy-authored-catalog-migrated.json"),
		legacyCredentialStore, scopedCredentialStore, credentials.NewMemoryCleanupQueue(),
	)
	if err != nil {
		t.Fatalf("catalog export under retirement lock: %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	models, err := baseTarget.ListModels(ctx)
	if err != nil || !reflect.DeepEqual(models, []domain.Model{model}) {
		t.Fatalf("exported models = %#v, %v; want %#v", models, err, model)
	}
	assertProductionDatabaseRetired(t, database)
}

type authoredLockRequiredMigrationTarget struct {
	legacyCatalogMigrationTestTarget
	retirementLockHeld bool
}

func (target *authoredLockRequiredMigrationTarget) WithAuthoredCatalogRetirementLock(
	ctx context.Context,
	action func(context.Context) error,
) error {
	if action == nil {
		return errLegacyAuthoredCatalogMigrationInvalid
	}
	target.retirementLockHeld = true
	defer func() { target.retirementLockHeld = false }()
	return action(ctx)
}

func (target *authoredLockRequiredMigrationTarget) CreateModel(ctx context.Context, value domain.Model) error {
	if !target.retirementLockHeld {
		return errors.New("catalog migration write occurred outside authored retirement lock")
	}
	return target.legacyCatalogMigrationTestTarget.CreateModel(ctx, value)
}

type tamperingAuthoredCatalogRetirementTarget struct {
	legacyCatalogMigrationTestTarget
	tamperOnListModels int
	listModelsCalls    int
	beforeListModels   func(context.Context) error
}

func (target *tamperingAuthoredCatalogRetirementTarget) ListModels(ctx context.Context) ([]domain.Model, error) {
	target.listModelsCalls++
	if target.listModelsCalls == target.tamperOnListModels && target.beforeListModels != nil {
		if err := target.beforeListModels(ctx); err != nil {
			return nil, err
		}
	}
	return target.legacyCatalogMigrationTestTarget.ListModels(ctx)
}

func TestProductionInitializerAcceptsRetiredDatabaseAndConsumesAnyCrashWindowMarker(t *testing.T) {
	tests := []struct {
		name   string
		marker string
	}{
		{name: "missing"},
		{name: "corrupt", marker: `{"schema_version":1,"completed":true,"processed_source_digests":["not-a-sha256"]}`},
		{name: "v1 checkpoint cannot authorize an already-retired database", marker: `{"schema_version":1,"completed":true,"processed_source_digests":["` + strings.Repeat("0", 64) + `"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configurationRoot := t.TempDir()
			executableDirectory := t.TempDir()
			executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
			_, database, err := productionStoragePaths(configurationRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := persistence.Migrate(context.Background(), database, persistence.MigrateOptions{AppVersion: "fixture-v10"}); err != nil {
				t.Fatal(err)
			}
			authoredCatalogDigest, err := persistence.AuthoredCatalogDigest(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			if err := persistence.Migrate(context.Background(), database, persistence.MigrateOptions{
				AppVersion:                    "fixture-v11",
				RetireAuthoredCatalog:         true,
				ExpectedAuthoredCatalogDigest: authoredCatalogDigest,
				BeforeAuthoredCatalogRetirement: func(context.Context) error {
					return nil
				},
			}); err != nil {
				t.Fatal(err)
			}
			if test.marker != "" {
				if err := os.WriteFile(filepath.Join(executableDirectory, ".llm-test-studio-authored-catalog-v1.json"), []byte(test.marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			dependencies, err := newProductionInitializer(productionOptions{
				userConfigDir:  func() (string, error) { return configurationRoot, nil },
				appVersion:     "desktop-test",
				caseBundle:     fstest.MapFS{},
				suiteBundle:    fstest.MapFS{},
				executablePath: func() (string, error) { return executable, nil },
			})(context.Background())
			if err != nil {
				t.Fatalf("production initializer error = %v", err)
			}
			if err := dependencies.close(); err != nil {
				t.Fatal(err)
			}
			markerPath := filepath.Join(executableDirectory, ".llm-test-studio-authored-catalog-v1.json")
			if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale marker stat error = %v, want consumed", err)
			}
			assertProductionDatabaseRetired(t, database)
		})
	}
}

func TestProductionInitializerDoesNotReuseReceiptAcrossOperationalDatabaseLifetimes(t *testing.T) {
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	newOptions := func(configurationRoot string) productionOptions {
		return productionOptions{
			userConfigDir:   func() (string, error) { return configurationRoot, nil },
			appVersion:      "desktop-test",
			caseBundle:      fstest.MapFS{},
			suiteBundle:     fstest.MapFS{},
			executablePath:  func() (string, error) { return executable, nil },
			credentialStore: credentials.NewMemoryStore(),
		}
	}
	firstRoot := t.TempDir()
	first, err := newProductionInitializer(newOptions(firstRoot))(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.catalogCommands.CreateModel(context.Background(), catalog.CreateModelCommand{
		Name: "legitimate post-cutover file edit", Protocol: domain.ProtocolOpenAIChat,
	})
	if err != nil {
		_ = first.close()
		t.Fatal(err)
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}

	secondRoot := t.TempDir()
	second, err := newProductionInitializer(newOptions(secondRoot))(context.Background())
	if err != nil {
		t.Fatalf("initialize a new operational database beside edited authored files: %v", err)
	}
	defer second.close()
	snapshot, err := second.catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Models) != 1 || snapshot.Models[0].ID != created.ID {
		t.Fatalf("models after new database cutover = %#v, want legitimate file edit preserved", snapshot.Models)
	}
	_, secondDatabase, err := productionStoragePaths(secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	assertProductionDatabaseRetired(t, secondDatabase)
}

func TestProductionInitializerKeepsV10WhenIntegrationBlocksCatalogRetirement(t *testing.T) {
	configurationRoot := t.TempDir()
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	_, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(context.Background(), database, persistence.MigrateOptions{AppVersion: "fixture-v10"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := persistence.OpenLegacyCatalogRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	integration := domain.Integration{
		EntityMeta: domain.EntityMeta{
			ID: "71000000-0000-4000-8000-000000000099", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Kind: domain.IntegrationNewAPI, Name: "legacy integration",
		Config: domain.IntegrationConfig{BaseURL: "https://admin.example.test"},
	}
	if err := legacy.CreateIntegration(context.Background(), integration); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		appVersion:     "desktop-test",
		caseBundle:     fstest.MapFS{},
		suiteBundle:    fstest.MapFS{},
		executablePath: func() (string, error) { return executable, nil },
	})(context.Background())
	if err == nil {
		_ = dependencies.close()
		t.Fatal("production initializer error = nil, want integration retirement block")
	}
	if !strings.Contains(err.Error(), "integrations") || !strings.Contains(err.Error(), "retirement") {
		t.Fatalf("production initializer error = %v, want integration retirement reason", err)
	}
	version, versionErr := persistence.SchemaVersion(context.Background(), database)
	if versionErr != nil || version != persistence.CatalogExportSchemaVersion {
		t.Fatalf("schema after blocked retirement = %d, %v; want v%d", version, versionErr, persistence.CatalogExportSchemaVersion)
	}
	legacy, err = persistence.OpenLegacyCatalogRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	integrations, err := legacy.ListIntegrations(context.Background())
	if err != nil || len(integrations) != 1 || integrations[0].ID != integration.ID {
		t.Fatalf("integrations after blocked retirement = %#v, %v", integrations, err)
	}
}

func TestProductionRetirementPreservesLegacyChannelBridgeAndRetriesNonChannelCleanupBeforeDrop(t *testing.T) {
	ctx := context.Background()
	configurationRoot := t.TempDir()
	executableDirectory := t.TempDir()
	executable := filepath.Join(executableDirectory, "llm-test-studio.exe")
	_, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(ctx, database, persistence.MigrateOptions{AppVersion: "fixture-v10"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := persistence.OpenLegacyCatalogRepository(ctx, database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	obsolete := legacyCredentialRefFixture("72000000-0000-4000-8000-000000000001", domain.CredentialChannelAPIKey, now, "obsolete-secret")
	current := legacyCredentialRefFixture("72000000-0000-4000-8000-000000000002", domain.CredentialChannelAPIKey, now, "current-secret")
	if err := legacy.CreateCredentialRef(ctx, obsolete); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	channel := domain.Channel{
		EntityMeta: domain.EntityMeta{
			ID: "72000000-0000-4000-8000-000000000003", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Name: "legacy channel", BaseURL: "https://legacy.example.test/v1", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, CredentialID: obsolete.ID,
	}
	if err := legacy.CreateChannel(ctx, channel); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.CreateCredentialRef(ctx, current); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	updatedChannel := channel
	updatedChannel.Revision = 2
	updatedChannel.UpdatedAt = now.Add(time.Minute)
	updatedChannel.CredentialID = current.ID
	if err := legacy.UpdateChannel(ctx, channel.Revision, updatedChannel); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	historicalChannelRef := legacyCredentialRefFixture("72000000-0000-4000-8000-000000000004", domain.CredentialChannelAPIKey, now, "historical-channel-secret")
	if err := legacy.CreateCredentialRef(ctx, historicalChannelRef); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	historicalIntegrationRef := historicalChannelRef
	historicalIntegrationRef.Revision = 2
	historicalIntegrationRef.UpdatedAt = now.Add(time.Minute)
	historicalIntegrationRef.Purpose = domain.CredentialIntegrationAdmin
	historicalIntegrationRef.StoreRef = "llm-test-studio/v1/integration_admin/" + historicalIntegrationRef.ID
	historicalIntegrationRef.Fingerprint = credentialFingerprint("historical-integration-secret")
	if err := legacy.UpdateCredentialRef(ctx, historicalChannelRef.Revision, historicalIntegrationRef); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	baseStore := credentials.NewMemoryStore()
	obsoleteRef, err := credentials.StoreRefFromCredential(obsolete)
	if err != nil {
		t.Fatal(err)
	}
	currentRef, err := credentials.StoreRefFromCredential(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, obsoleteRef, []byte("obsolete-secret")); err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, currentRef, []byte("current-secret")); err != nil {
		t.Fatal(err)
	}
	historicalChannelStoreRef, err := credentials.StoreRefFromCredential(historicalChannelRef)
	if err != nil {
		t.Fatal(err)
	}
	historicalIntegrationStoreRef, err := credentials.StoreRefFromCredential(historicalIntegrationRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, historicalChannelStoreRef, []byte("historical-channel-secret")); err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(ctx, historicalIntegrationStoreRef, []byte("historical-integration-secret")); err != nil {
		t.Fatal(err)
	}
	wantCleanupFailure := errors.New("injected keyring delete failure")
	store := &retirementDeleteFailingCredentialStore{
		Store: baseStore, failRef: historicalIntegrationStoreRef, err: wantCleanupFailure,
	}
	credentialScope, err := credentialScopeForAuthoredCatalogRoot(executableDirectory)
	if err != nil {
		t.Fatal(err)
	}
	scopedStore, err := credentials.NewScopedStore(baseStore, credentialScope)
	if err != nil {
		t.Fatal(err)
	}
	options := productionOptions{
		userConfigDir:   func() (string, error) { return configurationRoot, nil },
		appVersion:      "desktop-test",
		caseBundle:      fstest.MapFS{},
		suiteBundle:     fstest.MapFS{},
		executablePath:  func() (string, error) { return executable, nil },
		credentialStore: store,
	}
	dependencies, err := newProductionInitializer(options)(ctx)
	if err == nil {
		_ = dependencies.close()
		t.Fatal("production initializer error = nil, want keyring cleanup failure")
	}
	if !errors.Is(err, wantCleanupFailure) || !strings.Contains(err.Error(), historicalIntegrationRef.ID) {
		t.Fatalf("production initializer error = %v, want attributable keyring cleanup failure", err)
	}
	if got, versionErr := persistence.SchemaVersion(ctx, database); versionErr != nil || got != persistence.CatalogExportSchemaVersion {
		t.Fatalf("schema after failed cleanup = %d, %v; want v%d", got, versionErr, persistence.CatalogExportSchemaVersion)
	}
	markerPath := filepath.Join(executableDirectory, ".llm-test-studio-authored-catalog-v1.json")
	if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("receipt after pre-DROP cleanup failure stat error = %v, want consumed for safe replay", statErr)
	}
	legacy, err = persistence.OpenLegacyCatalogRepository(ctx, database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	credentialRefs, listErr := legacy.ListCredentialRefHistory(ctx)
	closeErr := legacy.Close()
	if listErr != nil || closeErr != nil || len(credentialRefs) != 4 {
		t.Fatalf("credential refs after failed cleanup = %#v, list=%v close=%v; want retry manifest", credentialRefs, listErr, closeErr)
	}
	if err := baseStore.Test(ctx, obsoleteRef); err != nil {
		t.Fatalf("obsolete key after failed delete = %v, want retained", err)
	}

	store.err = nil
	dependencies, err = newProductionInitializer(options)(ctx)
	if err != nil {
		t.Fatalf("production initializer retry error = %v", err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Test(ctx, obsoleteRef); err != nil {
		t.Fatalf("legacy channel bridge after successful retirement = %v, want retained", err)
	}
	// Another authored root can still reference A's locally obsolete legacy
	// credential and must be able to create its own scoped copy later.
	otherTarget := newLegacyCatalogMigrationTarget(t, t.TempDir())
	if err := otherTarget.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	otherScopedStore, err := credentials.NewScopedStore(baseStore, strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateUnscopedReferencedCredentials(
		ctx, otherTarget, baseStore, otherScopedStore, credentials.NewMemoryCleanupQueue(),
	); err != nil {
		t.Fatalf("migrate locally obsolete credential for another authored root: %v", err)
	}
	if err := otherScopedStore.Test(ctx, obsoleteRef); err != nil {
		t.Fatalf("other authored root scoped bridge = %v, want migrated", err)
	}
	if err := baseStore.Test(ctx, currentRef); err != nil {
		t.Fatalf("legacy current channel migration bridge after successful retirement = %v, want retained", err)
	}
	if err := scopedStore.Test(ctx, currentRef); err != nil {
		t.Fatalf("scoped current channel key after successful retirement = %v, want retained", err)
	}
	if err := baseStore.Test(ctx, historicalChannelStoreRef); err != nil {
		t.Fatalf("historical channel migration bridge after successful retirement = %v, want retained", err)
	}
	if err := baseStore.Test(ctx, historicalIntegrationStoreRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("historical non-channel key after successful retirement = %v, want not found", err)
	}
	assertProductionDatabaseRetired(t, database)
}

func TestRetireLegacyCredentialReferencesKeepsCurrentChannelAndImmutablePlanBindingKeys(t *testing.T) {
	ctx := context.Background()
	target := newLegacyCatalogMigrationTarget(t, t.TempDir())
	model, oldChannel, mapping, plan := legacyCatalogMigrationFixtures()
	at := oldChannel.CreatedAt
	oldCredential := legacyCredentialRefFixture("72100000-0000-4000-8000-000000000001", domain.CredentialChannelAPIKey, at, "retained-secret")
	currentCredential := legacyCredentialRefFixture("72100000-0000-4000-8000-000000000002", domain.CredentialChannelAPIKey, at, "retained-secret")
	oldChannel.Revision = 1
	oldChannel.UpdatedAt = oldChannel.CreatedAt
	oldChannel.CredentialID = oldCredential.ID
	model.Revision = 1
	model.UpdatedAt = model.CreatedAt
	mapping.Revision = 1
	mapping.UpdatedAt = mapping.CreatedAt
	currentChannel := oldChannel
	currentChannel.Revision = 2
	currentChannel.UpdatedAt = currentChannel.CreatedAt.Add(time.Minute)
	currentChannel.CredentialID = currentCredential.ID
	plan.Revision = 1
	plan.UpdatedAt = plan.CreatedAt
	if err := target.CreateModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := target.CreateChannel(ctx, oldChannel); err != nil {
		t.Fatal(err)
	}
	if err := target.UpdateChannel(ctx, oldChannel.Revision, currentChannel); err != nil {
		t.Fatal(err)
	}
	if err := target.CreatePlanDocument(ctx, plancatalog.Document{
		FileSchemaVersion: plancatalog.CurrentFileSchemaVersion,
		Plan:              plan,
		TargetBindings: []plancatalog.TargetBinding{{
			Model: model, Channel: oldChannel, Mapping: mapping,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	store := credentials.NewMemoryStore()
	scopedStore, err := credentials.NewScopedStore(store, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []domain.CredentialRef{oldCredential, currentCredential} {
		ref, err := credentials.StoreRefFromCredential(credential)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set(ctx, ref, []byte("retained-secret")); err != nil {
			t.Fatal(err)
		}
	}
	queue := credentials.NewMemoryCleanupQueue()
	if err := retireLegacyCredentialReferences(
		ctx, []domain.CredentialRef{oldCredential, currentCredential}, target, store, scopedStore, queue,
	); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []domain.CredentialRef{oldCredential, currentCredential} {
		ref, _ := credentials.StoreRefFromCredential(credential)
		if err := store.Test(ctx, ref); err != nil {
			t.Fatalf("legacy referenced migration bridge %s was deleted: %v", ref, err)
		}
		if err := scopedStore.Test(ctx, ref); err != nil {
			t.Fatalf("scoped referenced key %s was not retained: %v", ref, err)
		}
	}
	registry, err := queue.List(ctx)
	if err != nil || len(registry) != 2 || registry[0] != oldCredential.ID || registry[1] != currentCredential.ID {
		t.Fatalf("credential registry = %#v, %v; want current and immutable Plan binding ids", registry, err)
	}
}

func TestCopyLegacyCredentialToScopedStoreReconcilesRetryStatesWithoutReplacingSecrets(t *testing.T) {
	ctx := context.Background()
	const legacySecret = "authoritative-legacy-secret"
	tests := []struct {
		name               string
		legacySecret       string
		scopedSecret       string
		writeThenFail      bool
		wantErr            bool
		wantScopedSecret   string
		wantScopedNotFound bool
	}{
		{name: "initial copy", legacySecret: legacySecret, wantScopedSecret: legacySecret},
		{name: "idempotent same value", legacySecret: legacySecret, scopedSecret: legacySecret, wantScopedSecret: legacySecret},
		{name: "different scoped value", legacySecret: legacySecret, scopedSecret: "different", wantErr: true, wantScopedSecret: "different"},
		{name: "legacy missing after copy", scopedSecret: legacySecret, wantScopedSecret: legacySecret},
		{name: "legacy and scoped missing", wantScopedNotFound: true},
		{name: "legacy missing with wrong scoped value", scopedSecret: "different", wantErr: true, wantScopedSecret: "different"},
		{name: "set writes then reports failure", legacySecret: legacySecret, writeThenFail: true, wantScopedSecret: legacySecret},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := credentials.NewMemoryStore()
			scopeCharacters := "abcdef0"
			scoped, err := credentials.NewScopedStore(base, strings.Repeat(scopeCharacters[index:index+1], 64))
			if err != nil {
				t.Fatal(err)
			}
			ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, "72200000-0000-4000-8000-000000000001")
			if err != nil {
				t.Fatal(err)
			}
			credential := legacyCredentialRefFixture(ref.ID(), ref.Purpose(), time.Now().UTC(), legacySecret)
			if test.legacySecret != "" {
				if err := base.Set(ctx, ref, []byte(test.legacySecret)); err != nil {
					t.Fatal(err)
				}
			}
			if test.scopedSecret != "" {
				if err := scoped.Set(ctx, ref, []byte(test.scopedSecret)); err != nil {
					t.Fatal(err)
				}
			}
			var target credentials.Store = scoped
			if test.writeThenFail {
				target = &setWritesThenFailsCredentialStore{Store: scoped, err: errors.New("injected ambiguous set failure")}
			}

			err = copyLegacyCredentialToScopedStore(ctx, base, target, credential)
			if (err != nil) != test.wantErr {
				t.Fatalf("copyLegacyCredentialToScopedStore() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantScopedNotFound {
				if err := scoped.Test(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
					t.Fatalf("scoped credential = %v, want ErrNotFound", err)
				}
				return
			}
			lease, err := scoped.Get(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := lease.Bytes()
			if err != nil {
				_ = lease.Close()
				t.Fatal(err)
			}
			if err := lease.Close(); err != nil {
				clear(actual)
				t.Fatal(err)
			}
			if string(actual) != test.wantScopedSecret {
				clear(actual)
				t.Fatalf("scoped secret differs from expected retry state")
			}
			clear(actual)
		})
	}
}

type retirementDeleteFailingCredentialStore struct {
	credentials.Store
	failRef credentials.StoreRef
	err     error
}

type setWritesThenFailsCredentialStore struct {
	credentials.Store
	err error
}

func (store *setWritesThenFailsCredentialStore) Set(ctx context.Context, ref credentials.StoreRef, secret []byte) error {
	if err := store.Store.Set(ctx, ref, secret); err != nil {
		return err
	}
	return store.err
}

func (store *retirementDeleteFailingCredentialStore) Delete(ctx context.Context, ref credentials.StoreRef) error {
	if ref == store.failRef && store.err != nil {
		return store.err
	}
	return store.Store.Delete(ctx, ref)
}

func legacyCredentialRefFixture(id string, purpose domain.CredentialPurpose, at time.Time, secret string) domain.CredentialRef {
	return domain.CredentialRef{
		EntityMeta: domain.EntityMeta{
			ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: at, UpdatedAt: at,
		},
		StoreRef: "llm-test-studio/v1/" + string(purpose) + "/" + id,
		Purpose:  purpose, MaskedSuffix: "Ab12", Fingerprint: credentialFingerprint(secret),
	}
}

func credentialFingerprint(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func assertProductionDatabaseRetired(t *testing.T, database string) {
	t.Helper()
	version, err := persistence.SchemaVersion(context.Background(), database)
	if err != nil || version != persistence.AuthoredCatalogRetirementSchemaVersion {
		t.Fatalf("production schema version = %d, %v; want v%d", version, err, persistence.AuthoredCatalogRetirementSchemaVersion)
	}
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatalf("open production database for schema inspection: %v", err)
	}
	defer db.Close()
	for _, table := range []string{
		"integrations", "plan_channel_models", "plan_cases", "plan_channels", "plan_models", "test_plans",
		"suite_cases", "test_case_import_sources", "channel_models", "channels", "credential_refs",
		"test_suites", "test_cases", "models", "catalog_tombstones", "case_catalog_cutover",
		"pending_test_case_snapshots", "builtin_catalog_seeds",
	} {
		var count int
		if err := db.QueryRowContext(context.Background(), `
			SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&count); err != nil {
			t.Fatalf("inspect retired table %q: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("retired authored table %q still exists", table)
		}
	}
}

func productionLegacyCase(name string) string {
	return `{"schema_version":2,"key":"T001","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}}}`
}
