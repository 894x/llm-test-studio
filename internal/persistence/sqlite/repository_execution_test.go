package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestDomainRejectsLegacyRunSnapshotBeforeCreate(t *testing.T) {
	t.Parallel()

	base := runWithCompleteSnapshot(t)
	if _, err := domain.NewRun(base.Meta(), base.PlanID(), legacySnapshot(base.Snapshot())); err == nil {
		t.Fatal("NewRun() accepted a legacy snapshot")
	}
}

func TestDomainRejectsLegacyRunSnapshotBeforeUpdate(t *testing.T) {
	t.Parallel()

	base := runWithCompleteSnapshot(t)
	if _, err := domain.NewRun(base.Meta(), base.PlanID(), legacySnapshot(base.Snapshot())); err == nil {
		t.Fatal("NewRun() accepted a legacy snapshot")
	}
}

func TestDomainRejectsLegacyRunSnapshotBeforeReportCreate(t *testing.T) {
	t.Parallel()

	fixture := newRepositoryFixture(t)
	report := fixture.report
	report.PlanSnapshot = legacySnapshot(fixture.run.Snapshot())
	if err := report.Validate(); err == nil {
		t.Fatal("Report.Validate() accepted a legacy snapshot")
	}
}

func TestRepositoryRunSnapshotV3RoundTripWithoutCatalogRows(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	ctx := context.Background()
	run := runWithCompleteSnapshot(t)

	if err := repository.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() without catalog rows error = %v", err)
	}
	got, err := repository.GetRun(ctx, run.Meta().ID)
	if err != nil {
		t.Fatalf("GetRun() without catalog rows error = %v", err)
	}
	assertRoundTrip(t, "run backed only by its v3 snapshot", run, got)
}

