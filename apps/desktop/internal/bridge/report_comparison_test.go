package bridge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
)

type comparisonReportQuery struct {
	recordingReportingQuery
	details map[string]reporting.Detail
	readIDs []string
}

func TestDesktopReportComparisonAcceptsSixteenReports(t *testing.T) {
	query := &comparisonReportQuery{details: map[string]reporting.Detail{}}
	ids := make([]string, 0, 17)
	for index := range 17 {
		id := fmt.Sprintf("42000000-0000-4000-8000-%012d", index+100)
		ids = append(ids, id)
		query.details[id] = reporting.Detail{
			SchemaVersion: 1,
			Source:        reporting.SourceRun,
			Report:        domain.Report{ID: id},
		}
	}
	app := New(func(context.Context) (Dependencies, error) { return Dependencies{Reports: query}, nil })
	app.onStartup(context.Background())
	result, err := app.CompareReports(ids[:16], ids[0])
	if err != nil || len(result.Reports) != 16 || len(query.readIDs) != 16 {
		t.Fatalf("sixteen-report comparison: reports=%d, reads=%d, error=%v", len(result.Reports), len(query.readIDs), err)
	}
	if _, err := app.CompareReports(ids, ids[0]); err == nil {
		t.Fatal("accepted seventeen reports")
	}
	if len(query.readIDs) != 16 {
		t.Fatal("oversized selection must be rejected before reading reports")
	}
}

func (query *comparisonReportQuery) Detail(ctx context.Context, id string) (reporting.Detail, error) {
	query.ctx = ctx
	query.readIDs = append(query.readIDs, id)
	return query.details[id], query.err
}

func TestDesktopReportComparisonValidatesSelectionBeforeReadingAndRetainsOrder(t *testing.T) {
	const left = "42000000-0000-4000-8000-000000000090"
	const right = "42000000-0000-4000-8000-000000000091"
	query := &comparisonReportQuery{details: map[string]reporting.Detail{
		left:  {SchemaVersion: 1, Source: reporting.SourceRun, Report: domain.Report{ID: left}},
		right: {SchemaVersion: 1, Source: reporting.SourceRun, Report: domain.Report{ID: right}},
	}}
	app := New(func(context.Context) (Dependencies, error) { return Dependencies{Reports: query}, nil })
	app.onStartup(context.Background())
	for _, ids := range [][]string{{left}, {left, left}, {left, "bad"}, make([]string, reporting.MaxComparisonReports+1)} {
		if _, err := app.CompareReports(ids, left); err == nil {
			t.Fatal("accepted invalid selection")
		}
	}
	if _, err := app.CompareReports([]string{left, right}, "42000000-0000-4000-8000-000000000099"); err == nil {
		t.Fatal("accepted foreign baseline")
	}
	if len(query.readIDs) != 0 {
		t.Fatal("invalid selections must be rejected before reads")
	}
	result, err := app.CompareReports([]string{right, left}, left)
	if err != nil || result.BaselineID != left || result.Reports[0].Report.ID != right {
		t.Fatalf("comparison order: %#v, %v", result, err)
	}
	query.details[left] = query.details[right]
	if _, err := app.CompareReports([]string{left, right}, left); err == nil {
		t.Fatal("accepted a substituted report document")
	}
	query.err = errors.New("private report failure")
	if _, err := app.CompareReports([]string{left, right}, left); err == nil || err.Error() == query.err.Error() {
		t.Fatalf("report failure must cross the safe error boundary: %v", err)
	}
}
