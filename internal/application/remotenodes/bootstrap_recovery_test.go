package remotenodes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestManagerRecoversBootstrapIntentAfterProcessKill(t *testing.T) {
	if marker := os.Getenv("LLM_TEST_BOOTSTRAP_KILL_MARKER"); marker != "" {
		runBootstrapKillHelper(t, marker)
		return
	}
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	marker := filepath.Join(t.TempDir(), "remote-create-entered")
	command := exec.Command(os.Args[0], "-test.run=^TestManagerRecoversBootstrapIntentAfterProcessKill$")
	command.Env = append(os.Environ(), "LLM_TEST_BOOTSTRAP_KILL_MARKER="+marker,
		"LLM_TEST_BOOTSTRAP_KILL_CONFIG="+fixture.store.path,
		"TEMP="+filepath.Dir(marker), "TMP="+filepath.Dir(marker), "TMPDIR="+filepath.Dir(marker))
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var directory []byte
waiting:
	for {
		select {
		case <-ticker.C:
			if data, err := os.ReadFile(marker); err == nil && len(data) != 0 {
				directory = data
				break waiting
			}
		case <-deadline.C:
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("child did not enter remote creation: %s", output.String())
		}
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("installation child returned normally instead of being killed")
	}
	reopened, err := NewFileStore(fixture.store.path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := reopened.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	attempt := *config.Nodes[0].Installation
	validIntent := attempt.CleanupPending && attempt.ErrorCode == "" &&
		attempt.TemporaryDirectory == remotessh.BootstrapDirectory(attempt.Request.OperationID)
	if !validIntent || attempt.TemporaryDirectory != string(directory) {
		t.Fatal("process kill lost the original operation or exact bootstrap cleanup path")
	}
	fixture.store = reopened
	fixture.service, err = New(Dependencies{Store: reopened, Credentials: fixture.keys, Now: func() time.Time {
		return fixture.now
	}})
	if err != nil {
		t.Fatal(err)
	}
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	cleaned, installed, provisions := false, false, 0
	remote := &remoteFixture{
		cleanup: func(ctx context.Context, recovered string) error {
			if recovered != attempt.TemporaryDirectory {
				return remotessh.ErrIntegrity
			}
			cleaned = true
			return nil
		},
		shared: func(context.Context) (Pairing, error) {
			if !installed {
				return Pairing{}, os.ErrNotExist
			}
			return fixturePairing(t, node, identity), nil
		},
		provision: func(ctx context.Context, request runnerinstall.Request, artifact remotessh.Artifact,
			progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
			provisions++
			if !cleaned || request != attempt.Request {
				return runnerinstall.Result{}, remotessh.ErrIntegrity
			}
			installed = true
			return runnerinstall.Result{Contract: jobcontract.CurrentContract, OperationID: request.OperationID,
				Runner: fixturePairing(t, node, identity).Runner, ListenPort: identity.ListenPort,
				ObservedAt: fixture.now}, nil
		},
	}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
	if err := manager.Check(t.Context(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	config, err = reopened.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	completed := config.Nodes[0].Installation
	retained := completed.Request == attempt.Request && completed.TemporaryDirectory == attempt.TemporaryDirectory
	if provisions != 1 || !retained || completed.CleanupPending || completed.Stage != "completed" {
		t.Fatal("restart did not clean and complete the same installation operation")
	}
}

func runBootstrapKillHelper(t *testing.T, marker string) {
	t.Helper()
	store, err := NewFileStore(os.Getenv("LLM_TEST_BOOTSTRAP_KILL_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := credentials.NewScopedStore(credentials.NewMemoryStore(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Dependencies{Store: store, Credentials: keys})
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return config.Nodes[0].UpdatedAt }
	remote := &remoteFixture{provision: func(ctx context.Context, request runnerinstall.Request, artifact remotessh.Artifact,
		progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		if err := os.WriteFile(marker, []byte(remotessh.BootstrapDirectory(request.OperationID)), 0600); err != nil {
			return runnerinstall.Result{}, err
		}
		<-ctx.Done()
		return runnerinstall.Result{}, ctx.Err()
	}}
	fixture := &nodeFixture{service: service, store: store, keys: keys}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
	system, _ := remote.Probe(t.Context())
	if err := manager.install(t.Context(), config.Nodes[0], system, remote); err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper provisioning unexpectedly returned")
}

func TestManagerDoesNotCreateBootstrapWhenIntentDurabilityFails(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(map[bool]string{false: "write_rejected", true: "commit_unconfirmed"}[visible], func(t *testing.T) {
			fixture := newNodeFixture(t)
			node := fixture.save(t, passwordCommand(t))
			provisions := 0
			remote := &remoteFixture{provision: func(ctx context.Context, request runnerinstall.Request,
				artifact remotessh.Artifact, progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
				provisions++
				return runnerinstall.Result{}, remotessh.ErrConnection
			}}
			manager := managerFixture(t, fixture, remote)
			manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
			write := fixture.store.writeFile
			fixture.store.writeFile = func(ctx context.Context, path string, document []byte) error {
				if visible {
					if err := write(ctx, path, document); err != nil {
						return err
					}
				}
				return os.ErrPermission
			}
			system, _ := remote.Probe(t.Context())
			err := manager.install(t.Context(), node, system, remote)
			want := os.ErrPermission
			if visible {
				want = ErrCommitUnconfirmed
			}
			if !errors.Is(err, want) || provisions != 0 {
				t.Fatalf("unconfirmed intent created remote resources: %v", err)
			}
			config, err := fixture.store.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			attempt := config.Nodes[0].Installation
			if visible && (attempt == nil || !attempt.CleanupPending ||
				attempt.TemporaryDirectory != remotessh.BootstrapDirectory(attempt.Request.OperationID)) {
				t.Fatal("ambiguous persistence lost the exact cleanup intent")
			}
			if !visible && attempt != nil {
				t.Fatal("failed persistence changed installation data")
			}
		})
	}
}

func TestManagerRetainsBootstrapFactsWhenProvisionReturnsDifferentOwnership(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	otherOperationID := fixtureID(t)
	remote := &remoteFixture{provision: func(ctx context.Context, request runnerinstall.Request,
		artifact remotessh.Artifact, progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		return runnerinstall.Result{}, &remotessh.ProvisionError{OperationID: otherOperationID,
			TemporaryDirectory: remotessh.BootstrapDirectory(otherOperationID), Cause: remotessh.ErrConnection}
	}}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
	system, _ := remote.Probe(t.Context())
	if err := manager.install(t.Context(), node, system, remote); !errors.Is(err, remotessh.ErrIntegrity) {
		t.Fatalf("a provision error replaced bootstrap ownership: %v", err)
	}
	config, err := fixture.store.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	attempt := config.Nodes[0].Installation
	owned := attempt.Request.OperationID != otherOperationID && attempt.CleanupPending &&
		attempt.TemporaryDirectory == remotessh.BootstrapDirectory(attempt.Request.OperationID)
	if !owned {
		t.Fatal("untrusted provision facts replaced the original operation or cleanup intent")
	}
}

func TestManagerRetainsBootstrapIntentWhenCompletionCannotBePersisted(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	remote := &remoteFixture{provision: func(ctx context.Context, request runnerinstall.Request,
		artifact remotessh.Artifact, progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		return runnerinstall.Result{Contract: jobcontract.CurrentContract, OperationID: request.OperationID,
			Runner: fixturePairing(t, node, identity).Runner, ListenPort: identity.ListenPort,
			ObservedAt: fixture.now}, nil
	}}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
	write := fixture.store.writeFile
	fixture.store.writeFile = func(ctx context.Context, path string, document []byte) error {
		if bytes.Contains(document, []byte(`"stage": "completed"`)) {
			return os.ErrPermission
		}
		return write(ctx, path, document)
	}
	system, _ := remote.Probe(t.Context())
	if err := manager.install(t.Context(), node, system, remote); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("failed completion persistence was acknowledged: %v", err)
	}
	reopened, err := NewFileStore(fixture.store.path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := reopened.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	attempt := config.Nodes[0].Installation
	retained := attempt.CleanupPending && attempt.Stage == "uploading" &&
		attempt.TemporaryDirectory == remotessh.BootstrapDirectory(attempt.Request.OperationID)
	if !retained {
		t.Fatal("failed completion persistence lost the original cleanup intent")
	}
}
