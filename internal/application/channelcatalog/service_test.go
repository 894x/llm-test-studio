package channelcatalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

func TestChannelCatalogRoundTripsMappingWithoutPersistingSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	service, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 1, 0, 0, 0, time.UTC)
	channel := validChannel("10000000-0000-4000-8000-000000000001", now)
	channel.CredentialID = "50000000-0000-4000-8000-000000000001"
	mapping := validMapping(
		"20000000-0000-4000-8000-000000000001",
		channel.ID,
		"30000000-0000-4000-8000-000000000001",
		now,
	)
	if err := service.CreateChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateMapping(context.Background(), mapping); err != nil {
		t.Fatal(err)
	}

	gotChannel, err := service.GetChannel(context.Background(), channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotMapping, err := service.GetMapping(context.Background(), mapping.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotChannel, channel) || !reflect.DeepEqual(gotMapping, mapping) {
		t.Fatalf("round trip channel = %#v, mapping = %#v", gotChannel, gotMapping)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"api_key"`, `"secret"`, `"store_ref"`, `"masked_suffix"`, `"fingerprint"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("channels.json contains credential material field %s: %s", forbidden, raw)
		}
	}
	if !strings.Contains(string(raw), `"credential_id"`) || !strings.Contains(string(raw), `"model_mappings"`) {
		t.Fatalf("channels.json omitted safe credential identity or mappings: %s", raw)
	}
}

func TestCreateMappingRejectsDuplicateBinding(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 1, 0, 0, 0, time.UTC)
	channel := validChannel("10000000-0000-4000-8000-000000000001", now)
	if err := service.CreateChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	first := validMapping("20000000-0000-4000-8000-000000000001", channel.ID, "30000000-0000-4000-8000-000000000001", now)
	if err := service.CreateMapping(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	duplicateBinding := validMapping("20000000-0000-4000-8000-000000000002", channel.ID, first.ModelID, now.Add(time.Minute))
	if err := service.CreateMapping(context.Background(), duplicateBinding); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateMapping() duplicate binding error = %v, want %v", err, ErrConflict)
	}
	mappings, err := service.ListMappings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || mappings[0].ID != first.ID {
		t.Fatalf("mappings after rejected duplicate = %#v", mappings)
	}
}

func TestCreateMappingRejectsDuplicateIDInAnotherChannel(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 1, 0, 0, 0, time.UTC)
	firstChannel := validChannel("10000000-0000-4000-8000-000000000001", now)
	secondChannel := validChannel("10000000-0000-4000-8000-000000000002", now.Add(time.Minute))
	if err := service.CreateChannel(context.Background(), firstChannel); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateChannel(context.Background(), secondChannel); err != nil {
		t.Fatal(err)
	}
	mappingID := "20000000-0000-4000-8000-000000000001"
	if err := service.CreateMapping(context.Background(), validMapping(mappingID, secondChannel.ID, "30000000-0000-4000-8000-000000000002", now)); err != nil {
		t.Fatal(err)
	}

	err = service.CreateMapping(context.Background(), validMapping(mappingID, firstChannel.ID, "30000000-0000-4000-8000-000000000001", now.Add(time.Minute)))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateMapping() error = %v, want %v", err, ErrConflict)
	}
}

func TestChannelCatalogRevisionAndMissingGuards(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 1, 0, 0, 0, time.UTC)
	channel := validChannel("10000000-0000-4000-8000-000000000001", now)
	if err := service.CreateChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	conflicting := channel
	conflicting.Revision = 3
	conflicting.UpdatedAt = now.Add(2 * time.Minute)
	if err := service.UpdateChannel(context.Background(), 2, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateChannel() conflict error = %v, want %v", err, ErrConflict)
	}
	if err := service.DeleteChannel(context.Background(), channel.ID, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteChannel() conflict error = %v, want %v", err, ErrConflict)
	}
	missingID := "10000000-0000-4000-8000-000000000099"
	if _, err := service.GetChannel(context.Background(), missingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel() missing error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.GetMapping(context.Background(), "20000000-0000-4000-8000-000000000099"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMapping() missing error = %v, want %v", err, ErrNotFound)
	}
	if err := service.DeleteChannel(context.Background(), missingID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteChannel() missing error = %v, want %v", err, ErrNotFound)
	}
}

func TestChannelCatalogRejectsSecretOrUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "secret field",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"name":`), []byte(`"api_key": "sk-super-secret", "name":`), 1)
			},
		},
		{name: "trailing document", mutate: func(raw []byte) []byte { return append(raw, []byte("{}\n")...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "channels.json")
			service, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, time.September, 4, 1, 0, 0, 0, time.UTC)
			if err := service.CreateChannel(context.Background(), validChannel("10000000-0000-4000-8000-000000000001", now)); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutated := test.mutate(raw)
			if bytes.Equal(mutated, raw) {
				t.Fatal("fixture mutation did not change channels.json")
			}
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ListChannels(context.Background()); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("ListChannels() error = %v, want %v", err, ErrCorrupt)
			}
		})
	}
}

func TestEveryChannelCatalogMutationWaitsForTheCatalogLock(t *testing.T) {
	now := time.Date(2026, time.September, 4, 5, 0, 0, 0, time.UTC)
	newCatalog := func(t *testing.T) (*Service, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "channels.json")
		service, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		return service, path
	}
	seedChannel := func(t *testing.T, service *Service) domain.Channel {
		t.Helper()
		channel := validChannel("10000000-0000-4000-8000-000000000001", now)
		if err := service.CreateChannel(context.Background(), channel); err != nil {
			t.Fatal(err)
		}
		return channel
	}
	seedMapping := func(t *testing.T, service *Service, channel domain.Channel) domain.ChannelModel {
		t.Helper()
		mapping := validMapping(
			"20000000-0000-4000-8000-000000000001",
			channel.ID,
			"30000000-0000-4000-8000-000000000001",
			now,
		)
		if err := service.CreateMapping(context.Background(), mapping); err != nil {
			t.Fatal(err)
		}
		return mapping
	}

	t.Run("create channel", func(t *testing.T) {
		service, path := newCatalog(t)
		channel := validChannel("10000000-0000-4000-8000-000000000001", now)
		assertChannelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.CreateChannel(ctx, channel)
		})
		if _, err := service.GetChannel(context.Background(), channel.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetChannel() after cancelled create error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("update channel", func(t *testing.T) {
		service, path := newCatalog(t)
		channel := seedChannel(t, service)
		updated := channel
		updated.Revision = 2
		updated.UpdatedAt = now.Add(time.Minute)
		updated.Name = "updated channel"
		assertChannelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.UpdateChannel(ctx, 1, updated)
		})
		got, err := service.GetChannel(context.Background(), channel.ID)
		if err != nil || !reflect.DeepEqual(got, channel) {
			t.Fatalf("GetChannel() after cancelled update = %#v, %v; want original", got, err)
		}
	})

	t.Run("delete channel", func(t *testing.T) {
		service, path := newCatalog(t)
		channel := seedChannel(t, service)
		assertChannelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.DeleteChannel(ctx, channel.ID, 1)
		})
		if _, err := service.GetChannel(context.Background(), channel.ID); err != nil {
			t.Fatalf("GetChannel() after cancelled delete: %v", err)
		}
	})

	t.Run("create mapping", func(t *testing.T) {
		service, path := newCatalog(t)
		channel := seedChannel(t, service)
		mapping := validMapping(
			"20000000-0000-4000-8000-000000000001",
			channel.ID,
			"30000000-0000-4000-8000-000000000001",
			now,
		)
		assertChannelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.CreateMapping(ctx, mapping)
		})
		if _, err := service.GetMapping(context.Background(), mapping.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetMapping() after cancelled create error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("update mapping", func(t *testing.T) {
		service, path := newCatalog(t)
		channel := seedChannel(t, service)
		mapping := seedMapping(t, service, channel)
		updated := mapping
		updated.Revision = 2
		updated.UpdatedAt = now.Add(time.Minute)
		updated.UpstreamModelName = "updated-upstream-model"
		assertChannelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.UpdateMapping(ctx, 1, updated)
		})
		got, err := service.GetMapping(context.Background(), mapping.ID)
		if err != nil || !reflect.DeepEqual(got, mapping) {
			t.Fatalf("GetMapping() after cancelled update = %#v, %v; want original", got, err)
		}
	})

	t.Run("delete mapping", func(t *testing.T) {
		service, path := newCatalog(t)
		channel := seedChannel(t, service)
		mapping := seedMapping(t, service, channel)
		assertChannelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.DeleteMapping(ctx, mapping.ID, 1)
		})
		if _, err := service.GetMapping(context.Background(), mapping.ID); err != nil {
			t.Fatalf("GetMapping() after cancelled delete: %v", err)
		}
	})
}

func assertChannelMutationWaitsForLock(t *testing.T, catalogPath string, mutation func(context.Context) error) {
	t.Helper()
	var mutationErr error
	if err := fileconfig.WithExclusiveLock(context.Background(), catalogPath+".lock", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()
		mutationErr = mutation(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(mutationErr, context.DeadlineExceeded) {
		t.Fatalf("mutation while catalog lock held error = %v, want %v", mutationErr, context.DeadlineExceeded)
	}
}

func validChannel(id string, now time.Time) domain.Channel {
	return domain.Channel{
		EntityMeta: domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "Channel " + id,
		BaseURL:    "https://example.test/v1",
		Protocol:   domain.ProtocolOpenAIChat,
		Enabled:    true,
	}
}

func validMapping(id, channelID, modelID string, now time.Time) domain.ChannelModel {
	return domain.ChannelModel{
		EntityMeta:        domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		ChannelID:         channelID,
		ModelID:           modelID,
		UpstreamModelName: "upstream-" + modelID,
	}
}
