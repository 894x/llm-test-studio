package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-studio/internal/application/quicktest"
	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
	persistence "github.com/894x/llm-studio/internal/persistence/sqlite"
)

func TestRepositoryQuickPerformanceReportRoundTripIsIndependentAndNewestFirst(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	older := validQuickPerformanceReport("77777777-7777-4777-8777-777777777770", "2026-08-31T15:29:00Z")
	newer := validQuickPerformanceReport("77777777-7777-4777-8777-777777777771", "2026-08-31T15:30:00Z")
	if err := repository.SaveQuickPerformanceReport(context.Background(), older); err != nil {
		t.Fatalf("SaveQuickPerformanceReport(older) error = %v", err)
	}
	if err := repository.SaveQuickPerformanceReport(context.Background(), newer); err != nil {
		t.Fatalf("SaveQuickPerformanceReport(newer) error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), older.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if loaded.ReportID != older.ReportID || loaded.GeneratedAt != older.GeneratedAt || len(loaded.Samples) != 1 || loaded.Samples[0].TPOTMS != 4.5 {
		t.Fatalf("loaded report = %#v", loaded)
	}
	reports, err := repository.ListQuickPerformanceReportSummaries(context.Background())
	if err != nil {
		t.Fatalf("ListQuickPerformanceReports() error = %v", err)
	}
	if len(reports) != 2 || reports[0].ReportID != newer.ReportID || reports[1].ReportID != older.ReportID ||
		reports[0].ModelID != newer.ModelID || reports[0].BaseURL != newer.BaseURL || reports[0].Completed != newer.Metrics.Completed {
		t.Fatalf("reports = %#v", reports)
	}
	if err := repository.SaveQuickPerformanceReport(context.Background(), older); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("duplicate save error = %v, want ErrConflict", err)
	}
}

func TestRepositoryRejectsInvalidQuickPerformanceArchiveDocuments(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	for _, test := range []struct {
		name   string
		mutate func(*quicktest.PerformanceReport)
	}{
		{name: "missing id", mutate: func(report *quicktest.PerformanceReport) { report.ReportID = "" }},
		{name: "invalid timestamp", mutate: func(report *quicktest.PerformanceReport) { report.GeneratedAt = "2026-08-31 15:30" }},
		{name: "not archived", mutate: func(report *quicktest.PerformanceReport) { report.Archived = false }},
		{name: "wrong archive status", mutate: func(report *quicktest.PerformanceReport) { report.ArchiveStatus = quicktest.PerformanceArchiveFailed }},
		{name: "sample count mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Progress.Completed = 2 }},
		{name: "unsafe url", mutate: func(report *quicktest.PerformanceReport) { report.BaseURL = "http://example.com/v1" }},
		{name: "unsafe top level error code", mutate: func(report *quicktest.PerformanceReport) {
			report.ErrorCode = domain.ErrorCode("provider said api-key=sk-secret")
		}},
		{name: "unsafe sample error code", mutate: func(report *quicktest.PerformanceReport) {
			makeQuickPerformanceReportFailed(report, domain.ErrorCode("provider said api-key=sk-secret"))
		}},
		{name: "unsafe failure error code", mutate: func(report *quicktest.PerformanceReport) {
			makeQuickPerformanceReportFailed(report, load.ErrorNetwork)
			report.Failures[0].ErrorCode = domain.ErrorCode("credential=sk-secret")
		}},
		{name: "too many samples", mutate: func(report *quicktest.PerformanceReport) {
			sample := report.Samples[0]
			report.Profile.RequestCount = 0
			report.Profile.DurationMS = 60_000
			report.Samples = make([]quicktest.PerformanceSample, quicktest.MaxPerformanceRequests+1)
			for index := range report.Samples {
				report.Samples[index] = sample
				report.Samples[index].RequestIndex = uint64(index)
			}
			report.Progress.Planned = quicktest.MaxPerformanceRequests + 1
			report.Progress.Launched = quicktest.MaxPerformanceRequests + 1
			report.Progress.Completed = quicktest.MaxPerformanceRequests + 1
			report.Progress.Succeeded = quicktest.MaxPerformanceRequests + 1
			report.Metrics.Completed = quicktest.MaxPerformanceRequests + 1
			report.Metrics.Succeeded = quicktest.MaxPerformanceRequests + 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777779", "2026-08-31T15:30:00Z")
			test.mutate(&report)
			if err := repository.SaveQuickPerformanceReport(context.Background(), report); err == nil {
				t.Fatal("SaveQuickPerformanceReport() error = nil")
			}
		})
	}
}

func makeQuickPerformanceReportFailed(report *quicktest.PerformanceReport, code domain.ErrorCode) {
	report.Success = false
	report.Progress.Succeeded = 0
	report.Progress.Failed = 1
	report.Metrics.Succeeded = 0
	report.Metrics.Failed = 1
	report.Metrics.SuccessRatePercent = 0
	report.Metrics.PromptTokens = 0
	report.Metrics.CompletionTokens = 0
	report.Metrics.CachedTokens = 0
	report.Samples[0].Success = false
	report.Samples[0].PromptTokens = 0
	report.Samples[0].CompletionTokens = 0
	report.Samples[0].CachedTokens = 0
	report.Samples[0].ErrorCode = code
	report.Failures = []quicktest.PerformanceFailure{{ErrorCode: code, Count: 1}}
}

func validQuickPerformanceReport(id, generatedAt string) quicktest.PerformanceReport {
	return quicktest.PerformanceReport{
		SchemaVersion: quicktest.PerformanceSchemaVersion,
		ReportID:      id, GeneratedAt: generatedAt, Archived: true, ArchiveStatus: quicktest.PerformanceArchiveArchived,
		Success: true, AddressMode: quicktest.AddressModeBaseURL,
		BaseURL: "https://example.com/v1", Endpoint: "https://example.com/v1/chat/completions", ModelID: "model-a",
		Profile:   quicktest.PerformanceProfile{RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 3},
		Progress:  quicktest.PerformanceProgress{Phase: load.PhaseCompleted, Planned: 1, Launched: 1, Completed: 1, PeakInFlight: 1, Succeeded: 1, TotalDurationMS: 12},
		Metrics:   load.Metrics{Completed: 1, Succeeded: 1, SuccessRatePercent: 100, RequestQPS: 80, RPM: 4800, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2, TTFTP50: 2, TPOTP50: 4.5, E2EP50: 11},
		Failures:  []quicktest.PerformanceFailure{},
		Samples:   []quicktest.PerformanceSample{{RequestIndex: 0, StartedOffsetMS: 1, FinishedOffsetMS: 12, E2EMS: 11, TTFTMS: 2, TPOTMS: 4.5, HTTPStatus: 200, Success: true, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2}},
		ErrorCode: domain.ErrorCode(""),
	}
}
