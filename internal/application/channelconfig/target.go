package channelconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

type targetRepository interface {
	ListModels(context.Context) ([]domain.Model, error)
	CreateModel(context.Context, domain.Model) error
	UpdateModel(context.Context, uint64, domain.Model) error
	DeleteModel(context.Context, string, uint64) error
	CreateChannelWithMappings(context.Context, domain.Channel, []domain.ChannelModel) error
}

func (service *Service) SaveTarget(ctx context.Context, command catalog.SaveQuickTestTargetCommand) (MutationResult, error) {
	if service == nil || ctx == nil || command.Protocol.Validate() != nil ||
		strings.TrimSpace(command.ModelName) == "" || command.ModelName != strings.TrimSpace(command.ModelName) {
		return MutationResult{}, ErrInvalid
	}
	repository, ok := service.repository.(targetRepository)
	if !ok {
		return MutationResult{}, fmt.Errorf("save quick test target: repository does not support model mappings: %w", ErrInvalid)
	}
	var result MutationResult
	err := service.withCredentialMutation(ctx, func(lockedCtx context.Context) error {
		models, err := repository.ListModels(lockedCtx)
		if err != nil {
			return err
		}
		var original domain.Model
		for _, model := range models {
			if model.Name == command.ModelName {
				if original.ID != "" {
					return fmt.Errorf("model name is ambiguous: %w", catalog.ErrConflict)
				}
				original = model
			}
		}
		model := original
		changed := original.ID == "" || !original.SupportsProtocol(command.Protocol)
		if original.ID == "" {
			meta, err := service.metaFactory(service.clock.Now())
			if err != nil {
				return err
			}
			model = domain.Model{EntityMeta: meta, Name: command.ModelName, Protocols: []domain.Protocol{command.Protocol}}
		} else if changed {
			model.EntityMeta, err = original.EntityMeta.NextRevision(service.clock.Now())
			if err != nil {
				return err
			}
			model.Protocols = append(append([]domain.Protocol(nil), original.Protocols...), command.Protocol)
		}
		if model.Validate() != nil {
			return ErrInvalid
		}
		mappingMeta, err := service.metaFactory(service.clock.Now())
		if err != nil {
			return err
		}
		result, err = service.createWith(lockedCtx, CreateCommand{
			Name: command.Name, BaseURL: command.BaseURL, APIKey: command.APIKey, Enabled: true,
		}, func(writeCtx context.Context, channel domain.Channel) error {
			mapping := domain.ChannelModel{
				EntityMeta: mappingMeta, ChannelID: channel.ID, ModelID: model.ID,
				UpstreamModelName: command.ModelName, Protocols: []domain.Protocol{command.Protocol},
			}
			if mapping.Validate() != nil {
				return ErrInvalid
			}
			if changed {
				if original.ID == "" {
					err = repository.CreateModel(writeCtx, model)
				} else {
					err = repository.UpdateModel(writeCtx, original.Revision, model)
				}
				if err != nil {
					return err
				}
			}
			// The channel and mapping share one atomic channels.json write.
			if err := repository.CreateChannelWithMappings(writeCtx, channel, []domain.ChannelModel{mapping}); err != nil {
				if !changed {
					return err
				}
				rollbackCtx := context.WithoutCancel(writeCtx)
				var rollbackErr error
				if original.ID == "" {
					rollbackErr = repository.DeleteModel(rollbackCtx, model.ID, model.Revision)
				} else {
					original.EntityMeta, rollbackErr = model.EntityMeta.NextRevision(service.clock.Now())
					if rollbackErr == nil {
						rollbackErr = repository.UpdateModel(rollbackCtx, model.Revision, original)
					}
				}
				return errors.Join(err, rollbackErr)
			}
			return nil
		})
		return err
	})
	return result, err
}
