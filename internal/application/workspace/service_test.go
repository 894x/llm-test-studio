package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	planID    = "11111111-1111-4111-8111-111111111111"
	modelID   = "22222222-2222-4222-8222-222222222222"
	channelID = "33333333-3333-4333-8333-333333333333"
	caseID    = "44444444-4444-4444-8444-444444444444"
	runID     = "55555555-5555-4555-8555-555555555555"
	mappingID = "66666666-6666-4666-8666-666666666666"
	suiteID   = "77777777-7777-4777-8777-777777777771"
	entryID   = "88888888-8888-4888-8888-888888888881"
)

type fakeCatalog struct {
	plans       []domain.Plan
	projections []RunProjection
	plansErr    error
	runsErr     error
	listPlans   func(context.Context) ([]domain.Plan, error)
	listRuns    func(context.Context) ([]RunProjection, error)
	plansCalls  int
	runsCalls   int
}

func (catalog *fakeCatalog) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	catalog.plansCalls++
	if catalog.listPlans != nil {
		return catalog.listPlans(ctx)
	}
	return append([]domain.Plan(nil), catalog.plans...), catalog.plansErr
}

func (catalog *fakeCatalog) ListSuites(context.Context) ([]domain.Suite, error) {
	suites := []domain.Suite{}
	for _, plan := range catalog.plans {
		for _, entry := range plan.Entries {
			suites = append(suites, domain.Suite{
				EntityMeta: domain.EntityMeta{ID: entry.TargetID, Revision: 1},
				Cases:      []domain.CaseRef{{CaseID: caseID}},
			})
		}
	}
	return suites, nil
}

func (catalog *fakeCatalog) ListRunProjections(ctx context.Context) ([]RunProjection, error) {
	catalog.runsCalls++
	if catalog.listRuns != nil {
		return catalog.listRuns(ctx)
	}
	return append([]RunProjection(nil), catalog.projections...), catalog.runsErr
}

func TestSnapshotBuildsASecretFreeDesktopProjection(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	plan, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadFixedConcurrency, Concurrency: 2, RequestCount: 5, RequestTimeoutMS: 30_000,
	})
	var err error
	run, err = run.Transition(domain.RunCompleted, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("transition to completed: %v", err)
	}
	catalog := &fakeCatalog{
		plans: []domain.Plan{plan},
		projections: []RunProjection{{
			Run: run, PinnedPlan: plan, Completed: 1, Passed: 1,
			ArtifactCount: 2, Conclusion: ConclusionPassed,
		}},
	}
	if err := validateProjection(catalog.projections[0]); err != nil {
		t.Fatalf("valid projection fixture: %v", err)
	}

	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", snapshot.SchemaVersion, CurrentSchemaVersion)
	}
	if len(snapshot.Plans) != 1 || snapshot.Plans[0].ID != planID || snapshot.Plans[0].RunCount != 1 || snapshot.Plans[0].CaseCount != 1 {
		t.Fatalf("plans = %#v", snapshot.Plans)
	}
	if len(snapshot.Runs) != 1 {
		t.Fatalf("runs = %#v", snapshot.Runs)
	}
	got := snapshot.Runs[0]
	if got.ID != runID || got.PlanID != planID || got.PlanName != "OpenAI regression" || got.ModelName != "gpt-test" || got.ChannelName != "primary" {
		t.Fatalf("run identity projection = %#v", got)
	}
	if got.Planned != 5 || got.DurationMS != 0 || got.Completed != 1 || got.Passed != 1 || got.Failed != 0 || got.ArtifactCount != 2 {
		t.Fatalf("run aggregate projection = %#v", got)
	}
	if got.Conclusion != ConclusionPassed {
		t.Fatalf("run conclusion = %q, want %q", got.Conclusion, ConclusionPassed)
	}
	if snapshot.ActiveRunID != "" {
		t.Fatalf("completed run was reported active: %q", snapshot.ActiveRunID)
	}
	if catalog.plansCalls != 1 || catalog.runsCalls != 1 {
		t.Fatalf("port calls plans=%d runs=%d, want one each", catalog.plansCalls, catalog.runsCalls)
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, forbidden := range []string{"https://provider.example/v1", "corp-egress", "credential", "api_key", "gpt-upstream"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("desktop projection leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSnapshotUsesTwoPortCallsForManyRunsAndPinnedHistoricalLabels(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	historical, firstRun := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadFixedConcurrency, Concurrency: 2, RequestCount: 5, RequestTimeoutMS: 30_000,
	})
	latest := historical
	latest.Revision = 2
	latest.UpdatedAt = now.Add(time.Minute)
	latest.Name = "Renamed current plan"
	projections := make([]RunProjection, 0, 20)
	for index := 0; index < 20; index++ {
		run := firstRun
		if index != 0 {
			run = validRun(t, fmt.Sprintf("55555555-5555-4555-8555-%012d", index), historical, now.Add(time.Duration(index)*time.Second))
		}
		projections = append(projections, RunProjection{Run: run, PinnedPlan: historical, Conclusion: ConclusionNone})
	}
	catalog := &fakeCatalog{plans: []domain.Plan{latest}, projections: projections}

	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Runs) != len(projections) {
		t.Fatalf("runs = %d, want %d", len(snapshot.Runs), len(projections))
	}
	for _, run := range snapshot.Runs {
		if run.PlanName != historical.Name {
			t.Fatalf("run plan name = %q, want pinned historical %q", run.PlanName, historical.Name)
		}
	}
	if snapshot.Plans[0].Name != latest.Name || snapshot.Plans[0].RunCount != len(projections) {
		t.Fatalf("current plan projection = %#v", snapshot.Plans[0])
	}
	if catalog.plansCalls != 1 || catalog.runsCalls != 1 {
		t.Fatalf("port calls grew with runs: plans=%d runs=%d", catalog.plansCalls, catalog.runsCalls)
	}
}

