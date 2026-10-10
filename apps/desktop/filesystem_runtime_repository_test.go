package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestFilesystemRuntimeRepositoryResolvesCurrentTargetsAndCasesAtRunStart(t *testing.T) {
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
		Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites"),
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
		Name:       "file model", Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, Capabilities: []string{"chat"},
	}
	channel := domain.Channel{
		EntityMeta: meta("41000000-0000-4000-8000-000000000002"),
		Name:       "file channel", BaseURL: "https://api.example.test/v1",
		Enabled: true, CredentialID: "41000000-0000-4000-8000-000000000003",
	}
	mapping := domain.ChannelModel{Protocols: []domain.Protocol{domain.ProtocolOpenAIChat},
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
		Key:        "T970", Name: "file case", Dimension: "compatibility",
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic, Definitions: domain.ProtocolDefinitions{domain.Protocol(domain.CaseType("openai-chat")): json.RawMessage(`{"request":{"body":{"messages":[{"role":"user","content":"hi"}]}},"inputs":{},"assertions":[]}`)},
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
		Name:       "file plan", Protocol: domain.ProtocolOpenAIChat, Seed: 1,
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
	newMapping.UpstreamModelName = "upstream-model"
	if err := channels.UpdateMapping(ctx, mapping.Revision, newMapping); err != nil {
		t.Fatalf("UpdateMapping() error = %v", err)
	}
	updatedCase := caseEntry.TestCase
	updatedCase.Name = "updated file case"
	if err := catalogRepository.UpdateTestCase(ctx, caseEntry.TestCase.Revision, updatedCase); err != nil {
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
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, newChannel.CredentialID)
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

	runID, err := service.PrepareTarget(ctx, runs.StartCommand{PlanID: plan.ID, ModelID: model.ID, ChannelID: channel.ID})
	if err != nil {
		t.Fatalf("PrepareTarget() error = %v", err)
	}
	queued, err := repository.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetRun(queued) error = %v", err)
	}
	queuedSnapshot := queued.Snapshot()
	if len(queuedSnapshot.Entries) != 1 || len(queuedSnapshot.Entries[0].CaseDefinitions) != 1 ||
		queuedSnapshot.Entries[0].CaseDefinitions[0].Revision != currentCase.TestCase.Revision ||
		queuedSnapshot.Entries[0].CaseDefinitions[0].Name != currentCase.TestCase.Name {
		t.Fatalf("queued run suite definitions = %#v, want pinned revision %#v", queuedSnapshot.Entries, caseEntry.TestCase)
	}
	if queuedSnapshot.Model.Revision != newModel.Revision || queuedSnapshot.Model.Name != newModel.Name ||
		queuedSnapshot.Channel.Revision != newChannel.Revision || queuedSnapshot.Channel.BaseURL != newChannel.BaseURL ||
		queuedSnapshot.Mapping == nil || queuedSnapshot.Mapping.Revision != newMapping.Revision ||
		queuedSnapshot.Mapping.UpstreamModelName != mapping.UpstreamModelName {
		t.Fatalf("queued run did not resolve current targets: %#v", queuedSnapshot)
	}
	// Activating reloads the canonical SQLite document and validates the next
	// state against the file-derived in-memory Run.
	if err := service.ActivateRun(ctx, runID); err != nil {
		t.Fatalf("ActivateRun() error = %v", err)
	}
	select {
	case request := <-executor.requests:
		if len(request.Cases) != 1 || request.Cases[0].Revision != currentCase.TestCase.Revision || request.Cases[0].Name != currentCase.TestCase.Name {
			t.Fatalf("execution cases = %#v, want pinned revision %#v", request.Cases, caseEntry.TestCase)
		}
		snapshot := request.Run.Snapshot()
		if snapshot.Model.ID != model.ID || snapshot.Channel.ID != channel.ID || snapshot.Mapping == nil || snapshot.Mapping.ID != mapping.ID ||
			len(snapshot.Entries) != 1 || len(snapshot.Entries[0].CaseDefinitions) != 1 ||
			snapshot.Entries[0].CaseDefinitions[0].Revision != currentCase.TestCase.Revision {
			t.Fatalf("immutable run snapshot = %#v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run executor was not started")
	}
}

