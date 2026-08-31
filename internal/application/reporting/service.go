// Package reporting exposes the safe, presentation-neutral report read model
// used by overview, report, desktop, and CLI adapters.
package reporting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/application/quicktest"
	"github.com/894x/llm-studio/internal/domain"
)

const (
	CurrentSchemaVersion = 1
	// MaxSnapshotReports is the published latest-first report-list boundary.
	// Storage ports must never return more entries in one snapshot.
	MaxSnapshotReports = 100
)

var (
	ErrUnavailable  = errors.New("reporting service unavailable")
	ErrInconsistent = errors.New("reporting data is inconsistent")
)

// Catalog supplies one bounded storage read. ReportProjection contains only
// scalar values that the reporting application service is allowed to expose.
type Catalog interface {
	ListReportProjections(context.Context) ([]ReportProjection, error)
}

// DocumentCatalog exposes the sealed report and its request-level results.
// Large provider payloads remain behind the redacted evidence/artifact boundary.
type DocumentCatalog interface {
	Catalog
	GetReport(context.Context, string) (domain.Report, error)
	ListResults(context.Context, string) ([]domain.Result, error)
}

type QuickPerformanceCatalog interface {
	ListQuickPerformanceReportSummaries(context.Context) ([]quicktest.PerformanceArchiveSummary, error)
	GetQuickPerformanceReport(context.Context, string) (quicktest.PerformanceReport, error)
}

type ReportSource string

const (
	SourceRun              ReportSource = "run"
	SourceQuickPerformance ReportSource = "quick_performance"
)

type ReportProjection struct {
	ID              string
	RunID           string
	GeneratedAt     time.Time
	RunStatus       domain.RunStatus
	PlanName        string
	ModelName       string
	ChannelName     string
	Passed          bool
	Verdict         string
	IssueCount      int64
	CaseCount       int64
	FailedCaseCount int64
	AttachmentCount int64
}

type Service struct {
	catalog Catalog
}

func New(catalog Catalog) Service {
	return Service{catalog: catalog}
}

type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Reports       []Summary `json:"reports"`
}

type Summary struct {
	Source          ReportSource     `json:"source"`
	ID              string           `json:"id"`
	RunID           string           `json:"run_id,omitempty"`
	GeneratedAt     time.Time        `json:"generated_at"`
	RunStatus       domain.RunStatus `json:"run_status"`
	PlanName        string           `json:"plan_name"`
	ModelName       string           `json:"model_name"`
	ChannelName     string           `json:"channel_name"`
	Passed          bool             `json:"passed"`
	Verdict         string           `json:"verdict"`
	IssueCount      uint64           `json:"issue_count"`
	CaseCount       uint64           `json:"case_count"`
	FailedCaseCount uint64           `json:"failed_case_count"`
	AttachmentCount uint64           `json:"attachment_count"`
}

// Detail is the complete machine-readable report view. Report contains the
// sealed conclusion and case-level results; RequestResults contains the load
// and protocol observations used to derive the aggregate metrics.
type Detail struct {
	SchemaVersion  int                          `json:"schema_version"`
	Source         ReportSource                 `json:"source"`
	Report         domain.Report                `json:"report"`
	RequestResults []domain.Result              `json:"request_results"`
	Performance    *quicktest.PerformanceReport `json:"performance,omitempty"`
}

