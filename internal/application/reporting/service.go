// Package reporting exposes the safe, presentation-neutral report read model
// used by overview, report, desktop, and CLI adapters.
package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	CurrentSchemaVersion       = 1
	CurrentDetailSchemaVersion = 2
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
	SchemaVersion            int                          `json:"schema_version"`
	Source                   ReportSource                 `json:"source"`
	Report                   domain.Report                `json:"report"`
	RequestResults           []domain.Result              `json:"request_results"`
	Suites                   []SuiteDetail                `json:"suites"`
	UnassignedRequestResults []domain.Result              `json:"unassigned_request_results"`
	Performance              *quicktest.PerformanceReport `json:"performance,omitempty"`
}

type SuiteDetail struct {
	SuiteEntryID  string                        `json:"suite_entry_id"`
	SuiteID       string                        `json:"suite_id"`
	SuiteRevision uint64                        `json:"suite_revision"`
	SuiteKey      string                        `json:"suite_key"`
	SuiteName     string                        `json:"suite_name"`
	Status        domain.SuiteReportStatus      `json:"status"`
	Conclusion    domain.ReportConclusion       `json:"conclusion"`
	SLA           map[string]domain.MetricValue `json:"sla"`
	Metrics       map[string]domain.MetricValue `json:"metrics"`
	Timeline      []json.RawMessage             `json:"timeline"`
	Distributions []json.RawMessage             `json:"distributions"`
	Cases         []CaseDetail                  `json:"cases"`
}

