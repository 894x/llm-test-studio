package channelconfig_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
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
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.ChannelID != repository.channel.ID || repository.channel.BaseURL != "https://api.example.test/v1" || repository.channel.CredentialID != result.CredentialID {
		t.Fatalf("stored channel = %#v result = %#v", repository.channel, result)
	}
	ref, _ := credentials.NewStoreRef(domain.CredentialChannelAPIKey, result.CredentialID)
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

func TestCreateChannelRetainsCredentialWhenCatalogCommitCannotBeDetermined(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 15, 0, 0, time.UTC)
	writeFailure := errors.New("injected ambiguous catalog write failure")
	readFailure := errors.New("injected catalog verification failure")
	repository := &channelRepository{createErr: writeFailure, getErr: readFailure}
	store := credentials.NewMemoryStore()
	credentialID := "70000000-0000-4000-8000-000000000005"
	ids := []string{credentialID, "70000000-0000-4000-8000-000000000006"}
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
	if _, err := service.Create(context.Background(), channelconfig.CreateCommand{
		Name: "ambiguous", BaseURL: "https://api.example.test/v1", APIKey: "sk-retain-1234",
		Enabled: true,
	}); !errors.Is(err, writeFailure) || !errors.Is(err, readFailure) {
		t.Fatalf("Create() error = %v, want ambiguous write and verification causes", err)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Test(context.Background(), ref); err != nil {
		t.Fatalf("credential was deleted after an indeterminate catalog commit: %v", err)
	}
}

func TestCreateChannelCleansCredentialWhenKeyringSetMayHaveCommitted(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 20, 0, 0, time.UTC)
	setFailure := errors.New("injected uncertain keyring set failure")
	repository := &channelRepository{}
	store := &uncertainSetCredentialStore{Store: credentials.NewMemoryStore(), setErr: setFailure}
	credentialID := "70000000-0000-4000-8000-000000000007"
	ids := []string{credentialID, "70000000-0000-4000-8000-000000000008"}
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

	_, err = service.Create(context.Background(), channelconfig.CreateCommand{
		Name: "uncertain", BaseURL: "https://api.example.test/v1", APIKey: "sk-uncertain-1234",
		Enabled: true,
	})
	if !errors.Is(err, setFailure) {
		t.Fatalf("Create() error = %v, want uncertain keyring cause", err)
	}
	ref, refErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialID)
	if refErr != nil {
		t.Fatal(refErr)
	}
	if err := store.Test(context.Background(), ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("uncertain keyring write left an unreachable credential: %v", err)
	}
	if repository.channel.ID != "" {
		t.Fatalf("channel was persisted after credential store failure: %#v", repository.channel)
	}
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
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldCredentialID := repository.channel.CredentialID
	oldRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, oldCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: created.ChannelID, ExpectedRevision: created.ChannelRevision,
		Name: "primary updated", BaseURL: "https://new.example.test/v1", APIKey: "sk-new-9876",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ChannelRevision != 2 || updated.CredentialRevision != 1 || updated.CredentialID == oldCredentialID || repository.channel.BaseURL != "https://new.example.test/v1" {
		t.Fatalf("updated result = %#v channel = %#v", updated, repository.channel)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, updated.CredentialID)
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
	if err := store.Test(context.Background(), oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("old credential test error = %v, want ErrNotFound", err)
	}
}

func TestUpdateChannelCanBindCredentialToLegacyChannel(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 45, 0, 0, time.UTC)
	repository := &channelRepository{channel: domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000021", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "legacy", BaseURL: "https://old.example.test/v1", Enabled: true,
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
		APIKey: "sk-bound-1234", Enabled: true,
	})
	if err != nil || result.CredentialID == "" || repository.channel.CredentialID != result.CredentialID {
		t.Fatalf("Update(legacy channel) = %#v, %v; channel=%#v", result, err, repository.channel)
	}
}

