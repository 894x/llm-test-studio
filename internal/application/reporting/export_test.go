package reporting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/domain"
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
}

func TestLocalizedHTMLExportsUseTheRequestedLanguage(t *testing.T) {
	detail := exportFixture(t)
	runService := New(&fakeDocumentCatalog{report: detail.Report, results: detail.RequestResults})
	runExport, err := runService.ExportLocalized(context.Background(), detail.Report.ID, ExportHTML, "team-alpha", "en-US")
	if err != nil {
		t.Fatal(err)
	}
	assertLocalizedHTML(t, runExport, []string{`<html lang="en-US">`, "LLM Studio Test Report", "Conclusion:", "Core metrics", "Request details"}, []string{"测试报告", "结论：", "核心指标", "请求明细"})

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
		Metrics: map[string]float64{"e2e_ms": 120, "ttft_ms": 40, "tpot_ms": 10, "prompt_tokens": 20, "completion_tokens": 9},
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
