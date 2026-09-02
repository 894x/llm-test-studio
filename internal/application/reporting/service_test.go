package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

const (
	reportID = "11111111-1111-4111-8111-111111111111"
	runID    = "22222222-2222-4222-8222-222222222222"
)

type fakeCatalog struct {
	projections []ReportProjection
	err         error
	list        func(context.Context) ([]ReportProjection, error)
	calls       int
}

func (catalog *fakeCatalog) ListReportProjections(ctx context.Context) ([]ReportProjection, error) {
	catalog.calls++
	if catalog.list != nil {
		return catalog.list(ctx)
	}
	return append([]ReportProjection(nil), catalog.projections...), catalog.err
}

func TestSnapshotReturnsVersionedSecretFreeSummariesWithOnePortCall(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	catalog := &fakeCatalog{projections: []ReportProjection{{
		ID: reportID, RunID: runID, GeneratedAt: now, RunStatus: domain.RunCompleted,
		PlanName: "Regression", ModelName: "gpt-test", ChannelName: "primary",
		Passed: true, Verdict: "all checks passed", IssueCount: 0,
		CaseCount: 12, FailedCaseCount: 0, AttachmentCount: 2,
	}}}

	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.SchemaVersion != CurrentSchemaVersion || len(snapshot.Reports) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	want := Summary{
		Source: SourceRun,
		ID:     reportID, RunID: runID, GeneratedAt: now, RunStatus: domain.RunCompleted,
		PlanName: "Regression", ModelName: "gpt-test", ChannelName: "primary",
		Passed: true, Verdict: "all checks passed", IssueCount: 0,
		CaseCount: 12, FailedCaseCount: 0, AttachmentCount: 2,
	}
	if snapshot.Reports[0] != want {
		t.Fatalf("summary = %#v, want %#v", snapshot.Reports[0], want)
	}
	if catalog.calls != 1 {
		t.Fatalf("port calls = %d, want 1", catalog.calls)
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, forbidden := range []string{
		"network_egress", "baseline", "evidence", "detail", "relative_path",
		"credential", "base_url", "https://provider.example", "sk-do-not-leak",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("summary leaked forbidden field/value %q: %s", forbidden, encoded)
		}
	}
}

type fakeMixedCatalog struct {
	fakeCatalog
	quick []quicktest.PerformanceArchiveSummary
	get   quicktest.PerformanceReport
	err   error
}

func (catalog *fakeMixedCatalog) ListQuickPerformanceReportSummaries(context.Context) ([]quicktest.PerformanceArchiveSummary, error) {
	return append([]quicktest.PerformanceArchiveSummary(nil), catalog.quick...), catalog.err
}

func (catalog *fakeMixedCatalog) GetQuickPerformanceReport(_ context.Context, id string) (quicktest.PerformanceReport, error) {
	if catalog.err != nil {
		return quicktest.PerformanceReport{}, catalog.err
	}
	if catalog.get.ReportID == id {
		return catalog.get, nil
	}
	return quicktest.PerformanceReport{}, quicktest.ErrPerformanceArchiveNotFound
}

func TestSnapshotIncludesQuickPerformanceReportsWithoutRunOwnership(t *testing.T) {
	formalTime := time.Date(2026, time.August, 31, 15, 0, 0, 0, time.UTC)
	quickReport := validArchivedQuickPerformanceReport()
	catalog := &fakeMixedCatalog{
		fakeCatalog: fakeCatalog{projections: []ReportProjection{validProjection(formalTime)}},
		quick:       []quicktest.PerformanceArchiveSummary{quickPerformanceArchiveSummary(quickReport)},
	}
	snapshot, err := New(catalog).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Reports) != 2 || snapshot.Reports[0].Source != SourceQuickPerformance || snapshot.Reports[1].Source != SourceRun {
		t.Fatalf("reports = %#v", snapshot.Reports)
	}
	quickSummary := snapshot.Reports[0]
	if quickSummary.ID != quickReport.ReportID || quickSummary.RunID != "" || quickSummary.PlanName != "快速性能测试" ||
		quickSummary.ModelName != "quick-model" || quickSummary.ChannelName != "https://example.com/v1" || !quickSummary.Passed ||
		quickSummary.CaseCount != 1 || quickSummary.FailedCaseCount != 0 {
		t.Fatalf("quick summary = %#v", quickSummary)
	}
}

