package sqlite_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestPerformanceSLOPrecisionPersistsAcrossReportBoundaries(t *testing.T) {
	for _, name := range []string{
		"legacy-slo-boundary-220a2185", "legacy-slo-zero-220a2185", "current-slo-boundary", "current-slo-zero",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("../../application/quicktest/testdata/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var report quicktest.PerformanceReport
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			repository := openRepository(t)
			defer repository.Close()
			if err := repository.SaveQuickPerformanceReport(t.Context(), report); err != nil {
				t.Fatal(err)
			}
			loaded, err := repository.GetQuickPerformanceReport(t.Context(), report.ReportID)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(loaded)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("SQLite changed precision, raw values or the conclusion")
			}
			if _, err := jobcontract.EncodePerformanceArchive(loaded); err != nil {
				t.Fatal(err)
			}
			if _, err := quicktest.DecodePlanResult(domain.Result{
				EntryID: report.ReportID, Performance: got,
				Dimensions: map[string]string{"performance_stage": "measured", "performance_boundary": "returned"},
			}); err != nil {
				t.Fatal(err)
			}
			loaded.SLOAssessment.LatencyPrecision = "future_precision"
			if err := repository.SaveQuickPerformanceReport(t.Context(), loaded); err == nil {
				t.Fatal("unsupported precision was saved")
			}
			preserved, err := repository.GetQuickPerformanceReport(t.Context(), report.ReportID)
			if err != nil {
				t.Fatal(err)
			}
			unchanged, err := json.Marshal(preserved)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(unchanged, want) {
				t.Fatal("rejected precision mutated the saved report")
			}
		})
	}
}
