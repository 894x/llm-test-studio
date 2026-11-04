package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func overviewReport() quicktest.PerformanceReport {
	report := validArchivedQuickPerformanceReport()
	report.Profile.SLOTTFTMS, report.Profile.SLOTPOTMS, report.Profile.SLOE2EMS = 3, 4, 12
	report.Profile.SLOTargetPercent = 95
	report.SLOAssessment = &quicktest.PerformanceSLOAssessment{
		LatencyPrecision: domain.SLALatencyTwoDecimalsV1,
		Status:           quicktest.PerformanceSLOFailed,
		Thresholds:       quicktest.PerformanceSLOThresholds{TTFTMS: 3, TPOTMS: 4, E2EMS: 12},
		TargetPercent:    95, TotalRequests: 1, BadRequests: 1,
		Violations: quicktest.PerformanceSLOViolations{TPOT: 1},
	}
	return report
}

func TestPerformanceOverviewRetainsStoredConclusionsAndCountsOnlyAssessedMetrics(t *testing.T) {
	report := overviewReport()
	before, _ := json.Marshal(report)
	overview := performanceOverview(&report)
	if overview.Status != "failed" || overview.SampleCount != 1 || overview.PassedCount != 5 ||
		overview.EvaluatedCount != 6 || overview.ObservedCount != 0 || overview.UnevaluatedCount != 0 {
		t.Fatalf("unexpected overview: %+v", overview)
	}
	want := []string{"passed", "failed", "passed", "passed", "passed", "passed"}
	for index, metric := range overview.Metrics {
		if metric.Status != want[index] {
			t.Fatalf("metric %s: %s, want %s", metric.Key, metric.Status, want[index])
		}
	}
	if !overview.Metrics[3].Reference || *overview.Metrics[3].Target != 40 {
		t.Fatal("current P95 reference must remain separate from the saved per-request SLO limit")
	}
	if *overview.Metrics[4].Target != 12 || overview.Metrics[4].Comparison != "at_most" {
		t.Fatal("saved E2E limit must take precedence over the default reference")
	}
	after, _ := json.Marshal(report)
	if !bytes.Equal(before, after) {
		t.Fatal("presentation projection changed the archived report")
	}
}

func TestPerformanceOverviewAppliesFiveReferenceTargetsWithoutInventingSLO(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	overview := performanceOverview(&report)
	if overview.PassedCount != 5 || overview.EvaluatedCount != 5 || overview.ObservedCount != 1 {
		t.Fatalf("unexpected unconfigured overview: %+v", overview)
	}
	if overview.Rating == nil || overview.Rating.Score != 5 || overview.Rating.MetricCount != 5 {
		t.Fatalf("rating must disclose its five reference metrics: %+v", overview.Rating)
	}
	slo := overview.Metrics[1]
	if slo.Target != nil || slo.Status != "observed" || slo.Reference {
		t.Fatalf("invented target for %s: %+v", slo.Key, slo)
	}
}

func TestPerformanceOverviewReferenceBoundariesDoNotChangeArchivedVerdicts(t *testing.T) {
	for _, test := range []struct {
		name                  string
		success, ttft, tpot   float64
		wantSuccess, wantTTFT string
		wantTPOT              string
	}{
		{name: "below latency limits", success: 99, ttft: 14_999.99, tpot: 39.99,
			wantSuccess: "passed", wantTTFT: "passed", wantTPOT: "passed"},
		{name: "strict latency limits", success: 99, ttft: 15_000, tpot: 40,
			wantSuccess: "passed", wantTTFT: "failed", wantTPOT: "failed"},
		{name: "outside all limits", success: 98.99, ttft: 15_000.01, tpot: 40.01,
			wantSuccess: "failed", wantTTFT: "failed", wantTPOT: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := validArchivedQuickPerformanceReport()
			report.Success = false
			report.Metrics.SuccessRatePercent = test.success
			report.Metrics.TTFTP95, report.Metrics.TPOTP95 = test.ttft, test.tpot
			// P90 deliberately remains below the limits; the requested layout retains P95.
			report.Metrics.TTFTP90, report.Metrics.TPOTP90 = 1, 1
			before, _ := json.Marshal(report)
			overview := performanceOverview(&report)
			want := map[int]string{0: test.wantSuccess, 2: test.wantTTFT, 3: test.wantTPOT}
			for index, status := range want {
				if overview.Metrics[index].Status != status || !overview.Metrics[index].Reference {
					t.Fatalf("unexpected reference assessment: %+v", overview.Metrics[index])
				}
			}
			after, _ := json.Marshal(report)
			if !bytes.Equal(before, after) || report.Success || report.SLOAssessment != nil {
				t.Fatal("current reference assessment changed archived report meaning")
			}
		})
	}
}

