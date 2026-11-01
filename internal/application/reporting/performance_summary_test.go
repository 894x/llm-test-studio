package reporting

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestPerformanceSummaryWeightsTokensAndMergesSamplesWithoutChangingItems(t *testing.T) {
	first := validArchivedQuickPerformanceReport()
	first.Metrics = load.Metrics{Completed: 3, Succeeded: 2, Failed: 1, TimedOut: 1, PromptTokens: 100, CompletionTokens: 20}
	first.Progress.TotalDurationMS = 1_000
	first.Warmup = &quicktest.PerformanceTrafficSummary{PromptTokens: 99_999, CachedTokens: 99_999}
	first.Samples = []quicktest.PerformanceSample{
		{Success: true, TTFTObserved: true, TTFTMS: 0, E2EMS: 100},
		{Success: true, TTFTMS: 100, E2EMS: 200},
		{Success: false, TTFTMS: 99_999, E2EMS: 99_999},
	}
	second := validArchivedQuickPerformanceReport()
	second.Metrics = load.Metrics{Completed: 1, Succeeded: 1, PromptTokens: 900, CompletionTokens: 90, CachedTokens: 810}
	second.Progress.TotalDurationMS = 2_000
	second.Samples = []quicktest.PerformanceSample{{Success: true, TTFTMS: 1_000, E2EMS: 2_000}}
	entries := []EntryDetail{
		summaryEntry(t, first, domain.EntryReportCompleted),
		summaryEntry(t, second, domain.EntryReportCancelled),
		{TargetKind: domain.PlanTargetPerformance, Status: domain.EntryReportNotStarted},
		{TargetKind: domain.PlanTargetSuite, Metrics: map[string]domain.MetricValue{"cached_tokens": {Value: 99_999}}},
	}
	before, _ := json.Marshal(entries)
	summary, err := summarizePerformance(entries)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ItemCount != 3 || summary.MeasuredItemCount != 2 || summary.CompletedItemCount != 1 {
		t.Fatalf("item coverage = %+v", summary)
	}
	if summary.Completed != 4 || summary.Succeeded != 3 || summary.Failed != 1 || summary.TimedOut != 1 {
		t.Fatalf("request totals = %+v", summary)
	}
	if summary.PromptTokens != 1_000 || summary.CompletionTokens != 110 || summary.CachedTokens != 810 {
		t.Fatalf("token totals = %+v", summary)
	}
	if *summary.CacheRatePercent != 81 || *summary.SuccessRatePercent != 75 || *summary.SuccessfulRequestQPS != 1 {
		t.Fatalf("weighted rates = %+v", summary)
	}
	if summary.TTFT.Count != 3 || math.Abs(summary.TTFT.P95MS-910) > 1e-9 ||
		math.Abs(summary.E2E.P95MS-1_820) > 1e-9 {
		t.Fatalf("pooled percentiles = %+v / %+v", summary.TTFT, summary.E2E)
	}
	after, _ := json.Marshal(entries)
	if string(before) != string(after) {
		t.Fatal("summary changed sealed item data")
	}
}

func TestPerformanceSummaryKeepsMissingMeasurementsUnknown(t *testing.T) {
	entries := []EntryDetail{
		{TargetKind: domain.PlanTargetPerformance, Status: domain.EntryReportCancelled},
		{TargetKind: domain.PlanTargetPerformance, Status: domain.EntryReportNotStarted},
	}
	summary, err := summarizePerformance(entries)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Completed != 0 || summary.CacheRatePercent != nil || summary.SuccessRatePercent != nil ||
		summary.SuccessfulRequestQPS != nil || summary.TTFT != nil || summary.E2E != nil {
		t.Fatalf("unknown observations became measurements: %+v", summary)
	}
	for _, entries := range [][]EntryDetail{{}, entries[:1], {{TargetKind: domain.PlanTargetSuite}}} {
		if summary, err := summarizePerformance(entries); err != nil || summary != nil {
			t.Fatalf("non-multiple-performance report: summary=%+v error=%v", summary, err)
		}
	}
}

func TestPerformanceSummaryDoesNotEstimatePercentilesFromMissingSamples(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.Samples = []quicktest.PerformanceSample{}
	entries := []EntryDetail{
		summaryEntry(t, report, domain.EntryReportCompleted),
		summaryEntry(t, report, domain.EntryReportCompleted),
	}
	summary, err := summarizePerformance(entries)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SamplesComplete || summary.TTFT != nil || summary.E2E != nil || summary.Completed != 2 || summary.PromptTokens != 20 {
		t.Fatalf("incomplete samples changed totals or invented percentiles: %+v", summary)
	}
	entries[0].Performance = json.RawMessage(`{`)
	if _, err := summarizePerformance(entries); err == nil {
		t.Fatal("corrupt performance result accepted")
	}
}

func summaryEntry(t *testing.T, report quicktest.PerformanceReport, status domain.EntryReportStatus) EntryDetail {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return EntryDetail{TargetKind: domain.PlanTargetPerformance, Status: status, Performance: raw}
}

func TestPerformanceSummaryAppearsInMachineAndVisualExports(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	entries := []EntryDetail{
		summaryEntry(t, report, domain.EntryReportCompleted),
		summaryEntry(t, report, domain.EntryReportCompleted),
	}
	summary, err := summarizePerformance(entries)
	if err != nil {
		t.Fatal(err)
	}
	detail := exportFixture(t)
	detail.PerformanceSummary = summary
	encoded, err := renderJSON(detail, "summary-test")
	if err != nil {
		t.Fatal(err)
	}
	var decoded Detail
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.PerformanceSummary == nil || *decoded.PerformanceSummary.CacheRatePercent != 20 {
		t.Fatal("JSON lost the weighted summary")
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		html, err := renderHTML(detail, "summary-test", locale)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(html), exportHTMLLabels(locale).PerformanceSummary) ||
			!strings.Contains(string(html), "cache_rate_percent") {
			t.Fatalf("%s HTML lost the summary", locale)
		}
	}
	if !strings.Contains(strings.Join(formalReportImageLines(detail), "\n"), "cached_tokens") {
		t.Fatal("PNG/PDF presentation lost summary tokens")
	}
}