func TestSnapshotKeepsHistoricalV2RunAfterCurrentPlanIsDeleted(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	plan, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
	})
	catalog := &fakeCatalog{
		projections: []RunProjection{{Run: run, PinnedPlan: plan, Conclusion: ConclusionNone}},
	}

	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() after current plan deletion error = %v", err)
	}
	if len(snapshot.Plans) != 0 {
		t.Fatalf("current plans = %#v, want none", snapshot.Plans)
	}
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].PlanID != plan.ID || snapshot.Runs[0].PlanName != plan.Name {
		t.Fatalf("historical runs = %#v, want snapshot-backed run", snapshot.Runs)
	}
}

func TestSnapshotUsesCurrentEntriesForQuickTask(t *testing.T) {
	now := time.Now().UTC()
	plan, original := validPlanAndRun(t, now, domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000})
	snapshot := original.Snapshot()
	plan.ID = runID
	snapshot.Plan.ID = runID
	snapshot.PlanDocument = &plan
	snapshot.QuickTask = &domain.QuickTaskSnapshot{}
	run, err := domain.NewRun(meta(runID, now), plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(&fakeCatalog{projections: []RunProjection{{Run: run, PinnedPlan: plan, Conclusion: ConclusionNone}}}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 1 || got.Runs[0].PlanName != plan.Name || got.Runs[0].Planned != 1 || got.Runs[0].Source != "quick_task" {
		t.Fatalf("quick projection = %#v", got.Runs)
	}
}

func TestSnapshotRejectsIncompleteSuiteMembership(t *testing.T) {
	now := time.Now().UTC()
	plan, original := validPlanAndRun(t, now, domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000})
	snapshot := original.Snapshot()
	snapshot.Entries[0].Suite.Cases = append(snapshot.Entries[0].Suite.Cases, domain.CaseRef{CaseID: "99000000-0000-4000-8000-000000000001"})
	if _, err := domain.NewRun(original.Meta(), plan.ID, snapshot); err == nil {
		t.Fatal("incomplete Suite accepted")
	}
}

