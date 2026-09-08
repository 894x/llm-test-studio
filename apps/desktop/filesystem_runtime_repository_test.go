package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestFilesystemRuntimeRepositoryStartsRunWithPinnedCaseRevisionAfterCurrentCaseChanges(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	models, err := modelcatalog.New(filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	suites, err := suitecatalog.New(suitecatalog.Options{
		Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites"), Cases: cases,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	model := domain.Model{
		EntityMeta: meta("41000000-0000-4000-8000-000000000001"),
		Name:       "file model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"},
	}
	channel := domain.Channel{
		EntityMeta: meta("41000000-0000-4000-8000-000000000002"),
		Name:       "file channel", BaseURL: "https://api.example.test/v1", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, CredentialID: "41000000-0000-4000-8000-000000000003",
	}
	mapping := domain.ChannelModel{
		EntityMeta: meta("41000000-0000-4000-8000-000000000004"),
		ChannelID:  channel.ID, ModelID: model.ID, UpstreamModelName: "upstream-model",
	}
	if err := models.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	testCase := domain.TestCase{
		EntityMeta: meta("41000000-0000-4000-8000-000000000005"),
		Key:        "T970", Name: "file case", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          casetypes.TypeRequestSingle,
			TypeVersion:   1,
			Spec:          json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hi"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`),
		},
	}
	if err := cases.SaveCase(ctx, string(domain.ProtocolOpenAIChat), testCase.Key, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("case entries = %#v, %v", caseEntries, err)
	}
	caseEntry := caseEntries[0]
	suite := filesystemCatalogSuiteFixture(caseEntry.TestCase)
	if err := suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("suite entries = %#v, %v", suiteEntries, err)
	}
	suite = suiteEntries[0].Suite
	plan := domain.Plan{
		EntityMeta: meta("41000000-0000-4000-8000-000000000006"),
		Name:       "file plan", ModelIDs: []string{model.ID}, ChannelIDs: []string{channel.ID},
	}
	filesystemCatalogSetPlanSuites(&plan, suite)
	catalogRepository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), models: models, channels: channels,
		cases: cases, suites: suites, plans: plans,
	}
	if err := catalogRepository.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	newModel := model
	newModel.Revision++
	newModel.UpdatedAt = newModel.UpdatedAt.Add(time.Minute)
	newModel.Name = "new current model"
	if err := models.Update(ctx, model.Revision, newModel); err != nil {
		t.Fatalf("Update(model) error = %v", err)
	}
	newChannel := channel
	newChannel.Revision++
	newChannel.UpdatedAt = newChannel.UpdatedAt.Add(time.Minute)
	newChannel.Name = "new current channel"
	newChannel.BaseURL = "https://new.example.test/v1"
	newChannel.CredentialID = "41000000-0000-4000-8000-000000000007"
	if err := channels.UpdateChannel(ctx, channel.Revision, newChannel); err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	newMapping := mapping
	newMapping.Revision++
	newMapping.UpdatedAt = newMapping.UpdatedAt.Add(time.Minute)
	newMapping.UpstreamModelName = "new-upstream-model"
	if err := channels.UpdateMapping(ctx, mapping.Revision, newMapping); err != nil {
		t.Fatalf("UpdateMapping() error = %v", err)
	}
	updatedCase := caseEntry.TestCase
	updatedCase.Name = "updated file case"
	if err := cases.SaveCase(ctx, caseEntry.Group, caseEntry.Directory, updatedCase); err != nil {
		t.Fatalf("SaveCase(updated) error = %v", err)
	}
	currentCase, err := cases.Find(ctx, caseEntry.TestCase.ID)
	if err != nil {
		t.Fatal(err)
	}
	if currentCase.TestCase.Revision == caseEntry.TestCase.Revision {
		t.Fatal("case update did not create a new semantic revision")
	}

	database := filepath.Join(root, "runtime.db")
	if err := sqlite.Migrate(ctx, database, sqlite.MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	operational, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	t.Cleanup(func() { _ = operational.Close() })
	repository := filesystemRuntimeRepository{Repository: operational, catalog: catalogRepository}
	store := credentials.NewMemoryStore()
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, storeRef, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	executor := &filesystemRuntimeCaseExecutor{requests: make(chan runs.ExecutionRequest, 1)}
	environment := domain.EnvironmentSnapshot{
		OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct",
		AppVersion: "test", EngineVersion: "test",
	}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock:       &filesystemRuntimeClock{next: now},
		Environment: func() domain.EnvironmentSnapshot { return environment },
	})
	if err != nil {
		t.Fatalf("runs.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("runs.Service.Close() error = %v", err)
		}
	})

	runID, err := service.PrepareTarget(ctx, runs.StartCommand{PlanID: plan.ID})
	if err != nil {
		t.Fatalf("PrepareTarget() error = %v", err)
	}
	queued, err := repository.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetRun(queued) error = %v", err)
	}
	queuedSnapshot := queued.Snapshot()
	if len(queuedSnapshot.Suites) != 1 || len(queuedSnapshot.Suites[0].CaseDefinitions) != 1 ||
		queuedSnapshot.Suites[0].CaseDefinitions[0].Revision != caseEntry.TestCase.Revision ||
		queuedSnapshot.Suites[0].CaseDefinitions[0].Name != caseEntry.TestCase.Name {
		t.Fatalf("queued run suite definitions = %#v, want pinned revision %#v", queuedSnapshot.Suites, caseEntry.TestCase)
	}
	if queuedSnapshot.Model.Revision != model.Revision || queuedSnapshot.Model.Name != model.Name ||
		queuedSnapshot.Channel.Revision != channel.Revision || queuedSnapshot.Channel.BaseURL != channel.BaseURL ||
		queuedSnapshot.Mapping == nil || queuedSnapshot.Mapping.Revision != mapping.Revision ||
		queuedSnapshot.Mapping.UpstreamModelName != mapping.UpstreamModelName {
		t.Fatalf("queued run target drifted from Plan bindings: %#v", queuedSnapshot)
	}
	// Activating reloads the canonical SQLite document and validates the next
	// state against the file-derived in-memory Run.
	if err := service.ActivateRun(ctx, runID); err != nil {
		t.Fatalf("ActivateRun() error = %v", err)
	}
	select {
	case request := <-executor.requests:
		if len(request.Cases) != 1 || request.Cases[0].Revision != caseEntry.TestCase.Revision || request.Cases[0].Name != caseEntry.TestCase.Name {
			t.Fatalf("execution cases = %#v, want pinned revision %#v", request.Cases, caseEntry.TestCase)
		}
		snapshot := request.Run.Snapshot()
		if snapshot.Model.ID != model.ID || snapshot.Channel.ID != channel.ID || snapshot.Mapping == nil || snapshot.Mapping.ID != mapping.ID ||
			len(snapshot.Suites) != 1 || len(snapshot.Suites[0].CaseDefinitions) != 1 ||
			snapshot.Suites[0].CaseDefinitions[0].Revision != caseEntry.TestCase.Revision {
			t.Fatalf("immutable run snapshot = %#v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run executor was not started")
	}
}

type filesystemRuntimeClock struct {
	mu   sync.Mutex
	next time.Time
}

func (clock *filesystemRuntimeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	result := clock.next
	clock.next = clock.next.Add(time.Millisecond)
	return result
}

type filesystemRuntimeCaseExecutor struct {
	requests chan runs.ExecutionRequest
}

func (executor *filesystemRuntimeCaseExecutor) Execute(_ context.Context, request runs.ExecutionRequest, _ func(runs.ResultDraft) error) error {
	executor.requests <- request
	return nil
}