func TestPerformanceOverviewDoesNotEvaluateInterruptedOrEmptyRuns(t *testing.T) {
	report := overviewReport()
	report.Progress.Stopped = true
	report.SLOAssessment.Status = quicktest.PerformanceSLONotEvaluated
	overview := performanceOverview(&report)
	if overview.Status != "not_evaluated" || overview.EvaluatedCount != 0 || overview.UnevaluatedCount != 6 {
		t.Fatalf("stopped run was assessed: %+v", overview)
	}
	if overview.Rating != nil {
		t.Fatal("an unassessed run must not receive a rating")
	}
	report.Metrics = load.Metrics{}
	report.Samples = []quicktest.PerformanceSample{}
	report.SLOAssessment.TotalRequests = 0
	overview = performanceOverview(&report)
	for _, metric := range overview.Metrics {
		if metric.Value != nil || metric.Status != "unavailable" {
			t.Fatalf("missing metric became an observed zero: %+v", metric)
		}
	}
}

func TestPerformanceOverviewRetainsObservedZeroLatencyAndZeroThroughput(t *testing.T) {
	report := overviewReport()
	report.Metrics.TTFTP95, report.Metrics.TPOTP95 = 0, 0
	report.Samples[0].TTFTMS, report.Samples[0].TPOTMS = 0, 0
	report.Samples[0].TTFTObserved = true
	overview := performanceOverview(&report)
	for _, index := range []int{2, 3} {
		metric := overview.Metrics[index]
		if metric.Value == nil || *metric.Value != 0 || metric.Status != "passed" {
			t.Fatalf("observed zero was discarded: %+v", metric)
		}
	}
	report.Metrics.Succeeded, report.Metrics.SuccessfulRequestQPS = 0, 0
	metric := performanceOverview(&report).Metrics[5]
	if metric.Value == nil || *metric.Value != 0 || metric.Status != "failed" {
		t.Fatalf("valid zero throughput was discarded: %+v", metric)
	}
}

func TestPerformanceOverviewUsesTheArchivedLatencyPrecision(t *testing.T) {
	for _, test := range []struct {
		value, target float64
		precision     string
		want          string
	}{
		{value: 1.004, target: 1, precision: domain.SLALatencyTwoDecimalsV1, want: "passed"},
		{value: 1.005, target: 1, precision: domain.SLALatencyTwoDecimalsV1, want: "failed"},
		{value: 1.004, target: 1, want: "failed"},
		{value: 1, target: 1, want: "passed"},
	} {
		if got := compareOverviewLatency(test.value, test.target, test.precision); got != test.want {
			t.Fatalf("compare %v with %v (%q): %s, want %s", test.value, test.target, test.precision, got, test.want)
		}
	}
}

func TestPerformanceOverviewPreservesLegacyPositiveTPOTWithoutNewObservationMarkers(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.Samples[0].TTFTMS = 0
	report.Samples[0].TTFTObserved = false
	metric := performanceOverview(&report).Metrics[3]
	if metric.Value == nil || *metric.Value != report.Metrics.TPOTP95 {
		t.Fatal("legacy positive TPOT observation was hidden")
	}
}

func TestReportDetailIncludesOverviewWithoutChangingJSONExportOrArchivedData(t *testing.T) {
	report := overviewReport()
	if _, err := quicktest.ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("invalid integration fixture: %v", err)
	}
	detail, err := New(&fakeMixedCatalog{get: report}).Detail(context.Background(), report.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.PerformanceOverviews[report.ReportID].EvaluatedCount != 6 {
		t.Fatal("the report query did not supply its authoritative overview")
	}
	rating := detail.PerformanceOverviews[report.ReportID].Rating
	if rating == nil || rating.Score != 4.17 || rating.MetricCount != 6 || rating.Method != "target_ratio_v1" {
		t.Fatalf("unexpected target-closeness rating: %+v", rating)
	}
	encoded, err := renderJSON(detail, "watermark")
	if err != nil || bytes.Contains(encoded, []byte("performance_overviews")) {
		t.Fatalf("presentation projection changed the published JSON export: %v", err)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		html, err := renderHTML(detail, "watermark", locale)
		if err != nil || !bytes.Contains(html, []byte("5 / 6")) || !bytes.Contains(html, []byte(`class="po-metrics"`)) {
			t.Fatalf("HTML is missing the same overview (%s): %v", locale, err)
		}
		if locale == "en-US" && !strings.Contains(string(html), "Reference target") {
			t.Fatal("HTML export omitted the reference-limit semantics")
		}
		if !strings.Contains(string(html), "4.17 / 5") || strings.Contains(string(html), "ZgotmplZ") {
			t.Fatal("HTML rating must match Core and preserve valid fractional stars")
		}
	}
}

