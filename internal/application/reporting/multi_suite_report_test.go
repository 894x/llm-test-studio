package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestGeneratorAndDetailKeepRepeatedSuiteEntriesIndependentAndOrdered(t *testing.T) {
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	repository := &fakeReportRepository{run: run, results: results, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository,
		Clock:      fixedReportClock{now: run.Meta().UpdatedAt.Add(time.Minute)},
		IDFactory: func(time.Time) (string, error) {
			return "42000000-0000-4000-8000-000000000010", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}

	report := repository.report
	if report.SchemaVersion != domain.CurrentReportSchemaVersion || len(report.EntryReports) != 2 {
		t.Fatalf("report suites = %#v", report.EntryReports)
	}
	first, second := report.EntryReports[0], report.EntryReports[1]
	if first.EntryID != run.Snapshot().Entries[0].EntryID || second.EntryID != run.Snapshot().Entries[1].EntryID {
		t.Fatalf("report suite order = %q, %q", first.EntryID, second.EntryID)
	}
	if !first.Conclusion.Passed || second.Conclusion.Passed {
		t.Fatalf("independent suite conclusions = %#v, %#v", first.Conclusion, second.Conclusion)
	}
	if first.SLA["e2e_p95_ms"].Value != 50 || second.SLA["e2e_p95_ms"].Value != 50 {
		t.Fatalf("independent suite SLA = %#v, %#v", first.SLA, second.SLA)
	}
	if len(report.CaseResults) != 2 || report.CaseResults[0].EntryID == report.CaseResults[1].EntryID {
		t.Fatalf("case results lost suite ownership: %#v", report.CaseResults)
	}

	detail, err := New(&fakeDocumentCatalog{report: report, results: results}).Detail(context.Background(), report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != CurrentDetailSchemaVersion || len(detail.Entries) != 2 || len(detail.UnassignedRequestResults) != 0 {
		t.Fatalf("detail hierarchy = %#v", detail)
	}
	for index, suite := range detail.Entries {
		if suite.EntryID != run.Snapshot().Entries[index].EntryID || len(suite.Cases) != 1 ||
			suite.Cases[0].Protocol != "openai-chat" || len(suite.Cases[0].RequestResults) != 1 ||
			suite.Cases[0].SummaryResult == nil {
			t.Fatalf("detail suite %d = %#v", index, suite)
		}
	}
	exported, err := renderJSON(detail, "multi-suite-test")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		SchemaVersion int           `json:"schema_version"`
		Entries       []EntryDetail `json:"entries"`
	}
	if err := json.Unmarshal(exported, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != CurrentDetailSchemaVersion || len(document.Entries) != 2 || document.Entries[1].EntryID != run.Snapshot().Entries[1].EntryID {
		t.Fatalf("exported detail hierarchy = %s", exported)
	}
}

func TestBuildEntryReportsRejectsResultsForNotStartedEntry(t *testing.T) {
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	secondRequest := results[3]

	if _, err := buildEntryReports(run.Snapshot(), domain.RunCancelled, []domain.Result{secondRequest}); err == nil {
		t.Fatal("buildEntryReports accepted work for an entry reported as not_started")
	}
}

func multiSuiteReportFixture(t *testing.T, now time.Time) (domain.Run, []domain.Result) {
	t.Helper()
	const (
		planID    = "42000000-0000-4000-8000-000000000001"
		modelID   = "42000000-0000-4000-8000-000000000002"
		channelID = "42000000-0000-4000-8000-000000000003"
		mappingID = "42000000-0000-4000-8000-000000000004"
		caseID    = "42000000-0000-4000-8000-000000000005"
		suiteID   = "42000000-0000-4000-8000-000000000006"
		entryOne  = "42000000-0000-4000-8000-000000000007"
		entryTwo  = "42000000-0000-4000-8000-000000000008"
		runID     = "42000000-0000-4000-8000-000000000009"
	)
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	testCase := domain.TestCase{
		EntityMeta: generatorMeta(caseID, now), Key: "basic", Name: "Basic", Dimension: "boundary",
		Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic, Definitions: domain.ProtocolDefinitions{domain.Protocol("openai-chat"): json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`)},
	}
	suite := domain.Suite{
		EntityMeta: generatorMeta(suiteID, now), Key: "repeated", Name: "Repeated",
		Protocol: domain.ProtocolOpenAIChat, Cases: []domain.CaseRef{{CaseID: caseID}}, Inputs: []domain.SuiteInput{},
	}
	load := domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	firstSLA := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 100}}
	secondSLA := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 10}}
	entries := []domain.PlanEntry{
		{EntryID: entryOne, TargetKind: domain.PlanTargetSuite, TargetID: suiteID, Parameters: map[string]json.RawMessage{}, Load: load, SLA: firstSLA},
		{EntryID: entryTwo, TargetKind: domain.PlanTargetSuite, TargetID: suiteID, Parameters: map[string]json.RawMessage{}, Load: load, SLA: secondSLA},
	}
	plan := domain.Plan{
		EntityMeta: generatorMeta(planID, now), Name: "Repeated suites",
		Protocol: domain.ProtocolOpenAIChat, Entries: entries,
	}
	mapping := domain.ChannelModel{
		EntityMeta: generatorMeta(mappingID, now), ChannelID: channelID,
		ModelID: modelID, UpstreamModelName: "upstream",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: planID, Revision: 1},
		PlanDocument:  &plan,
		Mapping:       &mapping,
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1},
			Name:              "model", Protocol: domain.ProtocolOpenAIChat,
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1},
			Name:              "channel", BaseURL: "https://example.test/v1",
			Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: "upstream",
		},
		Environment: domain.EnvironmentSnapshot{
			OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct",
			AppVersion: "test", EngineVersion: "test",
		},
		Entries: []domain.RunEntrySnapshot{
			{EntryID: entryOne, TargetKind: domain.PlanTargetSuite, TargetID: suiteID, Name: suite.Name, Key: suite.Key, Suite: &suite, CaseInputs: map[string]map[string]json.RawMessage{caseID: {}}, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: firstSLA},
			{EntryID: entryTwo, TargetKind: domain.PlanTargetSuite, TargetID: suiteID, Name: suite.Name, Key: suite.Key, Suite: &suite, CaseInputs: map[string]map[string]json.RawMessage{caseID: {}}, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: secondSLA},
		},
	}
	run, err := domain.NewRun(generatorMeta(runID, now), planID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCompleted} {
		run, err = run.Transition(status, run.Meta().UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	results := make([]domain.Result, 0, 6)
	ids := []string{
		"42000000-0000-4000-8000-000000000011", "42000000-0000-4000-8000-000000000012", "42000000-0000-4000-8000-000000000013",
		"42000000-0000-4000-8000-000000000014", "42000000-0000-4000-8000-000000000015", "42000000-0000-4000-8000-000000000016",
	}
	success := testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}}
	for index, entryID := range []string{entryOne, entryTwo} {
		base := index * 3
		results = append(results,
			domain.Result{EntityMeta: generatorMeta(ids[base], now), RunID: runID, EntryID: entryID, CaseID: caseID, RequestID: entryID + ":request-1", ExecutionStatus: domain.ExecutionCompleted, Verification: success, Metrics: map[string]float64{"e2e_ms": 50}},
			domain.Result{EntityMeta: generatorMeta(ids[base+1], now), RunID: runID, EntryID: entryID, CaseID: caseID, ExecutionStatus: domain.ExecutionCompleted, Verification: success},
			domain.Result{EntityMeta: generatorMeta(ids[base+2], now), RunID: runID, EntryID: entryID, EntryStatus: domain.EntryExecutionCompleted},
		)
	}
	return run, results
}