func quickPerformanceArchiveSummary(report quicktest.PerformanceReport) quicktest.PerformanceArchiveSummary {
	return quicktest.PerformanceArchiveSummary{
		ReportID: report.ReportID, GeneratedAt: report.GeneratedAt, Success: report.Success,
		ModelID: report.ModelID, BaseURL: report.BaseURL, Phase: report.Progress.Phase,
		Completed: report.Metrics.Completed, Failed: report.Metrics.Failed,
	}
}

func TestDetailReturnsQuickPerformanceDocumentBySource(t *testing.T) {
	quickReport := validArchivedQuickPerformanceReport()
	catalog := &fakeMixedCatalog{get: quickReport}
	detail, err := New(catalog).Detail(context.Background(), quickReport.ReportID)
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if detail.Source != SourceQuickPerformance || detail.Performance == nil || detail.Performance.ReportID != quickReport.ReportID || len(detail.Performance.Samples) != 1 {
		t.Fatalf("detail = %#v", detail)
	}
	if detail.Report.ID != "" || len(detail.RequestResults) != 0 {
		t.Fatalf("quick detail leaked formal placeholders = %#v", detail)
	}
}

func validArchivedQuickPerformanceReport() quicktest.PerformanceReport {
	return quicktest.PerformanceReport{
		SchemaVersion: quicktest.PerformanceSchemaVersion,
		ReportID:      "77777777-7777-4777-8777-777777777777", GeneratedAt: "2026-08-31T15:30:00Z",
		Archived: true, ArchiveStatus: quicktest.PerformanceArchiveArchived,
		Success: true, AddressMode: quicktest.AddressModeBaseURL,
		BaseURL: "https://example.com/v1", Endpoint: "https://example.com/v1/chat/completions", ModelID: "quick-model",
		Profile:  quicktest.PerformanceProfile{RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 3},
		Progress: quicktest.PerformanceProgress{Phase: load.PhaseCompleted, Planned: 1, Launched: 1, Completed: 1, PeakInFlight: 1, Succeeded: 1, TotalDurationMS: 12},
		Metrics:  load.Metrics{Completed: 1, Succeeded: 1, SuccessRatePercent: 100, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2},
		Failures: []quicktest.PerformanceFailure{},
		Samples:  []quicktest.PerformanceSample{{RequestIndex: 0, StartedOffsetMS: 1, FinishedOffsetMS: 12, E2EMS: 11, TTFTMS: 2, TPOTMS: 4.5, HTTPStatus: 200, Success: true, PromptTokens: 10, CompletionTokens: 3, CachedTokens: 2}},
	}
}

func TestSnapshotSortsNewestFirstAndBreaksTiesByDescendingID(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	older := validProjection(now.Add(-time.Minute))
	older.ID = "11111111-1111-4111-8111-111111111110"
	older.RunID = "22222222-2222-4222-8222-222222222220"
	tieLow := validProjection(now)
	tieHigh := validProjection(now)
	tieHigh.ID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	tieHigh.RunID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

	snapshot, err := New(&fakeCatalog{projections: []ReportProjection{older, tieLow, tieHigh}}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	got := []string{snapshot.Reports[0].ID, snapshot.Reports[1].ID, snapshot.Reports[2].ID}
	want := []string{tieHigh.ID, tieLow.ID, older.ID}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("report order = %v, want %v", got, want)
		}
	}
}

