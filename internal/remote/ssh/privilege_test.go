package ssh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/runnerinstall"
	cryptossh "golang.org/x/crypto/ssh"
)

func sudoUpgradePeer(t *testing.T) *upgradeFixture {
	t.Helper()
	fixture := newUpgradeFixture(t)
	execute, serveSFTP := fixture.peer.execute, fixture.peer.sftpSession
	fixture.peer.sftpSession = func(cryptossh.Channel) { t.Error("sudo access fell back to the unprivileged SFTP subsystem") }
	fixture.peer.execute = func(command string, channel cryptossh.Channel) {
		if command == "/bin/sh -s" {
			input, _ := io.ReadAll(channel)
			if string(input) != systemProbe {
				t.Error("an administrative script ran without sudo")
				return
			}
			_, _ = io.WriteString(channel, "llm-test-system-probe\n1000\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\nyes\n")
			sendPrivilegeExit(channel, 0)
			return
		}
		if command == sudoSFTPCommand {
			serveSFTP(channel)
			return
		}
		if !strings.HasPrefix(command, sudoPrefix) {
			t.Errorf("administrative command omitted sudo: %q", command)
			return
		}
		execute(strings.TrimPrefix(command, sudoPrefix), channel)
	}
	return fixture
}

func sendPrivilegeExit(channel cryptossh.Channel, status uint32) {
	_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{Status: status}))
}

func TestSudoSharedPairingRetainsRootOwnershipAndDoesNotInstall(t *testing.T) {
	fixture := sudoUpgradePeer(t)
	token := []byte(strings.Repeat("synthetic-shared-token", 3))
	digest := sha256.Sum256(token)
	info := runnerMetadata()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+string(token) {
			t.Error("shared pairing lost its node-level authorization")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(writer).Encode(info)
	}))
	defer server.Close()
	identity := runnerinstall.Identity{Contract: info.Contract, NodeID: info.NodeID,
		InstallationID: info.NodeInstallationID, ListenPort: uint16(server.Listener.Addr().(*net.TCPAddr).Port),
		TokenSHA256: hex.EncodeToString(digest[:])}
	document, _ := json.Marshal(identity)
	fixture.fs.put(runnerinstall.IdentityPath, document, 0600)
	fixture.fs.put(runnerinstall.PairingPath, token, 0600)
	client := dialPeer(t, fixture.peer)
	system, err := client.Probe(t.Context())
	if err != nil || system.UID != 1000 || !system.HasSudo {
		t.Fatal("sudo probe replaced the login UID or failed to verify elevation")
	}
	node, _, err := client.ConnectNode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = node.Close()
	if fixture.execs.Load() != 0 || fixture.launches.Load() != 0 || len(client.endpoints) != 0 {
		t.Fatal("shared sudo pairing installed, upgraded, or retained an HTTP endpoint")
	}
	fixture.fs.mu.Lock()
	entry := fixture.fs.entries[runnerinstall.PairingPath]
	entry.owner = 1000
	fixture.fs.entries[runnerinstall.PairingPath] = entry
	fixture.fs.mu.Unlock()
	if _, err := client.ReadPrivate(t.Context(), runnerinstall.PairingPath, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("sudo relaxed root ownership checks on private pairing material")
	}
}

func TestSudoUpgradeUsesElevatedJournalAndFixedCommands(t *testing.T) {
	fixture := sudoUpgradePeer(t)
	fixture.journal(fixture.operation("backed_up"))
	fixture.onLaunch = func(string) uint32 {
		fixture.journal(fixture.terminal("completed"))
		return 0
	}
	artifact := fixture.artifact
	artifact.Source, artifact.Size = nil, 0
	client := dialPeer(t, fixture.peer)
	operation, err := client.RecoverUpgrade(t.Context(), fixture.request, artifact, nil)
	if err != nil || operation.Stage != "completed" || fixture.launches.Load() != 1 {
		t.Fatalf("sudo upgrade did not observe its one detached operation: %v", err)
	}
}

func TestSudoSFTPRejectsMissingTrustedServerAndDeniedElevationWithoutFallback(t *testing.T) {
	for _, test := range []struct {
		status uint32
		want   error
	}{
		{status: 78, want: ErrUnsupported},
		{status: 1, want: runnerinstall.ErrRootRequired},
	} {
		peer := newTestPeer(t)
		peer.sftpSession = func(cryptossh.Channel) { t.Error("failed sudo was retried without elevation") }
		peer.execute = func(command string, channel cryptossh.Channel) {
			if command == sudoSFTPCommand {
				sendPrivilegeExit(channel, test.status)
				return
			}
			input, _ := io.ReadAll(channel)
			if command != "/bin/sh -s" || string(input) != systemProbe {
				t.Error("unexpected command reached the peer")
				return
			}
			_, _ = io.WriteString(channel, "llm-test-system-probe\n1000\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\nyes\n")
			sendPrivilegeExit(channel, 0)
		}
		client := dialPeer(t, peer)
		if _, err := client.ReadPrivate(t.Context(), runnerinstall.IdentityPath, 0); !errors.Is(err, test.want) {
			t.Fatalf("sudo failure = %v, want %v", err, test.want)
		}
	}
}

func TestSudoSFTPCancellationAndClientCloseJoinTheStartedSession(t *testing.T) {
	for _, closeParent := range []bool{false, true} {
		name := "context"
		if closeParent {
			name = "client"
		}
		t.Run(name, func(t *testing.T) {
			testSudoSFTPInterrupted(t, closeParent)
		})
	}
}

func testSudoSFTPInterrupted(t *testing.T, closeParent bool) {
	t.Helper()
	peer := newTestPeer(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	peer.execute = func(command string, channel cryptossh.Channel) {
		if command == sudoSFTPCommand {
			close(started)
			_, _ = io.Copy(io.Discard, channel)
			close(stopped)
			return
		}
		input, _ := io.ReadAll(channel)
		if command != "/bin/sh -s" || string(input) != systemProbe {
			t.Error("unexpected command reached the peer")
			return
		}
		_, _ = io.WriteString(channel, "llm-test-system-probe\n1000\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\nyes\n")
		sendPrivilegeExit(channel, 0)
	}
	client := dialPeer(t, peer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := client.ReadPrivate(ctx, runnerinstall.IdentityPath, 0); result <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("test did not reach the elevated SFTP process")
	}
	want := error(context.Canceled)
	if closeParent {
		want = ErrConnection
		_ = client.Close()
	} else {
		cancel()
	}
	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("interrupted sudo SFTP returned %v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("sudo SFTP handshake ignored shutdown")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancelled elevated SFTP retained its session")
	}
	_ = client.Close()
}

func TestUnprivilegedNodeIsRejectedBeforeAdministrativeAccess(t *testing.T) {
	peer := newTestPeer(t)
	var administrative atomic.Int32
	peer.sftpSession = func(cryptossh.Channel) { administrative.Add(1) }
	peer.execute = func(command string, channel cryptossh.Channel) {
		input, _ := io.ReadAll(channel)
		if command != "/bin/sh -s" || string(input) != systemProbe {
			administrative.Add(1)
			return
		}
		_, _ = io.WriteString(channel, "llm-test-system-probe\n1000\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\nno\n")
		sendPrivilegeExit(channel, 0)
	}
	client := dialPeer(t, peer)
	if _, err := client.ReadPrivate(t.Context(), runnerinstall.IdentityPath, 0); !errors.Is(err, runnerinstall.ErrRootRequired) {
		t.Fatal("non-root node without sudo reached private storage")
	}
	if administrative.Load() != 0 {
		t.Fatal("rejected node attempted an administrative command")
	}
}
