// Package quickhistory exposes bounded, chronological quick-test history.
package quickhistory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

const PageSize = 25

var ErrInvalid = errors.New("invalid quick history page")

// Cursor timestamps have fixed nanosecond precision for lossless SQL ordering.
const TimeLayout = "2006-01-02T15:04:05.000000000Z"

type Cursor struct {
	Time string
	Kind string
	ID   string
}

func ParseCursor(value string) (Cursor, error) {
	if value == "" {
		return Cursor{}, nil
	}
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return Cursor{}, ErrInvalid
	}
	stamp, err := time.Parse(TimeLayout, parts[0])
	validKind := parts[1] == "run" || parts[1] == "performance"
	if err != nil || stamp.IsZero() || stamp.Format(TimeLayout) != parts[0] {
		return Cursor{}, ErrInvalid
	}
	if !validKind || !domain.IsUUID(parts[2]) {
		return Cursor{}, ErrInvalid
	}
	return Cursor{Time: parts[0], Kind: parts[1], ID: parts[2]}, nil
}

type Projection struct {
	Runs        []workspace.RunProjection
	Performance []quicktest.PerformanceArchiveSummary
	NextCursor  string
}

type Repository interface {
	ListQuickHistory(context.Context, string) (Projection, error)
}

type Page struct {
	SchemaVersion int                    `json:"schema_version"`
	Runs          []workspace.RunSummary `json:"runs"`
	Reports       []reporting.Summary    `json:"reports"`
	NextCursor    string                 `json:"next_cursor"`
}

type Service struct{ repository Repository }

func New(repository Repository) Service { return Service{repository: repository} }

func (service Service) Page(ctx context.Context, after string) (Page, error) {
	if _, err := ParseCursor(after); err != nil {
		return Page{}, err
	}
	projection, err := service.repository.ListQuickHistory(ctx, after)
	if err != nil {
		return Page{}, err
	}
	page := Page{SchemaVersion: 1, Runs: []workspace.RunSummary{}, Reports: []reporting.Summary{}, NextCursor: projection.NextCursor}
	if len(projection.Runs)+len(projection.Performance) > PageSize {
		return Page{}, workspace.ErrInconsistent
	}
	for _, run := range projection.Runs {
		if run.Source != "quick_task" {
			return Page{}, workspace.ErrInconsistent
		}
		summary, err := run.Summary()
		if err != nil {
			return Page{}, err
		}
		page.Runs = append(page.Runs, summary)
	}
	for _, report := range projection.Performance {
		summary, err := reporting.SummarizeQuickPerformance(report)
		if err != nil {
			return Page{}, err
		}
		page.Reports = append(page.Reports, summary)
	}
	return page, nil
}
