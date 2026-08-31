package channelconfig_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/application/channelconfig"
	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
)

func TestCreateChannelStoresBaseURLAndKeyAsOneBoundConfiguration(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	repository := &channelRepository{}
	store := credentials.NewMemoryStore()
	ids := []string{"70000000-0000-4000-8000-000000000001", "70000000-0000-4000-8000-000000000002"}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, Clock: fixedChannelClock{now},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			id := ids[0]
			ids = ids[1:]
			return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Create(context.Background(), channelconfig.CreateCommand{
		Name: "primary", BaseURL: "https://api.example.test/v1", APIKey: "sk-test-1234",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.ChannelID != repository.channel.ID || repository.channel.BaseURL != "https://api.example.test/v1" || repository.channel.CredentialID != repository.credential.ID {
		t.Fatalf("stored channel = %#v credential = %#v", repository.channel, repository.credential)
	}
	ref, _ := credentials.StoreRefFromCredential(repository.credential)
	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := lease.Bytes()
	_ = lease.Close()
	if string(secret) != "sk-test-1234" {
		t.Fatalf("stored secret = %q", secret)
	}
	clear(secret)
}

func TestUpdateChannelReplacesBaseURLAndKeyInOneRevision(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 30, 0, 0, time.UTC)
	repository := &channelRepository{}
	store := credentials.NewMemoryStore()
	ids := []string{"70000000-0000-4000-8000-000000000011", "70000000-0000-4000-8000-000000000012", "70000000-0000-4000-8000-000000000013"}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, Clock: fixedChannelClock{now},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			id := ids[0]
			ids = ids[1:]
			return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), channelconfig.CreateCommand{
		Name: "primary", BaseURL: "https://old.example.test/v1", APIKey: "sk-old-1234",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldCredential := repository.credential
	oldRef, err := credentials.StoreRefFromCredential(oldCredential)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: created.ChannelID, ExpectedRevision: created.ChannelRevision,
		Name: "primary updated", BaseURL: "https://new.example.test/v1", APIKey: "sk-new-9876",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ChannelRevision != 2 || updated.CredentialRevision != 1 || updated.CredentialID == oldCredential.ID || repository.channel.BaseURL != "https://new.example.test/v1" {
		t.Fatalf("updated result = %#v channel = %#v", updated, repository.channel)
	}
	ref, err := credentials.StoreRefFromCredential(repository.credential)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := lease.Bytes()
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	if string(secret) != "sk-new-9876" {
		t.Fatalf("updated secret = %q", secret)
	}
	oldLease, err := store.Get(context.Background(), oldRef)
	if err != nil {
		t.Fatalf("old revision credential was removed: %v", err)
	}
	oldSecret, err := oldLease.Bytes()
	_ = oldLease.Close()
	if err != nil || string(oldSecret) != "sk-old-1234" {
		t.Fatalf("old revision secret = %q, %v", oldSecret, err)
	}
	clear(oldSecret)
}

func TestUpdateChannelCanBindCredentialToLegacyChannel(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 45, 0, 0, time.UTC)
	repository := &channelRepository{channel: domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000021", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "legacy", BaseURL: "https://old.example.test/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	}}
	store := credentials.NewMemoryStore()
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, Clock: fixedChannelClock{now},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			return domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000022", SchemaVersion: 1, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: repository.channel.ID, ExpectedRevision: 1, Name: "legacy", BaseURL: "https://new.example.test/v1",
		APIKey: "sk-bound-1234", Protocol: domain.ProtocolOpenAIChat, Enabled: true,
	})
	if err != nil || result.CredentialID == "" || repository.channel.CredentialID != result.CredentialID {
		t.Fatalf("Update(legacy channel) = %#v, %v; channel=%#v", result, err, repository.channel)
	}
}

type channelRepository struct {
	mu          sync.Mutex
	credential  domain.CredentialRef
	credentials map[string]domain.CredentialRef
	channel     domain.Channel
}

func (repository *channelRepository) CreateCredentialRef(_ context.Context, credential domain.CredentialRef) error {
	if repository.credentials == nil {
		repository.credentials = make(map[string]domain.CredentialRef)
	}
	repository.credentials[credential.ID] = credential
	repository.credential = credential
	return nil
}
func (repository *channelRepository) DeleteCredentialRef(_ context.Context, id string, _ uint64) error {
	delete(repository.credentials, id)
	if repository.credential.ID == id {
		repository.credential = domain.CredentialRef{}
	}
	return nil
}
func (repository *channelRepository) CreateChannel(_ context.Context, channel domain.Channel) error {
	repository.channel = channel
	return nil
}
func (repository *channelRepository) GetChannel(context.Context, string) (domain.Channel, error) {
	return repository.channel, nil
}
func (repository *channelRepository) UpdateChannel(_ context.Context, _ uint64, channel domain.Channel) error {
	repository.channel = channel
	return nil
}

type fixedChannelClock struct{ now time.Time }

func (clock fixedChannelClock) Now() time.Time { return clock.now }
