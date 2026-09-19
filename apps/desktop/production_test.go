package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
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
	assertProductionOperationalSchemaV1(t, database)
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
			len(catalogSnapshot.Plans) != 0 || len(catalogSnapshot.TestCases) != 711 || len(catalogSnapshot.Suites) != 44 {
		t.Fatalf("initialized catalog cardinalities = models:%d channels:%d mappings:%d cases:%d suites:%d plans:%d, want 711 file-backed cases and 44 scenario suites",
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
	if runnable != 410 || disabled != 194 || manual != 107 {
		t.Fatalf("built-in case policy counts = runnable:%d disabled:%d manual:%d, want 410/194/107", runnable, disabled, manual)
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
	assertProductionOperationalSchemaV1(t, database)
	if len(firstSnapshot.Suites) != 44 {
		t.Fatalf("file suites = %d, want 44", len(firstSnapshot.Suites))
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

	if len(firstSnapshot.TestCases) != 711 || len(secondSnapshot.TestCases) != 711 {
		t.Fatalf("case counts across restart = %d/%d, want 711/711", len(firstSnapshot.TestCases), len(secondSnapshot.TestCases))
	}
	if len(firstSnapshot.Suites) != 44 || len(secondSnapshot.Suites) != 44 || len(firstSnapshot.Plans) != 0 || len(secondSnapshot.Plans) != 0 {
		t.Fatalf("suite/plan counts across restart = %d/%d suites, %d/%d plans, want 44 scenario suites and no plans",
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
		"Wan 3.0 标准版 · 参数拒绝测试":            {target: "wan3.0-video", count: 35},
		"Wan 3.0 标准版 · 完整测试（自动可执行）":       {target: "wan3.0-video", count: 70},
		"Wan 3.0 标准版 · 完整矩阵（含禁用模板）":       {target: "wan3.0-video", count: 187},
		"Wan 3.0 Prime · 连通性测试":           {target: "wan3.0-video-prime", count: 1},
		"Wan 3.0 Prime · 基本功能测试":          {target: "wan3.0-video-prime", count: 6},
		"Wan 3.0 Prime · 参数拒绝测试":          {target: "wan3.0-video-prime", count: 35},
		"Wan 3.0 Prime · 完整测试（自动可执行）":     {target: "wan3.0-video-prime", count: 70},
		"Wan 3.0 Prime · 完整矩阵（含禁用模板）":     {target: "wan3.0-video-prime", count: 187},
		"Wan 2.7 文生视频边界套件":                {target: "wan2.7-t2v", count: 7},
		"Wan 2.7 2026-06-12 快照边界套件":       {target: "wan2.7-t2v-2026-06-12", count: 7},
		"Wan 2.6 文生视频边界套件":                {target: "wan2.6-t2v", count: 4},
		"Wan 2.5 文生视频边界套件":                {target: "wan2.5-t2v-preview", count: 2},
		"Wan 2.2 文生视频边界套件":                {target: "wan2.2-t2v-plus", count: 3},
		"Wan 2.1 Turbo 边界套件":              {target: "wanx2.1-t2v-turbo", count: 3},
		"Wan 2.1 Plus 边界套件":               {target: "wanx2.1-t2v-plus", count: 3},
		"MiniMax H3 连通性测试套件":              {target: "MiniMax-H3", count: 1},
		"MiniMax H3 基本功能测试套件":             {target: "MiniMax-H3", count: 21},
		"MiniMax H3 参数拒绝测试套件":             {target: "MiniMax-H3", count: 42},
		"MiniMax H3 自动化核心回归套件":            {target: "MiniMax-H3", count: 43},
		"MiniMax H3 视频生成完整边界套件":           {target: "MiniMax-H3", count: 144},
		"GLM 5.3 连通性套件":                   {target: "glm-5.3", count: 1},
		"GLM 5.3 基本功能套件":                  {target: "glm-5.3", count: 10},
		"GLM 5.3 参数拒绝套件":                  {target: "glm-5.3", count: 96},
		"GLM 5.3 自动回归套件":                  {target: "glm-5.3", count: 158},
		"GLM 5.3 完整设计（含禁用模板）套件":           {target: "glm-5.3", count: 205},
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
			if !found || testCase.Protocol != firstSuite.Protocol {
				t.Fatalf("suite %q includes case %+v with protocol %s", name, ref, testCase.Protocol)
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
	firstBundle := fstest.MapFS{casePath: &fstest.MapFile{Data: []byte(productionCaseDocument("first bundle"))}}
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

	userDirectory := filepath.Join(executableDirectory, "data", "cases", "openai-chat", "T001")
	if err := os.MkdirAll(userDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDirectory, "case.json"), []byte(productionCaseDocument("user customization")), 0o600); err != nil {
		t.Fatal(err)
	}

	secondBundle := fstest.MapFS{casePath: &fstest.MapFile{Data: []byte(productionCaseDocument("second bundle"))}}
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
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    "openai-chat", TypeVersion: 1,
		Spec: json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"http","source":"http.status","operator":"equals","value":200}]}`),
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	path := filepath.Join(executableDirectory, "data", "cases", "openai-chat", "T900", "case.json")
	if raw, err := os.ReadFile(path); err != nil || !json.Valid(raw) || strings.Contains(string(raw), `"model_targets"`) || !strings.Contains(string(raw), `"openai-chat"`) {
		t.Fatalf("shareable case file = %q, %v", raw, err)
	}
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T900", Name: "must not replace", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    "openai-chat", TypeVersion: 1,
		Spec: json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"replacement"}]}},"assertions":[{"id":"http","source":"http.status","operator":"equals","value":200}]}`),
	})
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("duplicate CreateTestCase() error = %v, want ErrConflict", err)
	}
	_, err = dependencies.catalogCommands.CreateTestCase(context.Background(), catalog.CreateTestCaseCommand{
		Key: "T901", Name: "reserved", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: false, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    "removed.protocol", TypeVersion: 1,
		Spec: json.RawMessage(`{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}`),
	})
	if !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("reserved CreateTestCase() error = %v, want ErrInvalid", err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.TestCases) != 1 || snapshot.TestCases[0].Key != "T900" || snapshot.TestCases[0].Name != "shareable" {
		t.Fatalf("filesystem catalog after create = %#v, %v", snapshot.TestCases, err)
	}
}

