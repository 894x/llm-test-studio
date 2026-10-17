package remotenodes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

type observedKeyStore struct {
	credentials.Store
	set    func(context.Context, credentials.StoreRef, []byte) error
	delete func(context.Context, credentials.StoreRef) error
}

func (store *observedKeyStore) Set(ctx context.Context, ref credentials.StoreRef, secret []byte) error {
	if store.set != nil {
		return store.set(ctx, ref, secret)
	}
	return store.Store.Set(ctx, ref, secret)
}

func (store *observedKeyStore) Delete(ctx context.Context, ref credentials.StoreRef) error {
	if store.delete != nil {
		return store.delete(ctx, ref)
	}
	return store.Store.Delete(ctx, ref)
}

func TestCredentialIntentPrecedesKeyringAndTracksLostAcknowledgment(t *testing.T) {
	fixture := newNodeFixture(t)
	ctx := context.Background()
	keyring := &observedKeyStore{Store: fixture.keys}
	keyring.set = func(ctx context.Context, ref credentials.StoreRef, secret []byte) error {
		document, err := fixture.store.readBytes()
		if err != nil {
			t.Fatal(err)
		}
		config, err := decodeConfiguration(document)
		intentRecorded := slices.Contains(config.CredentialRefs, ref.Value()) && len(config.Nodes) == 0
		if err != nil || !intentRecorded {
			t.Fatal("keyring write preceded durable non-secret intent")
		}
		if err := fixture.keys.Set(ctx, ref, secret); err != nil {
			t.Fatal(err)
		}
		return credentials.ErrUnavailable // Keyring committed, caller lost its acknowledgment.
	}
	fixture.service.credentials = keyring
	if _, err := fixture.service.Save(ctx, passwordCommand(t)); !errors.Is(err, ErrCredential) {
		t.Fatalf("lost keyring acknowledgment: %v", err)
	}
	config, err := fixture.store.Read(ctx)
	trackedOrphan := len(config.Nodes) == 0 && len(config.CredentialRefs) == 1
	if err != nil || !trackedOrphan {
		t.Fatalf("orphan intent not recoverable: %v", err)
	}
	ref, _ := credentials.ParseStoreRef(config.CredentialRefs[0])
	lease, err := fixture.keys.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	if err := fixture.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.keys.Get(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("tracked orphan was not reclaimed")
	}
}

func TestFailedNodeCommitPreservesPreviousCredentialAndTracksNewKey(t *testing.T) {
	fixture := newNodeFixture(t)
	ctx := context.Background()
	node := fixture.save(t, passwordCommand(t))
	write := fixture.store.writeFile
	writes := 0
	fixture.store.writeFile = func(ctx context.Context, path string, document []byte) error {
		writes++
		if writes == 2 {
			return os.ErrPermission // Intent saved, final node reference replacement failed.
		}
		return write(ctx, path, document)
	}
	command := editCommand(node)
	command.Secrets.Password = temporarySecret(t, "new-fixture-key")
	if _, err := fixture.service.Save(ctx, command); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	fixture.store.writeFile = write
	config, err := fixture.store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if config.Nodes[0].Authentication != node.Authentication || len(config.CredentialRefs) != 2 {
		t.Fatal("failed edit lost old binding or uncommitted key registry")
	}
	if err := fixture.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	ref, _ := credentials.ParseStoreRef(node.Authentication.PasswordRef)
	lease, err := fixture.keys.Get(ctx, ref)
	if err != nil {
		t.Fatal("GC deleted the still-referenced key")
	}
	_ = lease.Close()
	config, err = fixture.store.Read(ctx)
	if err != nil || len(config.CredentialRefs) != 1 {
		t.Fatalf("orphan cleanup: %v", err)
	}
}

func TestUnconfirmedIntentNeverWritesKeyring(t *testing.T) {
	fixture := newNodeFixture(t)
	sets := 0
	fixture.service.credentials = &observedKeyStore{Store: fixture.keys,
		set: func(context.Context, credentials.StoreRef, []byte) error { sets++; return nil }}
	fixture.store.writeFile = func(ctx context.Context, path string, document []byte) error {
		if err := fileconfig.WriteAtomically(ctx, path, document); err != nil {
			return err
		}
		return os.ErrPermission // Visible data alone is not a durable commit acknowledgment.
	}
	if _, err := fixture.service.Save(context.Background(), passwordCommand(t)); !errors.Is(err, ErrCommitUnconfirmed) {
		t.Fatalf("unconfirmed intent: %v", err)
	}
	if sets != 0 {
		t.Fatal("secret written without confirmed durable intent")
	}
}

