package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const frozenSchemaV1QuickPerformanceReport = `{"address_mode":"base_url","archive_status":"archived","archived":true,"base_url":"https://example.com/v1","endpoint":"https://example.com/v1/chat/completions","failures":[],"generated_at":"2026-08-31T15:32:00Z","metrics":{"cache_rate_percent":0,"cached_tokens":2,"completed":1,"completion_tokens":3,"e2e_average_ms":0,"e2e_p50_ms":11,"e2e_p90_ms":0,"e2e_p95_ms":0,"e2e_p99_ms":0,"failed":0,"generation_tps":0,"input_tpm":0,"output_tpm":0,"prompt_tokens":10,"request_qps":80,"rpm":4800,"schedule_lag_average_ms":0,"schedule_lag_p50_ms":0,"schedule_lag_p95_ms":0,"succeeded":1,"success_rate_percent":100,"timed_out":0,"total_tpm":0,"tpot_average_ms":0,"tpot_p50_ms":4.5,"tpot_p90_ms":0,"tpot_p95_ms":0,"tpot_p99_ms":0,"ttft_average_ms":0,"ttft_p50_ms":2,"ttft_p90_ms":0,"ttft_p95_ms":0,"ttft_p99_ms":0},"model_id":"model-a","profile":{"concurrency":1,"duration_ms":0,"input_tokens":10,"output_tokens":3,"request_count":1,"timeout_ms":2000},"progress":{"completed":1,"drain_duration_ms":0,"failed":0,"launched":1,"peak_in_flight":1,"phase":"completed","planned":1,"rejected":0,"send_duration_ms":0,"succeeded":1,"total_duration_ms":12},"report_id":"77777777-7777-4777-8777-777777777773","samples":[{"cached_tokens":2,"completion_tokens":3,"e2e_ms":11,"finished_offset_ms":12,"http_status":200,"prompt_tokens":10,"request_index":0,"schedule_lag_ms":0,"scheduled_offset_ms":0,"started_offset_ms":1,"success":true,"timed_out":false,"tpot_ms":4.5,"ttft_ms":2}],"schema_version":1,"success":true}`

func TestRepositoryLoadsFrozenSchemaV1QuickPerformanceReport(t *testing.T) {
	repository := openRepositoryWithQuickPerformanceDocument(t, frozenSchemaV1QuickPerformanceReport)
	defer repository.Close()

	loaded, err := repository.GetQuickPerformanceReport(context.Background(), "77777777-7777-4777-8777-777777777773")
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if loaded.SchemaVersion != quicktest.LegacyPerformanceSchemaVersion || loaded.Profile.LoadMode != "" || loaded.Progress.Offered != 0 {
		t.Fatalf("loaded legacy report = %#v", loaded)
	}
	if loaded.Metrics.OfferedQPS != 0 || loaded.Metrics.LaunchedQPS != 0 || loaded.Metrics.CompletedQPS != 0 || loaded.Metrics.SuccessfulRequestQPS != 0 {
		t.Fatalf("loaded legacy throughput = %#v", loaded.Metrics)
	}
}

func TestRepositoryQuickPerformanceReportSchemaV2RoundTrip(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777774", "2026-08-31T15:33:00Z")

	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), report.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, report) {
		t.Fatalf("loaded report = %#v, want %#v", loaded, report)
	}
}

func TestRepositoryReadsPhaseOneSchemaV2DefaultsAsFixedAndConstant(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777776", "2026-08-31T15:35:00Z")
	report.Profile.ArrivalPattern = ""
	report.Profile.WorkloadMode = ""
	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), report.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if loaded.Profile.ArrivalPattern != load.ArrivalConstant || loaded.Profile.WorkloadMode != quicktest.PerformanceWorkloadFixed {
		t.Fatalf("loaded phase-one defaults = %#v", loaded.Profile)
	}
}

