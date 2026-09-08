package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestGeneratorBuildsAndPersistsACompletePerformanceReport(t *testing.T) {
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	run := generatorRun(t, now)
	requestResult := domain.Result{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000007", now),
		RunID:      run.Meta().ID, CaseID: run.Snapshot().Cases[0].CaseID, RequestID: "request-1",
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics: map[string]float64{"e2e_ms": 25, "ttft_ms": 10, "prompt_tokens": 3},
	}
	summaryResult := requestResult
	summaryResult.EntityMeta = generatorMeta("40000000-0000-4000-8000-000000000009", now)
	summaryResult.CaseID = run.Snapshot().Cases[0].CaseID
	summaryResult.RequestID = ""
	repository := &fakeReportRepository{run: run, results: []domain.Result{requestResult, summaryResult}, evidence: []domain.Evidence{}}
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
		RunID:      run.Meta().ID, CaseID: run.Snapshot().Cases[0].CaseID, RequestID: "request-1",
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics: map[string]float64{"e2e_ms": 1500},
	}
	summaryResult := requestResult
	summaryResult.EntityMeta = generatorMeta("40000000-0000-4000-8000-000000000019", now)
	summaryResult.CaseID = run.Snapshot().Cases[0].CaseID
	summaryResult.RequestID = ""
	repository := &fakeReportRepository{run: run, results: []domain.Result{requestResult, summaryResult}, evidence: []domain.Evidence{}}
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
	if metric := repository.report.SLA["e2e_p95_ms"]; metric.Value != 1500 || metric.Samples != 1 {
		t.Fatalf("observed SLA metric = %#v", metric)
	}
}

