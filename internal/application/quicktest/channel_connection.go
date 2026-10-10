package quicktest

import (
	"context"
	"errors"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

var errChannelConnectionUnavailable = errors.New("quick test channel connection is unavailable")

type ChannelConnection struct {
	BaseURL string
	APIKey  []byte
}

type ChannelSelection struct {
	ChannelID string
	Model     string
	Protocol  domain.Protocol
}

type ChannelConnectionResolver interface {
	Resolve(context.Context, ChannelSelection) (ChannelConnection, error)
}

type ChannelConnectionRepository interface {
	GetChannel(context.Context, string) (domain.Channel, error)
	ListChannelModels(context.Context) ([]domain.ChannelModel, error)
}

type StoredChannelConnectionResolver struct {
	repository  ChannelConnectionRepository
	credentials credentials.Store
}

func NewStoredChannelConnectionResolver(repository ChannelConnectionRepository, store credentials.Store) *StoredChannelConnectionResolver {
	return &StoredChannelConnectionResolver{repository: repository, credentials: store}
}

func (resolver *StoredChannelConnectionResolver) Resolve(
	ctx context.Context,
	selection ChannelSelection,
) (ChannelConnection, error) {
	if resolver == nil || ctx == nil {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	configured := resolver.repository != nil && resolver.credentials != nil
	if !configured || !domain.IsUUID(selection.ChannelID) {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	if !SupportsPerformance(selection.Protocol) {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	channel, err := resolver.repository.GetChannel(ctx, selection.ChannelID)
	if err != nil {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	usableChannel := channel.Enabled && channel.CredentialID != ""
	if !usableChannel || channel.Validate() != nil {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	mappings, err := resolver.repository.ListChannelModels(ctx)
	if err != nil {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	knownModel := false
	supported := false
	for _, mapping := range mappings {
		if mapping.ChannelID != channel.ID || mapping.UpstreamModelName != selection.Model {
			continue
		}
		if mapping.Validate() != nil {
			return ChannelConnection{}, errChannelConnectionUnavailable
		}
		knownModel = true
		supported = supported || mapping.SupportsProtocol(selection.Protocol)
	}
	if knownModel && !supported {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
	if err != nil {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	lease, err := resolver.credentials.Get(ctx, storeRef)
	if err != nil {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	defer lease.Close()
	secret, err := lease.Bytes()
	if err != nil || len(secret) == 0 {
		clear(secret)
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	return ChannelConnection{BaseURL: channel.BaseURL, APIKey: secret}, nil
}
