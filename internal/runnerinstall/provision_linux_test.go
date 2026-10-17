package runnerinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type fixtureSystem struct {
	layout      layout
	buildID     string
	starts      atomic.Int32
	accounts    atomic.Int32
	initializes atomic.Int32
	failStart   bool
	lostInit    bool
	onAccount   func()
}

func (*fixtureSystem) Preflight(context.Context) error { return nil }

func (system *fixtureSystem) Account(context.Context) (account, error) {
	system.accounts.Add(1)
	if system.onAccount != nil {
		system.onAccount()
	}
	return account{uid: os.Geteuid(), gid: os.Getegid()}, nil
}

func (system *fixtureSystem) Initialize(_ context.Context, input initializeInput) error {
	system.initializes.Add(1)
	config, err := runnerconfig.New(input.token)
	if err != nil {
		return err
	}
	defer config.Clear()
	config.ListenPort = input.port
	if err := runnerprocess.Initialize(system.layout.state, config); err != nil {
		return err
	}
	if system.lostInit {
		return ErrService
	}
	return nil
}

func (system *fixtureSystem) Start(context.Context) error {
	system.starts.Add(1)
	if system.failStart {
		return ErrService
	}
	return nil
}

func (system *fixtureSystem) Verify(_ context.Context, input verificationInput) error {
	if input.buildID != system.buildID {
		return ErrIntegrity
	}
	return nil
}

type provisionFixture struct {
	installer    provisioner
	system       *fixtureSystem
	request      Request
	program      []byte
	infoContract atomic.Int32
}

func newProvisionFixture(t *testing.T) *provisionFixture {
	t.Helper()
	base := t.TempDir()
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	paths := layout{
		program: filepath.Join(base, "program"), state: filepath.Join(base, "state"),
		install: filepath.Join(base, "install"), unit: filepath.Join(base, ServiceName),
	}
	program := []byte("immutable binary fixture")
	digest := sha256.Sum256(program)
	buildID := hex.EncodeToString(digest[:])
	system := &fixtureSystem{layout: paths, buildID: buildID}
	fixture := &provisionFixture{system: system, program: program}
	fixture.infoContract.Store(int32(jobcontract.CurrentContract))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		config, exists, err := paths.readConfig(account{uid: os.Geteuid(), gid: os.Getegid()})
		if err != nil || !exists {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer config.Clear()
		token, err := os.ReadFile(filepath.Join(paths.install, PairingName))
		if err != nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer clear(token)
		if request.Header.Get("Authorization") != "Bearer "+string(token) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(writer).Encode(jobcontract.RunnerInfo{
			Contract: int(fixture.infoContract.Load()), NodeID: config.NodeID, NodeInstallationID: config.InstallationID,
			EngineBuildID: system.buildID, RunnerVersion: "fixture-release", OS: "linux", Arch: runtime.GOARCH,
			Capabilities: jobcontract.RunnerCapabilities{
				JobKinds: []jobcontract.Kind{jobcontract.KindRun}, Protocols: []string{"openai-chat"}, Features: []string{},
			},
			Budget: config.Budget, Ready: true, Healthy: true,
		})
	}))
	t.Cleanup(server.Close)
	request := installationRequest()
	request.ArtifactSHA256 = buildID
	request.ListenPort = uint16(server.Listener.Addr().(*net.TCPAddr).Port)
	fixture.request = request
	fixture.installer = provisioner{layout: paths, system: system, source: bytes.NewReader(program)}
	return fixture
}

