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

func TestRepositoryRejectsLegacyRunSnapshotOnCreate(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	base := runWithCompleteSnapshot(t)
	legacy, err := domain.NewRun(base.Meta(), base.PlanID(), legacySnapshot(base.Snapshot()))
	if err != nil {
		t.Fatalf("NewRun() legacy snapshot error = %v", err)
	}

	if err := repository.CreateRun(context.Background(), legacy); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("CreateRun() legacy snapshot error = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryRejectsLegacyRunSnapshotOnUpdate(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	base := runWithCompleteSnapshot(t)
	if err := repository.CreateRun(context.Background(), base); err != nil {
		t.Fatalf("CreateRun() v2 snapshot error = %v", err)
	}
	legacy, err := domain.NewRun(base.Meta(), base.PlanID(), legacySnapshot(base.Snapshot()))
	if err != nil {
		t.Fatalf("NewRun() legacy snapshot error = %v", err)
	}
	updated, err := legacy.Transition(domain.RunStarting, repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatalf("Transition() legacy snapshot error = %v", err)
	}

	if err := repository.UpdateRun(context.Background(), legacy.Meta().Revision, updated); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("UpdateRun() legacy snapshot error = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryRejectsLegacyRunSnapshotOnReportCreate(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	report := fixture.report
	report.PlanSnapshot = legacySnapshot(fixture.run.Snapshot())
	if err := report.Validate(); err != nil {
		t.Fatalf("legacy report fixture Validate() error = %v", err)
	}

	if err := repository.CreateReport(context.Background(), report); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("CreateReport() legacy snapshot error = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryRunSnapshotV2RoundTripWithoutCatalogRows(t *testing.T) {
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
	assertRoundTrip(t, "run backed only by its v2 snapshot", run, got)
}

func TestRepositoryComparisonV2RoundTripWithoutCatalogRows(t *testing.T) {
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
	firstPlan.ChannelIDs = append(firstPlan.ChannelIDs, secondChannelID)
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
	assertRoundTrip(t, "comparison backed by v2 run snapshots", comparison, got)
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
			Type:          domain.CaseType("request.single"),
			TypeVersion:   1,
			Spec: json.RawMessage(
				`{"assertions":[{"config":{"contains":"ok"},"kind":"text"}],"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"request":{"body":{"messages":[{"content":"hello","role":"user"}]},"headers":{"Content-Type":"application/json"},"method":"POST","path":"/chat/completions"}}`,
			),
		},
	}
	plan := domain.Plan{
		EntityMeta: entityMeta(planID, 1),
		Name:       "Snapshot-only plan",
		ModelIDs:   []string{modelID},
		ChannelIDs: []string{channelID},
		Cases:      []domain.CaseRevisionRef{caseRef},
		Load:       load,
		SLA:        sla,
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
		Cases:           []domain.CaseRevisionRef{caseRef},
		Load:            load,
		SLA:             sla,
		Environment:     domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "go-test"},
		PlanDocument:    &plan,
		Mapping:         &mapping,
		CaseDefinitions: []domain.TestCase{testCase},
	}
	run, err := domain.NewRun(entityMeta(runID, 1), planID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	return run
}

func legacySnapshot(snapshot domain.RunSnapshot) domain.RunSnapshot {
	snapshot.SchemaVersion = 1
	snapshot.PlanDocument = nil
	snapshot.Mapping = nil
	snapshot.CaseDefinitions = nil
	return snapshot
}