func TestSnapshotAcceptsDurationOnlyRunsWithoutInventingARequestTarget(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	plan, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadOpenLoop, Concurrency: 3, RatePerSecond: 2.5,
		DurationMS: 90_000, RequestTimeoutMS: 30_000,
	})
	catalog := &fakeCatalog{
		plans: []domain.Plan{plan},
		projections: []RunProjection{{
			Run: run, PinnedPlan: plan, Completed: 175, Passed: 170, Failed: 5,
			Conclusion: ConclusionNone,
		}},
	}

	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() duration-only error = %v", err)
	}
	got := snapshot.Runs[0]
	if got.Planned != 0 || got.DurationMS != 90_000 || got.LoadMode != domain.LoadOpenLoop || got.Completed != 175 {
		t.Fatalf("duration-only projection = %#v", got)
	}
}

func TestSnapshotExposesStableRunFailureWithoutInternalCause(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	plan, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
	})
	var err error
	run, err = run.Fail(domain.RunFailure{Phase: "execute", ErrorCode: "run_execution_failed"}, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	catalog := &fakeCatalog{
		plans: []domain.Plan{plan},
		projections: []RunProjection{{
			Run: run, PinnedPlan: plan, Conclusion: ConclusionFailed,
		}},
	}

	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Runs[0]
	if got.FailurePhase != "execute" || got.ErrorCode != "run_execution_failed" {
		t.Fatalf("failed run projection = %#v", got)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "internal_cause") {
		t.Fatalf("failed run projection exposed internal cause: %s", encoded)
	}
}

func TestSnapshotRedactsPortErrorsAndPreservesContextTermination(t *testing.T) {
	secret := "api-key=sk-do-not-leak https://secret.provider.example/v1"
	for _, test := range []struct {
		name    string
		catalog *fakeCatalog
	}{
		{"plans", &fakeCatalog{plansErr: errors.New(secret)}},
		{"run projections", &fakeCatalog{runsErr: errors.New(secret)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.catalog).Snapshot(context.Background())
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Snapshot() error = %v, want ErrUnavailable", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret.provider") || strings.Contains(err.Error(), "sk-do-not-leak") {
				t.Fatalf("Snapshot() leaked port error: %v", err)
			}
		})
	}

	for _, termination := range []error{context.Canceled, context.DeadlineExceeded} {
		catalog := &fakeCatalog{plansErr: fmt.Errorf("wrapped: %w", termination)}
		_, err := New(catalog).Snapshot(context.Background())
		if !errors.Is(err, termination) {
			t.Fatalf("Snapshot() error = %v, want %v", err, termination)
		}
	}
}

func TestSnapshotChecksCancellationAfterEveryPortReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	plan, _ := validPlanAndRun(t, time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC), domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
	})
	catalog := &fakeCatalog{plans: []domain.Plan{plan}}
	catalog.listRuns = func(context.Context) ([]RunProjection, error) {
		cancel()
		return nil, nil
	}

	_, err := New(catalog).Snapshot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot() error = %v, want context.Canceled", err)
	}
	if catalog.plansCalls != 1 || catalog.runsCalls != 1 {
		t.Fatalf("port calls plans=%d runs=%d, want one each", catalog.plansCalls, catalog.runsCalls)
	}
}

func TestSnapshotRejectsInvalidConclusionsCountsDuplicatesAndPinnedPlans(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	plan, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
	})
	otherPlan := plan
	otherPlan.Revision = 2
	otherPlan.UpdatedAt = now.Add(time.Second)

	for _, test := range []struct {
		name        string
		projections []RunProjection
	}{
		{"unknown conclusion", []RunProjection{{Run: run, PinnedPlan: plan, Conclusion: Conclusion("maybe")}}},
		{"inconsistent counts", []RunProjection{{Run: run, PinnedPlan: plan, Completed: 1, Passed: 1, Failed: 1, Conclusion: ConclusionNone}}},
		{"wrong pinned revision", []RunProjection{{Run: run, PinnedPlan: otherPlan, Conclusion: ConclusionNone}}},
		{"duplicate run", []RunProjection{{Run: run, PinnedPlan: plan, Conclusion: ConclusionNone}, {Run: run, PinnedPlan: plan, Conclusion: ConclusionNone}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(&fakeCatalog{plans: []domain.Plan{plan}, projections: test.projections}).Snapshot(context.Background())
			if !errors.Is(err, ErrInconsistent) {
				t.Fatalf("Snapshot() error = %v, want ErrInconsistent", err)
			}
		})
	}

	if _, err := New(nil).Snapshot(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil catalog error = %v, want ErrUnavailable", err)
	}
}

