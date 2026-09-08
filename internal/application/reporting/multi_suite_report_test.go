package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
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
	if report.SchemaVersion != 2 || len(report.SuiteReports) != 2 {
		t.Fatalf("report suites = %#v", report.SuiteReports)
	}
	first, second := report.SuiteReports[0], report.SuiteReports[1]
	if first.SuiteEntryID != run.Snapshot().Suites[0].EntryID || second.SuiteEntryID != run.Snapshot().Suites[1].EntryID {
		t.Fatalf("report suite order = %q, %q", first.SuiteEntryID, second.SuiteEntryID)
	}
	if !first.Conclusion.Passed || second.Conclusion.Passed {
		t.Fatalf("independent suite conclusions = %#v, %#v", first.Conclusion, second.Conclusion)
	}
	if first.SLA["e2e_p95_ms"].Value != 50 || second.SLA["e2e_p95_ms"].Value != 50 {
		t.Fatalf("independent suite SLA = %#v, %#v", first.SLA, second.SLA)
	}
	if len(report.CaseResults) != 2 || report.CaseResults[0].SuiteEntryID == report.CaseResults[1].SuiteEntryID {
		t.Fatalf("case results lost suite ownership: %#v", report.CaseResults)
	}

	detail, err := New(&fakeDocumentCatalog{report: report, results: results}).Detail(context.Background(), report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != 2 || len(detail.Suites) != 2 || len(detail.UnassignedRequestResults) != 0 {
		t.Fatalf("detail hierarchy = %#v", detail)
	}
	for index, suite := range detail.Suites {
		if suite.SuiteEntryID != run.Snapshot().Suites[index].EntryID || len(suite.Cases) != 1 ||
			suite.Cases[0].CaseType != "request.single" || len(suite.Cases[0].RequestResults) != 1 ||
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
		Suites        []SuiteDetail `json:"suites"`
	}
	if err := json.Unmarshal(exported, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 2 || len(document.Suites) != 2 || document.Suites[1].SuiteEntryID != run.Snapshot().Suites[1].EntryID {
		t.Fatalf("exported detail hierarchy = %s", exported)
	}
}

func TestBuildSuiteReportsRejectsResultsForNotStartedEntry(t *testing.T) {
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	secondRequest := results[3]

	if _, err := buildSuiteReports(run.Snapshot(), domain.RunCancelled, []domain.Result{secondRequest}); err == nil {
		t.Fatal("buildSuiteReports accepted work for an entry reported as not_started")
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
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          "request.single",
			TypeVersion:   1,
			Spec: json.RawMessage(
				`{"assertions":[{"config":{"contains":"ok"},"kind":"text"}],"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"request":{"body":{"messages":[{"content":"hello","role":"user"}]},"headers":{"Content-Type":"application/json"},"method":"POST","path":"/chat/completions"}}`,
			),
		},
	}
	suite := domain.Suite{
		EntityMeta: generatorMeta(suiteID, now), Key: "repeated", Name: "Repeated",
		Protocol: domain.ProtocolOpenAIChat, ModelTarget: "upstream", Cases: []domain.CaseRevisionRef{caseRef},
	}
	load := domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	firstSLA := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 100}}
	secondSLA := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 10}}
	entries := []domain.PlanSuiteEntry{
		{EntryID: entryOne, SuiteID: suiteID, SuiteRevision: 1, Parameters: map[string]json.RawMessage{}, Load: load, SLA: firstSLA},
		{EntryID: entryTwo, SuiteID: suiteID, SuiteRevision: 1, Parameters: map[string]json.RawMessage{}, Load: load, SLA: secondSLA},
	}
	plan := domain.Plan{
		EntityMeta: generatorMeta(planID, now), Name: "Repeated suites",
		ModelIDs: []string{modelID}, ChannelIDs: []string{channelID}, Suites: entries,
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
		Suites: []domain.RunSuiteSnapshot{
			{EntryID: entryOne, Suite: suite, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: firstSLA},
			{EntryID: entryTwo, Suite: suite, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: secondSLA},
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
	success := domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}
	for index, entryID := range []string{entryOne, entryTwo} {
		base := index * 3
		results = append(results,
			domain.Result{EntityMeta: generatorMeta(ids[base], now), RunID: runID, SuiteEntryID: entryID, CaseID: caseID, RequestID: entryID + ":request-1", Success: success, Metrics: map[string]float64{"e2e_ms": 50}},
			domain.Result{EntityMeta: generatorMeta(ids[base+1], now), RunID: runID, SuiteEntryID: entryID, CaseID: caseID, Success: success},
			domain.Result{EntityMeta: generatorMeta(ids[base+2], now), RunID: runID, SuiteEntryID: entryID, SuiteStatus: domain.SuiteExecutionCompleted},
		)
	}
	return run, results
}
