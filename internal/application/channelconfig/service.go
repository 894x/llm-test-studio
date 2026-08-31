// Package channelconfig owns the atomic Base URL + API key channel workflow.
package channelconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
)

var ErrInvalid = errors.New("channel config: invalid input")

type Repository interface {
	CreateCredentialRef(context.Context, domain.CredentialRef) error
	DeleteCredentialRef(context.Context, string, uint64) error
	CreateChannel(context.Context, domain.Channel) error
	GetChannel(context.Context, string) (domain.Channel, error)
	UpdateChannel(context.Context, uint64, domain.Channel) error
}

type Clock interface{ Now() time.Time }
type MetaFactory func(time.Time) (domain.EntityMeta, error)

type Dependencies struct {
	Repository  Repository
	Credentials credentials.Store
	Clock       Clock
	MetaFactory MetaFactory
}

type Service struct {
	repository  Repository
	credentials credentials.Store
	clock       Clock
	metaFactory MetaFactory
}

type CreateCommand struct {
	Name     string
	BaseURL  string
	APIKey   string
	Protocol domain.Protocol
	Enabled  bool
}

type UpdateCommand struct {
	ID               string
	ExpectedRevision uint64
	Name             string
	BaseURL          string
	APIKey           string
	Protocol         domain.Protocol
	Enabled          bool
}

type MutationResult struct {
	ChannelID          string
	ChannelRevision    uint64
	CredentialID       string
	CredentialRevision uint64
}

func New(dependencies Dependencies) (*Service, error) {
	if nilDependency(dependencies.Repository) || nilDependency(dependencies.Credentials) || nilDependency(dependencies.Clock) {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	return &Service{repository: dependencies.Repository, credentials: dependencies.Credentials, clock: dependencies.Clock, metaFactory: factory}, nil
}

func (service *Service) Create(ctx context.Context, command CreateCommand) (MutationResult, error) {
	if service == nil || ctx == nil || command.APIKey == "" {
		return MutationResult{}, ErrInvalid
	}
	secret := []byte(command.APIKey)
	defer clear(secret)
	credentialMeta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialMeta.ID)
	if err != nil {
		return MutationResult{}, ErrInvalid
	}
	digest := sha256.Sum256(secret)
	credential := domain.CredentialRef{
		EntityMeta: credentialMeta, StoreRef: storeRef.Value(), Purpose: domain.CredentialChannelAPIKey,
		MaskedSuffix: safeSuffix(secret, digest), Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
	}
	channelMeta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	channel := domain.Channel{
		EntityMeta: channelMeta, Name: command.Name, BaseURL: command.BaseURL,
		Protocol: command.Protocol, Enabled: command.Enabled, CredentialID: credential.ID,
	}
	if err := credential.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := channel.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.credentials.Set(ctx, storeRef, secret); err != nil {
		return MutationResult{}, fmt.Errorf("store channel credential: %w", err)
	}
	if err := service.repository.CreateCredentialRef(ctx, credential); err != nil {
		cleanupErr := service.credentials.Delete(context.Background(), storeRef)
		return MutationResult{}, errors.Join(fmt.Errorf("persist channel credential metadata: %w", err), cleanupErr)
	}
	if err := service.repository.CreateChannel(ctx, channel); err != nil {
		cleanupErr := service.discardUnboundCredential(credential, storeRef)
		return MutationResult{}, errors.Join(fmt.Errorf("persist channel: %w", err), cleanupErr)
	}
	return MutationResult{
		ChannelID: channel.ID, ChannelRevision: channel.Revision,
		CredentialID: credential.ID, CredentialRevision: credential.Revision,
	}, nil
}

func (service *Service) Update(ctx context.Context, command UpdateCommand) (MutationResult, error) {
	if service == nil || ctx == nil || !domain.IsUUID(command.ID) || command.ExpectedRevision == 0 || command.APIKey == "" {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetChannel(ctx, command.ID)
	if err != nil || current.Revision != command.ExpectedRevision || current.Protocol != command.Protocol {
		return MutationResult{}, ErrInvalid
	}
	secret := []byte(command.APIKey)
	defer clear(secret)
	credentialMeta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialMeta.ID)
	if err != nil {
		return MutationResult{}, ErrInvalid
	}
	digest := sha256.Sum256(secret)
	credential := domain.CredentialRef{
		EntityMeta: credentialMeta, StoreRef: storeRef.Value(), Purpose: domain.CredentialChannelAPIKey,
		MaskedSuffix: safeSuffix(secret, digest), Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
	}
	channelMeta, err := current.EntityMeta.NextRevision(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	updatedChannel := domain.Channel{
		EntityMeta: channelMeta, Name: command.Name, BaseURL: command.BaseURL,
		Protocol: command.Protocol, Enabled: command.Enabled, CredentialID: credential.ID,
	}
	if err := credential.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := updatedChannel.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.credentials.Set(ctx, storeRef, secret); err != nil {
		return MutationResult{}, fmt.Errorf("store replacement channel credential: %w", err)
	}
	if err := service.repository.CreateCredentialRef(ctx, credential); err != nil {
		cleanupErr := service.credentials.Delete(context.Background(), storeRef)
		return MutationResult{}, errors.Join(fmt.Errorf("persist replacement channel credential metadata: %w", err), cleanupErr)
	}
	if err := service.repository.UpdateChannel(ctx, current.Revision, updatedChannel); err != nil {
		cleanupErr := service.discardUnboundCredential(credential, storeRef)
		return MutationResult{}, errors.Join(fmt.Errorf("persist replacement channel revision: %w", err), cleanupErr)
	}
	return MutationResult{
		ChannelID: updatedChannel.ID, ChannelRevision: updatedChannel.Revision,
		CredentialID: credential.ID, CredentialRevision: credential.Revision,
	}, nil
}

func (service *Service) discardUnboundCredential(credential domain.CredentialRef, storeRef credentials.StoreRef) error {
	metadataErr := service.repository.DeleteCredentialRef(context.Background(), credential.ID, credential.Revision)
	if metadataErr != nil {
		// A failed/ambiguous channel commit may already reference this credential.
		// Keep the secret rather than risk breaking or mismatching that revision.
		return fmt.Errorf("rollback channel credential metadata: %w", metadataErr)
	}
	if err := service.credentials.Delete(context.Background(), storeRef); err != nil {
		return fmt.Errorf("rollback channel credential secret: %w", err)
	}
	return nil
}

func safeSuffix(secret []byte, digest [32]byte) string {
	value := make([]byte, 0, 4)
	for index := len(secret) - 1; index >= 0 && len(value) < 4; index-- {
		character := secret[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			value = append(value, character)
		}
	}
	if len(value) < 4 {
		return hex.EncodeToString(digest[:2])
	}
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
	return string(value)
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
