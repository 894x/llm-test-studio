package comparisons_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/application/comparisons"
	"github.com/894x/llm-studio/internal/application/runs"
	"github.com/894x/llm-studio/internal/domain"
)

func TestStartCreatesOnePinnedRunPerChannelAndRefreshCompletesTheComparison(t *testing.T) {
	fixture := newComparisonFixture(t)
	repository := &comparisonRepository{fixture: fixture}
	runner := &comparisonRunner{fixture: fixture, repository: repository}
	service, err := comparisons.New(comparisons.Dependencies{
		Repository: repository, Runner: runner, Clock: &comparisonClock{now: fixture.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	comparisonID, err := service.Start(context.Background(), comparisons.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID,
		ChannelIDs: []string{fixture.channels[0].ID, fixture.channels[1].ID},
	})
	if err != nil || !domain.IsUUID(comparisonID) {
		t.Fatalf("Start() = %q, %v", comparisonID, err)
	}
	if len(runner.commands) != 2 || runner.commands[0].ModelID != fixture.model.ID || runner.commands[1].ChannelID != fixture.channels[1].ID {
		t.Fatalf("run commands = %#v", runner.commands)
	}
	if len(runner.activated) != 2 {
		t.Fatalf("activated runs = %#v", runner.activated)
	}
	comparison, err := service.Refresh(context.Background(), comparisonID)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Status() != domain.ComparisonCompleted || len(comparison.Runs()) != 2 {
		t.Fatalf("comparison = %#v", comparison)
	}
	latestPlan := fixture.plan
	latestPlan.EntityMeta, err = latestPlan.EntityMeta.NextRevision(fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	latestPlan.Name = "renamed after comparison"
	repository.latestPlan = &latestPlan
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Comparisons) != 1 || snapshot.Comparisons[0].PlanName != fixture.plan.Name || snapshot.Comparisons[0].ModelName != fixture.model.Name || len(snapshot.Comparisons[0].Channels) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

type comparisonRepository struct {
	mu         sync.Mutex
	fixture    comparisonFixture
	latestPlan *domain.Plan
	comparison domain.Comparison
}

func (repository *comparisonRepository) GetPlan(context.Context, string) (domain.Plan, error) {
	if repository.latestPlan != nil {
		return *repository.latestPlan, nil
	}
	return repository.fixture.plan, nil
}
func (repository *comparisonRepository) GetPlanRevision(context.Context, string, uint64) (domain.Plan, error) {
	return repository.fixture.plan, nil
}
func (repository *comparisonRepository) ResolvePlanTargetSelection(_ context.Context, _ domain.Plan, _ string, channelID string) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	for index, channel := range repository.fixture.channels {
		if channel.ID == channelID {
			return repository.fixture.model, channel, repository.fixture.mappings[index], nil
		}
	}
	return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, context.Canceled
}
func (repository *comparisonRepository) CreateComparison(_ context.Context, comparison domain.Comparison) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.comparison = comparison
	return nil
}
func (repository *comparisonRepository) GetComparison(context.Context, string) (domain.Comparison, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.comparison, nil
}
func (repository *comparisonRepository) UpdateComparison(_ context.Context, _ uint64, comparison domain.Comparison) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.comparison = comparison
	return nil
}
func (repository *comparisonRepository) ListComparisons(context.Context) ([]domain.Comparison, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.comparison.Meta().ID == "" {
		return []domain.Comparison{}, nil
	}
	return []domain.Comparison{repository.comparison}, nil
}
func (repository *comparisonRepository) ListReportsForRuns(context.Context, []string) ([]domain.Report, error) {
	return []domain.Report{}, nil
}

type comparisonRunner struct {
	fixture    comparisonFixture
	repository *comparisonRepository
	commands   []runs.StartCommand
	activated  []string
}

