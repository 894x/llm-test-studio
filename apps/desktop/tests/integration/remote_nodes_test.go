package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bootstrap "github.com/894x/llm-test-studio/apps/desktop/internal/bootstrap"
	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func TestDesktopRemoteNodeBindingsKeepPinsAndCredentialsInGo(t *testing.T) {
	root := t.TempDir()
	pin := remotenodes.HostKey{Type: "ssh-ed25519", Fingerprint: "SHA256:" + strings.Repeat("A", 43)}
	services, closeManager, err := bootstrap.NewProductionRemoteNodes(
		t.Context(),
		bootstrap.ProductionRemoteNodeDependencies{Directory: root, Credentials: credentials.NewMemoryStore(),
			Usage: unusedRemoteNodeUsage{err: remotenodes.ErrUsageUnknown},
			Options: bootstrap.Options{AppVersion: "v-test", RunnerSource: &unusedRemoteArtifactSource{},
				RemoteConnector: func(
					_ context.Context,
					endpoint remotessh.Endpoint,
					_ remotessh.Authentication,
				) (remotenodes.RemoteSession, error) {
					if endpoint.Fingerprint == "" {
						return nil, &remotessh.HostKeyError{KeyType: pin.Type, Fingerprint: pin.Fingerprint}
					}
					return nil, remotessh.ErrConnection
				}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	app := bridge.New(func(context.Context) (bridge.Dependencies, error) {
		return bridge.Dependencies{RemoteNodes: services.Manager, Close: closeManager}, nil
	})
	bridge.Startup(app, t.Context())
	t.Cleanup(func() { _ = bridge.Shutdown(app) })
	meta, _ := domain.NewEntityMeta(time.Now().UTC())
	draft := bridge.RemoteNodeDraft{ID: meta.ID, Name: "shared", Region: "test", Host: "runner.example.test",
		Port: 22, User: "root", Enabled: true, Authentication: remotenodes.AuthenticationPassword,
		Password: "binding-private-password"}
	if err := app.SaveRemoteNode(draft); err != nil {
		t.Fatal(err)
	}
	if err := services.Manager.Check(t.Context(), draft.ID, 1); !errors.Is(err, remotessh.ErrHostUnverified) {
		t.Fatal("first check did not stop before authentication for host confirmation")
	}
	snapshot, err := app.GetRemoteNodes()
	if err != nil || len(snapshot.Nodes) != 1 {
		t.Fatal("remote node was not projected")
	}
	node := snapshot.Nodes[0]
	if node.HostKey != nil || node.ProposedHostKey == nil || *node.ProposedHostKey != pin {
		t.Fatal("saving a connection silently confirmed its host key")
	}
	document, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	privateFields := []string{draft.Password, "password_ref", "token_ref", "temporary_directory", "artifact_sha256"}
	for _, private := range privateFields {
		if strings.Contains(string(document), private) {
			t.Fatal("remote presentation leaked private configuration")
		}
	}
	configuration, err := os.ReadFile(filepath.Join(root, "remote-nodes", "nodes.json"))
	if err != nil || strings.Contains(string(configuration), draft.Password) {
		t.Fatal("remote node save wrote a password to disk configuration")
	}
	if strings.Contains(fmt.Sprintf("%#v", draft), draft.Password) {
		t.Fatal("remote draft formatting leaked a password")
	}
	if err := app.ConfirmRemoteNodeHostKey(bridge.RemoteNodeHostKeyConfirmation{
		ID: draft.ID, ExpectedRevision: 1, Key: pin,
	}); err != nil {
		t.Fatal(err)
	}
	_ = services.Manager.Check(t.Context(), draft.ID, 2)
	draft.ExpectedRevision, draft.Password, draft.Name = 2, "", "renamed"
	if err := app.SaveRemoteNode(draft); err != nil {
		t.Fatal(err)
	}
	_ = services.Manager.Check(t.Context(), draft.ID, 3)
	snapshot, err = app.GetRemoteNodes()
	if err != nil || snapshot.Nodes[0].HostKey == nil || *snapshot.Nodes[0].HostKey != pin ||
		!snapshot.Nodes[0].HasAuthentication {
		t.Fatal("editing metadata replaced the fixed host key or saved authentication")
	}
	for _, update := range []bridge.RemoteNodeDraft{
		{ID: draft.ID, ExpectedRevision: 3, Password: strings.Repeat("p", 8193)},
		{ID: draft.ID, ExpectedRevision: 3, PrivateKey: strings.Repeat("k", (1<<20)+1)},
		{ID: draft.ID, ExpectedRevision: 3, Passphrase: strings.Repeat("p", 8193)},
	} {
		if err := app.SaveRemoteNode(update); err == nil || err.Error() != bridge.DesktopCodeRemoteNodeInvalid {
			t.Fatal("oversized remote credential crossed the binding")
		}
	}
	snapshot, err = app.GetRemoteNodes()
	if err != nil || snapshot.Nodes[0].Revision != 3 || snapshot.Nodes[0].Name != "renamed" {
		t.Fatal("invalid credential submission changed the saved node")
	}
	if err := app.SelectDefaultExecution(bridge.DefaultExecutionCommand{ExpectedRevision: snapshot.DefaultRevision,
		Location: jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: draft.ID}}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = app.GetRemoteNodes()
	if snapshot.Default.ConfigurationID != draft.ID || snapshot.Default.Kind != jobcontract.TargetRemote {
		t.Fatal("default selection confused configured node identity with runner identity")
	}
	if err := app.RemoveRemoteNode(bridge.RemoteNodeAction{ID: draft.ID, ExpectedRevision: 3}); err == nil ||
		err.Error() != bridge.DesktopCodeRemoteNodeReferenced {
		t.Fatal("default remote node removal was allowed")
	}
	if err := app.SelectDefaultExecution(bridge.DefaultExecutionCommand{ExpectedRevision: snapshot.DefaultRevision,
		Location: jobs.Location{Kind: jobcontract.TargetLocal}}); err != nil {
		t.Fatal(err)
	}
	if err := app.RemoveRemoteNode(bridge.RemoteNodeAction{ID: draft.ID, ExpectedRevision: 3}); err == nil ||
		err.Error() != bridge.DesktopCodeRemoteNodeUsageUnknown {
		t.Fatal("unknown usage bypassed the removal guard")
	}
	if err := app.RefreshRemoteNode(bridge.RemoteNodeAction{ID: "bad", ExpectedRevision: 1}); err == nil ||
		err.Error() != bridge.DesktopCodeRemoteNodeInvalid {
		t.Fatal("refresh exposed an invalid internal action")
	}
	if err := bridge.Shutdown(app); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetRemoteNodes(); err == nil || err.Error() != bridge.DesktopCodeStopped {
		t.Fatal("remote node queries survived desktop shutdown")
	}
}
