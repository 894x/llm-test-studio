package sqlite_test

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
)

func TestReportComparisonReadsSameChannelArchivesWithoutChangingStoredFacts(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	left := validQuickPerformanceReport("77777777-7777-4777-8777-777777777774", "2026-08-31T15:33:00Z")
	right := validQuickPerformanceReport("77777777-7777-4777-8777-777777777775", "2026-08-31T16:33:00Z")
	right.Profile.TimeoutMS++
	for _, value := range []quicktest.PerformanceReport{left, right} {
		if err := repository.SaveQuickPerformanceReport(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	service := reporting.New(repository)
	first, err := service.Detail(t.Context(), left.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Detail(t.Context(), right.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal([]reporting.Detail{first, second})
	comparison, err := reporting.Compare([]reporting.Detail{first, second}, left.ReportID)
	if err != nil || len(comparison.Reports) != 2 || len(comparison.Rows) != 1 {
		t.Fatalf("stored comparison: %#v, %v", comparison.Rows, err)
	}
	if comparison.Rows[0].Cells[1].Change != "conditions_changed" {
		t.Fatal("different archived performance profiles were treated as comparable")
	}
	for _, metric := range comparison.Rows[0].Cells[1].Metrics {
		if metric.Delta != nil {
			t.Fatal("different archived loads must not have performance deltas")
		}
	}
	first, err = service.Detail(t.Context(), left.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	second, err = service.Detail(t.Context(), right.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal([]reporting.Detail{first, second})
	if string(before) != string(after) {
		t.Fatal("comparison changed stored report facts")
	}
}