func TestConclusionValidateIsClosed(t *testing.T) {
	for _, conclusion := range []Conclusion{ConclusionNone, ConclusionPassed, ConclusionFailed} {
		if err := conclusion.Validate(); err != nil {
			t.Fatalf("Conclusion(%q).Validate() error = %v", conclusion, err)
		}
	}
	if err := Conclusion("completed").Validate(); err == nil {
		t.Fatal("Conclusion(completed).Validate() accepted an unknown value")
	}
}

func validPlanAndRun(t *testing.T, now time.Time, load domain.LoadProfile) (domain.Plan, domain.Run) {
	t.Helper()
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 5_000}}
	plan := domain.Plan{
		EntityMeta: meta(planID, now), Name: "OpenAI regression",
		Protocol: domain.ProtocolOpenAIChat,
		Entries: []domain.PlanEntry{{
			EntryID: entryID, TargetKind: domain.PlanTargetSuite, TargetID: suiteID,
			Parameters: map[string]json.RawMessage{},
			Load:       load, SLA: sla,
		}},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan fixture: %v", err)
	}
	return plan, validRun(t, runID, plan, now)
}

func validRun(t *testing.T, id string, plan domain.Plan, now time.Time) domain.Run {
	t.Helper()
	caseMeta := meta(caseID, now)
	entry := plan.Entries[0]
	caseMeta.Revision = 1
	testCase := domain.TestCase{
		EntityMeta: caseMeta, Key: "T001", Name: "workspace case", Dimension: "boundary",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          domain.CaseType("openai-chat"),
			TypeVersion:   1,
			Spec:          json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[]}`),
		},
	}
	mapping := domain.ChannelModel{
		EntityMeta: meta(mappingID, now), ChannelID: channelID, ModelID: modelID,
		UpstreamModelName: "gpt-upstream",
	}
	suite := domain.Suite{
		EntityMeta: meta(entry.TargetID, now), Key: "workspace", Name: "Workspace",
		Protocol: domain.ProtocolOpenAIChat,
		Cases:    []domain.CaseRef{{CaseID: caseID}}, Inputs: []domain.SuiteInput{},
	}
	run, err := domain.NewRun(meta(id, now), plan.ID, domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 4},
			Name:              "gpt-test", Protocol: domain.ProtocolOpenAIChat,
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 2},
			Name:              "primary", BaseURL: "https://provider.example/v1",
			Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: "gpt-upstream",
		},
		Environment: domain.EnvironmentSnapshot{
			OS: "windows", Arch: "amd64", NetworkEgress: "corp-egress",
			AppVersion: "test", EngineVersion: "test",
		},
		PlanDocument: &plan,
		Mapping:      &mapping,
		Entries: []domain.RunEntrySnapshot{{
			EntryID: entry.EntryID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Name: suite.Name, Key: suite.Key, Suite: &suite, Cases: []domain.CaseRevisionRef{{CaseID: caseID, Revision: 1}},
			CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, CaseInputs: map[string]map[string]json.RawMessage{caseID: {}},
			Load: entry.Load, SLA: entry.SLA,
		}},
	})
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	run, err = run.Transition(domain.RunStarting, now.Add(time.Second))
	if err != nil {
		t.Fatalf("transition to starting: %v", err)
	}
	run, err = run.Transition(domain.RunRunning, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	return run
}

func meta(id string, now time.Time) domain.EntityMeta {
	return domain.EntityMeta{
		ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
}