func (service Service) Detail(ctx context.Context, reportID string) (Detail, error) {
	if !domain.IsUUID(reportID) {
		return Detail{}, classified(ErrInconsistent, errors.New("report id must be a canonical UUID"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Detail{}, err
	}
	if quickCatalog, ok := service.catalog.(QuickPerformanceCatalog); ok && quickCatalog != nil {
		performance, err := quickCatalog.GetQuickPerformanceReport(ctx, reportID)
		if err == nil {
			if _, validationErr := quicktest.ValidateArchivedPerformanceReport(performance); validationErr != nil || performance.ReportID != reportID {
				return Detail{}, classified(ErrInconsistent, errors.New("quick performance report is inconsistent"))
			}
			return Detail{SchemaVersion: CurrentSchemaVersion, Source: SourceQuickPerformance, Performance: &performance, RequestResults: []domain.Result{}}, nil
		}
		if !errors.Is(err, quicktest.ErrPerformanceArchiveNotFound) {
			return Detail{}, classifyPortError(ctx, err)
		}
	}
	documents, ok := service.catalog.(DocumentCatalog)
	if !ok || documents == nil {
		return Detail{}, ErrUnavailable
	}
	report, err := documents.GetReport(ctx, reportID)
	if err != nil {
		return Detail{}, classifyPortError(ctx, err)
	}
	if report.ID != reportID {
		return Detail{}, classified(ErrInconsistent, errors.New("report catalog returned another report"))
	}
	results, err := documents.ListResults(ctx, report.RunID)
	if err != nil {
		return Detail{}, classifyPortError(ctx, err)
	}
	requestResults := make([]domain.Result, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result.RunID != report.RunID {
			return Detail{}, classified(ErrInconsistent, errors.New("request result belongs to another run"))
		}
		if _, duplicate := seen[result.ID]; duplicate {
			return Detail{}, classified(ErrInconsistent, errors.New("duplicate result id"))
		}
		seen[result.ID] = struct{}{}
		if result.RequestID != "" {
			requestResults = append(requestResults, result)
		}
	}
	return Detail{SchemaVersion: CurrentSchemaVersion, Source: SourceRun, Report: report, RequestResults: requestResults}, nil
}

func (service Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if service.catalog == nil {
		return Snapshot{}, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	projections, err := service.catalog.ListReportProjections(ctx)
	if err != nil {
		return Snapshot{}, classifyPortError(ctx, err)
	}
	if len(projections) > MaxSnapshotReports {
		return Snapshot{}, classified(
			ErrInconsistent,
			fmt.Errorf("report catalog returned %d entries, limit is %d", len(projections), MaxSnapshotReports),
		)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	summaries := make([]Summary, 0, len(projections))
	reportIDs := make(map[string]struct{}, len(projections))
	runIDs := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := reportIDs[projection.ID]; duplicate {
			return Snapshot{}, classified(ErrInconsistent, fmt.Errorf("duplicate report id %q", projection.ID))
		}
		if _, duplicate := runIDs[projection.RunID]; duplicate {
			return Snapshot{}, classified(ErrInconsistent, fmt.Errorf("duplicate report run id %q", projection.RunID))
		}
		if err := projection.validate(); err != nil {
			return Snapshot{}, classified(ErrInconsistent, err)
		}
		reportIDs[projection.ID] = struct{}{}
		runIDs[projection.RunID] = struct{}{}
		summaries = append(summaries, projection.summary())
	}
	if quickCatalog, ok := service.catalog.(QuickPerformanceCatalog); ok && quickCatalog != nil {
		quickReports, err := quickCatalog.ListQuickPerformanceReportSummaries(ctx)
		if err != nil {
			return Snapshot{}, classifyPortError(ctx, err)
		}
		if len(quickReports) > MaxSnapshotReports {
			return Snapshot{}, classified(ErrInconsistent, errors.New("quick performance report catalog exceeded the published limit"))
		}
		for _, report := range quickReports {
			if _, duplicate := reportIDs[report.ReportID]; duplicate {
				return Snapshot{}, classified(ErrInconsistent, fmt.Errorf("duplicate report id %q", report.ReportID))
			}
			summary, err := quickPerformanceSummary(report)
			if err != nil {
				return Snapshot{}, classified(ErrInconsistent, err)
			}
			reportIDs[report.ReportID] = struct{}{}
			summaries = append(summaries, summary)
		}
	}

	sort.Slice(summaries, func(left, right int) bool {
		if summaries[left].GeneratedAt.Equal(summaries[right].GeneratedAt) {
			return summaries[left].ID > summaries[right].ID
		}
		return summaries[left].GeneratedAt.After(summaries[right].GeneratedAt)
	})
	if len(summaries) > MaxSnapshotReports {
		summaries = summaries[:MaxSnapshotReports]
	}
	return Snapshot{SchemaVersion: CurrentSchemaVersion, Reports: summaries}, nil
}

func quickPerformanceSummary(report quicktest.PerformanceArchiveSummary) (Summary, error) {
	generatedAt, err := quicktest.ValidatePerformanceArchiveSummary(report)
	if err != nil {
		return Summary{}, err
	}
	status := domain.RunFailed
	switch report.Phase {
	case "completed":
		status = domain.RunCompleted
	case "cancelled":
		status = domain.RunCancelled
	}
	verdict := "性能测试未通过"
	if report.Success {
		verdict = "全部请求成功"
	}
	return Summary{
		Source: SourceQuickPerformance, ID: report.ReportID, GeneratedAt: generatedAt,
		RunStatus: status, PlanName: "快速性能测试", ModelName: report.ModelID, ChannelName: report.BaseURL,
		Passed: report.Success, Verdict: verdict, IssueCount: report.Failed,
		CaseCount: report.Completed, FailedCaseCount: report.Failed,
	}, nil
}

func (projection ReportProjection) validate() error {
	if !domain.IsUUID(projection.ID) || !domain.IsUUID(projection.RunID) {
		return errors.New("report projection requires canonical report and run UUIDs")
	}
	if projection.GeneratedAt.IsZero() {
		return errors.New("report projection requires a generation timestamp")
	}
	_, offset := projection.GeneratedAt.Zone()
	if offset != 0 {
		return errors.New("report projection generation timestamp must be UTC")
	}
	if strings.TrimSpace(projection.PlanName) == "" || strings.TrimSpace(projection.ModelName) == "" ||
		strings.TrimSpace(projection.ChannelName) == "" || strings.TrimSpace(projection.Verdict) == "" {
		return errors.New("report projection requires plan, model, channel, and verdict labels")
	}
	switch projection.RunStatus {
	case domain.RunCompleted:
	case domain.RunFailed, domain.RunCancelled:
		if projection.Passed {
			return fmt.Errorf("%s run cannot have a passing report", projection.RunStatus)
		}
	default:
		return fmt.Errorf("report projection requires a terminal run status, got %q", projection.RunStatus)
	}
	if projection.IssueCount < 0 || projection.CaseCount < 0 || projection.FailedCaseCount < 0 || projection.AttachmentCount < 0 {
		return errors.New("report projection counts must be non-negative")
	}
	if projection.FailedCaseCount > projection.CaseCount {
		return errors.New("report projection failed case count exceeds case count")
	}
	if projection.Passed && projection.FailedCaseCount != 0 {
		return errors.New("passing report projection contains failed cases")
	}
	return nil
}

func (projection ReportProjection) summary() Summary {
	return Summary{
		Source: SourceRun, ID: projection.ID, RunID: projection.RunID, GeneratedAt: projection.GeneratedAt,
		RunStatus: projection.RunStatus, PlanName: projection.PlanName,
		ModelName: projection.ModelName, ChannelName: projection.ChannelName,
		Passed: projection.Passed, Verdict: projection.Verdict,
		IssueCount: uint64(projection.IssueCount), CaseCount: uint64(projection.CaseCount),
		FailedCaseCount: uint64(projection.FailedCaseCount), AttachmentCount: uint64(projection.AttachmentCount),
	}
}

func classifyPortError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return classified(ErrUnavailable, err)
}

type classifiedError struct {
	kind  error
	cause error
}

func classified(kind, cause error) error {
	return classifiedError{kind: kind, cause: cause}
}

func (err classifiedError) Error() string {
	return err.kind.Error()
}

func (err classifiedError) Is(target error) bool {
	return errors.Is(err.kind, target) || errors.Is(err.cause, target)
}

func (err classifiedError) Unwrap() error {
	return err.cause
}
