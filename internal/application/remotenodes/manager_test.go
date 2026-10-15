package remotenodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type provisionFixture func(
	context.Context,
	runnerinstall.Request,
	remotessh.Artifact,
	func(runnerinstall.Progress),
) (runnerinstall.Result, error)

type remoteFixture struct {
	probe        func(context.Context) (remotessh.SystemInfo, error)
	shared       func(context.Context) (Pairing, error)
	bound        func(context.Context, Binding, *credentials.Lease) (jobcontract.RunnerInfo, error)
	provision    provisionFixture
	operation    func(context.Context, string) (runnerinstall.Operation, error)
	cleanup      func(context.Context, string) error
	closeSession func() error
	closed       atomic.Uint32
}

func (remote *remoteFixture) Probe(ctx context.Context) (remotessh.SystemInfo, error) {
	if remote.probe != nil {
		return remote.probe(ctx)
	}
	return remotessh.SystemInfo{UID: 0, OS: "linux", Arch: "amd64", SystemdVersion: 249,
		SystemdPID1: true, HasSHA256: true, HasRunuser: true}, nil
}

func (remote *remoteFixture) Shared(ctx context.Context) (Pairing, error) {
	if remote.shared == nil {
		return Pairing{}, os.ErrNotExist
	}
	return remote.shared(ctx)
}

func (remote *remoteFixture) Bound(
	ctx context.Context,
	binding Binding,
	token *credentials.Lease,
) (jobcontract.RunnerInfo, error) {
	if remote.bound == nil {
		return jobcontract.RunnerInfo{}, runnerinstall.ErrService
	}
	return remote.bound(ctx, binding, token)
}

func (remote *remoteFixture) Provision(
	ctx context.Context,
	request runnerinstall.Request,
	artifact remotessh.Artifact,
	progress func(runnerinstall.Progress),
) (runnerinstall.Result, error) {
	if remote.provision == nil {
		return runnerinstall.Result{}, runnerinstall.ErrService
	}
	return remote.provision(
		ctx,
		request,
		artifact,
		progress,
	)
}

func (remote *remoteFixture) Operation(ctx context.Context, id string) (runnerinstall.Operation, error) {
	if remote.operation == nil {
		return runnerinstall.Operation{}, os.ErrNotExist
	}
	return remote.operation(ctx, id)
}

func (remote *remoteFixture) CleanupBootstrap(ctx context.Context, directory string) error {
	if remote.cleanup != nil {
		return remote.cleanup(ctx, directory)
	}
	return nil
}

func (remote *remoteFixture) Close() error {
	remote.closed.Add(1)
	if remote.closeSession != nil {
		return remote.closeSession()
	}
	return nil
}

type artifactFixtureSource struct {
	open func(context.Context, string, string) (*runnerartifacts.Artifact, error)
}

func (source artifactFixtureSource) Open(ctx context.Context, release, arch string) (*runnerartifacts.Artifact, error) {
	return source.open(ctx, release, arch)
}

func managerArtifactSource(t *testing.T) ArtifactSource {
	t.Helper()
	return artifactFixtureSource{open: func(ctx context.Context, release, arch string) (*runnerartifacts.Artifact, error) {
		file, err := os.CreateTemp(t.TempDir(), "artifact")
		if err != nil {
			return nil, err
		}
		content := []byte("fixture-runner")
		if _, err := file.Write(content); err != nil {
			_ = file.Close()
			return nil, err
		}
		_, _ = file.Seek(0, io.SeekStart)
		digest := sha256.Sum256(content)
		return &runnerartifacts.Artifact{Manifest: runnerartifacts.Manifest{Release: release}, Source: file,
			Entry: runnerartifacts.Entry{OS: "linux", Arch: arch, Size: int64(len(content)),
				SHA256: hex.EncodeToString(digest[:])}}, nil
	}}
}