func TestPerformanceOverviewE2EAndThroughputReferenceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, e2eStatus, throughputStatus string
		e2e, qps                          float64
	}{
		{name: "within limits", e2e: 29_999.99, qps: 2, e2eStatus: "passed", throughputStatus: "passed"},
		{name: "at E2E limit", e2e: 30_000, qps: 2, e2eStatus: "failed", throughputStatus: "passed"},
		{name: "below throughput", e2e: 30_000.01, qps: 1.99, e2eStatus: "failed", throughputStatus: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := validArchivedQuickPerformanceReport()
			report.Metrics.E2EP95, report.Metrics.SuccessfulRequestQPS = test.e2e, test.qps
			overview := performanceOverview(&report)
			latency, throughput := overview.Metrics[4], overview.Metrics[5]
			if latency.Status != test.e2eStatus || *latency.Target != 30_000 || latency.Comparison != "less_than" {
				t.Fatalf("unexpected E2E reference: %+v", latency)
			}
			if throughput.Status != test.throughputStatus || *throughput.Target != 2 || !throughput.Reference {
				t.Fatalf("unexpected throughput reference: %+v", throughput)
			}
			if overview.Rating == nil || overview.Rating.MetricCount != 5 {
				t.Fatal("both references must contribute to the rating")
			}
		})
	}
	report := validArchivedQuickPerformanceReport()
	report.Metrics.SuccessfulRequestQPS = 0
	if metric := performanceOverview(&report).Metrics[5]; metric.Value != nil || metric.Status != "unavailable" {
		t.Fatal("missing legacy throughput must not be treated as an observed zero")
	}
}

func TestPerformanceTargetRating(t *testing.T) {
	metric := func(status, comparison string, value, target float64) PerformanceOverviewMetric {
		return PerformanceOverviewMetric{
			Status: status, Comparison: comparison, Value: floatPointer(value), Target: floatPointer(target),
		}
	}
	passed := metric("passed", "at_least", 100, 100)
	for _, test := range []struct {
		name    string
		metrics []PerformanceOverviewMetric
		score   float64
		count   uint64
	}{
		{name: "full", metrics: []PerformanceOverviewMetric{passed}, score: 5, count: 1},
		{name: "minimum ratio", metrics: []PerformanceOverviewMetric{
			passed, passed, passed, passed, metric("failed", "at_least", 65, 100),
		}, score: 4.65, count: 5},
		{name: "maximum ratio", metrics: []PerformanceOverviewMetric{
			passed, metric("failed", "at_most", 200, 100),
		}, score: 3.75, count: 2},
		{name: "strict maximum ratio", metrics: []PerformanceOverviewMetric{
			passed, metric("failed", "less_than", 80, 40),
		}, score: 3.75, count: 2},
		{name: "zero success", metrics: []PerformanceOverviewMetric{
			metric("failed", "at_least", 0, 100),
		}, count: 1},
		{name: "archived rounded pass", metrics: []PerformanceOverviewMetric{
			metric("passed", "at_most", 1.004, 1),
		}, score: 5, count: 1},
		{name: "observed zero latency", metrics: []PerformanceOverviewMetric{
			metric("passed", "at_most", 0, 100),
		}, score: 5, count: 1},
		{name: "exclude unassessed", metrics: []PerformanceOverviewMetric{
			passed, metric("not_evaluated", "at_most", 200, 100), {Status: "observed"}, {Status: "unavailable"},
		}, score: 5, count: 1},
		{name: "no targets", metrics: []PerformanceOverviewMetric{{Status: "observed"}}},
		{name: "no value", metrics: []PerformanceOverviewMetric{{Status: "failed", Target: floatPointer(100)}}},
		{name: "nonfinite", metrics: []PerformanceOverviewMetric{metric("failed", "at_least", math.NaN(), 100)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := performanceTargetRating(test.metrics)
			if test.count == 0 {
				if got != nil {
					t.Fatalf("unexpected rating: %+v", got)
				}
				return
			}
			if got == nil || got.Score != test.score || got.MetricCount != test.count || got.MaxScore != 5 {
				t.Fatalf("got %+v, want score %v from %d metrics", got, test.score, test.count)
			}
		})
	}
}
