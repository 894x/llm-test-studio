package runs

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

// ConnectionCommand selects an upstream model with a saved channel or a
// temporary credential. It is consumed before a prepared Run is persisted.
type ConnectionCommand struct {
	Protocol        domain.Protocol `json:"protocol"`
	Model           string          `json:"model"`
	ChannelID       string          `json:"channel_id,omitempty"`
	BaseURL         string          `json:"base_url,omitempty"`
	APIKey          string          `json:"api_key,omitempty"`
	CredentialRunID string          `json:"credential_run_id,omitempty"`
}

func (command ConnectionCommand) String() string   { return "ConnectionCommand{redacted}" }
func (command ConnectionCommand) GoString() string { return command.String() }

func (service *Preparer) resolveRunConnection(
	ctx context.Context,
	command ConnectionCommand,
) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	invalidModel := command.Model == "" || command.Model != strings.TrimSpace(command.Model) || len(command.Model) > 256
	conflictingChannel := command.ChannelID != "" &&
		(!domain.IsUUID(command.ChannelID) || command.BaseURL != "" || command.APIKey != "" || command.CredentialRunID != "")
	conflictingCredential := command.CredentialRunID != "" &&
		(!domain.IsUUID(command.CredentialRunID) || command.APIKey != "")
	invalidProtocol := command.Protocol.Validate() != nil
	invalidTarget := invalidModel || invalidProtocol || strings.ContainsFunc(command.Model, unicode.IsControl)
	conflictingSelection := conflictingChannel || conflictingCredential
	if invalidTarget || conflictingSelection {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrInvalid
	}
	modelMeta, err := domain.NewEntityMeta(service.clock.Now())
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	channelMeta, err := domain.NewEntityMeta(service.clock.Now())
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	channel := domain.Channel{EntityMeta: channelMeta, BaseURL: command.BaseURL, Enabled: true}
	if command.ChannelID != "" {
		if isNil(service.quickTasks) {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrNotRunnable
		}
		channel, err = service.quickTasks.GetChannel(ctx, command.ChannelID)
		if err != nil {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("load run channel: %w", err)
		}
		usable := channel.ID == command.ChannelID && channel.Enabled && channel.CredentialID != ""
		if !usable || channel.Validate() != nil {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrNotRunnable
		}
		mappings, err := service.quickTasks.ListChannelModels(ctx)
		if err != nil {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("load run mappings: %w", err)
		}
		known, supported := false, false
		for _, mapping := range mappings {
			if mapping.ChannelID != channel.ID || mapping.UpstreamModelName != command.Model {
				continue
			}
			if mapping.Validate() != nil {
				return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrNotRunnable
			}
			known = true
			supported = supported || mapping.SupportsProtocol(command.Protocol)
		}
		if known && !supported {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrNotRunnable
		}
	} else {
		invalidKey := command.CredentialRunID == "" && !validQuickTaskAPIKey(command.APIKey)
		if len(command.BaseURL) > 4096 || invalidKey {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrInvalid
		}
		endpoint, err := url.Parse(command.BaseURL)
		if err != nil {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrInvalid
		}
		channel.Name = endpoint.Host
		if channel.Validate() != nil {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrNotRunnable
		}
	}
	model := domain.Model{
		EntityMeta: modelMeta, Name: command.Model,
		Protocols: []domain.Protocol{command.Protocol}, Capabilities: []string{},
	}
	mapping := domain.ChannelModel{
		EntityMeta: modelMeta, ModelID: model.ID, ChannelID: channel.ID,
		Protocols: []domain.Protocol{command.Protocol}, UpstreamModelName: command.Model,
	}
	return model, channel, mapping, nil
}

func (service *Preparer) leaseRunConnection(
	ctx context.Context,
	channel domain.Channel,
	command ConnectionCommand,
) (*credentials.Lease, error) {
	if command.ChannelID != "" {
		ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
		if err != nil {
			return nil, ErrNotRunnable
		}
		return service.credentials.Get(ctx, ref)
	}
	if command.CredentialRunID != "" {
		return service.LeaseQuickTaskCredential(ctx, command.CredentialRunID, channel.BaseURL, command.Protocol)
	}
	secret := []byte(command.APIKey)
	defer clear(secret)
	return credentials.NewTemporaryLease(secret)
}