func TestUpdateChannelCleansReplacementCredentialWhenKeyringSetMayHaveCommitted(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 46, 0, 0, time.UTC)
	setFailure := errors.New("injected uncertain replacement keyring failure")
	oldCredentialID := "70000000-0000-4000-8000-000000000026"
	newCredentialID := "70000000-0000-4000-8000-000000000027"
	repository := &channelRepository{channel: domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000028", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "primary", BaseURL: "https://old.example.test/v1", Enabled: true,
		CredentialID: oldCredentialID,
	}}
	baseStore := credentials.NewMemoryStore()
	oldRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, oldCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Set(context.Background(), oldRef, []byte("sk-old-1234")); err != nil {
		t.Fatal(err)
	}
	store := &uncertainSetCredentialStore{Store: baseStore, setErr: setFailure}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, Clock: fixedChannelClock{now},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			return domain.EntityMeta{ID: newCredentialID, SchemaVersion: 1, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: repository.channel.ID, ExpectedRevision: repository.channel.Revision,
		Name: "primary", BaseURL: "https://new.example.test/v1", APIKey: "sk-new-1234",
		Enabled: true,
	})
	if !errors.Is(err, setFailure) {
		t.Fatalf("Update() error = %v, want uncertain keyring cause", err)
	}
	newRef, refErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, newCredentialID)
	if refErr != nil {
		t.Fatal(refErr)
	}
	if err := store.Test(context.Background(), newRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("uncertain replacement left an unreachable credential: %v", err)
	}
	if repository.channel.CredentialID != oldCredentialID || repository.channel.Revision != 1 {
		t.Fatalf("channel changed after replacement credential failure: %#v", repository.channel)
	}
	if err := store.Test(context.Background(), oldRef); err != nil {
		t.Fatalf("old referenced credential was removed: %v", err)
	}
}

func TestCreateChannelReturnsUncertainSetAndCleanupFailures(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 46, 30, 0, time.UTC)
	setFailure := errors.New("injected uncertain keyring set failure")
	cleanupFailure := errors.New("injected uncertain keyring cleanup failure")
	credentialID := "70000000-0000-4000-8000-000000000029"
	store := &uncertainSetCredentialStore{
		Store: credentials.NewMemoryStore(), setErr: setFailure, deleteErr: cleanupFailure,
	}
	ids := []string{credentialID, "70000000-0000-4000-8000-000000000030"}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: &channelRepository{}, Credentials: store, Clock: fixedChannelClock{now},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			id := ids[0]
			ids = ids[1:]
			return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Create(context.Background(), channelconfig.CreateCommand{
		Name: "uncertain", BaseURL: "https://api.example.test/v1", APIKey: "sk-uncertain-1234",
		Enabled: true,
	})
	if !errors.Is(err, setFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("Create() error = %v, want set and cleanup causes", err)
	}
}

func TestUpdateChannelPreservesRepositoryReadFailure(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 47, 0, 0, time.UTC)
	readFailure := errors.New("read channels.json: access denied")
	repository := &channelRepository{
		channel: domain.Channel{
			EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000041", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
			Name:       "primary", BaseURL: "https://old.example.test/v1", Enabled: true,
			CredentialID: "70000000-0000-4000-8000-000000000042",
		},
		getErr: readFailure,
	}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: credentials.NewMemoryStore(), Clock: fixedChannelClock{now},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: repository.channel.ID, ExpectedRevision: 1, Name: "primary",
		BaseURL: "https://new.example.test/v1", APIKey: "sk-new-1234",
		Enabled: true,
	})
	if !errors.Is(err, readFailure) {
		t.Fatalf("Update() error = %v, want wrapped repository read failure", err)
	}
}

func TestUpdateChannelMapsStaleRevisionToCatalogConflict(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 48, 0, 0, time.UTC)
	repository := &channelRepository{channel: domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000043", SchemaVersion: 1, Revision: 2, CreatedAt: now, UpdatedAt: now},
		Name:       "primary", BaseURL: "https://old.example.test/v1", Enabled: true,
		CredentialID: "70000000-0000-4000-8000-000000000044",
	}}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: credentials.NewMemoryStore(), Clock: fixedChannelClock{now},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: repository.channel.ID, ExpectedRevision: 1, Name: "primary",
		BaseURL: "https://new.example.test/v1", APIKey: "sk-new-1234",
		Enabled: true,
	})
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("Update() error = %v, want %v", err, catalog.ErrConflict)
	}
}

func TestUpdateChannelMapsInvalidAddressToCatalogInvalid(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 48, 30, 0, time.UTC)
	repository := &channelRepository{channel: domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000054", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "primary", BaseURL: "https://old.example.test/v1", Enabled: true,
		CredentialID: "70000000-0000-4000-8000-000000000055",
	}}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: credentials.NewMemoryStore(), Clock: fixedChannelClock{now},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: repository.channel.ID, ExpectedRevision: 1, Name: "primary",
		BaseURL: "invalid-url", APIKey: "sk-new-1234",
		Enabled: true,
	})
	if !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("Update() error = %v, want %v", err, catalog.ErrInvalid)
	}
}