func TestRepositoryQuickPerformanceNormalWorkloadRoundTrip(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777777", "2026-08-31T15:36:00Z")
	report.Profile.WorkloadMode = quicktest.PerformanceWorkloadNormal
	report.Profile.RandomSeed = 5
	report.Profile.SharedPrefixTokens = 2
	report.Samples[0].TargetInputTokens = report.Profile.InputTokens
	report.Samples[0].TargetOutputTokens = report.Profile.OutputTokens
	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), report.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, report) {
		t.Fatalf("loaded report = %#v, want %#v", loaded, report)
	}
}

func TestRepositoryQuickPerformanceSinglePoissonRequestUsesNominalWindow(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777778", "2026-08-31T15:37:00Z")
	report.Profile.LoadMode = domain.LoadOpenLoop
	report.Profile.ArrivalPattern = load.ArrivalPoisson
	report.Profile.RandomSeed = 1
	report.Profile.Concurrency = 0
	report.Profile.RatePerSecond = 10
	report.Profile.MaxInFlight = 1
	report.Metrics.OfferedQPS = 10
	report.Metrics.LaunchedQPS = 10
	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), report.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, report) {
		t.Fatalf("loaded report = %#v, want %#v", loaded, report)
	}
}

func TestRepositoryQuickPerformancePhaseThreeRoundTrip(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777769", "2026-08-31T15:38:00Z")
	addPhaseThreeFixture(&report)
	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), report.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, report) {
		t.Fatalf("loaded report = %#v, want %#v", loaded, report)
	}
}

func TestRepositoryQuickPerformanceOpenDurationPhaseThreeRoundTrip(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777761", "2026-08-31T15:39:00Z")
	addOpenDurationPhaseThreeFixture(&report)
	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
}

func TestRepositoryOnlyWritesCurrentQuickPerformanceSchema(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777775", "2026-08-31T15:34:00Z")
	report.SchemaVersion = quicktest.LegacyPerformanceSchemaVersion
	report.Profile.LoadMode = ""
	report.Progress.Offered = 0
	report.Metrics.OfferedQPS = 0
	report.Metrics.LaunchedQPS = 0
	report.Metrics.CompletedQPS = 0
	report.Metrics.SuccessfulRequestQPS = 0

	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err == nil {
		t.Fatal("SaveQuickPerformanceReport(schema v1) error = nil")
	}
}