func TestQuickTaskDetailUsesTheSuiteAwareSchema(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 30, 0, 0, time.UTC)
	run := generatorRun(t, now)
	snapshot := run.Snapshot()
	requestResult := domain.Result{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000027", now),
		RunID:      run.Meta().ID, CaseID: snapshot.Cases[0].CaseID, RequestID: "request-1",
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics: map[string]float64{"e2e_ms": 25},
	}
	summaryResult := requestResult
	summaryResult.EntityMeta = generatorMeta("40000000-0000-4000-8000-000000000028", now)
	summaryResult.RequestID = ""
	repository := &fakeReportRepository{
		run: run, results: []domain.Result{requestResult, summaryResult}, evidence: []domain.Evidence{},
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
	if len(repository.report.SuiteReports) != 1 ||
		repository.report.SuiteReports[0].SuiteEntryID != run.Meta().ID ||
		len(repository.report.CaseResults) != 1 || repository.report.CaseResults[0].SuiteEntryID != run.Meta().ID ||
		len(repository.report.SuiteReports[0].CaseResults) != 1 ||
		repository.report.SuiteReports[0].CaseResults[0].SuiteEntryID != run.Meta().ID {
		t.Fatalf("canonical quick-task report tree = %#v", repository.report)
	}
	detail, err := New(&fakeDocumentCatalog{report: repository.report, results: repository.results}).Detail(
		context.Background(),
		repository.report.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != CurrentDetailSchemaVersion || len(detail.Suites) != 1 ||
		detail.Suites[0].SuiteEntryID != run.Meta().ID ||
		detail.Suites[0].SuiteID != snapshot.QuickTask.Suite.ID ||
		len(detail.Suites[0].Cases) != 1 ||
		detail.Suites[0].Cases[0].SummaryResult == nil ||
		detail.Suites[0].Cases[0].SummaryResult.SuiteEntryID != run.Meta().ID ||
		len(detail.Suites[0].Cases[0].RequestResults) != 1 ||
		detail.Suites[0].Cases[0].RequestResults[0].SuiteEntryID != run.Meta().ID ||
		len(detail.UnassignedRequestResults) != 0 {
		t.Fatalf("quick task detail = %#v", detail)
	}
}

func TestProbeDistributionsGroupByCaseBucketAndShape(t *testing.T) {
	caseID := "40000000-0000-4000-8000-000000000004"
	results := []domain.Result{
		{RequestID: "request-1", Dimensions: map[string]string{"probe_case_id": caseID, "probe_bucket": "provider-a", "probe_classification": "matched", "probe_format": "json", "probe_shape": "sha256:known"}},
		{RequestID: "request-2", Dimensions: map[string]string{"probe_case_id": caseID, "probe_bucket": "provider-a", "probe_classification": "matched", "probe_format": "json", "probe_shape": "sha256:known"}},
		{RequestID: "request-3", Dimensions: map[string]string{"probe_case_id": caseID, "probe_bucket": "unknown", "probe_classification": "unknown", "probe_format": "json", "probe_shape": "sha256:mystery"}},
	}
	distributions := probeDistributions(results)
	if len(distributions) != 2 {
		t.Fatalf("probe distribution count = %d", len(distributions))
	}
	var first map[string]any
	if err := json.Unmarshal(distributions[0], &first); err != nil {
		t.Fatal(err)
	}
	if first["kind"] != "response_probe" || first["case_id"] != caseID || first["bucket"] != "provider-a" ||
		first["count"] != float64(2) || first["share_percent"] != 200.0/3.0 {
		t.Fatalf("first probe distribution = %#v", first)
	}
}

func TestSLAObservationSupportsLegacyDesktopAliases(t *testing.T) {
	results := []domain.Result{{
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
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

func TestFineStreamingReportMetricsUseOnlySuccessfulCohorts(t *testing.T) {
	success := domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}
	results := []domain.Result{
		{Success: success, Metrics: map[string]float64{
			"e2e_ms": 10, "ttfb_ms": 1, "ttft_ms": 2, "ttft_any_ms": 2, "ttft_visible_ms": 3,
			"ttst_ms": 4, "observed_icl_ms": 2, "semantic_chunk_count": 2, "custom_latency_ms": 7,
		}},
		{Success: success, Metrics: map[string]float64{
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
		"e2e_ms": {15, 2}, "ttfb_ms": {2, 2}, "ttfb_average_ms": {2, 2},
		"ttft_any_ms": {2, 1}, "ttft_any_p99_ms": {2, 1},
		"observed_icl_ms": {2, 1}, "semantic_chunk_count": {1, 2}, "semantic_chunk_count_average": {1, 2},
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
			foundICL = distribution.Samples == 1 && distribution.Average == 2
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
			Type:          "request.single",
			TypeVersion:   1,
			Spec: json.RawMessage(
				`{"assertions":[{"config":{"contains":"ok"},"kind":"text"}],"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"request":{"body":{"messages":[{"content":"hello","role":"user"}]},"headers":{"Content-Type":"application/json"},"method":"POST","path":"/chat/completions"}}`,
			),
		},
	}
	load := domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1_000}}
	suite := domain.Suite{
		EntityMeta: generatorMeta(suiteID, now), Key: "quick-report", Name: "Quick report",
		Protocol: domain.ProtocolOpenAIChat, ModelTarget: "upstream", Cases: []domain.CaseRevisionRef{caseRef},
		QuickTest: &domain.SuiteQuickTest{Description: "Quick report", TimeoutMS: 1_000, Inputs: []domain.SuiteInput{}},
	}
	mapping := domain.ChannelModel{
		EntityMeta: generatorMeta("40000000-0000-4000-8000-000000000010", now),
		ModelID:    modelID, ChannelID: channelID, UpstreamModelName: "upstream",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.FlatRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: runID, Revision: 1},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1}, Name: "model", Protocol: domain.ProtocolOpenAIChat},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1}, Name: "channel", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: "upstream"},
		Cases:         []domain.CaseRevisionRef{caseRef}, Load: load, SLA: sla,
		Environment:     domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "test"},
		Mapping:         &mapping,
		CaseDefinitions: []domain.TestCase{testCase},
		QuickTask:       &domain.QuickTaskSnapshot{Suite: suite, Inputs: map[string]json.RawMessage{}},
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
