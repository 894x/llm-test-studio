package workspace

import (
	"context"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository Catalog) ListModels(ctx context.Context) ([]domain.Model, error) {
	return repository.Models.List(ctx)
}

func (repository Catalog) GetModel(ctx context.Context, id string) (domain.Model, error) {
	value, err := repository.Models.Get(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository Catalog) CreateModel(ctx context.Context, model domain.Model) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.Models.Create(ctx, model)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetModel(verifyCtx, model.ID)
		return catalogDesiredStateEqual(got, model, err)
	})
}

func (repository Catalog) UpdateModel(ctx context.Context, expectedRevision uint64, model domain.Model) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		mappings, err := repository.ListChannelModels(ctx)
		if err != nil {
			return err
		}
		for _, mapping := range mappings {
			if mapping.ModelID != model.ID {
				continue
			}
			for _, value := range mapping.Protocols {
				if !model.SupportsProtocol(value) {
					return catalog.ErrConflict
				}
			}
		}
		return state.write(func() error { return mapFileCatalogError(repository.Models.Update(ctx, expectedRevision, model)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetModel(verifyCtx, model.ID)
		return catalogDesiredStateEqual(got, model, err)
	})
}

func (repository Catalog) DeleteModel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteModelUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetModel(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository Catalog) deleteModelUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	mappings, err := repository.ListChannelModels(ctx)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if mapping.ModelID == id {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error { return mapFileCatalogError(repository.Models.Delete(ctx, id, expectedRevision)) })
}
