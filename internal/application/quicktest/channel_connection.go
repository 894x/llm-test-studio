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

type ChannelConnectionResolver interface {
	Resolve(context.Context, string) (ChannelConnection, error)
}

type ChannelConnectionRepository interface {
	GetChannel(context.Context, string) (domain.Channel, error)
	GetCredentialRef(context.Context, string) (domain.CredentialRef, error)
}

type StoredChannelConnectionResolver struct {
	repository  ChannelConnectionRepository
	credentials credentials.Store
}

func NewStoredChannelConnectionResolver(repository ChannelConnectionRepository, store credentials.Store) *StoredChannelConnectionResolver {
	return &StoredChannelConnectionResolver{repository: repository, credentials: store}
}

func (resolver *StoredChannelConnectionResolver) Resolve(ctx context.Context, channelID string) (ChannelConnection, error) {
	if resolver == nil || resolver.repository == nil || resolver.credentials == nil || ctx == nil || !domain.IsUUID(channelID) {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	channel, err := resolver.repository.GetChannel(ctx, channelID)
	if err != nil || !channel.Enabled || channel.Protocol != domain.ProtocolOpenAIChat || channel.CredentialID == "" {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	credential, err := resolver.repository.GetCredentialRef(ctx, channel.CredentialID)
	if err != nil || credential.Purpose != domain.CredentialChannelAPIKey {
		return ChannelConnection{}, errChannelConnectionUnavailable
	}
	storeRef, err := credentials.StoreRefFromCredential(credential)
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
