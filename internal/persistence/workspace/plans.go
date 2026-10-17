package workspace

import (
	"context"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository Catalog) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	return repository.Plans.List(ctx)
}

func (repository Catalog) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	value, err := repository.Plans.Get(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository Catalog) ListPlanDocuments(ctx context.Context) ([]plancatalog.Document, error) {
	values, err := repository.Plans.ListDocuments(ctx)
	if err != nil {
		return nil, mapFileCatalogError(err)
	}
	return values, nil
}

func (repository Catalog) GetPlanDocument(ctx context.Context, id string) (plancatalog.Document, error) {
	value, err := repository.Plans.GetDocument(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository Catalog) CreatePlan(ctx context.Context, plan domain.Plan) error {
	desired := plancatalog.Document{FileSchemaVersion: plancatalog.CurrentFileSchemaVersion, Plan: plan}
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.Plans.CreateDocument(ctx, desired)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, plan.ID)
		return catalogDesiredStateEqual(got, desired, err)
	})
}

func (repository Catalog) UpdatePlan(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	desired := plancatalog.Document{FileSchemaVersion: plancatalog.CurrentFileSchemaVersion, Plan: plan}
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error {
			return mapFileCatalogError(repository.Plans.UpdateDocument(ctx, expectedRevision, desired))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, plan.ID)
		return catalogDesiredStateEqual(got, desired, err)
	})
}

func (repository Catalog) CreatePlanDocument(ctx context.Context, document plancatalog.Document) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if document.Validate() != nil {
			return catalog.ErrInvalid
		}
		return state.write(func() error { return mapFileCatalogError(repository.Plans.CreateDocument(ctx, document)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, document.ID)
		return catalogDesiredStateEqual(got, document, err)
	})
}

func (repository Catalog) UpdatePlanDocument(ctx context.Context, expectedRevision uint64, document plancatalog.Document) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if document.Validate() != nil {
			return catalog.ErrInvalid
		}
		return state.write(func() error {
			return mapFileCatalogError(repository.Plans.UpdateDocument(ctx, expectedRevision, document))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, document.ID)
		return catalogDesiredStateEqual(got, document, err)
	})
}

func (repository Catalog) DeletePlan(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.Plans.Delete(ctx, id, expectedRevision)) })
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetPlan(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}
