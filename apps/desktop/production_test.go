package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
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
	storedSuites, suitesErr := repository.ListSuites(context.Background())
	_ = repository.Close()
	if err != nil || suitesErr != nil || len(firstSnapshot.Suites) != 26 || len(storedCases) != 0 || len(storedSuites) != 0 {
		t.Fatalf("file suites = %d, database case snapshots = %d, database suites = %d, errors = %v/%v; want 26/0/0", len(firstSnapshot.Suites), len(storedCases), len(storedSuites), err, suitesErr)
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
	repository, err := persistence.OpenRepository(context.Background(), database, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	storedSuites, err := repository.ListSuites(context.Background())
	_ = repository.Close()
	if err != nil || len(storedSuites) != 0 {
		t.Fatalf("database suites = %#v, %v; want none", storedSuites, err)
	}
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

func TestProductionReadsCaseCatalogOnlyFromFilesWithoutLegacyDatabaseCutover(t *testing.T) {
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
	if err != nil || len(snapshot.TestCases) != 0 {
		t.Fatalf("file-only catalog = %#v, %v; want no cases from SQLite", snapshot.TestCases, err)
	}
	casePath := filepath.Join(executableDirectory, "cases", "openai-chat", "T777", "case.json")
	if _, err := os.Stat(casePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy database case was exported to %s: %v", casePath, err)
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
	if err != nil || len(remaining) != 1 || remaining[0].ID != legacyCase.ID {
		t.Fatalf("legacy database cases after file-only startup = %#v, %v; want untouched legacy row", remaining, err)
	}
}

func productionLegacyCase(name string) string {
	return `{"schema_version":2,"key":"T001","name":"` + name + `","dimension":"boundary","protocol":"openai-chat","enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}}}`
}
