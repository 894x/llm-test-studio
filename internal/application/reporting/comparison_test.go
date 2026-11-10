package reporting

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func comparisonPair(t *testing.T) (Detail, Detail) {
	t.Helper()
	left := exportFixture(t)
	raw, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	var right Detail
	if err := json.Unmarshal(raw, &right); err != nil {
		t.Fatal(err)
	}
	right.Report.ID = "42000000-0000-4000-8000-000000000090"
	right.Report.RunID = "42000000-0000-4000-8000-000000000091"
	right.Report.GeneratedAt = left.Report.GeneratedAt.Add(time.Hour)
	return left, right
}

func TestComparisonRetainsSameChannelHistoryAndDoesNotMutateReports(t *testing.T) {
	left, right := comparisonPair(t)
	before, _ := json.Marshal([]Detail{left, right})
	comparison, err := Compare([]Detail{left, right}, left.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Reports) != 2 || comparison.Reports[0].Report.Channel.ID != comparison.Reports[1].Report.Channel.ID {
		t.Fatalf("same-channel reports were lost: %#v", comparison.Reports)
	}
	if len(comparison.Rows) != 1 || comparison.Rows[0].Different || comparison.Rows[0].Cells[1].Change != "same" {
		t.Fatalf("equal historical reports: %#v", comparison.Rows)
	}
	after, _ := json.Marshal([]Detail{left, right})
	if string(before) != string(after) {
		t.Fatal("comparison mutated the source reports")
	}
}

func TestComparisonProjectsRegressionRecoveryAndChangedConditions(t *testing.T) {
	left, right := comparisonPair(t)
	right.Entries[0].Cases[0].Verification.Status = "failed"
	comparison, err := Compare([]Detail{left, right}, left.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Rows[0].Cells[1].Change != "regressed" || !comparison.Rows[0].Different {
		t.Fatalf("regression: %#v", comparison.Rows)
	}
	reversed, err := Compare([]Detail{left, right}, right.Report.ID)
	if err != nil || reversed.Rows[0].Cells[0].Change != "recovered" {
		t.Fatalf("recovery: %#v, %v", reversed.Rows, err)
	}
	right.Entries[0].Load.Concurrency++
	changed, err := Compare([]Detail{left, right}, left.Report.ID)
	if err != nil || changed.Rows[0].Cells[1].Change != "conditions_changed" {
		t.Fatalf("changed load: %#v, %v", changed.Rows, err)
	}
	for _, metric := range changed.Rows[0].Cells[1].Metrics {
		if metric.Delta != nil {
			t.Fatal("different workloads must not receive a delta")
		}
	}
}

func TestComparisonAlignsFrozenIdentityAcrossPlansAndSeparatesRepeatedEntries(t *testing.T) {
	left, right := comparisonPair(t)
	right.Entries[0].EntryID = "42000000-0000-4000-8000-000000000092"
	comparison, err := Compare([]Detail{left, right}, left.Report.ID)
	if err != nil || len(comparison.Rows) != 1 || comparison.Rows[0].Cells[1].Change != "same" {
		t.Fatalf("same suite from independent plan entries: %#v, %v", comparison.Rows, err)
	}
	repeated := right.Entries[0]
	repeated.EntryID = "42000000-0000-4000-8000-000000000093"
	right.Entries = append(right.Entries, repeated)
	right.Report.PlanSnapshot.Entries = append(right.Report.PlanSnapshot.Entries, right.Report.PlanSnapshot.Entries[0])
	comparison, err = Compare([]Detail{left, right}, left.Report.ID)
	if err != nil || len(comparison.Rows) != 2 {
		t.Fatalf("repeated suites: %#v, %v", comparison.Rows, err)
	}
	if comparison.Rows[1].Cells[0].Change != "missing" || comparison.Rows[1].Cells[1].Change != "added" {
		t.Fatalf("missing coverage must not be failed: %#v", comparison.Rows[1])
	}
	third := right
	third.Report.ID = "42000000-0000-4000-8000-000000000094"
	third.Entries = []EntryDetail{left.Entries[0], right.Entries[0]}
	comparison, err = Compare([]Detail{left, right, third}, left.Report.ID)
	if err != nil || len(comparison.Rows) != 2 {
		t.Fatalf("combined independent entry identities: %#v, %v", comparison.Rows, err)
	}
	if comparison.Rows[0].Cells[2].EntryID == comparison.Rows[1].Cells[2].EntryID {
		t.Fatal("repeated target entries overwrote a report column")
	}
}

func TestComparisonDetectsDefinitionChangesWithTheSameCaseName(t *testing.T) {
	left, right := comparisonPair(t)
	right.Report.PlanSnapshot.Entries[0].CaseDefinitions[0].Revision++
	comparison, err := Compare([]Detail{left, right}, left.Report.ID)
	if err != nil || comparison.Rows[0].Cells[1].Change != "conditions_changed" {
		t.Fatalf("frozen definition revision: %#v, %v", comparison.Rows, err)
	}
}

func TestComparisonMetricDeltaUsesMatchingUnitsAndObservedSamples(t *testing.T) {
	left, right := comparisonPair(t)
	left.Entries[0].Cases[0].Metrics = map[string]domain.MetricValue{"e2e_ms": {Value: 20, Unit: "ms", Samples: 1}}
	right.Entries[0].Cases[0].Metrics = map[string]domain.MetricValue{
		"e2e_ms": {Value: 35, Unit: "ms", Samples: 2}, "missing": {Value: 0, Unit: "ms", Samples: 0},
	}
	comparison, err := Compare([]Detail{left, right}, left.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	metrics := comparison.Rows[0].Cells[1].Metrics
	if len(metrics) != 1 || metrics[0].Delta == nil || *metrics[0].Delta != 15 {
		t.Fatalf("observed delta: %#v", metrics)
	}
	right.Entries[0].Cases[0].Metrics["e2e_ms"] = domain.MetricValue{Value: 1, Unit: "s", Samples: 1}
	comparison, err = Compare([]Detail{left, right}, left.Report.ID)
	if err != nil || comparison.Rows[0].Cells[1].Metrics[0].Delta != nil {
		t.Fatalf("unit mismatch: %#v, %v", comparison.Rows, err)
	}
}

func TestComparisonRejectsInvalidSelections(t *testing.T) {
	left, right := comparisonPair(t)
	for _, details := range [][]Detail{{left}, {left, left}, make([]Detail, MaxComparisonReports+1)} {
		if _, err := Compare(details, left.Report.ID); err == nil {
			t.Fatalf("accepted invalid report selection of length %d", len(details))
		}
	}
	if _, err := Compare([]Detail{left, right}, "42000000-0000-4000-8000-000000000099"); err == nil {
		t.Fatal("accepted a foreign baseline")
	}
	if !reflect.DeepEqual(left.Report.Channel, right.Report.Channel) {
		t.Fatal("test requires the same historical channel")
	}
}

func TestPerformanceComparisonRetainsMetricSpecificSampleCounts(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.Metrics.Completed = 5
	report.Metrics.Succeeded = 3
	report.Metrics.TTFTSamples = 2
	value, err := performanceComparison(report, report.ReportID, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"success_rate": 5, "ttft_p95": 2, "tpot_p95": 1, "e2e_p95": 3}
	for _, metric := range value.cell.Metrics {
		if count, exists := want[metric.Key]; exists && metric.Samples != count {
			t.Fatalf("%s samples = %d, want %d", metric.Key, metric.Samples, count)
		}
	}
}
