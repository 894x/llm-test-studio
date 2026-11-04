package reporting

import (
	"bytes"
	"context"
	"encoding/json"
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
	if overview.Status != "failed" || overview.SampleCount != 1 || overview.PassedCount != 3 ||
		overview.EvaluatedCount != 5 || overview.ObservedCount != 1 || overview.UnevaluatedCount != 0 {
		t.Fatalf("unexpected overview: %+v", overview)
	}
	want := []string{"passed", "failed", "passed", "failed", "passed", "observed"}
	for index, metric := range overview.Metrics {
		if metric.Status != want[index] {
			t.Fatalf("metric %s: %s, want %s", metric.Key, metric.Status, want[index])
		}
	}
	if !overview.Metrics[3].Reference || *overview.Metrics[3].Target != 4 {
		t.Fatal("P95 limit must be explicitly identified as a saved SLO reference")
	}
	after, _ := json.Marshal(report)
	if !bytes.Equal(before, after) {
		t.Fatal("presentation projection changed the archived report")
	}
}

func TestPerformanceOverviewDoesNotInventUnconfiguredTargets(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	overview := performanceOverview(&report)
	if overview.PassedCount != 1 || overview.EvaluatedCount != 1 || overview.ObservedCount != 5 {
		t.Fatalf("unexpected unconfigured overview: %+v", overview)
	}
	for _, metric := range overview.Metrics[1:] {
		if metric.Target != nil || metric.Status != "observed" || metric.Reference {
			t.Fatalf("invented target for %s: %+v", metric.Key, metric)
		}
	}
}

func TestPerformanceOverviewDoesNotEvaluateInterruptedOrEmptyRuns(t *testing.T) {
	report := overviewReport()
	report.Progress.Stopped = true
	report.SLOAssessment.Status = quicktest.PerformanceSLONotEvaluated
	overview := performanceOverview(&report)
	if overview.Status != "not_evaluated" || overview.EvaluatedCount != 0 || overview.UnevaluatedCount != 5 {
		t.Fatalf("stopped run was assessed: %+v", overview)
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
	if metric.Value == nil || *metric.Value != 0 || metric.Status != "observed" {
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
	if detail.PerformanceOverviews[report.ReportID].EvaluatedCount != 5 {
		t.Fatal("the report query did not supply its authoritative overview")
	}
	encoded, err := renderJSON(detail, "watermark")
	if err != nil || bytes.Contains(encoded, []byte("performance_overviews")) {
		t.Fatalf("presentation projection changed the published JSON export: %v", err)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		html, err := renderHTML(detail, "watermark", locale)
		if err != nil || !bytes.Contains(html, []byte("3 / 5")) || !bytes.Contains(html, []byte(`class="po-metrics"`)) {
			t.Fatalf("HTML is missing the same overview (%s): %v", locale, err)
		}
		if locale == "en-US" && !strings.Contains(string(html), "Reference limit") {
			t.Fatal("HTML export omitted the reference-limit semantics")
		}
	}
}