func TestProductionModelWithoutCapabilitiesReturnsJSONArray(t *testing.T) {
	root := t.TempDir()
	configurationRoot := t.TempDir()
	app := newDesktopApp(newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(root, "app.exe"), nil },
		appVersion:     "test",
	}))
	app.onStartup(context.Background())
	defer app.shutdown()
	snapshot, err := app.CreateModel(catalog.CreateModelCommand{
		Name: "Optional capabilities", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyModelCapabilities := func(snapshot catalog.Snapshot) {
		t.Helper()
		raw, err := json.Marshal(snapshot.Models)
		if err != nil || !strings.Contains(string(raw), `"capabilities":[]`) {
			t.Fatalf("model snapshot must encode an empty array: %s, %v", raw, err)
		}
	}
	assertEmptyModelCapabilities(snapshot)
	// Reload the persisted model, whose optional capabilities field is omitted.
	reloaded, err := app.GetCatalog()
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyModelCapabilities(reloaded)
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
		Type:                    "openai-chat", TypeVersion: 1,
		Spec: json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"http","source":"http.status","operator":"equals","value":200}]}`),
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	createdSuite, err := dependencies.catalogCommands.CreateSuite(context.Background(), catalog.CreateSuiteCommand{
		Key: "file-backed-suite", Name: "file-backed suite", Protocol: domain.ProtocolOpenAIChat,
		Cases: []catalog.CaseInput{{CaseID: createdCase.ID}},
	})
	if err != nil {
		t.Fatalf("CreateSuite() error = %v", err)
	}
	createdPlan, err := dependencies.catalogCommands.CreatePlan(context.Background(), catalog.CreatePlanCommand{
		Name: "file-backed plan", Protocol: domain.ProtocolOpenAIChat, Seed: 1,
		Entries: []catalog.PlanEntryInput{{
			TargetKind: domain.PlanTargetSuite, TargetID: createdSuite.ID,
			LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
			SLAThresholds: map[string]float64{"e2e_p95_ms": 3_000}, Parameters: map[string]json.RawMessage{},
		}},
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	modelsPath := filepath.Join(executableDirectory, "data", "models.json")
	modelsRaw, err := os.ReadFile(modelsPath)
	if err != nil || !json.Valid(modelsRaw) || !strings.Contains(string(modelsRaw), createdModel.ID) || !strings.Contains(string(modelsRaw), "file-backed model") {
		t.Fatalf("model catalog file = %q, %v", modelsRaw, err)
	}
	planPath := filepath.Join(executableDirectory, "data", "plans", createdPlan.ID+".json")
	planRaw, err := os.ReadFile(planPath)
	if err != nil || !json.Valid(planRaw) || !strings.Contains(string(planRaw), createdPlan.ID) || strings.Contains(string(planRaw), createdCase.ID) {
		t.Fatalf("plan file = %q, %v", planRaw, err)
	}
	entries, err := os.ReadDir(executableDirectory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "data" || !entries[0].IsDir() {
		t.Fatalf("authored files and locks must stay under data: entries = %v, err = %v", entries, err)
	}

	database := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	assertProductionOperationalSchemaV1(t, database)
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

	channelsPath := filepath.Join(executableDirectory, "data", "channels.json")
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
	credentialScope, err := credentialScopeForRoot(executableDirectory)
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
	assertProductionOperationalSchemaV1(t, database)
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

	channelsRaw, err := os.ReadFile(filepath.Join(rootA, "data", "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootB, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "data", "channels.json"), channelsRaw, 0o600); err != nil {
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
	scopeA, err := credentialScopeForRoot(rootA)
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
		Key: "T900", Name: "shareable", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: false,
		Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    "openai-chat", TypeVersion: 1,
		Spec: json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"http","source":"http.status","operator":"equals","value":200}]}`),
	})
	if err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	createdSuite, err := dependencies.catalogCommands.CreateSuite(context.Background(), catalog.CreateSuiteCommand{
		Key: "gpt-5.2-smoke", Name: "GPT-5.2 smoke", Protocol: domain.ProtocolOpenAIChat,
		Cases: []catalog.CaseInput{{CaseID: createdCase.ID}},
	})
	if err != nil {
		t.Fatalf("CreateSuite() error = %v", err)
	}

	path := filepath.Join(executableDirectory, "data", "suites", "openai-chat", "gpt-5.2-smoke", "suite.json")
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) || strings.Contains(string(raw), `"case_keys"`) || !strings.Contains(string(raw), `"case_id"`) {
		t.Fatalf("shareable suite file = %q, %v", raw, err)
	}
	snapshot, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.Suites) != 1 || snapshot.Suites[0].ID != createdSuite.ID || snapshot.Suites[0].Protocol != domain.ProtocolOpenAIChat {
		t.Fatalf("filesystem suite catalog = %#v, %v", snapshot.Suites, err)
	}

	database := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	assertProductionOperationalSchemaV1(t, database)
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
		Name: "filesystem suite plan", Protocol: suite.Protocol, Seed: 1,
		Entries: []catalog.PlanEntryInput{{
			TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
			LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
			SLAThresholds: map[string]float64{"e2e_p95_ms": 3_000}, Parameters: map[string]json.RawMessage{},
		}},
	})
	if err != nil {
		t.Fatalf("CreatePlan() with filesystem suite error = %v", err)
	}
	updated, err := dependencies.catalog.Snapshot(context.Background())
	if err != nil || len(updated.Plans) != 1 || len(updated.Plans[0].Entries) != 1 ||
		updated.Plans[0].Entries[0].TargetID != suite.ID {
		t.Fatalf("plan referencing filesystem suite = %#v, %v", updated.Plans, err)
	}
}

func assertProductionOperationalSchemaV1(t *testing.T, database string) {
	t.Helper()
	version, err := persistence.SchemaVersion(context.Background(), database)
	if err != nil || version != persistence.CurrentSchemaVersion {
		t.Fatalf("production schema version = %d, %v; want operational v%d", version, err, persistence.CurrentSchemaVersion)
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
			t.Fatalf("authored table %q still exists in operational schema v1", table)
		}
	}
}

func productionCaseDocument(name string) string {
	return `{"schema_version":1,"key":"T001","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":1,"type":"openai-chat","type_version":1,"spec":{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[]}}}`
}