func TestRepositoryRejectsNonCanonicalOrUnsupportedQuickPerformanceDocuments(t *testing.T) {
	for _, test := range []struct {
		name     string
		document string
	}{
		{name: "unsupported schema", document: strings.Replace(frozenSchemaV1QuickPerformanceReport, `"schema_version":1`, `"schema_version":99`, 1)},
		{name: "unknown field", document: strings.TrimSuffix(frozenSchemaV1QuickPerformanceReport, "}") + `,"unknown":true}`},
		{name: "v2 field in v1", document: strings.Replace(frozenSchemaV1QuickPerformanceReport, `"input_tokens":10,`, `"input_tokens":10,"load_mode":"fixed_concurrency",`, 1)},
		{name: "phase two profile field in v1", document: strings.Replace(frozenSchemaV1QuickPerformanceReport, `"profile":{"concurrency":`, `"profile":{"arrival_pattern":"poisson","concurrency":`, 1)},
		{name: "phase two sample field in v1", document: strings.Replace(frozenSchemaV1QuickPerformanceReport, `"timed_out":false,`, `"target_input_tokens":10,"timed_out":false,`, 1)},
		{name: "phase three profile field in v1", document: strings.Replace(frozenSchemaV1QuickPerformanceReport, `"timeout_ms":2000}`, `"timeout_ms":2000,"warmup_requests":1}`, 1)},
		{name: "noncanonical whitespace", document: " " + frozenSchemaV1QuickPerformanceReport},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := openRepositoryWithQuickPerformanceDocument(t, test.document)
			defer repository.Close()

			_, err := repository.GetQuickPerformanceReport(context.Background(), "77777777-7777-4777-8777-777777777773")
			if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("GetQuickPerformanceReport() error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func openRepositoryWithQuickPerformanceDocument(t *testing.T, document string) *persistence.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repository.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	generatedAt := time.Date(2026, 8, 31, 15, 32, 0, 0, time.UTC)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO quick_performance_reports(
			id, generated_at, generated_at_unix_nano, success, model_id, base_url, phase, completed, failed, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "77777777-7777-4777-8777-777777777773", generatedAt.Format(time.RFC3339Nano), generatedAt.UnixNano(), true,
		"model-a", "https://example.com/v1", load.PhaseCompleted, 1, 0, []byte(document))
	closeErr := db.Close()
	if err != nil {
		t.Fatalf("insert quick performance document error = %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close raw database error = %v", closeErr)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	return repository
}

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

func TestRepositoryQuickPerformanceReportRoundTripPreservesFailureResponseEvidence(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777772", "2026-08-31T15:31:00Z")
	makeQuickPerformanceReportFailed(&report, quicktest.ErrorAuthenticationFailed)
	report.Samples[0].HTTPStatus = 401
	report.Samples[0].ResponseEvidence = &quicktest.PerformanceResponseEvidence{
		CaptureStatus: quicktest.PerformanceEvidenceCaptured,
		ContentType:   "application/json",
		RequestID:     "req-safe",
		Body:          `{"error":{"message":"quota exhausted","api_key":"[REDACTED]"}}`,
		BodyBytes:     72,
		Redacted:      true,
	}

	if err := repository.SaveQuickPerformanceReport(context.Background(), report); err != nil {
		t.Fatalf("SaveQuickPerformanceReport() error = %v", err)
	}
	loaded, err := repository.GetQuickPerformanceReport(context.Background(), report.ReportID)
	if err != nil {
		t.Fatalf("GetQuickPerformanceReport() error = %v", err)
	}
	evidence := loaded.Samples[0].ResponseEvidence
	if evidence == nil || evidence.CaptureStatus != quicktest.PerformanceEvidenceCaptured || evidence.RequestID != "req-safe" || evidence.Body != report.Samples[0].ResponseEvidence.Body || !evidence.Redacted {
		t.Fatalf("loaded evidence = %#v", evidence)
	}
}

func TestRepositoryRejectsInvalidQuickPerformanceArchiveDocuments(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*quicktest.PerformanceReport)
	}{
		{name: "missing id", mutate: func(report *quicktest.PerformanceReport) { report.ReportID = "" }},
		{name: "invalid timestamp", mutate: func(report *quicktest.PerformanceReport) { report.GeneratedAt = "2026-08-31 15:30" }},
		{name: "not archived", mutate: func(report *quicktest.PerformanceReport) { report.Archived = false }},
		{name: "wrong archive status", mutate: func(report *quicktest.PerformanceReport) { report.ArchiveStatus = quicktest.PerformanceArchiveFailed }},
		{name: "sample count mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Progress.Completed = 2 }},
		{name: "offered count mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Progress.Offered = 2 }},
		{name: "successful throughput mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Metrics.SuccessfulRequestQPS = 1 }},
		{name: "legacy throughput mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Metrics.RequestQPS = 1 }},
		{name: "negative send window", mutate: func(report *quicktest.PerformanceReport) { report.Progress.SendDurationMS = -1 }},
		{name: "window total mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Progress.DrainDurationMS = 1 }},
		{name: "load rate ratio mismatch", mutate: func(report *quicktest.PerformanceReport) { report.Metrics.LaunchedQPS = 1 }},
		{name: "absolute load rates mismatch", mutate: func(report *quicktest.PerformanceReport) {
			report.Metrics.OfferedQPS /= 2
			report.Metrics.LaunchedQPS /= 2
		}},
		{name: "fixed workload target", mutate: func(report *quicktest.PerformanceReport) { report.Samples[0].TargetInputTokens = 10 }},
		{name: "normal workload target mismatch", mutate: func(report *quicktest.PerformanceReport) {
			report.Profile.WorkloadMode = quicktest.PerformanceWorkloadNormal
			report.Profile.RandomSeed = 5
			report.Profile.SharedPrefixTokens = 2
			report.Samples[0].TargetInputTokens = 3
			report.Samples[0].TargetOutputTokens = 3
		}},
		{name: "phase three budget mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.RequestBudget.TotalCap++
		}},
		{name: "phase three measured count incomplete", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Profile.RequestCount = 2
			report.RequestBudget.MeasuredCap = 2
			report.RequestBudget.TotalCap = 4
			report.Progress.Planned = 2
		}},
		{name: "phase three fixed duration cap hidden", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Profile.RequestCount = 0
			report.Profile.DurationMS = 60_000
			report.Profile.WarmupRequests = quicktest.MaxPerformanceRequests - 2
			report.RequestBudget.WarmupCap = quicktest.MaxPerformanceRequests - 2
			report.RequestBudget.MeasuredCap = 1
			report.RequestBudget.TotalCap = quicktest.MaxPerformanceRequests
			report.Warmup.RequestCap = quicktest.MaxPerformanceRequests - 2
			report.Warmup.Offered = quicktest.MaxPerformanceRequests - 2
			report.Warmup.Launched = quicktest.MaxPerformanceRequests - 2
			report.Warmup.Completed = quicktest.MaxPerformanceRequests - 2
			report.Warmup.Succeeded = quicktest.MaxPerformanceRequests - 2
			report.Progress.Capped = false
		}},
		{name: "phase three explicit count marked capped", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Progress.Capped = true
		}},
		{name: "phase three open duration marked capped", mutate: func(report *quicktest.PerformanceReport) {
			addOpenDurationPhaseThreeFixture(report)
			report.Progress.Capped = true
		}},
		{name: "phase three sample TPOT and slice coordinated tamper", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Samples[0].TPOTMS = 99
			report.TimeSlices[0].TPOT = quicktest.PerformanceLatencySlice{Count: 1, P50MS: 99, P95MS: 99, P99MS: 99}
		}},
		{name: "phase three sample TTFT exceeds E2E", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Samples[0].TTFTMS = 12
		}},
		{name: "phase three sample schedule lag tamper", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Samples[0].ScheduleLagMS = 2
		}},
		{name: "phase three sample E2E exceeds observed wall time", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Samples[0].E2EMS = 12
			report.Samples[0].TPOTMS = 5
			report.TimeSlices[0].E2E = quicktest.PerformanceLatencySlice{Count: 1, P50MS: 12, P95MS: 12, P99MS: 12}
			report.TimeSlices[0].TPOT = quicktest.PerformanceLatencySlice{Count: 1, P50MS: 5, P95MS: 5, P99MS: 5}
		}},
		{name: "phase three warmup zero observed duration", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Warmup.SendDurationMS = 0
			report.Warmup.TotalDurationMS = 0
		}},
		{name: "phase three fixed ramp cap hidden", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Ramp.Traffic.Capped = false
			report.Ramp.CompletedWindow = true
			report.Ramp.Traffic.SendDurationMS = 1_000
			report.Ramp.Traffic.TotalDurationMS = 1_000
		}},
		{name: "phase three warmup count mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Warmup.Completed++
		}},
		{name: "phase three slice sum mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.TimeSlices[0].Completed++
		}},
		{name: "phase three summary without configuration", mutate: func(report *quicktest.PerformanceReport) {
			report.Warmup = &quicktest.PerformanceTrafficSummary{RequestCap: 1}
		}},
		{name: "phase three missing warmup summary", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Warmup = nil
		}},
		{name: "phase three slice boundary mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.TimeSlices[0].StartMS = 1
		}},
		{name: "phase three slice latency mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.TimeSlices[0].TTFT.P95MS = 3
		}},
		{name: "phase three slice totals disagree with headline", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			makeQuickPerformanceReportFailed(report, load.ErrorSchedulerOverload)
			report.TimeSlices[0].Launched = 0
			report.TimeSlices[0].Succeeded = 0
			report.TimeSlices[0].Failed = 1
			report.TimeSlices[0].Rejected = 1
			report.TimeSlices[0].PromptTokens = 0
			report.TimeSlices[0].CompletionTokens = 0
			report.TimeSlices[0].CachedTokens = 0
			report.TimeSlices[0].TTFT = quicktest.PerformanceLatencySlice{}
			report.TimeSlices[0].TPOT = quicktest.PerformanceLatencySlice{}
			report.TimeSlices[0].E2E = quicktest.PerformanceLatencySlice{}
		}},
		{name: "phase three ramp shape mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Ramp.Shape = "linear"
		}},
		{name: "phase three ramp completion mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Ramp.CompletedWindow = true
		}},
		{name: "phase three ramp rejection failure mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Ramp.Traffic.Launched = 0
			report.Ramp.Traffic.Succeeded = 0
			report.Ramp.Traffic.Failed = 1
			report.Ramp.Traffic.Rejected = 1
			report.Ramp.Traffic.PeakInFlight = 0
			report.Ramp.Traffic.PromptTokens = 0
			report.Ramp.Traffic.CompletionTokens = 0
			report.Ramp.Traffic.CachedTokens = 0
			report.Ramp.Traffic.Failures = []quicktest.PerformanceFailure{{ErrorCode: load.ErrorHTTP, Count: 1}}
		}},
		{name: "phase three ramp timeout failure mismatch", mutate: func(report *quicktest.PerformanceReport) {
			addPhaseThreeFixture(report)
			report.Ramp.Traffic.Succeeded = 0
			report.Ramp.Traffic.Failed = 1
			report.Ramp.Traffic.TimedOut = 1
			report.Ramp.Traffic.PromptTokens = 0
			report.Ramp.Traffic.CompletionTokens = 0
			report.Ramp.Traffic.CachedTokens = 0
			report.Ramp.Traffic.Failures = []quicktest.PerformanceFailure{{ErrorCode: load.ErrorHTTP, Count: 1}}
		}},
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
			report.Progress.Offered = quicktest.MaxPerformanceRequests + 1
			report.Progress.Launched = quicktest.MaxPerformanceRequests + 1
			report.Progress.Completed = quicktest.MaxPerformanceRequests + 1
			report.Progress.Succeeded = quicktest.MaxPerformanceRequests + 1
			report.Metrics.Completed = quicktest.MaxPerformanceRequests + 1
			report.Metrics.Succeeded = quicktest.MaxPerformanceRequests + 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := openRepository(t)
			defer repository.Close()
			report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777779", "2026-08-31T15:30:00Z")
			test.mutate(&report)
			err := repository.SaveQuickPerformanceReport(context.Background(), report)
			if err == nil {
				t.Fatal("SaveQuickPerformanceReport() error = nil")
			}
			if test.name == "too many samples" && !strings.Contains(err.Error(), "sample limit") {
				t.Fatalf("SaveQuickPerformanceReport() error = %v, want sample-limit rejection", err)
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
	report.Metrics.SuccessfulRequestQPS = 0
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
		Profile: quicktest.PerformanceProfile{
			LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: load.ArrivalConstant, WorkloadMode: quicktest.PerformanceWorkloadFixed,
			RequestCount: 1, Concurrency: 1,
			TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 3,
		},
		Progress: quicktest.PerformanceProgress{
			Phase: load.PhaseCompleted, Planned: 1, Offered: 1, Launched: 1, Completed: 1,
			PeakInFlight: 1, Succeeded: 1, SendDurationMS: 12.5, TotalDurationMS: 12.5,
		},
		Metrics: load.Metrics{
			Completed: 1, Succeeded: 1, SuccessRatePercent: 100,
			OfferedQPS: 80, LaunchedQPS: 80, CompletedQPS: 80, SuccessfulRequestQPS: 80,
			RequestQPS: 80, RPM: 4800, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2,
			TTFTP50: 2, TPOTP50: 4.5, E2EP50: 11,
		},
		Failures:  []quicktest.PerformanceFailure{},
		Samples:   []quicktest.PerformanceSample{{RequestIndex: 0, StartedOffsetMS: 1, FinishedOffsetMS: 12, ScheduleLagMS: 1, E2EMS: 11, TTFTMS: 2, TPOTMS: 4.5, HTTPStatus: 200, Success: true, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2}},
		ErrorCode: domain.ErrorCode(""),
	}
}