func TestUnknownProgramAndInstallRootsAreRejectedBeforeOtherMutations(t *testing.T) {
	for _, root := range []string{"program", "install"} {
		t.Run(root, func(t *testing.T) {
			fixture := newProvisionFixture(t)
			name, mode := fixture.installer.layout.program, os.FileMode(0755)
			if root == "install" {
				name, mode = fixture.installer.layout.install, 0700
			}
			if err := os.Mkdir(name, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(name, "unrelated"), []byte("preserve foreign data"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.installer.provision(t.Context(), fixture.request); !errors.Is(err, ErrUnownedPath) {
				t.Fatal("an unknown directory was adopted")
			}
			entries, err := os.ReadDir(name)
			if err != nil || len(entries) != 1 || entries[0].Name() != "unrelated" {
				t.Fatal("rejected directory was changed")
			}
			if fixture.system.accounts.Load() != 0 || fixture.system.initializes.Load() != 0 {
				t.Fatal("ownership rejection followed account or configuration mutation")
			}
			if _, err := os.Stat(fixture.installer.layout.state); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("foreign program/install root caused service state creation")
			}
		})
	}
}

func TestProvisionPublishesDurablyAndReusesAnotherClientBuildWithoutOverwriting(t *testing.T) {
	fixture := newProvisionFixture(t)
	result, err := fixture.installer.provision(t.Context(), fixture.request)
	if err != nil || result.Reused || result.Runner.EngineBuildID != fixture.request.ArtifactSHA256 {
		t.Fatalf("first installation failed: %v", err)
	}
	configBefore, _ := os.ReadFile(filepath.Join(fixture.installer.layout.state, runnerprocess.ConfigName))
	defer clear(configBefore)
	tokenBefore, _ := os.ReadFile(filepath.Join(fixture.installer.layout.install, PairingName))
	defer clear(tokenBefore)
	confirmed, err := fixture.installer.provision(t.Context(), fixture.request)
	if err != nil || !confirmed.ObservedAt.Equal(result.ObservedAt) || fixture.system.starts.Load() != 1 {
		t.Fatal("completed operation retry repeated installation or changed its receipt")
	}
	secondProgram := []byte("another client's different binary")
	secondHash := sha256.Sum256(secondProgram)
	second := fixture.request
	second.OperationID = "66666666-6666-4666-8666-666666666666"
	second.ArtifactSHA256 = hex.EncodeToString(secondHash[:])
	second.ListenPort++
	fixture.installer.source = bytes.NewReader(secondProgram)
	reused, err := fixture.installer.provision(t.Context(), second)
	if err != nil || !reused.Reused || reused.ListenPort != fixture.request.ListenPort {
		t.Fatal("another client changed the installed port or failed to reuse the node")
	}
	if reused.Runner.EngineBuildID != result.Runner.EngineBuildID || fixture.system.initializes.Load() != 1 {
		t.Fatal("another client overwrote the runner or initialized another identity")
	}
	if _, err := os.Stat(fixture.installer.layout.programPath(second.ArtifactSHA256)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("compatible connection published a replacement binary")
	}
	configAfter, _ := os.ReadFile(filepath.Join(fixture.installer.layout.state, runnerprocess.ConfigName))
	defer clear(configAfter)
	tokenAfter, _ := os.ReadFile(filepath.Join(fixture.installer.layout.install, PairingName))
	defer clear(tokenAfter)
	if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(tokenBefore, tokenAfter) {
		t.Fatal("shared-node reuse changed the identity or pairing authorization")
	}
}

func TestProvisionRecoversPartialStartAndLostInitAcknowledgment(t *testing.T) {
	fixture := newProvisionFixture(t)
	fixture.system.lostInit, fixture.system.failStart = true, true
	if _, err := fixture.installer.provision(t.Context(), fixture.request); !errors.Is(err, ErrService) {
		t.Fatal("injected service failure did not stop installation")
	}
	before, _ := os.ReadFile(filepath.Join(fixture.installer.layout.state, runnerprocess.ConfigName))
	defer clear(before)
	fixture.system.failStart = false
	if _, err := fixture.installer.provision(t.Context(), fixture.request); err != nil {
		t.Fatalf("owned partial installation could not resume: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(fixture.installer.layout.state, runnerprocess.ConfigName))
	defer clear(after)
	if !bytes.Equal(before, after) || fixture.system.initializes.Load() != 1 {
		t.Fatal("partial installation retry created another identity")
	}
}

func TestInstallationLockIsSharedAndIndependentOfRunnerRuntimeLock(t *testing.T) {
	fixture := newProvisionFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	fixture.system.onAccount = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { _, err := fixture.installer.provision(t.Context(), fixture.request); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("first installer did not hold the installation lock")
	}
	other := fixture.request
	other.OperationID = "66666666-6666-4666-8666-666666666666"
	second := provisioner{
		layout: fixture.installer.layout, system: fixture.system, source: bytes.NewReader(fixture.program),
	}
	_, err := second.provision(t.Context(), other)
	close(release)
	if !errors.Is(err, ErrBusy) {
		t.Fatal("two clients held the installation lock")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runtimeLock, err := runnerstate.Open(fixture.installer.layout.state)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeLock.Close()
	fixture.system.onAccount = nil
	if _, err := fixture.installer.provision(t.Context(), other); err != nil {
		t.Fatalf("runner runtime lock prevented compatible installation inspection: %v", err)
	}
}

func TestProvisionRejectsUnknownStateAndPairingLossWithoutResettingData(t *testing.T) {
	fixture := newProvisionFixture(t)
	if err := os.Mkdir(fixture.installer.layout.state, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(fixture.installer.layout.state, "runner.db")
	if err := os.WriteFile(sentinel, []byte("preserve unknown database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.installer.provision(t.Context(), fixture.request); !errors.Is(err, ErrUnownedPath) {
		t.Fatal("unknown unconfigured state was adopted or reset")
	}
	retained, _ := os.ReadFile(sentinel)
	if string(retained) != "preserve unknown database" {
		t.Fatal("rejected database was changed")
	}
	paired := newProvisionFixture(t)
	if _, err := paired.installer.provision(t.Context(), paired.request); err != nil {
		t.Fatal(err)
	}
	configName := filepath.Join(paired.installer.layout.state, runnerprocess.ConfigName)
	before, _ := os.ReadFile(configName)
	defer clear(before)
	if err := os.Remove(filepath.Join(paired.installer.layout.install, PairingName)); err != nil {
		t.Fatal(err)
	}
	other := paired.request
	other.OperationID = "66666666-6666-4666-8666-666666666666"
	if _, err := paired.installer.provision(t.Context(), other); !errors.Is(err, ErrPairingRequired) {
		t.Fatal("missing pairing token was silently rotated")
	}
	after, _ := os.ReadFile(configName)
	defer clear(after)
	if !bytes.Equal(before, after) {
		t.Fatal("pairing failure changed existing authorization")
	}
}

func TestProvisionRejectsIncompatibleRunnerAndOperationConflictWithoutReplacing(t *testing.T) {
	fixture := newProvisionFixture(t)
	if _, err := fixture.installer.provision(t.Context(), fixture.request); err != nil {
		t.Fatal(err)
	}
	conflict := fixture.request
	conflict.ListenPort++
	if _, err := fixture.installer.provision(t.Context(), conflict); !errors.Is(err, ErrConflict) {
		t.Fatal("operation ID was reused with different inputs")
	}
	fixture.infoContract.Add(1)
	other := fixture.request
	other.OperationID = "66666666-6666-4666-8666-666666666666"
	if _, err := fixture.installer.provision(t.Context(), other); !errors.Is(err, ErrUpgradeRequired) {
		t.Fatal("incompatible runner was accepted or overwritten")
	}
	selected, err := fixture.installer.layout.currentBuild()
	if err != nil || selected != fixture.request.ArtifactSHA256 {
		t.Fatal("incompatible connection changed the selected binary")
	}
}

func TestProvisionUnsupportedRequestDoesNotCreateInstallationFiles(t *testing.T) {
	fixture := newProvisionFixture(t)
	fixture.request.Contract++
	if _, err := fixture.installer.provision(t.Context(), fixture.request); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported contract did not fail first")
	}
	if _, err := os.Stat(fixture.installer.layout.install); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsupported request modified installation state")
	}
}