type CaseDetail struct {
	CaseID          string          `json:"case_id"`
	Revision        uint64          `json:"revision"`
	Key             string          `json:"key"`
	Name            string          `json:"name"`
	CaseType        domain.CaseType `json:"case_type"`
	CaseTypeVersion uint32          `json:"case_type_version"`
	SummaryResult   *domain.Result  `json:"summary_result,omitempty"`
	RequestResults  []domain.Result `json:"request_results"`
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
			return Detail{
				SchemaVersion: CurrentSchemaVersion, Source: SourceQuickPerformance,
				Performance: &performance, RequestResults: []domain.Result{},
				Suites: []SuiteDetail{}, UnassignedRequestResults: []domain.Result{},
			}, nil
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
	if err := report.Validate(); err != nil {
		return Detail{}, classified(ErrInconsistent, errors.New("report catalog returned an invalid report"))
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
	var suites []SuiteDetail
	var unassigned []domain.Result
	if len(report.PlanSnapshot.Suites) > 0 {
		suites, unassigned, err = hierarchicalDetail(report, requestResults)
	} else if report.PlanSnapshot.QuickTask != nil {
		canonical := make([]domain.Result, len(requestResults))
		for index, result := range requestResults {
			if result.SuiteEntryID != "" || result.CaseID == "" {
				return Detail{}, classified(ErrInconsistent, errors.New("quick-task request result has invalid stored ownership"))
			}
			result.SuiteEntryID = report.RunID
			canonical[index] = result
		}
		requestResults = canonical
		suites, unassigned, err = quickTaskDetail(report, requestResults)
	} else {
		err = errors.New("run report has neither authored suites nor quick-task provenance")
	}
	if err != nil {
		return Detail{}, classified(ErrInconsistent, err)
	}
	return Detail{
		SchemaVersion:            CurrentDetailSchemaVersion,
		Source:                   SourceRun,
		Report:                   report,
		RequestResults:           requestResults,
		Suites:                   suites,
		UnassignedRequestResults: unassigned,
	}, nil
}

func quickTaskDetail(report domain.Report, requestResults []domain.Result) ([]SuiteDetail, []domain.Result, error) {
	snapshot := report.PlanSnapshot
	if snapshot.QuickTask == nil || len(snapshot.Cases) != len(snapshot.CaseDefinitions) {
		return nil, nil, errors.New("quick-task report snapshot is incomplete")
	}
	knownCases := make(map[string]int, len(snapshot.Cases))
	for index, ref := range snapshot.Cases {
		knownCases[ref.CaseID] = index
	}
	groupedRequests := make(map[string][]domain.Result, len(snapshot.Cases))
	unassigned := make([]domain.Result, 0)
	for _, result := range requestResults {
		if result.SuiteEntryID != report.RunID || result.CaseID == "" {
			unassigned = append(unassigned, result)
			continue
		}
		if _, exists := knownCases[result.CaseID]; !exists {
			return nil, nil, errors.New("quick-task request result references a case outside the report snapshot")
		}
		groupedRequests[result.CaseID] = append(groupedRequests[result.CaseID], result)
	}
	summaries := make(map[string]domain.Result, len(report.CaseResults))
	for _, result := range report.CaseResults {
		if result.SuiteEntryID != report.RunID {
			return nil, nil, errors.New("quick-task case result has invalid synthetic ownership")
		}
		if _, exists := knownCases[result.CaseID]; !exists {
			return nil, nil, errors.New("quick-task case result references a case outside the report snapshot")
		}
		if _, duplicate := summaries[result.CaseID]; duplicate {
			return nil, nil, errors.New("duplicate quick-task case result ownership")
		}
		summaries[result.CaseID] = result
	}
	cases := make([]CaseDetail, 0, len(snapshot.CaseDefinitions))
	for index, definition := range snapshot.CaseDefinitions {
		ref := snapshot.Cases[index]
		var summary *domain.Result
		if result, exists := summaries[ref.CaseID]; exists {
			copy := result
			summary = &copy
		}
		cases = append(cases, CaseDetail{
			CaseID: ref.CaseID, Revision: ref.Revision, Key: definition.Key, Name: definition.Name,
			CaseType: definition.Definition.Type, CaseTypeVersion: definition.Definition.TypeVersion,
			SummaryResult: summary, RequestResults: append([]domain.Result{}, groupedRequests[ref.CaseID]...),
		})
	}
	suiteReport := report.SuiteReports[0]
	return []SuiteDetail{{
		SuiteEntryID: suiteReport.SuiteEntryID, SuiteID: suiteReport.SuiteID, SuiteRevision: suiteReport.SuiteRevision,
		SuiteKey: suiteReport.SuiteKey, SuiteName: suiteReport.SuiteName, Status: suiteReport.Status,
		Conclusion: suiteReport.Conclusion, SLA: suiteReport.SLA, Metrics: suiteReport.Metrics,
		Timeline:      append([]json.RawMessage{}, suiteReport.Timeline...),
		Distributions: append([]json.RawMessage{}, suiteReport.Distributions...), Cases: cases,
	}}, unassigned, nil
}

func hierarchicalDetail(report domain.Report, requestResults []domain.Result) ([]SuiteDetail, []domain.Result, error) {
	type owner struct {
		suiteIndex int
		caseIndex  int
	}
	owners := make(map[string]owner)
	for suiteIndex, suite := range report.PlanSnapshot.Suites {
		for caseIndex, ref := range suite.Cases {
			owners[detailResultKey(suite.EntryID, ref.CaseID)] = owner{suiteIndex: suiteIndex, caseIndex: caseIndex}
		}
	}
	groupedRequests := make(map[string][]domain.Result)
	unassigned := make([]domain.Result, 0)
	for _, result := range requestResults {
		if result.SuiteEntryID == "" || result.CaseID == "" {
			unassigned = append(unassigned, result)
			continue
		}
		key := detailResultKey(result.SuiteEntryID, result.CaseID)
		if _, exists := owners[key]; !exists {
			return nil, nil, errors.New("request result references a suite or case outside the report snapshot")
		}
		groupedRequests[key] = append(groupedRequests[key], result)
	}

	summaries := make(map[string]domain.Result, len(report.CaseResults))
	for _, result := range report.CaseResults {
		key := detailResultKey(result.SuiteEntryID, result.CaseID)
		if _, exists := owners[key]; !exists {
			return nil, nil, errors.New("case result references a suite or case outside the report snapshot")
		}
		if _, duplicate := summaries[key]; duplicate {
			return nil, nil, errors.New("duplicate case result ownership")
		}
		summaries[key] = result
	}

	suites := make([]SuiteDetail, 0, len(report.PlanSnapshot.Suites))
	for suiteIndex, snapshot := range report.PlanSnapshot.Suites {
		suiteReport := report.SuiteReports[suiteIndex]
		cases := make([]CaseDetail, 0, len(snapshot.CaseDefinitions))
		for caseIndex, definition := range snapshot.CaseDefinitions {
			ref := snapshot.Cases[caseIndex]
			key := detailResultKey(snapshot.EntryID, ref.CaseID)
			var summary *domain.Result
			if result, exists := summaries[key]; exists {
				copy := result
				summary = &copy
			}
			requests := append([]domain.Result{}, groupedRequests[key]...)
			cases = append(cases, CaseDetail{
				CaseID:          ref.CaseID,
				Revision:        ref.Revision,
				Key:             definition.Key,
				Name:            definition.Name,
				CaseType:        definition.Definition.Type,
				CaseTypeVersion: definition.Definition.TypeVersion,
				SummaryResult:   summary,
				RequestResults:  requests,
			})
		}
		suites = append(suites, SuiteDetail{
			SuiteEntryID:  suiteReport.SuiteEntryID,
			SuiteID:       suiteReport.SuiteID,
			SuiteRevision: suiteReport.SuiteRevision,
			SuiteKey:      suiteReport.SuiteKey,
			SuiteName:     suiteReport.SuiteName,
			Status:        suiteReport.Status,
			Conclusion:    suiteReport.Conclusion,
			SLA:           suiteReport.SLA,
			Metrics:       suiteReport.Metrics,
			Timeline:      append([]json.RawMessage{}, suiteReport.Timeline...),
			Distributions: append([]json.RawMessage{}, suiteReport.Distributions...),
			Cases:         cases,
		})
	}
	return suites, unassigned, nil
}

func detailResultKey(suiteEntryID, caseID string) string {
	return suiteEntryID + "\x00" + caseID
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