func addPhaseThreeFixture(report *quicktest.PerformanceReport) {
	report.Profile.WarmupRequests = 1
	report.Profile.RampDurationMS = 1_000
	report.Profile.RampRequestCap = 1
	report.Profile.SliceDurationMS = 100
	report.RequestBudget = &quicktest.PerformanceRequestBudget{
		Limit: 10_000, WarmupCap: 1, RampCap: 1, MeasuredCap: 1, TotalCap: 3,
	}
	report.Warmup = &quicktest.PerformanceTrafficSummary{
		RequestCap: 1, Offered: 1, Launched: 1, Completed: 1, Succeeded: 1,
		PeakInFlight: 1, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2,
		SendDurationMS: 5, TotalDurationMS: 5, Failures: []quicktest.PerformanceFailure{},
	}
	report.Ramp = &quicktest.PerformanceRampSummary{
		Shape: "linear_staircase", DurationMS: 1_000, Steps: 1, TargetConcurrency: 1, CompletedWindow: false,
		Traffic: quicktest.PerformanceTrafficSummary{
			RequestCap: 1, Offered: 1, Launched: 1, Completed: 1, Succeeded: 1,
			PeakInFlight: 1, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2,
			SendDurationMS: 5, TotalDurationMS: 5, Failures: []quicktest.PerformanceFailure{}, Capped: true,
		},
	}
	report.TimeSlices = []quicktest.PerformanceTimeSlice{{
		SliceIndex: 0, StartMS: 0, EndMS: 12.5, Partial: true,
		Offered: 1, Launched: 1, Completed: 1, Succeeded: 1,
		PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2,
		TTFT: quicktest.PerformanceLatencySlice{Count: 1, P50MS: 2, P95MS: 2, P99MS: 2},
		TPOT: quicktest.PerformanceLatencySlice{Count: 1, P50MS: 4.5, P95MS: 4.5, P99MS: 4.5},
		E2E:  quicktest.PerformanceLatencySlice{Count: 1, P50MS: 11, P95MS: 11, P99MS: 11},
	}}
}