func managerFixture(t *testing.T, fixture *nodeFixture, remote *remoteFixture) *Manager {
	t.Helper()
	manager, err := NewManager(context.Background(), ManagerDependencies{
		Configuration: fixture.service,
		Connect: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
			return remote, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func waitManager(t *testing.T, manager *Manager, id string) {
	t.Helper()
	manager.mu.Lock()
	task := manager.checks[id]
	manager.mu.Unlock()
	if task == nil {
		return
	}
	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		t.Fatal("node check did not finish")
	}
}

func fixturePairing(t *testing.T, node Node, identity runnerinstall.Identity) Pairing {
	t.Helper()
	node.Binding = &Binding{Identity: identity}
	return Pairing{Identity: identity, Runner: *readyObservation(node, time.Now().UTC()).Runner,
		Token: temporarySecret(t, strings.Repeat("fixture-node-token", 3))}
}

func TestManagerRequiresFreshExactHostConfirmationBeforePairing(t *testing.T) {
	fixture := newNodeFixture(t)
	command := passwordCommand(t)
	key := *command.HostKey
	command.HostKey = nil
	node := fixture.save(t, command)
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	remote := &remoteFixture{shared: func(context.Context) (Pairing, error) {
		return fixturePairing(t, node, identity), nil
	}}
	manager := managerFixture(t, fixture, remote)
	manager.connect = func(
		ctx context.Context,
		endpoint remotessh.Endpoint,
		auth remotessh.Authentication,
	) (RemoteSession, error) {
		if endpoint.Fingerprint == "" {
			return nil, &remotessh.HostKeyError{KeyType: key.Type, Fingerprint: key.Fingerprint}
		}
		if endpoint.Fingerprint != key.Fingerprint || endpoint.HostKeyType != key.Type {
			t.Error("confirmation changed the host identity")
			return nil, remotessh.ErrIntegrity
		}
		return remote, nil
	}
	if err := manager.Check(context.Background(), node.ID, node.Revision); !errors.Is(err, remotessh.ErrHostUnverified) {
		t.Fatal(err)
	}
	wrong := key
	wrong.Type = "ssh-rsa"
	if _, err := manager.ConfirmHostKey(context.Background(), HostKeyConfirmation{
		ID: node.ID, ExpectedRevision: node.Revision, Key: wrong,
	}); !errors.Is(err, ErrConflict) {
		t.Fatal("different key was confirmed")
	}
	confirmed, err := manager.ConfirmHostKey(context.Background(), HostKeyConfirmation{
		ID: node.ID, ExpectedRevision: node.Revision, Key: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitManager(t, manager, confirmed.ID)
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil || !snapshot.Nodes[0].Eligible {
		t.Fatalf("confirmed node not ready: %v", err)
	}
	if snapshot.Nodes[0].RunnerNodeID != identity.NodeID || snapshot.Nodes[0].Revision != confirmed.Revision+1 {
		t.Fatal("shared identity not bound after confirmation")
	}
}

func TestManagerReusesDifferentBuildAndNeverRequestsAnArtifact(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	remote := &remoteFixture{shared: func(context.Context) (Pairing, error) {
		pairing := fixturePairing(t, node, identity)
		pairing.Runner.RunnerVersion = "other-client-release"
		return pairing, nil
	}}
	manager := managerFixture(t, fixture, remote)
	manager.release = "this-client-release"
	manager.artifacts = artifactFixtureSource{open: func(
		context.Context,
		string,
		string,
	) (*runnerartifacts.Artifact, error) {
		t.Error("compatible runner caused an artifact download")
		return nil, runnerartifacts.ErrUnavailable
	}}
	if err := manager.Check(context.Background(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	config, err := fixture.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	node = config.Nodes[0]
	reused := node.Binding.Identity == identity && node.Installation == nil &&
		node.Observation.Runner.RunnerVersion == "other-client-release"
	if !reused {
		t.Fatal("compatible connection installed or changed the runner")
	}
	remote.bound = func(ctx context.Context, binding Binding, token *credentials.Lease) (jobcontract.RunnerInfo, error) {
		value, err := token.Bytes()
		if err != nil || !tokenMatches(value, identity) {
			t.Error("saved node token was not used")
		}
		clear(value)
		info := *node.Observation.Runner
		info.MaintenanceOperationID, info.Ready = fixtureID(t), false
		return info, nil
	}
	remote.shared = func(context.Context) (Pairing, error) {
		t.Error("bound connection reread or replaced server pairing")
		return Pairing{}, runnerinstall.ErrPairingRequired
	}
	if err := manager.Check(context.Background(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := manager.Snapshot(context.Background())
	if snapshot.Nodes[0].Status != "maintenance" || snapshot.Nodes[0].Eligible {
		t.Fatal("maintenance runner remained eligible")
	}
}

func TestManagerInstallsOnceWithDurableStableOperationAndRecordsActualRunner(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	identity := fixtureIdentity(t, strings.Repeat("fixture-node-token", 3))
	installed := false
	provisions := 0
	remote := &remoteFixture{shared: func(context.Context) (Pairing, error) {
		if !installed {
			return Pairing{}, os.ErrNotExist
		}
		return fixturePairing(t, node, identity), nil
	}}
	manager := managerFixture(t, fixture, remote)
	manager.release = "v-fixture"
	manager.artifacts = managerArtifactSource(t)
	remote.provision = func(ctx context.Context, request runnerinstall.Request, artifact remotessh.Artifact,
		progress func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		provisions++
		config, err := fixture.store.Read(ctx)
		if err != nil || config.Nodes[0].Installation == nil {
			t.Error("remote mutation preceded durable installation intent")
			return runnerinstall.Result{}, ErrInvalid
		}
		if config.Nodes[0].Installation.Request != request {
			t.Error("installation request changed after persistence")
			return runnerinstall.Result{}, ErrInvalid
		}
		progress(runnerinstall.Progress{OperationID: request.OperationID, Stage: "checking"})
		config, err = fixture.store.Read(ctx)
		if err != nil || config.Nodes[0].Installation.Stage != "checking" {
			t.Error("progress was not persisted")
			return runnerinstall.Result{}, ErrInvalid
		}
		installed = true
		return runnerinstall.Result{Contract: jobcontract.CurrentContract, OperationID: request.OperationID,
			Runner: fixturePairing(t, node, identity).Runner, ListenPort: identity.ListenPort, ObservedAt: fixture.now}, nil
	}
	if err := manager.Check(context.Background(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	config, err := fixture.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	node = config.Nodes[0]
	completed := node.Installation.Stage == "completed" && node.Observation.Runner.Ready
	if provisions != 1 || !completed {
		t.Fatal("installation did not reach actual healthy observation")
	}
	if node.Installation.Request.ArtifactSHA256 == node.Observation.Runner.EngineBuildID {
		t.Fatal("test must preserve a different actual compatible runner build")
	}
}

func TestManagerObserverCancellationAndSlowNodeDoNotStopOtherNodes(t *testing.T) {
	fixture := newNodeFixture(t)
	slow := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	fastCommand := passwordCommand(t)
	fastCommand.Host = "fast.example.test"
	fast := fixture.bind(t, fixture.save(t, fastCommand))
	entered := make(chan struct{})
	ended := make(chan struct{})
	slowRemote := &remoteFixture{probe: func(ctx context.Context) (remotessh.SystemInfo, error) {
		close(entered)
		<-ctx.Done()
		close(ended)
		return remotessh.SystemInfo{}, ctx.Err()
	}}
	fastRemote := &remoteFixture{bound: func(
		context.Context,
		Binding,
		*credentials.Lease,
	) (jobcontract.RunnerInfo, error) {
		return *readyObservation(fast, fixture.now).Runner, nil
	}}
	manager := managerFixture(t, fixture, slowRemote)
	manager.connect = func(
		ctx context.Context,
		endpoint remotessh.Endpoint,
		auth remotessh.Authentication,
	) (RemoteSession, error) {
		if endpoint.Host == fast.Host {
			return fastRemote, nil
		}
		return slowRemote, nil
	}
	observer, cancel := context.WithCancel(context.Background())
	checked := make(chan error, 1)
	go func() { checked <- manager.Check(observer, slow.ID, slow.Revision) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow check did not start")
	}
	cancel()
	if err := <-checked; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-ended:
		t.Fatal("observer cancellation stopped owned node operation")
	default:
	}
	if err := manager.Check(context.Background(), fast.ID, fast.Revision); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	default:
		t.Fatal("manager close did not join its remote operation")
	}
	if slowRemote.closed.Load() != 1 || fastRemote.closed.Load() != 1 {
		t.Fatal("remote session ownership leaked")
	}
	if _, err := manager.Save(context.Background(), passwordCommand(t)); !errors.Is(err, context.Canceled) {
		t.Fatal("closed manager accepted a mutation")
	}
}

func TestManagerMonitorStartsChecksAndCloseJoinsItsLoop(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	entered := make(chan struct{})
	remote := &remoteFixture{bound: func(
		context.Context,
		Binding,
		*credentials.Lease,
	) (jobcontract.RunnerInfo, error) {
		close(entered)
		return *readyObservation(node, fixture.now).Runner, nil
	}}
	manager := managerFixture(t, fixture, remote)
	finished := make(chan error, 1)
	go func() { finished <- manager.Run() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("monitor did not start the enabled node")
	}
	waitManager(t, manager, node.ID)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
