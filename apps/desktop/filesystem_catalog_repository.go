package main

import (
	"context"
	"errors"
	"io/fs"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

// filesystemCatalogRepository keeps authored Case and Suite configuration out
// of SQLite while delegating the remaining catalog and execution records to the
// existing repository during the broader file-catalog cutover.
type filesystemCatalogRepository struct {
	catalog.Repository
	cases interface {
		Entries(context.Context) ([]casecatalog.Entry, error)
		Find(context.Context, string) (casecatalog.Entry, error)
	}
	suites interface {
		Entries(context.Context) ([]suitecatalog.Entry, error)
		Find(context.Context, string) (suitecatalog.Entry, error)
	}
}

type externalSuitePlanWriter interface {
	CreatePlanWithExternalSuite(context.Context, domain.Plan) error
	UpdatePlanWithExternalSuite(context.Context, uint64, domain.Plan) error
}

func (repository filesystemCatalogRepository) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	entries, err := repository.cases.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.TestCase, len(entries))
	for index, entry := range entries {
		result[index] = entry.TestCase
	}
	return result, nil
}

func (repository filesystemCatalogRepository) GetTestCase(ctx context.Context, id string) (domain.TestCase, error) {
	entry, err := repository.cases.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.TestCase{}, catalog.ErrNotFound
	}
	return entry.TestCase, err
}

func (repository filesystemCatalogRepository) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	entries, err := repository.suites.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Suite, len(entries))
	for index, entry := range entries {
		result[index] = entry.Suite
	}
	return result, nil
}

func (repository filesystemCatalogRepository) GetSuite(ctx context.Context, id string) (domain.Suite, error) {
	entry, err := repository.suites.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Suite{}, catalog.ErrNotFound
	}
	return entry.Suite, err
}

func (repository filesystemCatalogRepository) CreatePlan(ctx context.Context, plan domain.Plan) error {
	if plan.SuiteID == "" {
		return repository.Repository.CreatePlan(ctx, plan)
	}
	writer, ok := repository.Repository.(externalSuitePlanWriter)
	if !ok {
		return catalog.ErrUnavailable
	}
	return writer.CreatePlanWithExternalSuite(ctx, plan)
}

func (repository filesystemCatalogRepository) UpdatePlan(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	if plan.SuiteID == "" {
		return repository.Repository.UpdatePlan(ctx, expectedRevision, plan)
	}
	writer, ok := repository.Repository.(externalSuitePlanWriter)
	if !ok {
		return catalog.ErrUnavailable
	}
	return writer.UpdatePlanWithExternalSuite(ctx, expectedRevision, plan)
}
