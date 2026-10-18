package overview

import (
	"context"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

type Activity struct {
	RunCount    uint64
	ReportCount uint64
	Active      []workspace.RunProjection
}

type Repository interface {
	reporting.QuickPerformanceCatalog
	OverviewActivity(context.Context) (Activity, error)
	ListRecentReportProjections(context.Context, int) ([]reporting.ReportProjection, error)
	ListRecentQuickPerformanceSummaries(context.Context, int) ([]quicktest.PerformanceArchiveSummary, error)
}

type Catalog interface {
	Counts(context.Context) (catalog.Counts, error)
}

type Snapshot struct {
	SchemaVersion int                `json:"schema_version"`
	Catalog       catalog.Counts     `json:"catalog"`
	RunCount      uint64             `json:"run_count"`
	ReportCount   uint64             `json:"report_count"`
	Workspace     workspace.Snapshot `json:"workspace"`
	Reports       reporting.Snapshot `json:"reports"`
}

type Service struct {
	repository Repository
	catalog    Catalog
}

func New(repository Repository, catalog Catalog) Service {
	return Service{repository: repository, catalog: catalog}
}

func (service Service) Snapshot(ctx context.Context) (Snapshot, error) {
	counts, err := service.catalog.Counts(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	activity, err := service.repository.OverviewActivity(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{SchemaVersion: 1, Catalog: counts, RunCount: activity.RunCount, ReportCount: activity.ReportCount,
		Workspace: workspace.Snapshot{SchemaVersion: 1, Plans: []workspace.PlanSummary{}, Runs: []workspace.RunSummary{}},
	}
	for _, projection := range activity.Active {
		summary, err := projection.Summary()
		if err != nil {
			return Snapshot{}, err
		}
		result.Workspace.Runs = append(result.Workspace.Runs, summary)
		result.Workspace.ActiveRunID = summary.ID
	}
	reports, err := reporting.New(recentReports{Repository: service.repository}).Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if len(reports.Reports) > 4 {
		reports.Reports = reports.Reports[:4]
	}
	result.Reports = reports
	return result, nil
}

type recentReports struct{ Repository }

func (reader recentReports) ListReportProjections(ctx context.Context) ([]reporting.ReportProjection, error) {
	return reader.ListRecentReportProjections(ctx, 4)
}
func (reader recentReports) ListQuickPerformanceReportSummaries(ctx context.Context) ([]quicktest.PerformanceArchiveSummary, error) {
	return reader.ListRecentQuickPerformanceSummaries(ctx, 4)
}
