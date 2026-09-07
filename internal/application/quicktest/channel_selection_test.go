package quicktest

import (
	"context"
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

func TestPerformanceResolvesSelectedChannelCredentialInsideApplicationCore(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer stored-secret" {
			t.Errorf("authorization header was not populated from the selected channel")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
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
	result, err := service.RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL,
		URL:         "https://stale.example.test/v1",
		ChannelID:   channelID,
		ModelID:     "model-a",
		TimeoutMS:   2_000, RequestCount: 1, Concurrency: 1, InputTokens: 2, OutputTokens: 2,
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
	channel domain.Channel
}

func (repository storedChannelRepository) GetChannel(context.Context, string) (domain.Channel, error) {
	return repository.channel, nil
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
	now := time.Now().UTC()
	repository := storedChannelRepository{
		channel: domain.Channel{
			EntityMeta: domain.EntityMeta{ID: channelID, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
			Name:       "OpenAI 主渠道", BaseURL: "https://api.example.test/v1", Protocol: domain.ProtocolOpenAIChat,
			Enabled: true, CredentialID: credentialID,
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