func TestLostFileAcknowledgmentRepeatsSameReplacement(t *testing.T) {
	fixture := newNodeFixture(t)
	write := fixture.store.writeFile
	writes := 0
	var uncertain []byte
	fixture.store.writeFile = func(ctx context.Context, path string, document []byte) error {
		writes++
		if err := write(ctx, path, document); err != nil {
			return err
		}
		if writes == 2 {
			uncertain = slices.Clone(document)
			return os.ErrPermission
		}
		if writes == 3 && string(uncertain) != string(document) {
			t.Fatal("confirmation changed the ambiguous document")
		}
		return nil
	}
	fixture.save(t, passwordCommand(t))
	if writes != 3 {
		t.Fatalf("expected intent, ambiguous commit, confirmed commit; writes=%d", writes)
	}
}

func TestCleanupRequiresDurableReferenceSetAndRetriesMissingKeys(t *testing.T) {
	fixture := newNodeFixture(t)
	ctx := context.Background()
	node := fixture.save(t, passwordCommand(t))
	if err := fixture.service.Remove(ctx, node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	deletes := 0
	fixture.service.credentials = &observedKeyStore{Store: fixture.keys,
		delete: func(ctx context.Context, ref credentials.StoreRef) error {
			deletes++
			return fixture.keys.Delete(ctx, ref)
		}}
	write := fixture.store.writeFile
	fixture.store.writeFile = func(context.Context, string, []byte) error { return os.ErrPermission }
	if err := fixture.service.Cleanup(ctx); err == nil || deletes != 0 {
		t.Fatal("GC deleted before confirming reference durability")
	}
	writes := 0
	fixture.store.writeFile = func(ctx context.Context, path string, document []byte) error {
		writes++
		if writes == 2 {
			return os.ErrPermission // Key deleted, registry removal did not commit.
		}
		return write(ctx, path, document)
	}
	if err := fixture.service.Cleanup(ctx); err == nil || deletes != 1 {
		t.Fatal("expected recoverable registry removal failure")
	}
	fixture.store.writeFile = write
	if err := fixture.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := fixture.store.Read(ctx)
	cleaned := len(config.CredentialRefs) == 0 && deletes == 2
	if err != nil || !cleaned {
		t.Fatalf("retry did not retire already-deleted key: %v", err)
	}
}

func TestUnsupportedNodeDocumentsRemainUnchanged(t *testing.T) {
	base, _ := json.Marshal(emptyConfiguration())
	valid := string(base)
	documents := map[string]string{
		"unknown format": strings.Replace(
			valid,
			`"format":1`,
			`"format":2`,
			1,
		),
		"missing format": strings.Replace(
			valid,
			`"format":1,`,
			"",
			1,
		),
		"duplicate field": strings.Replace(
			valid,
			`"format":1`,
			`"format":1,"format":1`,
			1,
		),
		"mis-cased field": strings.Replace(
			valid,
			`"format":1`,
			`"Format":1`,
			1,
		),
		"unknown field": strings.Replace(
			valid,
			`"format":1`,
			`"format":1,"password":"secret"`,
			1,
		),
		"null collection": strings.Replace(
			valid,
			`"nodes":[]`,
			`"nodes":null`,
			1,
		),
		"missing collection": strings.Replace(
			valid,
			`"nodes":[],`,
			"",
			1,
		),
		"oversized": strings.Repeat(" ", MaxFileBytes+1),
	}
	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			fixture := newNodeFixture(t)
			if err := os.WriteFile(fixture.store.path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.service.Snapshot(context.Background()); err == nil {
				t.Fatal("unsupported document defaulted or accepted")
			}
			if _, err := fixture.service.Save(context.Background(), passwordCommand(t)); err == nil {
				t.Fatal("unsupported document overwritten")
			}
			after, err := os.ReadFile(fixture.store.path)
			if err != nil || string(after) != document {
				t.Fatal("rejected document changed")
			}
		})
	}
}

func TestFileStoreRequiresStableExistingRoot(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing", "nodes.json")
	if _, err := NewFileStore(missing); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing root was accepted")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("constructor created an unconfirmed directory")
	}
	stable := filepath.Join(root, "stable")
	if err := os.Mkdir(stable, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(filepath.Join(stable, "nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stable, filepath.Join(root, "previous")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stable, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced root was silently reused")
	}
}
