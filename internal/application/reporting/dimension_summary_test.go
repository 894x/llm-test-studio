package reporting

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestDimensionStatisticsCountCasesInsteadOfRequests(t *testing.T) {
	request := func(status testspec.VerdictStatus, phase string) domain.Result {
		return domain.Result{
			Verification: testspec.Verdict{Status: status},
			Dimensions:   map[string]string{"phase": phase},
		}
	}
	caseDetail := func(dimension string, requests ...domain.Result) CaseDetail {
		return CaseDetail{
			CaseID: "same-case", Dimension: dimension,
			Verification: domain.SummarizeVerification(requests), RequestResults: requests,
		}
	}
	passed := request(testspec.VerdictPassed, "")
	failed := request(testspec.VerdictFailed, "")
	entries := []EntryDetail{
		{TargetKind: domain.PlanTargetSuite, Cases: []CaseDetail{
			caseDetail("tools", passed, passed, passed),
			caseDetail("tools", passed, failed),
			caseDetail("tools", request(testspec.VerdictPassed, "warmup")),
			caseDetail("protocol", request(testspec.VerdictNotApplicable, "")),
			caseDetail("multimodal", passed, request(testspec.VerdictIndeterminate, "")),
			caseDetail("custom", request(testspec.VerdictFailed, "checkpoint")),
		}},
		{TargetKind: domain.PlanTargetCase, Cases: []CaseDetail{caseDetail("tools", passed)}},
		{TargetKind: domain.PlanTargetPerformance, Cases: []CaseDetail{caseDetail("performance", passed)}},
	}
	before, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	rate := 200.0 / 3
	want := []DimensionStatistic{
		{Dimension: "custom", CaseCount: 1, NotRunCaseCount: 1},
		{Dimension: "multimodal", CaseCount: 1, IndeterminateCaseCount: 1},
		{Dimension: "protocol", CaseCount: 1, ObservedCaseCount: 1},
		{Dimension: "tools", CaseCount: 4, PassedCaseCount: 2, FailedCaseCount: 1,
			NotRunCaseCount: 1, PassRatePercent: &rate},
	}
	if got := summarizeDimensions(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("dimension statistics = %#v, want %#v", got, want)
	}
	after, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("statistics mutated report detail")
	}
	if got := summarizeDimensions([]EntryDetail{}); got == nil || len(got) != 0 {
		t.Fatalf("empty statistics = %#v", got)
	}
}

func TestHistoricalReportDimensionsUseFrozenSnapshotAndPreserveSealedDocument(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy-sla-precision-c561055d.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Report  domain.Report
		Results []domain.Result
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	service := New(&fakeDocumentCatalog{report: fixture.Report, results: fixture.Results})
	detail, err := service.Detail(t.Context(), fixture.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantDimension := fixture.Report.PlanSnapshot.Entries[0].CaseDefinitions[0].Dimension
	if len(detail.DimensionStatistics) != 1 || detail.DimensionStatistics[0].Dimension != wantDimension ||
		detail.Entries[0].Cases[0].Dimension != wantDimension {
		t.Fatalf("historical dimension statistics = %#v", detail.DimensionStatistics)
	}
	if !reflect.DeepEqual(detail.Report, fixture.Report) || detail.Report.Conclusion.Passed {
		t.Fatal("reading dimensions changed the historical failure")
	}
	for _, locale := range []string{"en-US", "zh-CN"} {
		html, err := renderHTML(detail, "dimension-test", locale)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(html), exportHTMLLabels(locale).DimensionStatistics) ||
			!strings.Contains(string(html), wantDimension) {
			t.Fatalf("%s HTML omits dimension statistics", locale)
		}
	}
	exported, err := renderJSON(detail, "dimension-test")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Report              json.RawMessage      `json:"report"`
		DimensionStatistics []DimensionStatistic `json:"dimension_statistics"`
	}
	if err := json.Unmarshal(exported, &document); err != nil {
		t.Fatal(err)
	}
	sealedReport, err := json.Marshal(fixture.Report)
	if err != nil {
		t.Fatal(err)
	}
	var exportedReport bytes.Buffer
	if err := json.Compact(&exportedReport, document.Report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.DimensionStatistics, detail.DimensionStatistics) ||
		!bytes.Equal(exportedReport.Bytes(), sealedReport) {
		t.Fatal("JSON export changed the sealed report or omitted statistics")
	}
	if lines := strings.Join(formalReportImageLines(detail), "\n"); !strings.Contains(lines, "COMPATIBILITY BY DIMENSION") || !strings.Contains(lines, "DIMENSION "+wantDimension) {
		t.Fatal("PNG/PDF layout omits dimension statistics")
	}
	after, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("historical source was modified")
	}
}
