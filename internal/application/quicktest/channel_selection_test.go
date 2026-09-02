package quicktest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type stubChannelConnectionResolver struct {
	connection ChannelConnection
	channelID  string
}

func (resolver *stubChannelConnectionResolver) Resolve(_ context.Context, channelID string) (ChannelConnection, error) {
	resolver.channelID = channelID
	return resolver.connection, nil
}

func TestRunResolvesSelectedChannelCredentialInsideApplicationCore(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer stored-secret" {
			t.Errorf("authorization header was not populated from the selected channel")
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	secret := []byte("stored-secret")
	resolver := &stubChannelConnectionResolver{connection: ChannelConnection{
		BaseURL: server.URL + "/v1",
		APIKey:  secret,
	}}
	service := New(Dependencies{
		Transport:          server.Client().Transport,
		ChannelConnections: resolver,
	})
	const channelID = "10000000-0000-4000-8000-000000000001"
	result, err := service.Run(context.Background(), Command{
		AddressMode: AddressModeBaseURL,
		URL:         "https://stale.example.test/v1",
		ChannelID:   channelID,
		ModelID:     "model-a",
		TimeoutMS:   2_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.BaseURL != server.URL+"/v1" {
		t.Fatalf("result = %#v", result)
	}
	if resolver.channelID != channelID {
		t.Fatalf("resolved channel = %q, want %q", resolver.channelID, channelID)
	}
	for _, value := range secret {
		if value != 0 {
			t.Fatal("resolved credential bytes were not cleared")
		}
	}
}

type storedChannelRepository struct {
	channel    domain.Channel
	credential domain.CredentialRef
}

func (repository storedChannelRepository) GetChannel(context.Context, string) (domain.Channel, error) {
	return repository.channel, nil
}

func (repository storedChannelRepository) GetCredentialRef(context.Context, string) (domain.CredentialRef, error) {
	return repository.credential, nil
}

func TestStoredChannelConnectionResolverReadsTheBoundCredentialStore(t *testing.T) {
	const channelID = "10000000-0000-4000-8000-000000000001"
	const credentialID = "10000000-0000-4000-8000-000000000002"
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	store := credentials.NewMemoryStore()
	if err := store.Set(context.Background(), storeRef, []byte("stored-secret")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), storeRef) })
	digest := sha256.Sum256([]byte("stored-secret"))
	now := time.Now().UTC()
	repository := storedChannelRepository{
		channel: domain.Channel{
			EntityMeta: domain.EntityMeta{ID: channelID, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
			Name:       "OpenAI 主渠道", BaseURL: "https://api.example.test/v1", Protocol: domain.ProtocolOpenAIChat,
			Enabled: true, CredentialID: credentialID,
		},
		credential: domain.CredentialRef{
			EntityMeta: domain.EntityMeta{ID: credentialID, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
			StoreRef:   storeRef.Value(), Purpose: domain.CredentialChannelAPIKey, MaskedSuffix: "cret",
			Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
		},
	}

	connection, err := NewStoredChannelConnectionResolver(repository, store).Resolve(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(connection.APIKey)
	if connection.BaseURL != repository.channel.BaseURL || string(connection.APIKey) != "stored-secret" {
		t.Fatalf("connection = %#v", connection)
	}
}
