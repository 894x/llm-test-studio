package workspace

import (
	"context"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository Catalog) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	return repository.Channels.ListChannels(ctx)
}

func (repository Catalog) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	value, err := repository.Channels.GetChannel(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository Catalog) CreateChannel(ctx context.Context, channel domain.Channel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.Channels.CreateChannel(ctx, channel)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannel(verifyCtx, channel.ID)
		return catalogDesiredStateEqual(got, channel, err)
	})
}

func (repository Catalog) CreateChannelWithMappings(ctx context.Context, channel domain.Channel, mappings []domain.ChannelModel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		for _, mapping := range mappings {
			if mapping.ChannelID != channel.ID || mapping.Validate() != nil {
				return catalog.ErrInvalid
			}
			model, err := repository.GetModel(ctx, mapping.ModelID)
			if err != nil {
				return err
			}
			for _, protocol := range mapping.Protocols {
				if !model.SupportsProtocol(protocol) {
					return catalog.ErrInvalid
				}
			}
		}
		return state.write(func() error {
			return mapFileCatalogError(repository.Channels.CreateChannelWithMappings(ctx, channel, mappings))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannel(verifyCtx, channel.ID)
		if equal, err := catalogDesiredStateEqual(got, channel, err); !equal || err != nil {
			return equal, err
		}
		for _, mapping := range mappings {
			got, err := repository.GetChannelModel(verifyCtx, mapping.ID)
			if equal, err := catalogDesiredStateEqual(got, mapping, err); !equal || err != nil {
				return equal, err
			}
		}
		return true, nil
	})
}

func (repository Catalog) UpdateChannel(ctx context.Context, expectedRevision uint64, channel domain.Channel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error {
			return mapFileCatalogError(repository.Channels.UpdateChannel(ctx, expectedRevision, channel))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannel(verifyCtx, channel.ID)
		return catalogDesiredStateEqual(got, channel, err)
	})
}

func (repository Catalog) DeleteChannel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteChannelUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetChannel(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository Catalog) deleteChannelUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	mappings, err := repository.ListChannelModels(ctx)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if mapping.ChannelID == id {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error { return mapFileCatalogError(repository.Channels.DeleteChannel(ctx, id, expectedRevision)) })
}

func (repository Catalog) ListChannelModels(ctx context.Context) ([]domain.ChannelModel, error) {
	return repository.Channels.ListMappings(ctx)
}

func (repository Catalog) GetChannelModel(ctx context.Context, id string) (domain.ChannelModel, error) {
	value, err := repository.Channels.GetMapping(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository Catalog) CreateChannelModel(ctx context.Context, mapping domain.ChannelModel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if err := repository.validateMappingReferencesUnlocked(ctx, mapping); err != nil {
			return err
		}
		return state.write(func() error { return mapFileCatalogError(repository.Channels.CreateMapping(ctx, mapping)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannelModel(verifyCtx, mapping.ID)
		return catalogDesiredStateEqual(got, mapping, err)
	})
}

func (repository Catalog) UpdateChannelModel(ctx context.Context, expectedRevision uint64, mapping domain.ChannelModel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if err := repository.validateMappingReferencesUnlocked(ctx, mapping); err != nil {
			return err
		}
		return state.write(func() error {
			return mapFileCatalogError(repository.Channels.UpdateMapping(ctx, expectedRevision, mapping))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannelModel(verifyCtx, mapping.ID)
		return catalogDesiredStateEqual(got, mapping, err)
	})
}

func (repository Catalog) DeleteChannelModel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteChannelModelUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetChannelModel(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository Catalog) deleteChannelModelUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	return state.write(func() error { return mapFileCatalogError(repository.Channels.DeleteMapping(ctx, id, expectedRevision)) })
}

func (repository Catalog) validateMappingReferencesUnlocked(ctx context.Context, mapping domain.ChannelModel) error {
	model, err := repository.GetModel(ctx, mapping.ModelID)
	if err != nil {
		return err
	}
	_, err = repository.GetChannel(ctx, mapping.ChannelID)
	if err != nil {
		return err
	}
	for _, value := range mapping.Protocols {
		if !model.SupportsProtocol(value) {
			return catalog.ErrInvalid
		}
	}
	return nil
}