func TestRepositoryComparisonV3RoundTripWithoutCatalogRows(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	ctx := context.Background()
	base := runWithCompleteSnapshot(t)
	firstSnapshot := base.Snapshot()
	const (
		secondChannelID = "30000000-0000-4000-8000-000000000007"
		secondMappingID = "30000000-0000-4000-8000-000000000008"
		secondRunID     = "30000000-0000-4000-8000-000000000009"
		comparisonID    = "30000000-0000-4000-8000-000000000010"
	)
	firstPlan := *firstSnapshot.PlanDocument

	firstSnapshot.PlanDocument = &firstPlan
	firstRun, err := domain.NewRun(base.Meta(), base.PlanID(), firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := firstRun.Snapshot()
	secondSnapshot.Channel = domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: secondChannelID, Revision: 1},
		Name:              "Snapshot-only channel B",
		BaseURL:           "https://snapshot-only-b.example.test/v1",
		Protocol:          domain.ProtocolOpenAIChat,
		UpstreamModelName: "upstream-snapshot-only-b",
	}
	secondMapping := *secondSnapshot.Mapping
	secondMapping.EntityMeta = entityMeta(secondMappingID, 1)
	secondMapping.ChannelID = secondChannelID
	secondMapping.UpstreamModelName = secondSnapshot.Channel.UpstreamModelName
	secondSnapshot.Mapping = &secondMapping
	secondRun, err := domain.NewRun(entityMeta(secondRunID, 1), base.PlanID(), secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.Run{firstRun, secondRun} {
		if err := repository.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun() without catalog rows error = %v", err)
		}
	}
	comparison, err := domain.NewComparison(
		entityMeta(comparisonID, 1),
		firstSnapshot.Plan,
		firstSnapshot.Model.EntityRevisionRef,
		[]domain.ComparisonRunRef{
			{Channel: firstSnapshot.Channel.EntityRevisionRef, RunID: firstRun.Meta().ID},
			{Channel: secondSnapshot.Channel.EntityRevisionRef, RunID: secondRun.Meta().ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateComparison(ctx, comparison); err != nil {
		t.Fatalf("CreateComparison() without catalog rows error = %v", err)
	}
	got, err := repository.GetComparison(ctx, comparison.Meta().ID)
	if err != nil {
		t.Fatalf("GetComparison() without catalog rows error = %v", err)
	}
	assertRoundTrip(t, "comparison backed by v3 run snapshots", comparison, got)
	listed, err := repository.ListComparisons(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListComparisons() = %#v, %v; want one comparison", listed, err)
	}
	assertRoundTrip(t, "listed comparison", comparison, listed[0])

	completed, err := comparison.Transition(domain.ComparisonCompleted, repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateComparison(ctx, comparison.Meta().Revision, completed); err != nil {
		t.Fatalf("UpdateComparison() error = %v", err)
	}
	updated, err := repository.GetComparison(ctx, comparison.Meta().ID)
	if err != nil {
		t.Fatalf("GetComparison() after terminal update error = %v", err)
	}
	assertRoundTrip(t, "completed comparison", completed, updated)
	if err := repository.UpdateComparison(ctx, comparison.Meta().Revision, completed); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale UpdateComparison() error = %v, want ErrConflict", err)
	}
}

func runWithCompleteSnapshot(t *testing.T) domain.Run {
	t.Helper()

	const (
		modelID   = "30000000-0000-4000-8000-000000000001"
		channelID = "30000000-0000-4000-8000-000000000002"
		mappingID = "30000000-0000-4000-8000-000000000003"
		caseID    = "30000000-0000-4000-8000-000000000004"
		planID    = "30000000-0000-4000-8000-000000000005"
		runID     = "30000000-0000-4000-8000-000000000006"
		suiteID   = "30000000-0000-4000-8000-00000000000b"
		entryID   = "30000000-0000-4000-8000-00000000000c"
	)
	load := domain.LoadProfile{
		Mode:             domain.LoadSingle,
		Concurrency:      1,
		RequestCount:     1,
		RequestTimeoutMS: 30_000,
	}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 5_000}}
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	testCase := domain.TestCase{
		EntityMeta:    entityMeta(caseID, 1),
		Key:           "T-SNAPSHOT-ONLY",
		Name:          "Snapshot-only run case",
		Dimension:     "persistence",
		Protocol:      domain.ProtocolOpenAIChat,
		Enabled:       true,
		Default:       true,
		Severity:      domain.CaseSeverityCritical,
		ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          domain.CaseType("openai-chat"),
			TypeVersion:   1,
			Spec: json.RawMessage(
				`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`,
			),
		},
	}
	suite := domain.Suite{
		EntityMeta: entityMeta(suiteID, 1), Key: "snapshot-only", Name: "Snapshot-only suite",
		Protocol: domain.ProtocolOpenAIChat,
		Cases:    []domain.CaseRef{{CaseID: caseRef.CaseID}}, Inputs: []domain.SuiteInput{},
	}
	plan := domain.Plan{
		EntityMeta: entityMeta(planID, 1),
		Name:       "Snapshot-only plan",
		Protocol:   domain.ProtocolOpenAIChat,
		Entries: []domain.PlanEntry{{
			EntryID: entryID, TargetKind: domain.PlanTargetSuite, TargetID: suiteID,
			Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla,
		}},
	}
	mapping := domain.ChannelModel{
		EntityMeta:        entityMeta(mappingID, 1),
		ChannelID:         channelID,
		ModelID:           modelID,
		UpstreamModelName: "upstream-snapshot-only",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: planID, Revision: 1},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1},
			Name:              "Snapshot-only model",
			Protocol:          domain.ProtocolOpenAIChat,
			Capabilities:      []string{"chat", "streaming"},
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1},
			Name:              "Snapshot-only channel",
			BaseURL:           "https://snapshot-only.example.test/v1",
			Protocol:          domain.ProtocolOpenAIChat,
			UpstreamModelName: mapping.UpstreamModelName,
		},
		Environment:  domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "go-test"},
		PlanDocument: &plan,
		Mapping:      &mapping,
		Entries: []domain.RunEntrySnapshot{{
			EntryID: entryID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Name: suite.Name, Key: suite.Key, Suite: &suite, CaseInputs: map[string]map[string]json.RawMessage{caseRef.CaseID: {}}, Cases: []domain.CaseRevisionRef{caseRef},
			CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla,
		}},
	}
	run, err := domain.NewRun(entityMeta(runID, 1), planID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	return run
}

func legacySnapshot(snapshot domain.RunSnapshot) domain.RunSnapshot {
	snapshot.SchemaVersion = 99
	snapshot.PlanDocument = nil
	snapshot.Mapping = nil
	snapshot.Entries = nil
	return snapshot
}