func (runner *comparisonRunner) PrepareTarget(_ context.Context, command runs.StartCommand) (string, error) {
	runner.commands = append(runner.commands, command)
	return runner.fixture.completedRuns[len(runner.commands)-1].Meta().ID, nil
}
func (runner *comparisonRunner) ActivateRun(_ context.Context, runID string) error {
	runner.repository.mu.Lock()
	persisted := runner.repository.comparison.Meta().ID != ""
	runner.repository.mu.Unlock()
	if !persisted {
		return context.Canceled
	}
	runner.activated = append(runner.activated, runID)
	return nil
}
func (runner *comparisonRunner) GetRun(_ context.Context, runID string) (domain.Run, error) {
	for _, run := range runner.fixture.completedRuns {
		if run.Meta().ID == runID {
			return run, nil
		}
	}
	return domain.Run{}, context.Canceled
}
func (runner *comparisonRunner) CancelRun(context.Context, string) error { return nil }

type comparisonClock struct{ now time.Time }

func (clock *comparisonClock) Now() time.Time {
	result := clock.now
	clock.now = clock.now.Add(time.Second)
	return result
}

type comparisonFixture struct {
	now           time.Time
	model         domain.Model
	channels      []domain.Channel
	mappings      []domain.ChannelModel
	plan          domain.Plan
	completedRuns []domain.Run
}

func newComparisonFixture(t *testing.T) comparisonFixture {
	t.Helper()
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	model := domain.Model{EntityMeta: meta("60000000-0000-4000-8000-000000000001"), Name: "model", Protocol: domain.ProtocolOpenAIChat}
	channels := []domain.Channel{
		{EntityMeta: meta("60000000-0000-4000-8000-000000000002"), Name: "a", BaseURL: "https://a.example.test/v1", Protocol: model.Protocol, Enabled: true, CredentialID: "60000000-0000-4000-8000-000000000008"},
		{EntityMeta: meta("60000000-0000-4000-8000-000000000003"), Name: "b", BaseURL: "https://b.example.test/v1", Protocol: model.Protocol, Enabled: true, CredentialID: "60000000-0000-4000-8000-000000000009"},
	}
	mappings := []domain.ChannelModel{
		{EntityMeta: meta("60000000-0000-4000-8000-000000000004"), ChannelID: channels[0].ID, ModelID: model.ID, UpstreamModelName: "model-a"},
		{EntityMeta: meta("60000000-0000-4000-8000-000000000005"), ChannelID: channels[1].ID, ModelID: model.ID, UpstreamModelName: "model-b"},
	}
	caseID := "60000000-0000-4000-8000-000000000006"
	plan := domain.Plan{EntityMeta: meta("60000000-0000-4000-8000-000000000007"), Name: "compare", ModelIDs: []string{model.ID}, ChannelIDs: []string{channels[0].ID, channels[1].ID}, Cases: []domain.CaseRevisionRef{{CaseID: caseID, Revision: 1}}, Load: domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000}, SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1000}}}
	runIDs := []string{"60000000-0000-4000-8000-000000000010", "60000000-0000-4000-8000-000000000011"}
	completed := make([]domain.Run, 2)
	for index := range channels {
		snapshot := domain.RunSnapshot{SchemaVersion: 1, Plan: domain.EntityRevisionRef{ID: plan.ID, Revision: 1}, Model: domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: 1}, Name: model.Name, Protocol: model.Protocol}, Channel: domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channels[index].ID, Revision: 1}, Name: channels[index].Name, BaseURL: channels[index].BaseURL, Protocol: model.Protocol, UpstreamModelName: mappings[index].UpstreamModelName}, Cases: plan.Cases, Load: plan.Load, SLA: plan.SLA, Environment: domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "test"}}
		run, err := domain.NewRun(meta(runIDs[index]), plan.ID, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCompleted} {
			run, err = run.Transition(status, run.Meta().UpdatedAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
		}
		completed[index] = run
	}
	return comparisonFixture{now: now, model: model, channels: channels, mappings: mappings, plan: plan, completedRuns: completed}
}
