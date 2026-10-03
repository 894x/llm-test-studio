package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestGeneratorBuildsAndPersistsACompletePerformanceReport(t *testing.T) {
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	run := generatorRun(t, now)
	requestResult := domain.Result{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000007", now),
		RunID:      run.Meta().ID, EntryID: run.Snapshot().Entries[0].EntryID, CaseID: run.Snapshot().Entries[0].Cases[0].CaseID, RequestID: "request-1",
		ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}},
		Metrics: map[string]float64{"e2e_ms": 25, "ttft_ms": 10, "prompt_tokens": 3},
	}
	summaryResult := requestResult
	summaryResult.EntityMeta = generatorMeta("40000000-0000-4000-8000-000000000009", now)
	summaryResult.CaseID = run.Snapshot().Entries[0].Cases[0].CaseID
	summaryResult.RequestID = ""
	repository := &fakeReportRepository{run: run, results: []domain.Result{requestResult, summaryResult, completedEntryMarker(run)}, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository,
		Clock:      fixedReportClock{now: now.Add(time.Minute)},
		IDFactory:  func(time.Time) (string, error) { return "40000000-0000-4000-8000-000000000008", nil },
	})
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if repository.report.ID == "" || !repository.report.Conclusion.Passed || len(repository.report.CaseResults) != 1 {
		t.Fatalf("generated report = %#v", repository.report)
	}
	if metric := repository.report.Metrics["e2e_ms"]; metric.Value != 25 || metric.Samples != 1 || metric.Unit != "ms" {
		t.Fatalf("e2e metric = %#v", metric)
	}
	if metric := repository.report.Metrics["e2e_p95_ms"]; metric.Value != 25 || metric.Samples != 1 || metric.Unit != "ms" {
		t.Fatalf("e2e P95 metric = %#v", metric)
	}
	if len(repository.report.Timeline) != 1 || len(repository.report.Distributions) == 0 {
		t.Fatalf("timeline/distributions = %d/%d", len(repository.report.Timeline), len(repository.report.Distributions))
	}
}

func TestGeneratorFailsConclusionWhenObservedSLAIsBreached(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	run := generatorRun(t, now)
	requestResult := domain.Result{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000017", now),
		RunID:      run.Meta().ID, EntryID: run.Snapshot().Entries[0].EntryID, CaseID: run.Snapshot().Entries[0].Cases[0].CaseID, RequestID: "request-1",
		ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}},
		Metrics: map[string]float64{"e2e_ms": 1500},
	}
	summaryResult := requestResult
	summaryResult.EntityMeta = generatorMeta("40000000-0000-4000-8000-000000000019", now)
	summaryResult.CaseID = run.Snapshot().Entries[0].Cases[0].CaseID
	summaryResult.RequestID = ""
	repository := &fakeReportRepository{run: run, results: []domain.Result{requestResult, summaryResult, completedEntryMarker(run)}, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)},
		IDFactory: func(time.Time) (string, error) { return "40000000-0000-4000-8000-000000000018", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	if repository.report.Conclusion.Passed || repository.report.Conclusion.Verdict != "fail" || len(repository.report.Conclusion.Issues) == 0 {
		t.Fatalf("SLA conclusion = %#v", repository.report.Conclusion)
	}
	if metric := repository.report.EntryReports[0].SLA["e2e_p95_ms"]; metric.Value != 1500 || metric.Samples != 1 {
		t.Fatalf("observed SLA metric = %#v", metric)
	}
}

