package remotenodes

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestManagerResolvesLostInstallAcknowledgmentBeforeBinding(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	installed, cleaned, provisions := false, false, 0
	var receipt runnerinstall.Operation
	remote := &remoteFixture{
		shared: func(context.Context) (Pairing, error) {
			if !installed {
				return Pairing{}, os.ErrNotExist
			}
			return fixturePairing(t, node, identity), nil
		},
		operation: func(context.Context, string) (runnerinstall.Operation, error) { return receipt, nil },
		cleanup: func(ctx context.Context, directory string) error {
			if directory != "/var/tmp/llm-test-runner-bootstrap.ABCDE12345" {
				return remotessh.ErrIntegrity
			}
			cleaned = true
			return nil
		},
	}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
	remote.provision = func(ctx context.Context, request runnerinstall.Request, artifact remotessh.Artifact,
		progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		provisions++
		installed = true
		result := runnerinstall.Result{Contract: jobcontract.CurrentContract, OperationID: request.OperationID,
			Runner: fixturePairing(t, node, identity).Runner, ListenPort: identity.ListenPort, ObservedAt: fixture.now}
		receipt = runnerinstall.Operation{Request: request, Stage: "completed", ChangedAt: fixture.now, Result: &result}
		return runnerinstall.Result{}, &remotessh.ProvisionError{OperationID: request.OperationID,
			TemporaryDirectory: "/var/tmp/llm-test-runner-bootstrap.ABCDE12345", CleanupPending: true,
			Cause: remotessh.ErrConnection}
	}
	if err := manager.Check(context.Background(), node.ID, node.Revision); !errors.Is(err, remotessh.ErrConnection) {
		t.Fatal(err)
	}
	config, err := fixture.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	attempt := config.Nodes[0].Installation
	unconfirmed := attempt.CleanupPending && attempt.ErrorCode == "remote_connection_failed"
	if !unconfirmed || config.Nodes[0].Binding != nil {
		t.Fatal("lost acknowledgment was bound or its cleanup information was dropped")
	}
	if err := manager.Check(context.Background(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	config, _ = fixture.store.Read(context.Background())
	current := config.Nodes[0]
	recovered := cleaned && !current.Installation.CleanupPending && current.Installation.ErrorCode == ""
	if provisions != 1 || !recovered {
		t.Fatal("recovery reissued install or did not confirm cleanup and durable receipt")
	}
	completed := current.Installation.Stage == "completed" && current.Observation.Runner.Ready
	if current.Binding == nil || !completed {
		t.Fatal("confirmed installation did not become ready")
	}
}

func TestManagerRetryQueuesAnotherCheckWhileOldSessionIsClosing(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	source := managerArtifactSource(t)
	artifact, err := source.Open(context.Background(), "v-fixture", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	request := runnerinstall.Request{Contract: jobcontract.CurrentContract, OperationID: fixtureID(t),
		ArtifactSHA256: artifact.Entry.SHA256, ListenPort: 44917}
	_ = artifact.Close()
	if err := fixture.service.RecordInstallation(context.Background(), InstallationCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Installation: Installation{
			Request: request, Release: "v-fixture", Arch: "amd64", Stage: "service", ErrorCode: "runner_installation_failed",
		},
	}); err != nil {
		t.Fatal(err)
	}
	closing, allowClose := make(chan struct{}), make(chan struct{})
	var closes, provisions atomic.Uint32
	remote := &remoteFixture{
		operation: func(context.Context, string) (runnerinstall.Operation, error) {
			return runnerinstall.Operation{Request: request, Stage: "service", ChangedAt: fixture.now}, nil
		},
		shared: func(context.Context) (Pairing, error) { return fixturePairing(t, node, identity), nil },
		closeSession: func() error {
			if closes.Add(1) == 1 {
				close(closing)
				<-allowClose
			}
			return nil
		},
		provision: func(ctx context.Context, retried runnerinstall.Request, artifact remotessh.Artifact,
			progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
			provisions.Add(1)
			if retried != request {
				return runnerinstall.Result{}, remotessh.ErrIntegrity
			}
			return runnerinstall.Result{Contract: jobcontract.CurrentContract, OperationID: request.OperationID,
				Runner: fixturePairing(t, node, identity).Runner, ListenPort: identity.ListenPort, ObservedAt: fixture.now}, nil
		},
	}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = source, "v-fixture"
	checked := make(chan error, 1)
	go func() { checked <- manager.Check(context.Background(), node.ID, node.Revision) }()
	select {
	case <-closing:
	case <-time.After(5 * time.Second):
		t.Fatal("first failed check did not enter closing")
	}
	if err := manager.RetryInstallation(context.Background(), node.ID, node.Revision); err != nil {
		close(allowClose)
		t.Fatal(err)
	}
	close(allowClose)
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("explicit retry was coalesced away")
	}
	if provisions.Load() != 1 {
		t.Fatal("explicit retry did not execute before another monitor tick")
	}
}

func TestManagerRetriesPartialInstallationWithExistingIdentityAndSameRequest(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	request := runnerinstall.Request{Contract: jobcontract.CurrentContract, OperationID: fixtureID(t),
		ArtifactSHA256: "", ListenPort: 44917}
	managerSource := managerArtifactSource(t)
	artifact, err := managerSource.Open(context.Background(), "v-fixture", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	request.ArtifactSHA256 = artifact.Entry.SHA256
	_ = artifact.Close()
	attempt := Installation{Request: request, Release: "v-fixture", Arch: "amd64", Stage: "service",
		ErrorCode: "runner_installation_failed"}
	if err := fixture.service.RecordInstallation(context.Background(), InstallationCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Installation: attempt,
	}); err != nil {
		t.Fatal(err)
	}
	installed, provisions, sharedCalls := false, 0, 0
	remote := &remoteFixture{
		operation: func(context.Context, string) (runnerinstall.Operation, error) {
			return runnerinstall.Operation{Request: request, Stage: "service", ChangedAt: fixture.now}, nil
		},
		shared: func(context.Context) (Pairing, error) {
			sharedCalls++
			if !installed {
				return Pairing{}, runnerinstall.ErrService // Identity exists, daemon is not ready.
			}
			return fixturePairing(t, node, identity), nil
		},
	}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerSource, "different-selected-release"
	remote.provision = func(ctx context.Context, retried runnerinstall.Request, artifact remotessh.Artifact,
		progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		provisions++
		if retried != request {
			return runnerinstall.Result{}, remotessh.ErrIntegrity
		}
		installed = true
		return runnerinstall.Result{Contract: jobcontract.CurrentContract, OperationID: request.OperationID,
			Runner: fixturePairing(t, node, identity).Runner, ListenPort: identity.ListenPort, ObservedAt: fixture.now}, nil
	}
	if err := manager.Check(context.Background(), node.ID, node.Revision); !errors.Is(err, runnerinstall.ErrService) {
		t.Fatal(err)
	}
	if sharedCalls != 0 || provisions != 0 {
		t.Fatal("failed own operation bypassed its durable completion gate")
	}
	if err := manager.RetryInstallation(context.Background(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	waitManager(t, manager, node.ID)
	snapshot, err := manager.Snapshot(context.Background())
	recovered := snapshot.Nodes[0].Eligible && provisions == 1
	if err != nil || !recovered {
		t.Fatalf("same-operation retry did not recover partial service: %v", err)
	}
}

func TestManagerDoesNotBindHealthyDaemonUntilOwnFailedInstallIsResolved(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	request := runnerinstall.Request{Contract: jobcontract.CurrentContract, OperationID: fixtureID(t),
		ArtifactSHA256: strings.Repeat("b", 64), ListenPort: 44917}
	if err := fixture.service.RecordInstallation(context.Background(), InstallationCommand{
		ID: node.ID, ExpectedRevision: node.Revision,
		Installation: Installation{Request: request, Release: "v-fixture", Arch: "amd64", Stage: "verifying",
			ErrorCode: "runner_installation_failed"},
	}); err != nil {
		t.Fatal(err)
	}
	remote := &remoteFixture{
		operation: func(context.Context, string) (runnerinstall.Operation, error) {
			return runnerinstall.Operation{Request: request, Stage: "verifying", ChangedAt: fixture.now}, nil
		},
		shared: func(context.Context) (Pairing, error) {
			t.Error("failed installation was bypassed just because daemon responds")
			return Pairing{}, remotessh.ErrIntegrity
		},
	}
	manager := managerFixture(t, fixture, remote)
	if err := manager.Check(context.Background(), node.ID, node.Revision); !errors.Is(err, runnerinstall.ErrService) {
		t.Fatal(err)
	}
	config, _ := fixture.store.Read(context.Background())
	if config.Nodes[0].Binding != nil || config.Nodes[0].Observation.Status == "ready" {
		t.Fatal("unconfirmed installation was bound")
	}
}

func TestManagerKeepsInstallCleanupFactsByRejectingConcurrentEdit(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	entered, finish := make(chan struct{}), make(chan struct{})
	remote := &remoteFixture{provision: func(
		ctx context.Context,
		request runnerinstall.Request,
		artifact remotessh.Artifact,
		progress func(runnerinstall.Progress),
	) (runnerinstall.Result, error) {
		close(entered)
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return runnerinstall.Result{}, &remotessh.ProvisionError{OperationID: request.OperationID,
			TemporaryDirectory: "/var/tmp/llm-test-runner-bootstrap.ABCDE12345", CleanupPending: true,
			Cause: remotessh.ErrConnection}
	}}
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-fixture"
	checked := make(chan error, 1)
	go func() { checked <- manager.Check(context.Background(), node.ID, node.Revision) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("installation did not start")
	}
	command := editCommand(node)
	command.Name = "edited while installing"
	if _, err := manager.Save(context.Background(), command); !errors.Is(err, ErrBusy) {
		t.Fatal("edit superseded installation before its cleanup facts were durable")
	}
	otherStore, err := NewFileStore(fixture.store.path)
	if err != nil {
		t.Fatal(err)
	}
	otherService, err := New(Dependencies{Store: otherStore, Credentials: fixture.keys, Now: fixture.service.now,
		CanRemove: func(context.Context, Node) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	otherManager, err := NewManager(context.Background(), ManagerDependencies{
		Configuration: otherService,
		Connect: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
			return nil, remotessh.ErrConnection
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherManager.Close() })
	if _, err := otherManager.Save(context.Background(), command); !errors.Is(err, ErrBusy) {
		t.Fatal("another manager superseded the installer's node revision")
	}
	if _, err := otherService.Save(context.Background(), command); !errors.Is(err, ErrBusy) {
		t.Fatal("another service bypassed the cross-process operation lock")
	}
	if err := otherService.Remove(context.Background(), node.ID, node.Revision); !errors.Is(err, ErrBusy) {
		t.Fatal("another service removed an installer-owned node")
	}
	close(finish)
	if err := <-checked; !errors.Is(err, remotessh.ErrConnection) {
		t.Fatal(err)
	}
	config, _ := fixture.store.Read(context.Background())
	if config.Nodes[0].Revision != node.Revision || !config.Nodes[0].Installation.CleanupPending {
		t.Fatal("cleanup ownership was lost across concurrent edit")
	}
	if err := otherService.Remove(context.Background(), node.ID, node.Revision); !errors.Is(err, ErrReferenced) {
		t.Fatal("node with outstanding installation resources was removed")
	}
	command.Host = "different.example.test"
	if _, err := manager.Save(context.Background(), command); !errors.Is(err, ErrRePairRequired) {
		t.Fatal("unfinished installation moved to a different server")
	}
}

func TestManagerSerializesRecoveryWithOtherManagerRetryAndRefreshesCleanupFacts(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	source := managerArtifactSource(t)
	artifact, err := source.Open(context.Background(), "v-fixture", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	request := runnerinstall.Request{Contract: jobcontract.CurrentContract, OperationID: fixtureID(t),
		ArtifactSHA256: artifact.Entry.SHA256, ListenPort: 44917}
	_ = artifact.Close()
	oldDirectory := "/var/tmp/llm-test-runner-bootstrap.ABCDE12345"
	newDirectory := "/var/tmp/llm-test-runner-bootstrap.FGHIJ67890"
	attempt := Installation{Request: request, Release: "v-fixture", Arch: "amd64", Stage: "verifying",
		ErrorCode: "runner_installation_failed", TemporaryDirectory: oldDirectory, CleanupPending: true}
	if err := fixture.service.RecordInstallation(context.Background(), InstallationCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Installation: attempt,
	}); err != nil {
		t.Fatal(err)
	}
	config, err := fixture.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	staleNode := config.Nodes[0]
	entered, finish := make(chan string, 2), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recovery := &remoteFixture{
		cleanup: func(ctx context.Context, directory string) error {
			entered <- directory
			select {
			case <-finish:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		operation: func(context.Context, string) (runnerinstall.Operation, error) {
			return runnerinstall.Operation{Request: request, Stage: "verifying", ChangedAt: fixture.now}, nil
		},
	}
	manager := managerFixture(t, fixture, recovery)
	otherStore, err := NewFileStore(fixture.store.path)
	if err != nil {
		t.Fatal(err)
	}
	otherService, err := New(Dependencies{Store: otherStore, Credentials: fixture.keys, Now: fixture.service.now})
	if err != nil {
		t.Fatal(err)
	}
	var provisions atomic.Uint32
	installing := &remoteFixture{operation: recovery.operation, provision: func(
		ctx context.Context,
		request runnerinstall.Request,
		artifact remotessh.Artifact,
		progress func(runnerinstall.Progress),
	) (runnerinstall.Result, error) {
		provisions.Add(1)
		return runnerinstall.Result{}, &remotessh.ProvisionError{OperationID: request.OperationID,
			TemporaryDirectory: newDirectory, CleanupPending: true, Cause: remotessh.ErrConnection}
	}}
	otherManager, err := NewManager(context.Background(), ManagerDependencies{
		Configuration: otherService, Artifacts: source, Release: "v-fixture",
		Connect: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
			return installing, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherManager.Close() })
	recovered := make(chan error, 1)
	go func() {
		_, err := manager.resolveInstallation(ctx, staleNode, recovery)
		recovered <- err
	}()
	select {
	case directory := <-entered:
		if directory != oldDirectory {
			t.Fatal("recovery did not start with its persisted directory")
		}
	case <-ctx.Done():
		t.Fatal("recovery did not start")
	}
	if err := otherManager.RetryInstallation(ctx, node.ID, node.Revision); !errors.Is(err, ErrBusy) {
		t.Fatalf("another manager retried during cleanup: %v", err)
	}
	if provisions.Load() != 0 {
		t.Fatal("retry provisioned while recovery owned the node")
	}
	close(finish)
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	if err := otherManager.RetryInstallation(ctx, node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	waitManager(t, otherManager, node.ID)
	config, err = fixture.store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current := config.Nodes[0].Installation
	newCleanupRetained := current.CleanupPending && current.TemporaryDirectory == newDirectory &&
		current.ErrorCode == "remote_connection_failed"
	if provisions.Load() != 1 || !newCleanupRetained {
		t.Fatal("retry lost its new cleanup facts")
	}
	if _, err := manager.resolveInstallation(ctx, staleNode, recovery); err != nil {
		t.Fatal(err)
	}
	if directory := <-entered; directory != newDirectory {
		t.Fatal("recovery reused a stale directory instead of reading the locked node")
	}
}
