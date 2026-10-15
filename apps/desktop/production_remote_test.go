package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

type unusedRemoteArtifactSource struct{ calls atomic.Uint32 }

func (source *unusedRemoteArtifactSource) Fetch(context.Context, string, string) (io.ReadCloser, error) {
	source.calls.Add(1)
	return nil, runnerartifacts.ErrUnavailable
}

func TestProductionRemoteNodesPersistDefaultAndScopeCredentialsAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, base := t.TempDir(), credentials.NewMemoryStore()
	source := &unusedRemoteArtifactSource{}
	options := productionOptions{appVersion: "v-test", runnerSource: source,
		remoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
			return nil, remotessh.ErrConnection
		}}
	services, closeManager, err := newProductionRemoteNodes(
		ctx,
		root,
		base,
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := services.manager
	t.Cleanup(func() { _ = closeManager() })
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	password, err := credentials.NewTemporaryLease([]byte("test-secret-never-in-node-file"))
	if err != nil {
		t.Fatal(err)
	}
	defer password.Close()
	node, err := manager.Save(ctx, remotenodes.SaveCommand{
		ID: meta.ID, Name: "test node", Host: "runner.example.test", Port: 22, User: "root", Enabled: true,
		Authentication: remotenodes.AuthenticationPassword,
		HostKey:        &remotenodes.HostKey{Type: "ssh-ed25519", Fingerprint: "SHA256:" + strings.Repeat("A", 43)},
		Secrets:        remotenodes.SecretUpdate{Password: password},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Check(ctx, node.ID, node.Revision); !errors.Is(err, remotessh.ErrConnection) {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectDefault(ctx, snapshot.Default.Revision, jobcontract.ExecutionTarget{
		Kind: jobcontract.TargetRemote, NodeID: node.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := closeManager(); err != nil {
		t.Fatal(err)
	}
	if err := closeManager(); err != nil {
		t.Fatal("production node closer is not idempotent")
	}
	services, closeReopened, err := newProductionRemoteNodes(
		ctx,
		root,
		base,
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager = services.manager
	t.Cleanup(func() { _ = closeReopened() })
	snapshot, err = manager.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained := snapshot.Default.NodeID == node.ID && len(snapshot.Nodes) == 1 &&
		snapshot.Nodes[0].HasAuthentication && !snapshot.Nodes[0].Eligible
	if !retained || source.calls.Load() != 0 {
		t.Fatal("offline default, credentials or lazy artifact behavior changed on restart")
	}
	directory := filepath.Join(root, "remote-nodes")
	store, err := remotenodes.NewFileStore(filepath.Join(directory, "nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := credentials.ParseStoreRef(config.Nodes[0].Authentication.PasswordRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Get(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("node password escaped its root scope")
	}
	scope, err := credentialScopeForRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := credentials.NewScopedStore(base, scope)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := keys.Get(ctx, ref)
	if err != nil {
		t.Fatal("saved node password was not available in the reopened root scope")
	}
	_ = secret.Close()
	document, err := os.ReadFile(filepath.Join(directory, "nodes.json"))
	if err != nil || bytes.Contains(document, []byte("test-secret-never-in-node-file")) {
		t.Fatal("node metadata contains a secret")
	}
}

func TestProductionRemoteNodesRejectUnsupportedConfigurationWithoutMutation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "remote-nodes")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "nodes.json")
	document := []byte(`{"format":999}`)
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	_, closeManager, err := newProductionRemoteNodes(
		context.Background(),
		root,
		credentials.NewMemoryStore(),
		productionOptions{appVersion: "v-test"},
	)
	if !errors.Is(err, remotenodes.ErrUnsupported) || closeManager != nil {
		t.Fatal("unsupported node configuration did not stop bootstrap")
	}
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(retained, document) {
		t.Fatal("unsupported configuration was changed")
	}
}