func TestSnapshotRejectsInvalidIdentityTimestampAndLabels(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*ReportProjection)
	}{
		{"report id", func(value *ReportProjection) { value.ID = "not-a-uuid" }},
		{"run id", func(value *ReportProjection) { value.RunID = "not-a-uuid" }},
		{"zero timestamp", func(value *ReportProjection) { value.GeneratedAt = time.Time{} }},
		{"non utc timestamp", func(value *ReportProjection) { value.GeneratedAt = now.In(time.FixedZone("UTC+8", 8*60*60)) }},
		{"blank plan", func(value *ReportProjection) { value.PlanName = " " }},
		{"blank model", func(value *ReportProjection) { value.ModelName = " " }},
		{"blank channel", func(value *ReportProjection) { value.ChannelName = " " }},
		{"blank verdict", func(value *ReportProjection) { value.Verdict = " " }},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := validProjection(now)
			test.mutate(&projection)
			_, err := New(&fakeCatalog{projections: []ReportProjection{projection}}).Snapshot(context.Background())
			if !errors.Is(err, ErrInconsistent) || err.Error() != ErrInconsistent.Error() {
				t.Fatalf("Snapshot() error = %v, want stable ErrInconsistent", err)
			}
		})
	}
}

func TestSnapshotRejectsUnknownAndNonTerminalStatuses(t *testing.T) {
	for _, status := range []domain.RunStatus{
		domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining, domain.RunStatus("unknown"),
	} {
		projection := validProjection(time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
		projection.RunStatus = status
		_, err := New(&fakeCatalog{projections: []ReportProjection{projection}}).Snapshot(context.Background())
		if !errors.Is(err, ErrInconsistent) {
			t.Fatalf("status %q error = %v, want ErrInconsistent", status, err)
		}
	}
}

func TestSnapshotRejectsInvalidConclusionAndCountRelationships(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*ReportProjection)
	}{
		{"passing failed run", func(value *ReportProjection) { value.RunStatus = domain.RunFailed }},
		{"passing cancelled run", func(value *ReportProjection) { value.RunStatus = domain.RunCancelled }},
		{"passing with failed case", func(value *ReportProjection) { value.FailedCaseCount = 1 }},
		{"more failures than cases", func(value *ReportProjection) { value.Passed = false; value.FailedCaseCount = value.CaseCount + 1 }},
		{"negative issues", func(value *ReportProjection) { value.IssueCount = -1 }},
		{"negative cases", func(value *ReportProjection) { value.CaseCount = -1 }},
		{"negative failed cases", func(value *ReportProjection) { value.FailedCaseCount = -1 }},
		{"negative attachments", func(value *ReportProjection) { value.AttachmentCount = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := validProjection(now)
			test.mutate(&projection)
			_, err := New(&fakeCatalog{projections: []ReportProjection{projection}}).Snapshot(context.Background())
			if !errors.Is(err, ErrInconsistent) {
				t.Fatalf("Snapshot() error = %v, want ErrInconsistent", err)
			}
		})
	}
}

func TestSnapshotAllowsFailedAndCancelledNonPassingReports(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	for _, status := range []domain.RunStatus{domain.RunCompleted, domain.RunFailed, domain.RunCancelled} {
		projection := validProjection(now)
		projection.RunStatus = status
		projection.Passed = false
		projection.Verdict = "run did not pass"
		projection.IssueCount = 1
		projection.FailedCaseCount = 1
		snapshot, err := New(&fakeCatalog{projections: []ReportProjection{projection}}).Snapshot(context.Background())
		if err != nil {
			t.Fatalf("status %q Snapshot() error = %v", status, err)
		}
		if snapshot.Reports[0].Passed {
			t.Fatalf("status %q unexpectedly passed", status)
		}
	}
}