func addOpenDurationPhaseThreeFixture(report *quicktest.PerformanceReport) {
	report.Profile.LoadMode = domain.LoadOpenLoop
	report.Profile.RequestCount = 0
	report.Profile.DurationMS = 1_000
	report.Profile.Concurrency = 0
	report.Profile.RatePerSecond = 1
	report.Profile.MaxInFlight = 1
	report.Profile.SliceDurationMS = 1_000
	report.RequestBudget = &quicktest.PerformanceRequestBudget{Limit: 10_000, MeasuredCap: 1, TotalCap: 1}
	report.Progress.Planned = 1
	report.Progress.SendDurationMS = 1_000
	report.Progress.DrainDurationMS = 0
	report.Progress.TotalDurationMS = 1_000
	report.Metrics.OfferedQPS = 1
	report.Metrics.LaunchedQPS = 1
	report.Metrics.CompletedQPS = 1
	report.Metrics.SuccessfulRequestQPS = 1
	report.Metrics.RequestQPS = 1
	report.Metrics.RPM = 60
	report.Metrics.InputTPM = 600
	report.Metrics.OutputTPM = 180
	report.Metrics.TotalTPM = 780
	report.Metrics.GenerationTPS = 3
	report.TimeSlices = []quicktest.PerformanceTimeSlice{{
		SliceIndex: 0, StartMS: 0, EndMS: 1_000,
		Offered: 1, Launched: 1, Completed: 1, Succeeded: 1,
		PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2,
		TTFT: quicktest.PerformanceLatencySlice{Count: 1, P50MS: 2, P95MS: 2, P99MS: 2},
		TPOT: quicktest.PerformanceLatencySlice{Count: 1, P50MS: 4.5, P95MS: 4.5, P99MS: 4.5},
		E2E:  quicktest.PerformanceLatencySlice{Count: 1, P50MS: 11, P95MS: 11, P99MS: 11},
	}}
}
