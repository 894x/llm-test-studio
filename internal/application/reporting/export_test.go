package reporting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

type fakeDocumentCatalog struct {
	fakeCatalog
	report  domain.Report
	results []domain.Result
}

func (catalog *fakeDocumentCatalog) GetReport(context.Context, string) (domain.Report, error) {
	return catalog.report, nil
}

func (catalog *fakeDocumentCatalog) ListResults(context.Context, string) ([]domain.Result, error) {
	return append([]domain.Result(nil), catalog.results...), nil
}

func TestDetailAndExportsUseTheSameSealedReportAndRequestResults(t *testing.T) {
	detail := exportFixture(t)
	catalog := &fakeDocumentCatalog{report: detail.Report, results: detail.RequestResults}
	service := New(catalog)

	loaded, err := service.Detail(context.Background(), detail.Report.ID)
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if loaded.Report.ID != detail.Report.ID || len(loaded.RequestResults) != 1 || loaded.RequestResults[0].RequestID != "request-1" {
		t.Fatalf("detail = %#v", loaded)
	}

	for _, test := range []struct {
		format    ExportFormat
		mediaType string
		prefix    []byte
		contains  string
	}{
		{ExportJSON, "application/json", []byte("{"), `"request_id": "request-1"`},
		{ExportHTML, "text/html; charset=utf-8", []byte("<!doctype html>"), "request-1"},
		{ExportPNG, "image/png", []byte("\x89PNG\r\n\x1a\n"), ""},
		{ExportPDF, "application/pdf", []byte("%PDF-1.4"), ""},
	} {
		t.Run(string(test.format), func(t *testing.T) {
			exported, err := service.Export(context.Background(), detail.Report.ID, test.format, "team-alpha")
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			if exported.MediaType != test.mediaType || !strings.HasSuffix(exported.Filename, "."+string(test.format)) {
				t.Fatalf("metadata = %#v", exported)
			}
			contents, err := base64.StdEncoding.DecodeString(exported.DataBase64)
			if err != nil {
				t.Fatalf("base64: %v", err)
			}
			if !strings.HasPrefix(string(contents), string(test.prefix)) {
				t.Fatalf("prefix = %q, want %q", contents[:min(len(contents), 20)], test.prefix)
			}
			if test.contains != "" && !strings.Contains(string(contents), test.contains) {
				t.Fatalf("export does not contain %q", test.contains)
			}
			if test.format == ExportJSON {
				var document map[string]any
				if err := json.Unmarshal(contents, &document); err != nil {
					t.Fatal(err)
				}
				if _, exists := document["performance"]; exists {
					t.Fatalf("formal report JSON contains an inapplicable performance field: %s", contents)
				}
			}
			if (test.format == ExportJSON || test.format == ExportHTML) && !strings.Contains(string(contents), "team-alpha") {
				t.Fatalf("export does not contain the configured watermark")
			}
			for _, secret := range []string{"sk-do-not-leak", "authorization"} {
				if strings.Contains(strings.ToLower(string(contents)), secret) {
					t.Fatalf("export leaked %q", secret)
				}
			}
		})
	}
}