func TestSnapshotRejectsDuplicateReportAndRunOwnership(t *testing.T) {
	projection := validProjection(time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
	duplicateReport := projection
	duplicateReport.RunID = "33333333-3333-4333-8333-333333333333"
	duplicateRun := projection
	duplicateRun.ID = "44444444-4444-4444-8444-444444444444"
	for _, values := range [][]ReportProjection{{projection, duplicateReport}, {projection, duplicateRun}} {
		_, err := New(&fakeCatalog{projections: values}).Snapshot(context.Background())
		if !errors.Is(err, ErrInconsistent) {
			t.Fatalf("Snapshot() error = %v, want ErrInconsistent", err)
		}
	}
}

func TestSnapshotRejectsCatalogResultsAboveThePublishedLimit(t *testing.T) {
	const publishedLimit = 100
	projections := make([]ReportProjection, 0, publishedLimit+1)
	for index := 0; index <= publishedLimit; index++ {
		projection := validProjection(time.Date(2026, time.August, 30, 12, 0, index, 0, time.UTC))
		projection.ID = fmt.Sprintf("11111111-1111-4111-8111-%012x", index+1)
		projection.RunID = fmt.Sprintf("22222222-2222-4222-8222-%012x", index+1)
		projections = append(projections, projection)
	}

	_, err := New(&fakeCatalog{projections: projections}).Snapshot(context.Background())
	if !errors.Is(err, ErrInconsistent) || err.Error() != ErrInconsistent.Error() {
		t.Fatalf("Snapshot() error = %v, want ErrInconsistent", err)
	}
}

func TestSnapshotUsesStableErrorsWhileRetainingTheInternalCause(t *testing.T) {
	secret := errors.New("api-key=sk-do-not-leak https://secret.provider.example/v1")
	_, err := New(&fakeCatalog{err: secret}).Snapshot(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Snapshot() error = %v, want ErrUnavailable", err)
	}
	if err.Error() != ErrUnavailable.Error() || strings.Contains(err.Error(), "sk-do-not-leak") {
		t.Fatalf("Snapshot() exposed an unstable/secret error: %v", err)
	}
	if !errors.Is(err, secret) {
		t.Fatalf("Snapshot() did not retain internal cause: %v", err)
	}

	invalid := validProjection(time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
	invalid.ID = "invalid"
	_, err = New(&fakeCatalog{projections: []ReportProjection{invalid}}).Snapshot(context.Background())
	if !errors.Is(err, ErrInconsistent) || err.Error() != ErrInconsistent.Error() || errors.Unwrap(err) == nil {
		t.Fatalf("invalid projection error = %#v, want stable classification with retained cause", err)
	}
}

func TestSnapshotPreservesContextTermination(t *testing.T) {
	for _, termination := range []error{context.Canceled, context.DeadlineExceeded} {
		_, err := New(&fakeCatalog{err: fmt.Errorf("wrapped: %w", termination)}).Snapshot(context.Background())
		if !errors.Is(err, termination) || err.Error() != termination.Error() {
			t.Fatalf("Snapshot() error = %v, want %v", err, termination)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	catalog := &fakeCatalog{list: func(context.Context) ([]ReportProjection, error) {
		cancel()
		return []ReportProjection{validProjection(time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))}, nil
	}}
	_, err := New(catalog).Snapshot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot() post-port cancellation error = %v", err)
	}
	if catalog.calls != 1 {
		t.Fatalf("port calls = %d, want 1", catalog.calls)
	}
}

func TestSnapshotRejectsMissingCatalog(t *testing.T) {
	_, err := New(nil).Snapshot(context.Background())
	if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
		t.Fatalf("Snapshot() error = %v, want stable ErrUnavailable", err)
	}
}

func validProjection(at time.Time) ReportProjection {
	return ReportProjection{
		ID: reportID, RunID: runID, GeneratedAt: at, RunStatus: domain.RunCompleted,
		PlanName: "Regression", ModelName: "gpt-test", ChannelName: "primary",
		Passed: true, Verdict: "all checks passed", CaseCount: 2, AttachmentCount: 1,
	}
}