func TestQuickTaskDetailUsesTheSuiteAwareSchema(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 30, 0, 0, time.UTC)
	run := generatorRun(t, now)
	snapshot := run.Snapshot()
	requestResult := domain.Result{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000027", now),
		RunID:      run.Meta().ID, EntryID: run.Snapshot().Entries[0].EntryID, CaseID: snapshot.Entries[0].Cases[0].CaseID, RequestID: "request-1",
		ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}},
		Metrics: map[string]float64{"e2e_ms": 25},
	}
	summaryResult := requestResult
	summaryResult.EntityMeta = generatorMeta("40000000-0000-4000-8000-000000000028", now)
	summaryResult.RequestID = ""
	repository := &fakeReportRepository{
		run: run, results: []domain.Result{requestResult, summaryResult, completedEntryMarker(run)}, evidence: []domain.Evidence{},
	}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)},
		IDFactory: func(time.Time) (string, error) { return "40000000-0000-4000-8000-000000000029", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	if len(repository.report.EntryReports) != 1 ||
		repository.report.EntryReports[0].EntryID != run.Meta().ID ||
		len(repository.report.CaseResults) != 1 || repository.report.CaseResults[0].EntryID != run.Meta().ID ||
		len(repository.report.EntryReports[0].CaseResults) != 1 ||
		repository.report.EntryReports[0].CaseResults[0].EntryID != run.Meta().ID {
		t.Fatalf("canonical quick-task report tree = %#v", repository.report)
	}
	detail, err := New(&fakeDocumentCatalog{report: repository.report, results: repository.results}).Detail(
		context.Background(),
		repository.report.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != CurrentDetailSchemaVersion || len(detail.Entries) != 1 ||
		detail.Entries[0].EntryID != run.Meta().ID ||
		detail.Entries[0].TargetID != snapshot.Entries[0].TargetID ||
		len(detail.Entries[0].Cases) != 1 ||
		detail.Entries[0].Cases[0].SummaryResult == nil ||
		detail.Entries[0].Cases[0].SummaryResult.EntryID != run.Meta().ID ||
		len(detail.Entries[0].Cases[0].RequestResults) != 1 ||
		detail.Entries[0].Cases[0].RequestResults[0].EntryID != run.Meta().ID ||
		len(detail.UnassignedRequestResults) != 0 {
		t.Fatalf("quick task detail = %#v", detail)
	}
}

func TestSLAObservationSupportsPercentileAndPercentUnits(t *testing.T) {
	results := []domain.Result{{
		ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}},
		Metrics: map[string]float64{"e2e_ms": 250},
	}}
	aggregates := aggregateMetrics(results)
	latency, comparison, ok := slaObservation("p95_ms", results, aggregates)
	if !ok || comparison != "max" || latency.Value != 250 || latency.Unit != "ms" {
		t.Fatalf("p95_ms observation = %#v, %q, %v", latency, comparison, ok)
	}
	rate, comparison, ok := slaObservation("success_rate_percent", results, aggregates)
	if !ok || comparison != "min" || rate.Value != 100 || rate.Unit != "percent" {
		t.Fatalf("success_rate_percent observation = %#v, %q, %v", rate, comparison, ok)
	}
}

func TestFineStreamingMetricsRetainObservedFactsRegardlessOfAssertions(t *testing.T) {
	success := testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}}
	results := []domain.Result{
		{ExecutionStatus: domain.ExecutionCompleted, Verification: success, Metrics: map[string]float64{
			"e2e_ms": 10, "ttfb_ms": 1, "ttft_ms": 2, "ttft_any_ms": 2, "ttft_visible_ms": 3,
			"ttst_ms": 4, "observed_icl_ms": 2, "semantic_chunk_count": 2, "custom_latency_ms": 7,
		}},
		{ExecutionStatus: domain.ExecutionCompleted, Verification: success, Metrics: map[string]float64{
			"e2e_ms": 20, "ttfb_ms": 3, "semantic_chunk_count": 0, "custom_latency_ms": 8,
		}},
		{Metrics: map[string]float64{
			"e2e_ms": 1000, "ttfb_ms": 100, "ttft_ms": 200, "ttft_any_ms": 200, "ttst_ms": 300,
			"observed_icl_ms": 100, "semantic_chunk_count": 2, "custom_latency_ms": 9,
		}},
	}
	metrics := aggregateMetrics(results)
	for name, want := range map[string]struct {
		value   float64
		samples int
	}{
		"e2e_ms": {1030.0 / 3, 3}, "ttfb_ms": {104.0 / 3, 3}, "ttfb_average_ms": {104.0 / 3, 3},
		"ttft_any_ms": {101, 2}, "ttft_any_p99_ms": {198.02, 2},
		"observed_icl_ms": {51, 2}, "semantic_chunk_count": {4.0 / 3, 3}, "semantic_chunk_count_average": {4.0 / 3, 3},
	} {
		got := metrics[name]
		if got.Value != want.value || got.Samples != want.samples {
			t.Fatalf("metric %s = %#v, want value=%v samples=%d", name, got, want.value, want.samples)
		}
	}
	if _, ok := metrics["ttfb_p90_ms"]; ok {
		t.Fatalf("new fine metric unexpectedly exposes P90: %#v", metrics["ttfb_p90_ms"])
	}
	if samples := metricSamples(results, "custom_latency_ms"); len(samples) != 3 {
		t.Fatalf("generic metricSamples changed arbitrary SLA cohort: %v", samples)
	}

	distributions := resultDistributions(results)
	foundICL := false
	for _, raw := range distributions {
		var distribution struct {
			Metric  string  `json:"metric"`
			Samples int     `json:"samples"`
			Average float64 `json:"average"`
		}
		if err := json.Unmarshal(raw, &distribution); err != nil {
			t.Fatal(err)
		}
		if distribution.Metric == "observed_icl_ms" {
			foundICL = distribution.Samples == 2 && distribution.Average == 51
		}
	}
	if !foundICL {
		t.Fatalf("observed ICL distribution missing: %s", distributions)
	}

	var failedTimeline map[string]any
	if err := json.Unmarshal(resultTimeline(results)[2], &failedTimeline); err != nil {
		t.Fatal(err)
	}
	if failedTimeline["ttfb_ms"] != float64(100) || failedTimeline["observed_icl_ms"] != float64(100) {
		t.Fatalf("failed partial timeline = %#v", failedTimeline)
	}
}

