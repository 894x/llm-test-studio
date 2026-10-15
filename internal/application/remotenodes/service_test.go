package remotenodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type nodeFixture struct {
	service *Service
	store   *FileStore
	keys    credentials.Store
	rawKeys *credentials.MemoryStore
	now     time.Time
}

func newNodeFixture(t *testing.T) *nodeFixture {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := credentials.NewMemoryStore()
	keys, err := credentials.NewScopedStore(raw, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &nodeFixture{store: store, keys: keys, rawKeys: raw,
		now: time.Date(
			2026,
			10,
			16,
			0,
			0,
			0,
			0,
			time.UTC,
		)}
	fixture.service, err = New(Dependencies{
		Store: store, Credentials: keys, Now: func() time.Time { return fixture.now },
		CanRemove: func(context.Context, Node) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fixtureID(t *testing.T) string {
	t.Helper()
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

func temporarySecret(t *testing.T, value string) *credentials.Lease {
	t.Helper()
	lease, err := credentials.NewTemporaryLease([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	return lease
}

func passwordCommand(t *testing.T) SaveCommand {
	t.Helper()
	return SaveCommand{
		ID: fixtureID(t), Name: "测试节点", Region: "香港", Host: "runner.example.test", Port: 22,
		User: "root", Enabled: true, Authentication: AuthenticationPassword,
		HostKey: &HostKey{Type: "ssh-ed25519", Fingerprint: "SHA256:" + strings.Repeat("A", 43)},
		Secrets: SecretUpdate{Password: temporarySecret(t, "fixture-password-never-persist")},
	}
}

func editCommand(node Node) SaveCommand {
	return SaveCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Name: node.Name, Region: node.Region,
		Host: node.Host, Port: node.Port, User: node.User, Enabled: node.Enabled,
		Authentication: node.Authentication.Kind, HostKey: node.HostKey,
	}
}

func (fixture *nodeFixture) save(t *testing.T, command SaveCommand) Node {
	t.Helper()
	node, err := fixture.service.Save(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func fixtureIdentity(t *testing.T, token string) runnerinstall.Identity {
	t.Helper()
	digest := sha256.Sum256([]byte(token))
	return runnerinstall.Identity{
		Contract: jobcontract.CurrentContract, NodeID: fixtureID(t), InstallationID: fixtureID(t),
		ListenPort: 44917, TokenSHA256: hex.EncodeToString(digest[:]),
	}
}

func (fixture *nodeFixture) bind(t *testing.T, node Node) Node {
	t.Helper()
	token := strings.Repeat("fixture-node-token", 3)
	bound, err := fixture.service.Bind(context.Background(), BindCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Identity: fixtureIdentity(t, token),
		Token: temporarySecret(t, token),
	})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func readyObservation(node Node, now time.Time) Observation {
	return Observation{CheckedAt: now, Status: "ready", Runner: &jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: node.Binding.Identity.NodeID,
		NodeInstallationID: node.Binding.Identity.InstallationID,
		EngineBuildID:      strings.Repeat("b", 64), RunnerVersion: "fixture-build", OS: "linux", Arch: "amd64",
		Ready: true, Healthy: true,
		Budget: jobcontract.RunnerBudget{MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 2000,
			QueueTimeoutNS: int64(time.Hour), ExecutionTimeoutNS: int64(time.Hour)},
		Capabilities: jobcontract.RunnerCapabilities{JobKinds: []jobcontract.Kind{jobcontract.KindRun},
			Protocols: []string{"openai-chat"}, Features: []string{}},
	}}
}

func TestRemoteDefaultSurvivesDisabledOfflineAndExpiredObservations(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	ctx := context.Background()
	if err := fixture.service.SelectDefault(ctx, 1, jobcontract.ExecutionTarget{
		Kind: jobcontract.TargetRemote, NodeID: node.ID,
	}); err != nil {
		t.Fatal(err)
	}
	observation := readyObservation(node, fixture.now)
	if err := fixture.service.RecordObservation(
		ctx,
		node.ID,
		node.Revision,
		observation,
	); err != nil {
		t.Fatal(err)
	}
	assertSelection := func(status string, eligible bool) {
		t.Helper()
		snapshot, err := fixture.service.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		view := snapshot.Nodes[0]
		if snapshot.Default.Kind != jobcontract.TargetRemote || snapshot.Default.NodeID != node.ID {
			t.Fatal("remote default was replaced")
		}
		if view.Status != status || view.Eligible != eligible {
			t.Fatalf("status=%s eligible=%v", view.Status, view.Eligible)
		}
		if view.Revision != node.Revision || snapshot.Default.Revision != 2 {
			t.Fatal("observation changed configuration or selection revision")
		}
	}
	assertSelection("ready", true)
	fixture.now = fixture.now.Add(ObservationLifetime + time.Nanosecond)
	assertSelection("offline", false)
	observation.CheckedAt, observation.Status = fixture.now, "offline"
	observation.Runner = nil
	if err := fixture.service.RecordObservation(
		ctx,
		node.ID,
		node.Revision,
		observation,
	); err != nil {
		t.Fatal(err)
	}
	assertSelection("offline", false)
	command := editCommand(node)
	command.Enabled = false
	node = fixture.save(t, command)
	assertSelection("disabled", false)
	if err := fixture.service.Remove(ctx, node.ID, node.Revision); !errors.Is(err, ErrReferenced) {
		t.Fatalf("selected default removal: %v", err)
	}
	// Health writes never invalidate a user's independently edited default.
	err := fixture.service.SelectDefault(ctx, 2, jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentServicesRejectStaleEdit(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	otherStore, err := NewFileStore(fixture.store.path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(Dependencies{Store: otherStore, Credentials: fixture.keys, Now: fixture.service.now})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, service := range []*Service{fixture.service, other} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			command := editCommand(node)
			command.Name = fmt.Sprintf("client-%d", index)
			<-start
			_, err := service.Save(context.Background(), command)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflict++
		case errors.Is(err, ErrBusy):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	config, err := fixture.store.Read(context.Background())
	if err != nil || config.Nodes[0].Revision != node.Revision+1 {
		t.Fatalf("winner not durably retained: %v", err)
	}
	if _, err := other.Save(context.Background(), editCommand(node)); !errors.Is(err, ErrConflict) {
		t.Fatal("stale retry was not rejected after the operation lock became available")
	}
}

func TestCredentialsAreScopedAndLeasesSurviveRotationCleanup(t *testing.T) {
	fixture := newNodeFixture(t)
	ctx := context.Background()
	command := passwordCommand(t)
	node := fixture.save(t, command)
	oldRef, _ := credentials.ParseStoreRef(node.Authentication.PasswordRef)
	if oldRef.Purpose() != domain.CredentialSSHPassword {
		t.Fatal("SSH credential used another credential namespace")
	}
	if _, err := fixture.rawKeys.Get(ctx, oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("credential escaped its storage-root scope")
	}
	connection, err := fixture.service.AcquireSSH(ctx, node.ID, node.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	edit := editCommand(node)
	edit.Secrets.Password = temporarySecret(t, "rotated-fixture-password")
	node = fixture.save(t, edit)
	if node.Authentication.PasswordRef == oldRef.Value() {
		t.Fatal("rotation reused a mutable credential reference")
	}
	if err := fixture.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.keys.Get(ctx, oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("unreferenced old key retained")
	}
	value, err := connection.leases[0].Bytes()
	if err != nil || string(value) != "fixture-password-never-persist" {
		t.Fatal("in-flight credential changed after rotation")
	}
	clear(value)
	for _, redacted := range []any{command, command.Secrets, connection} {
		if _, err := json.Marshal(redacted); err == nil {
			t.Fatal("secret-bearing input allowed JSON serialization")
		}
		if strings.Contains(fmt.Sprintf("%+v %#v", redacted, redacted), "fixture-password") {
			t.Fatal("secret-bearing input leaked through formatting")
		}
	}
	document, err := os.ReadFile(fixture.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(document), "fixture-password") {
		t.Fatal("password persisted in node configuration")
	}
	snapshot, err := fixture.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), oldRef.Value()) || strings.Contains(string(encoded), "password_ref") {
		t.Fatal("public snapshot exposed internal credential references")
	}
	_ = connection.Close()
	if _, err := connection.leases[0].Bytes(); !errors.Is(err, credentials.ErrClosed) {
		t.Fatal("closed connection retained credential")
	}
}

func TestBindingsRejectAliasIdentityAndConnectionChanges(t *testing.T) {
	fixture := newNodeFixture(t)
	ctx := context.Background()
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	other := fixture.save(t, passwordCommand(t))
	before, _ := os.ReadFile(fixture.store.path)
	token := temporarySecret(t, strings.Repeat("fixture-node-token", 3))
	_, err := fixture.service.Bind(ctx, BindCommand{ID: other.ID, ExpectedRevision: other.Revision,
		Identity: node.Binding.Identity, Token: token})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("installation alias accepted: %v", err)
	}
	identity := node.Binding.Identity
	identity.InstallationID = fixtureID(t)
	_, err = fixture.service.Bind(ctx, BindCommand{ID: node.ID, ExpectedRevision: node.Revision,
		Identity: identity, Token: token})
	if !errors.Is(err, ErrRePairRequired) {
		t.Fatalf("installation identity changed: %v", err)
	}
	for _, mutate := range []func(*SaveCommand){
		func(command *SaveCommand) { command.Host = "different.example.test" },
		func(command *SaveCommand) { command.Port++ },
		func(command *SaveCommand) { command.User = "different" },
		func(command *SaveCommand) { command.HostKey = nil },
		func(command *SaveCommand) {
			command.HostKey = &HostKey{Type: "ssh-ed25519", Fingerprint: "SHA256:" + strings.Repeat("B", 42) + "A"}
		},
	} {
		command := editCommand(node)
		mutate(&command)
		if _, err := fixture.service.Save(ctx, command); !errors.Is(err, ErrRePairRequired) {
			t.Fatalf("bound connection changed: %v", err)
		}
	}
	after, _ := os.ReadFile(fixture.store.path)
	if string(before) != string(after) {
		t.Fatal("rejected edits mutated persisted data")
	}
	ref, _ := credentials.ParseStoreRef(node.Binding.TokenRef)
	if err := fixture.keys.Replace(ctx, ref, []byte(strings.Repeat("wrong-fixture-token", 3))); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.service.AcquireToken(ctx, node.ID, node.Revision)
	if lease != nil || !errors.Is(err, ErrCredential) {
		t.Fatal("corrupted token was accepted")
	}
}

func TestRemovalRequiresUsageGuardAndDoesNotDeleteCredentialsEarly(t *testing.T) {
	fixture := newNodeFixture(t)
	ctx := context.Background()
	node := fixture.save(t, passwordCommand(t))
	fixture.service.canRemove = nil
	if err := fixture.service.Remove(ctx, node.ID, node.Revision); !errors.Is(err, ErrUsageUnknown) {
		t.Fatal(err)
	}
	fixture.service.canRemove = func(context.Context, Node) error { return ErrReferenced }
	if err := fixture.service.Remove(ctx, node.ID, node.Revision); !errors.Is(err, ErrReferenced) {
		t.Fatal(err)
	}
	fixture.service.canRemove = func(context.Context, Node) error { return nil }
	if err := fixture.service.Remove(ctx, node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	ref, _ := credentials.ParseStoreRef(node.Authentication.PasswordRef)
	lease, err := fixture.keys.Get(ctx, ref)
	if err != nil {
		t.Fatal("key deleted before durable GC confirmation")
	}
	_ = lease.Close()
	if err := fixture.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.keys.Get(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("removed node key not collected")
	}
}