func TestExportDefaultsWatermarkAndSupportsQuickPerformanceReports(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	service := New(&fakeMixedCatalog{get: report})

	for _, test := range []struct {
		format    ExportFormat
		mediaType string
		prefix    []byte
		contains  string
	}{
		{ExportJSON, "application/json", []byte("{"), `"source": "quick_performance"`},
		{ExportHTML, "text/html; charset=utf-8", []byte("<!doctype html>"), "quick-model"},
		{ExportPNG, "image/png", []byte("\x89PNG\r\n\x1a\n"), ""},
		{ExportPDF, "application/pdf", []byte("%PDF-1.4"), ""},
	} {
		t.Run(string(test.format), func(t *testing.T) {
			exported, err := service.Export(context.Background(), report.ReportID, test.format, "")
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			if exported.MediaType != test.mediaType {
				t.Fatalf("media type = %q, want %q", exported.MediaType, test.mediaType)
			}
			contents, err := base64.StdEncoding.DecodeString(exported.DataBase64)
			if err != nil {
				t.Fatalf("base64: %v", err)
			}
			if !strings.HasPrefix(string(contents), string(test.prefix)) {
				t.Fatalf("prefix = %q, want %q", contents[:min(len(contents), 20)], test.prefix)
			}
			if test.contains != "" && !strings.Contains(string(contents), test.contains) {
				t.Fatalf("export does not contain %q", test.contains)
			}
			if (test.format == ExportJSON || test.format == ExportHTML) && !strings.Contains(string(contents), DefaultWatermark) {
				t.Fatalf("export does not contain default watermark %q", DefaultWatermark)
			}
			if test.format == ExportHTML && (!strings.Contains(string(contents), "Observed ICL（语义块间隔，非 Token ITL） (ms)") || !strings.Contains(string(contents), "语义块数")) {
				t.Fatal("quick HTML export omits fine streaming telemetry labels")
			}
			if test.format == ExportHTML && !strings.Contains(string(contents), "Schema v3") {
				t.Fatal("quick HTML export shows the wrong performance schema version")
			}
			if test.format == ExportJSON && (!strings.Contains(string(contents), `"observed_icl_ms"`) || !strings.Contains(string(contents), `"semantic_chunk_count"`)) {
				t.Fatal("quick JSON export omits fine streaming telemetry")
			}
		})
	}

	defaultPNG, err := service.Export(context.Background(), report.ReportID, ExportPNG, "")
	if err != nil {
		t.Fatal(err)
	}
	customPNG, err := service.Export(context.Background(), report.ReportID, ExportPNG, "team-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if defaultPNG.DataBase64 == customPNG.DataBase64 {
		t.Fatal("PNG watermark did not change with configured text")
	}
	changed := report
	changed.Metrics.ObservedICLAverage++
	originalPNG, err := renderPNG(Detail{Source: SourceQuickPerformance, Performance: &report}, DefaultWatermark)
	if err != nil {
		t.Fatal(err)
	}
	changedPNG, err := renderPNG(Detail{Source: SourceQuickPerformance, Performance: &changed}, DefaultWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if string(originalPNG) == string(changedPNG) {
		t.Fatal("quick PNG export does not reflect observed inter-chunk latency")
	}
}

func TestQuickPerformanceSchemaV2ExportKeepsLegacyTTFTPresentation(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.SchemaVersion = quicktest.PerformanceSchemaVersionV2
	service := New(&fakeMixedCatalog{get: report})
	exported, err := service.Export(context.Background(), report.ReportID, ExportHTML, "")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := base64.StdEncoding.DecodeString(exported.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	html := string(contents)
	if !strings.Contains(html, "TTFT P50 / P95") || !strings.Contains(html, "<th>TTFT ms</th>") {
		t.Fatal("schema v2 HTML export lost legacy TTFT presentation")
	}
	for _, v3Label := range []string{"Streaming timing distributions", "Observed ICL (semantic inter-chunk latency, not Token ITL) (ms)", "Semantic chunks"} {
		if strings.Contains(html, v3Label) {
			t.Fatalf("schema v2 HTML export contains v3 label %q", v3Label)
		}
	}

	baseline, err := renderPNG(Detail{Source: SourceQuickPerformance, Performance: &report}, DefaultWatermark)
	if err != nil {
		t.Fatal(err)
	}
	fineOnly := report
	fineOnly.Metrics.ObservedICLAverage++
	ignored, err := renderPNG(Detail{Source: SourceQuickPerformance, Performance: &fineOnly}, DefaultWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != string(ignored) {
		t.Fatal("schema v2 PNG export rendered v3-only telemetry")
	}
	legacyTTFT := report
	legacyTTFT.Metrics.TTFTP50++
	visible, err := renderPNG(Detail{Source: SourceQuickPerformance, Performance: &legacyTTFT}, DefaultWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) == string(visible) {
		t.Fatal("schema v2 PNG export does not reflect legacy TTFT")
	}
}

func TestQuickPerformanceSchemaV2MarksMissingRequestMetricsUnavailable(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.SchemaVersion = quicktest.PerformanceSchemaVersionV2
	report.Samples[0].Success = false
	report.Samples[0].TTFTMS = 0
	report.Samples[0].TPOTMS = 0

	contents, err := renderHTML(Detail{Source: SourceQuickPerformance, Performance: &report}, DefaultWatermark, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	want := "<tr><td>0</td><td>failed</td><td>200</td><td>11</td><td>-</td><td>-</td>"
	if !strings.Contains(string(contents), want) {
		t.Fatalf("schema v2 missing request metrics did not render unavailable markers: %s", contents)
	}
	line := quickPerformanceLegacySampleLine(report.Samples[0])
	for _, want := range []string{"TTFT -", "TPOT -"} {
		if !strings.Contains(line, want) {
			t.Fatalf("legacy PNG request text omits %q: %s", want, line)
		}
	}
}

func TestQuickPerformanceLegacyJSONExportExcludesSchemaV3FieldsRecursively(t *testing.T) {
	base := legacyQuickPerformanceExportFixture()
	for _, schemaVersion := range []int{quicktest.LegacyPerformanceSchemaVersion, quicktest.PerformanceSchemaVersionV2} {
		t.Run(fmt.Sprintf("schema-v%d", schemaVersion), func(t *testing.T) {
			report := base
			report.SchemaVersion = schemaVersion
			exported, err := New(&fakeMixedCatalog{get: report}).Export(context.Background(), report.ReportID, ExportJSON, "")
			if err != nil {
				t.Fatal(err)
			}
			contents, err := base64.StdEncoding.DecodeString(exported.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(contents, &document); err != nil {
				t.Fatal(err)
			}
			performance, ok := document["performance"].(map[string]any)
			if !ok {
				t.Fatalf("performance payload = %#v", document["performance"])
			}
			for _, forbidden := range schemaV3QuickPerformanceJSONFields() {
				if jsonTreeContainsKey(performance, forbidden) {
					t.Fatalf("schema v%d JSON export contains v3-only field %q: %s", schemaVersion, forbidden, contents)
				}
			}
			if !jsonTreeContainsKey(performance, "ttft_ms") || !jsonTreeContainsKey(performance, "ttft_p50_ms") {
				t.Fatalf("schema v%d JSON export lost legacy TTFT fields: %s", schemaVersion, contents)
			}
		})
	}
}

func TestQuickPerformanceHTMLRendersUnavailableFineCohortAsDashes(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.Metrics.Succeeded = 0
	report.Metrics.TPOTP50 = 123
	report.Metrics.TPOTP95 = 123
	report.Metrics.E2EP50 = 123
	report.Metrics.E2EP95 = 123
	report.Metrics.TTFBSamples = 0
	report.Metrics.TTFBAverage = 123
	report.Metrics.TTFBP50 = 123
	report.Metrics.TTFBP95 = 123
	report.Metrics.TTFBP99 = 123
	report.Samples[0].TTFBMS = 0
	report.Samples[0].TTFTAnyMS = 0
	report.Samples[0].TTFTVisibleMS = 0
	report.Samples[0].TTSTMS = 0
	report.Samples[0].ObservedICLMS = 0
	report.Samples[0].SemanticChunkCount = 0
	report.Samples[0].TPOTMS = 0
	report.Samples[0].Success = false

	contents, err := renderHTML(Detail{Source: SourceQuickPerformance, Performance: &report}, DefaultWatermark, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	want := "<tr><td>TTFB (ms)</td><td>-</td><td>-</td><td>-</td><td>-</td><td>-</td></tr>"
	if !strings.Contains(string(contents), want) {
		t.Fatalf("zero-sample TTFB row did not render unavailable markers: %s", contents)
	}
	want = "<tr><td>0</td><td>failed</td><td>200</td><td>11</td><td>-</td><td>-</td><td>-</td><td>-</td><td>-</td><td>0</td><td>-</td><td>10</td><td>3</td><td></td></tr>"
	if !strings.Contains(string(contents), want) {
		t.Fatalf("request with missing timing milestones did not render unavailable markers: %s", contents)
	}
	for _, want := range []string{
		"<strong>- / - ms/token</strong><span>TPOT P50 / P95</span>",
		"<strong>- / - ms</strong><span>E2E P50 / P95</span>",
	} {
		if !strings.Contains(string(contents), want) {
			t.Fatalf("zero-sample core metric did not render unavailable markers %q: %s", want, contents)
		}
	}
}

func TestQuickPerformanceHTMLUsesPreciseStreamingTerminology(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	contents, err := renderHTML(Detail{Source: SourceQuickPerformance, Performance: &report}, DefaultWatermark, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	html := string(contents)
	for _, term := range []string{
		"TTFT Any (includes reasoning) (ms)",
		"TTFT Visible (visible content) (ms)",
		"Observed ICL (semantic inter-chunk latency, not Token ITL) (ms)",
	} {
		if !strings.Contains(html, term) {
			t.Fatalf("quick HTML export omits precise term %q", term)
		}
	}
}

func TestQuickPerformancePNGPresentationMarksMissingMetricsAndUsesPreciseTerms(t *testing.T) {
	report := validArchivedQuickPerformanceReport()
	report.Metrics.Succeeded = 0
	report.Metrics.TTFTAnySamples = 0
	report.Metrics.ObservedICLSamples = 0
	report.Metrics.TPOTP50 = 123
	report.Metrics.E2EP95 = 123
	report.Samples[0].Success = false
	report.Samples[0].TPOTMS = 0
	cards := quickPerformanceImageMetrics(report)
	values := make(map[string]string, len(cards))
	for _, card := range cards {
		values[card.name] = card.value
	}
	for label, want := range map[string]string{
		"TTFT ANY INCLUDES REASONING P50":        "-",
		"OBS ICL SEMANTIC GAP NOT TOKEN ITL AVG": "-",
		"TPOT P50":                               "-",
		"E2E P95":                                "-",
	} {
		if values[label] != want {
			t.Fatalf("PNG metric card %q = %q, want %q; cards = %#v", label, values[label], want, cards)
		}
	}

	sample := quicktest.PerformanceSample{
		RequestIndex: 4, HTTPStatus: 200, Success: true, E2EMS: 10, TTFBMS: 1,
	}
	lines := strings.Join(quickPerformanceSampleLines(sample)[:], " ")
	for _, text := range []string{
		"TTFT ANY INCLUDES REASONING -",
		"TTFT VISIBLE CONTENT -",
		"TTST SECOND SEMANTIC CHUNK -",
		"OBS ICL SEMANTIC GAP NOT TOKEN ITL -",
		"TPOT -",
	} {
		if !strings.Contains(lines, text) {
			t.Fatalf("PNG request text omits %q: %s", text, lines)
		}
	}
}

func TestFormalMetricPresentationMarksZeroSamplesUnavailable(t *testing.T) {
	metrics := sortedMetrics(map[string]domain.MetricValue{
		"unsupported_latency": {Value: 123, Unit: "ms", Samples: 0},
	})
	if len(metrics) != 1 || metrics[0].Value != "-" {
		t.Fatalf("zero-sample metric presentation = %#v, want unavailable value", metrics)
	}
}

func TestFormalPNGRequestLinesFitCanvasAndUsePreciseTerms(t *testing.T) {
	row := resultRow{
		RequestID: "50000000-0000-4000-8000-000000000001", Status: "failed",
		E2E: "120.00", TTFB: "10.00", TTFT: "40.00", TTFTVisible: "50.00",
		TTST: "60.00", ObservedICL: "20.00", SemanticChunk: "2.00", Error: "stream_protocol_error",
	}
	lines := resultImageLines(row)
	joined := strings.Join(lines, " ")
	for _, term := range []string{"TTFT ANY INCLUDES REASONING", "OBS ICL SEMANTIC GAP NOT TOKEN ITL"} {
		if !strings.Contains(joined, term) {
			t.Fatalf("formal PNG request text omits %q: %s", term, joined)
		}
	}
	for _, line := range lines {
		if width := len(line) * 6 * 2; width > 1400-80 {
			t.Fatalf("formal PNG request line is clipped: width=%d line=%q", width, line)
		}
	}
}

func TestLocalizedHTMLExportsUseTheRequestedLanguage(t *testing.T) {
	detail := exportFixture(t)
	runService := New(&fakeDocumentCatalog{report: detail.Report, results: detail.RequestResults})
	runExport, err := runService.ExportLocalized(context.Background(), detail.Report.ID, ExportHTML, "team-alpha", "en-US")
	if err != nil {
		t.Fatal(err)
	}
	assertLocalizedHTML(t, runExport, []string{`<html lang="en-US">`, "LLM Test Studio Test Report", "Conclusion:", "Core metrics", "Request details"}, []string{"测试报告", "结论：", "核心指标", "请求明细"})

	quick := validArchivedQuickPerformanceReport()
	quickExport, err := New(&fakeMixedCatalog{get: quick}).ExportLocalized(context.Background(), quick.ReportID, ExportHTML, "team-alpha", "en-US")
	if err != nil {
		t.Fatal(err)
	}
	assertLocalizedHTML(t, quickExport, []string{`<html lang="en-US">`, "Quick Performance Test Report", "All requests passed", "Request samples"}, []string{"快速性能测试报告", "全部请求成功", "请求样本"})
}

func assertLocalizedHTML(t *testing.T, exported ExportedDocument, contains, excludes []string) {
	t.Helper()
	contents, err := base64.StdEncoding.DecodeString(exported.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	body := string(contents)
	for _, expected := range contains {
		if !strings.Contains(body, expected) {
			t.Fatalf("localized HTML does not contain %q", expected)
		}
	}
	for _, excluded := range excludes {
		if strings.Contains(body, excluded) {
			t.Fatalf("localized HTML contains %q", excluded)
		}
	}
}

func TestDetailRejectsForeignAndDuplicateResults(t *testing.T) {
	detail := exportFixture(t)
	for _, mutate := range []func(*[]domain.Result){
		func(results *[]domain.Result) { (*results)[0].RunID = "99999999-9999-4999-8999-999999999999" },
		func(results *[]domain.Result) { *results = append(*results, (*results)[0]) },
	} {
		results := append([]domain.Result(nil), detail.RequestResults...)
		mutate(&results)
		_, err := New(&fakeDocumentCatalog{report: detail.Report, results: results}).Detail(context.Background(), detail.Report.ID)
		if err == nil || !strings.Contains(err.Error(), ErrInconsistent.Error()) {
			t.Fatalf("Detail() error = %v, want inconsistent", err)
		}
	}
}

func exportFixture(t *testing.T) Detail {
	t.Helper()
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	run := generatorRun(t, now)
	request := domain.Result{
		EntityMeta: generatorMeta("50000000-0000-4000-8000-000000000001", now),
		RunID:      run.Meta().ID, RequestID: "request-1",
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics: map[string]float64{
			"e2e_ms": 120, "ttfb_ms": 10, "ttft_ms": 40, "ttft_any_ms": 40, "ttft_visible_ms": 50,
			"ttst_ms": 60, "observed_icl_ms": 20, "semantic_chunk_count": 2, "tpot_ms": 10,
			"prompt_tokens": 20, "completion_tokens": 9,
		},
	}
	caseResult := request
	caseResult.EntityMeta = generatorMeta("50000000-0000-4000-8000-000000000002", now)
	caseResult.RequestID = ""
	caseResult.CaseID = run.Snapshot().Cases[0].CaseID
	repository := &fakeReportRepository{run: run, results: []domain.Result{request, caseResult}, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)},
		IDFactory: func(time.Time) (string, error) { return "50000000-0000-4000-8000-000000000003", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(repository.report); err != nil {
		t.Fatal(err)
	}
	return Detail{SchemaVersion: CurrentSchemaVersion, Report: repository.report, RequestResults: []domain.Result{request}}
}

func legacyQuickPerformanceExportFixture() quicktest.PerformanceReport {
	report := validArchivedQuickPerformanceReport()
	report.Profile.SliceDurationMS = 1_000
	report.RequestBudget = &quicktest.PerformanceRequestBudget{
		Limit: quicktest.MaxPerformanceRequests, MeasuredCap: 1, TotalCap: 1,
	}
	report.Samples[0].ScheduleLagMS = 1
	report.Metrics.ScheduleLagP50 = 1
	report.Metrics.ScheduleLagP90 = 1
	report.Metrics.ScheduleLagP95 = 1
	report.Metrics.ScheduleLagP99 = 1
	report.Metrics.ScheduleLagAverage = 1
	report.TimeSlices = []quicktest.PerformanceTimeSlice{{
		SliceIndex: 0, StartMS: 0, EndMS: 12, Partial: true,
		Offered: 1, Launched: 1, Completed: 1, Succeeded: 1,
		PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2,
		TTFB:               quicktest.PerformanceLatencySlice{Count: 1, P50MS: 1, P95MS: 1, P99MS: 1, AverageMS: 1},
		TTFTAny:            quicktest.PerformanceLatencySlice{Count: 1, P50MS: 2, P95MS: 2, P99MS: 2, AverageMS: 2},
		TTFTVisible:        quicktest.PerformanceLatencySlice{Count: 1, P50MS: 3, P95MS: 3, P99MS: 3, AverageMS: 3},
		TTFT:               quicktest.PerformanceLatencySlice{Count: 1, P50MS: 2, P95MS: 2, P99MS: 2, AverageMS: 2},
		TTST:               quicktest.PerformanceLatencySlice{Count: 1, P50MS: 4, P95MS: 4, P99MS: 4, AverageMS: 4},
		ObservedICL:        quicktest.PerformanceLatencySlice{Count: 1, P50MS: 2, P95MS: 2, P99MS: 2, AverageMS: 2},
		SemanticChunkCount: quicktest.PerformanceCountSlice{Count: 1, P50: 2, P95: 2, P99: 2, Average: 2},
		TPOT:               quicktest.PerformanceLatencySlice{Count: 1, P50MS: 4.5, P95MS: 4.5, P99MS: 4.5, AverageMS: 4.5},
		E2E:                quicktest.PerformanceLatencySlice{Count: 1, P50MS: 11, P95MS: 11, P99MS: 11, AverageMS: 11},
	}}
	return report
}

func schemaV3QuickPerformanceJSONFields() []string {
	return []string{
		"average_ms", "ttfb", "ttft_any", "ttft_visible", "ttst", "observed_icl", "semantic_chunk_count",
		"ttfb_ms", "ttft_any_ms", "ttft_visible_ms", "ttst_ms", "observed_icl_ms", "ttft_samples",
		"ttfb_samples", "ttfb_p50_ms", "ttfb_p95_ms", "ttfb_p99_ms", "ttfb_average_ms",
		"ttft_any_samples", "ttft_any_p50_ms", "ttft_any_p95_ms", "ttft_any_p99_ms", "ttft_any_average_ms",
		"ttft_visible_samples", "ttft_visible_p50_ms", "ttft_visible_p95_ms", "ttft_visible_p99_ms", "ttft_visible_average_ms",
		"ttst_samples", "ttst_p50_ms", "ttst_p95_ms", "ttst_p99_ms", "ttst_average_ms",
		"observed_icl_samples", "observed_icl_p50_ms", "observed_icl_p95_ms", "observed_icl_p99_ms", "observed_icl_average_ms",
		"semantic_chunk_count_samples", "semantic_chunk_count_p50", "semantic_chunk_count_p95", "semantic_chunk_count_p99", "semantic_chunk_count_average",
	}
}

func jsonTreeContainsKey(value any, target string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == target || jsonTreeContainsKey(child, target) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if jsonTreeContainsKey(child, target) {
				return true
			}
		}
	}
	return false
}