type fakeReportRepository struct {
	run      domain.Run
	results  []domain.Result
	evidence []domain.Evidence
	report   domain.Report
}

func (repository *fakeReportRepository) GetRun(context.Context, string) (domain.Run, error) {
	return repository.run, nil
}
func (repository *fakeReportRepository) ListResults(context.Context, string) ([]domain.Result, error) {
	return append([]domain.Result(nil), repository.results...), nil
}
func (repository *fakeReportRepository) ListEvidence(context.Context, string) ([]domain.Evidence, error) {
	return append([]domain.Evidence(nil), repository.evidence...), nil
}
func (repository *fakeReportRepository) CreateReport(_ context.Context, report domain.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	repository.report = report
	return nil
}

type fixedReportClock struct{ now time.Time }

func (clock fixedReportClock) Now() time.Time { return clock.now }

func generatorMeta(id string, now time.Time) domain.EntityMeta {
	return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
}

func generatorRun(t *testing.T, now time.Time) domain.Run {
	t.Helper()
	modelID := "40000000-0000-4000-8000-000000000002"
	channelID := "40000000-0000-4000-8000-000000000003"
	caseID := "40000000-0000-4000-8000-000000000004"
	runID := "40000000-0000-4000-8000-000000000005"
	suiteID := "40000000-0000-4000-8000-000000000006"
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	testCase := domain.TestCase{
		EntityMeta: generatorMeta(caseID, now), Key: "basic", Name: "Basic", Dimension: "boundary",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          "openai-chat",
			TypeVersion:   1,
			Spec:          json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`),
		},
	}
	load := domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1_000}}
	suite := domain.Suite{
		EntityMeta: generatorMeta(suiteID, now), Key: "quick-report", Name: "Quick report",
		Protocol: domain.ProtocolOpenAIChat, Cases: []domain.CaseRef{{CaseID: caseID}}, Inputs: []domain.SuiteInput{},
	}
	mapping := domain.ChannelModel{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000010", now),
		ModelID:    modelID, ChannelID: channelID, UpstreamModelName: "upstream",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: runID, Revision: 1},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1}, Name: "model", Protocol: domain.ProtocolOpenAIChat},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1}, Name: "channel", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: "upstream"},
		Environment:   domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "test"},
		Mapping:       &mapping,
		QuickTask:     &domain.QuickTaskSnapshot{},
		PlanDocument:  &domain.Plan{EntityMeta: generatorMeta(runID, now), Name: suite.Name, Protocol: suite.Protocol, Entries: []domain.PlanEntry{{EntryID: runID, TargetKind: domain.PlanTargetSuite, TargetID: suiteID, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla}}},
		Entries:       []domain.RunEntrySnapshot{{EntryID: runID, TargetKind: domain.PlanTargetSuite, TargetID: suiteID, Name: suite.Name, Key: suite.Key, Suite: &suite, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, CaseInputs: map[string]map[string]json.RawMessage{caseID: {}}, Load: load, SLA: sla}},
	}
	run, err := domain.NewRun(generatorMeta(runID, now), runID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCompleted} {
		run, err = run.Transition(status, run.Meta().UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := json.Marshal(run); err != nil {
		t.Fatal(err)
	}
	return run
}

func completedEntryMarker(run domain.Run) domain.Result {
	return domain.Result{EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000090", run.Meta().CreatedAt), RunID: run.Meta().ID, EntryID: run.Snapshot().Entries[0].EntryID, EntryStatus: domain.EntryExecutionCompleted}
}

func TestAssertionRateExcludesObservationOnlyAndIndeterminateResults(t *testing.T) {
	results := []domain.Result{}
	for _, status := range []testspec.VerdictStatus{testspec.VerdictPassed, testspec.VerdictFailed, testspec.VerdictNotApplicable, testspec.VerdictIndeterminate} {
		results = append(results, domain.Result{ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: status, Assertions: []testspec.AssertionResult{}}, Metrics: map[string]float64{"e2e_ms": 10}})
	}
	metrics := aggregateMetrics(results)
	if got := metrics["success_rate"]; got.Value != .5 || got.Samples != 2 {
		t.Fatalf("assertion rate=%#v", got)
	}
	if got := metrics["e2e_ms"]; got.Value != 10 || got.Samples != 4 {
		t.Fatalf("observation population=%#v", got)
	}
	if got := aggregateMetrics(results[2:]); got["observed_count"].Value != 1 {
		t.Fatalf("observations=%#v", got)
	} else if _, exists := got["success_rate"]; exists {
		t.Fatal("unasserted observations fabricated a success rate")
	}
}