func TestDeleteChannelPreservesRepositoryReadFailure(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 49, 0, 0, time.UTC)
	readFailure := errors.New("decode channels.json: truncated document")
	repository := &channelRepository{
		channel: domain.Channel{
			EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000045", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
			Name:       "primary", BaseURL: "https://old.example.test/v1", Enabled: true,
			CredentialID: "70000000-0000-4000-8000-000000000046",
		},
		getErr: readFailure,
	}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: credentials.NewMemoryStore(), Clock: fixedChannelClock{now},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = service.Delete(context.Background(), repository.channel.ID, 1)
	if !errors.Is(err, readFailure) {
		t.Fatalf("Delete() error = %v, want wrapped repository read failure", err)
	}
}

func TestDeleteChannelMapsMissingAndStaleChannelsToCatalogErrors(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 50, 0, 0, time.UTC)
	channel := domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000047", SchemaVersion: 1, Revision: 2, CreatedAt: now, UpdatedAt: now},
		Name:       "primary", BaseURL: "https://old.example.test/v1", Enabled: true,
		CredentialID: "70000000-0000-4000-8000-000000000048",
	}
	tests := []struct {
		name       string
		repository *channelRepository
		want       error
	}{
		{name: "missing", repository: &channelRepository{channel: channel, getErr: catalog.ErrNotFound}, want: catalog.ErrNotFound},
		{name: "stale", repository: &channelRepository{channel: channel}, want: catalog.ErrConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := channelconfig.New(channelconfig.Dependencies{
				Repository: test.repository, Credentials: credentials.NewMemoryStore(), Clock: fixedChannelClock{now},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = service.Delete(context.Background(), channel.ID, 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("Delete() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUpdateChannelRetainsOldCredentialWhilePlanBindingReferencesIt(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 50, 0, 0, time.UTC)
	repository := &channelRepository{referencedCredentials: map[string]bool{}}
	store := credentials.NewMemoryStore()
	ids := []string{
		"70000000-0000-4000-8000-000000000023",
		"70000000-0000-4000-8000-000000000024",
		"70000000-0000-4000-8000-000000000025",
	}
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
		Name: "pinned", BaseURL: "https://old.example.test/v1", APIKey: "sk-old-pinned",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldID := repository.channel.CredentialID
	repository.referencedCredentials[oldID] = true
	oldRef, _ := credentials.NewStoreRef(domain.CredentialChannelAPIKey, oldID)

	if _, err := service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: created.ChannelID, ExpectedRevision: created.ChannelRevision,
		Name: "pinned", BaseURL: "https://new.example.test/v1", APIKey: "sk-new-current",
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := store.Get(context.Background(), oldRef)
	if err != nil {
		t.Fatalf("old credential referenced by Plan was deleted: %v", err)
	}
	secret, err := lease.Bytes()
	_ = lease.Close()
	if err != nil || string(secret) != "sk-old-pinned" {
		t.Fatalf("retained old credential = %q, %v", secret, err)
	}
	clear(secret)
}

func TestUpdateReportsObsoleteCredentialCleanupFailureAfterCommit(t *testing.T) {
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	repository := &channelRepository{}
	cleanupFailure := errors.New("injected keyring delete failure")
	store := &deleteFailingCredentialStore{Store: credentials.NewMemoryStore(), err: cleanupFailure}
	ids := []string{
		"70000000-0000-4000-8000-000000000031",
		"70000000-0000-4000-8000-000000000032",
		"70000000-0000-4000-8000-000000000033",
	}
	var reported error
	queue := credentials.NewMemoryCleanupQueue()
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, Clock: fixedChannelClock{now},
		CleanupQueue: queue,
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			id := ids[0]
			ids = ids[1:]
			return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
		},
		ReportCleanupFailure: func(err error) { reported = err },
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), channelconfig.CreateCommand{
		Name: "primary", BaseURL: "https://old.example.test/v1", APIKey: "sk-old-1234",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.failID = repository.channel.CredentialID

	updated, err := service.Update(context.Background(), channelconfig.UpdateCommand{
		ID: created.ChannelID, ExpectedRevision: created.ChannelRevision,
		Name: "primary", BaseURL: "https://new.example.test/v1", APIKey: "sk-new-1234",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v; committed cleanup failure must be diagnostic-only", err)
	}
	if updated.ChannelRevision != 2 || repository.channel.BaseURL != "https://new.example.test/v1" {
		t.Fatalf("updated result = %#v channel = %#v", updated, repository.channel)
	}
	if !errors.Is(reported, cleanupFailure) {
		t.Fatalf("reported error = %v, want wrapped cleanup failure", reported)
	}
	pending, err := queue.List(context.Background())
	if err != nil || len(pending) != 2 || pending[0] != store.failID || pending[1] != updated.CredentialID {
		t.Fatalf("credential registry after failure = %#v, %v; want obsolete and active ids", pending, err)
	}
	store.err = nil
	if err := service.RetryPendingCredentialCleanup(context.Background()); err != nil {
		t.Fatalf("RetryPendingCredentialCleanup() error = %v", err)
	}
	if pending, err := queue.List(context.Background()); err != nil || len(pending) != 1 || pending[0] != updated.CredentialID {
		t.Fatalf("credential registry after retry = %#v, %v; want active id only", pending, err)
	}
}

func TestCleanupCredentialIfUnreferencedRemovesObsoleteKeyringEntry(t *testing.T) {
	now := time.Date(2026, 8, 31, 13, 15, 0, 0, time.UTC)
	obsoleteCredentialID := "70000000-0000-4000-8000-000000000051"
	repository := &channelRepository{channel: domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "70000000-0000-4000-8000-000000000052", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "current", BaseURL: "https://api.example.test/v1", Enabled: true,
		CredentialID: "70000000-0000-4000-8000-000000000053",
	}}
	store := credentials.NewMemoryStore()
	obsoleteRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, obsoleteCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), obsoleteRef, []byte("sk-obsolete-plan-key")); err != nil {
		t.Fatal(err)
	}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, Clock: fixedChannelClock{now},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleaner, ok := any(service).(interface{ CleanupCredentialIfUnreferenced(string) })
	if !ok {
		t.Fatal("channel service does not expose guarded cleanup for credentials released by Plan mutations")
	}

	cleaner.CleanupCredentialIfUnreferenced(obsoleteCredentialID)

	if err := store.Test(context.Background(), obsoleteRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("obsolete credential remains after guarded cleanup: %v", err)
	}
}

type channelRepository struct {
	mu                    sync.Mutex
	channel               domain.Channel
	referencedCredentials map[string]bool
	referenceErr          error
	createErr             error
	getErr                error
}

func (repository *channelRepository) CreateChannel(_ context.Context, channel domain.Channel) error {
	repository.channel = channel
	return repository.createErr
}
func (repository *channelRepository) GetChannel(context.Context, string) (domain.Channel, error) {
	return repository.channel, repository.getErr
}
func (repository *channelRepository) UpdateChannel(_ context.Context, _ uint64, channel domain.Channel) error {
	repository.channel = channel
	return nil
}
func (repository *channelRepository) DeleteChannel(_ context.Context, _ string, _ uint64) error {
	repository.channel = domain.Channel{}
	return nil
}
func (repository *channelRepository) IsCredentialReferenced(_ context.Context, id string) (bool, error) {
	if repository.referenceErr != nil {
		return false, repository.referenceErr
	}
	return repository.channel.CredentialID == id || repository.referencedCredentials[id], nil
}
func (repository *channelRepository) WithCredentialUnreferenced(_ context.Context, id string, action func() error) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.referenceErr != nil {
		return false, repository.referenceErr
	}
	if repository.channel.CredentialID == id || repository.referencedCredentials[id] {
		return false, nil
	}
	return true, action()
}

type fixedChannelClock struct{ now time.Time }

func (clock fixedChannelClock) Now() time.Time { return clock.now }

type deleteFailingCredentialStore struct {
	credentials.Store
	failID string
	err    error
}

type uncertainSetCredentialStore struct {
	credentials.Store
	setErr    error
	deleteErr error
}

func (store *uncertainSetCredentialStore) Set(ctx context.Context, ref credentials.StoreRef, secret []byte) error {
	if err := store.Store.Set(ctx, ref, secret); err != nil {
		return err
	}
	return store.setErr
}

func (store *uncertainSetCredentialStore) Delete(ctx context.Context, ref credentials.StoreRef) error {
	if store.deleteErr != nil {
		return store.deleteErr
	}
	return store.Store.Delete(ctx, ref)
}

func (store *deleteFailingCredentialStore) Delete(ctx context.Context, ref credentials.StoreRef) error {
	if ref.ID() == store.failID && store.err != nil {
		return store.err
	}
	return store.Store.Delete(ctx, ref)
}