func TestFilesystemRuntimeRepositoryDistinguishesUnmappedAndDisabledTargets(t *testing.T) {
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
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	model := domain.Model{
		EntityMeta: meta("41000000-0000-4000-8000-000000000011"),
		Name:       "selection model", Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, Capabilities: []string{"chat"},
	}
	mapped := domain.Channel{
		EntityMeta: meta("41000000-0000-4000-8000-000000000012"),
		Name:       "mapped channel", BaseURL: "https://mapped.example.test/v1",
		Enabled: true, CredentialID: "41000000-0000-4000-8000-000000000013",
	}
	unmapped := domain.Channel{
		EntityMeta: meta("41000000-0000-4000-8000-000000000014"),
		Name:       "unmapped channel", BaseURL: "https://unmapped.example.test/v1",
		Enabled: true, CredentialID: "41000000-0000-4000-8000-000000000015",
	}
	disabled := domain.Channel{
		EntityMeta: meta("41000000-0000-4000-8000-000000000016"),
		Name:       "disabled channel", BaseURL: "https://disabled.example.test/v1",
		Enabled: false, CredentialID: "41000000-0000-4000-8000-000000000017",
	}
	if err := models.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []domain.Channel{mapped, unmapped, disabled} {
		if err := channels.CreateChannel(ctx, channel); err != nil {
			t.Fatal(err)
		}
	}
	if err := channels.CreateMapping(ctx, domain.ChannelModel{Protocols: []domain.Protocol{domain.ProtocolOpenAIChat},
		EntityMeta: meta("41000000-0000-4000-8000-000000000018"),
		ChannelID:  mapped.ID, ModelID: model.ID, UpstreamModelName: "upstream-mapped",
	}); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateMapping(ctx, domain.ChannelModel{Protocols: []domain.Protocol{domain.ProtocolOpenAIChat},
		EntityMeta: meta("41000000-0000-4000-8000-000000000019"),
		ChannelID:  disabled.ID, ModelID: model.ID, UpstreamModelName: "upstream-disabled",
	}); err != nil {
		t.Fatal(err)
	}
	testCase := domain.TestCase{
		EntityMeta: meta("41000000-0000-4000-8000-000000000020"),
		Key:        "T971", Name: "selection case", Dimension: "compatibility",
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic, Definitions: domain.ProtocolDefinitions{domain.Protocol(domain.CaseType("openai-chat")): json.RawMessage(`{"request":{"body":{"messages":[{"role":"user","content":"hi"}]}},"inputs":{},"assertions":[]}`)},
	}
	if err := cases.SaveCase(ctx, string(domain.ProtocolOpenAIChat), testCase.Key, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("case entries = %#v, %v", caseEntries, err)
	}
	suite := filesystemCatalogSuiteFixture(caseEntries[0].TestCase)
	if err := suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("suite entries = %#v, %v", suiteEntries, err)
	}
	plan := domain.Plan{
		EntityMeta: meta("41000000-0000-4000-8000-000000000021"),
		Name:       "selection plan", Protocol: domain.ProtocolOpenAIChat, Seed: 1,
	}
	filesystemCatalogSetPlanSuites(&plan, suiteEntries[0].Suite)
	catalogRepository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), models: models, channels: channels,
		cases: cases, suites: suites, plans: plans,
	}
	if err := catalogRepository.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repository := filesystemRuntimeRepository{catalog: catalogRepository}

	_, _, mapping, err := repository.ResolvePlanTargetSelection(ctx, plan, model.ID, mapped.ID)
	if err != nil || mapping.ChannelID != mapped.ID {
		t.Fatalf("mapped target = %#v, %v", mapping, err)
	}
	_, _, _, err = repository.ResolvePlanTargetSelection(ctx, plan, model.ID, unmapped.ID)
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unmapped target error = %v, want ErrNotFound", err)
	}
	gotModel, gotChannel, gotMapping, err := repository.ResolvePlanTargetSelection(ctx, plan, model.ID, disabled.ID)
	if err != nil || gotChannel.Enabled || gotMapping.ChannelID != disabled.ID || gotModel.ID != model.ID {
		t.Fatalf("disabled mapped target = model=%#v channel=%#v mapping=%#v err=%v", gotModel, gotChannel, gotMapping, err)
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
