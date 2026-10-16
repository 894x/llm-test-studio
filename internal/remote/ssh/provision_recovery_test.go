package ssh

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
	"github.com/pkg/sftp"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestProvisionRecoversExactBootstrapAfterLostCreateAcknowledgment(t *testing.T) {
	requireBootstrapFilesystem(t)
	directory := testBootstrapDirectory(t)
	peer := bootstrapRecoveryPeer(t, directory, true)
	client := dialPeer(t, peer)
	request, artifact := bootstrapRecoveryArtifact(directory)
	_, err := client.Provision(t.Context(), request, artifact, nil)
	var failure *ProvisionError
	validFailure := errors.As(err, &failure) && failure.OperationID == request.OperationID &&
		failure.TemporaryDirectory == directory && failure.CleanupPending
	if !validFailure {
		t.Fatalf("lost mkdir acknowledgment lost bootstrap ownership: %v", err)
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("fixture did not leave the created private directory behind")
	}
	reconnected := dialPeer(t, peer)
	if err := reconnected.CleanupBootstrap(t.Context(), BootstrapDirectory(request.OperationID)); err != nil {
		t.Fatal(err)
	}
	if err := reconnected.CleanupBootstrap(t.Context(), directory); err != nil {
		t.Fatal("cleanup of a directory absent after a lost acknowledgment was not idempotent")
	}
}

func TestProvisionRecoversPartialUploadWithoutOverwritingExistingDirectory(t *testing.T) {
	requireBootstrapFilesystem(t)
	directory := testBootstrapDirectory(t)
	peer := bootstrapRecoveryPeer(t, directory, false)
	// pkg/sftp's test server omits fsync. Advertise it solely to reach the
	// interrupted write; this test never reaches or claims a successful fsync.
	peer.sftpSession = func(channel cryptossh.Channel) {
		server, err := sftp.NewServer(&bootstrapSyncAdvertisement{Channel: channel})
		if err != nil {
			return
		}
		defer server.Close()
		_ = server.Serve()
	}
	client := dialPeer(t, peer)
	request, artifact := bootstrapRecoveryArtifact(directory)
	artifact.Source = &interruptedBootstrapSource{first: true, interrupt: func() {
		disconnectBootstrapPeer(peer)
	}}
	_, err := client.Provision(t.Context(), request, artifact, nil)
	var failure *ProvisionError
	if !errors.As(err, &failure) || !failure.CleanupPending || failure.TemporaryDirectory != directory {
		t.Fatalf("upload interruption lost the exact cleanup path: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || !validPartialUploadName(entries[0].Name()) {
		t.Fatal("interrupted SFTP write did not leave its bounded partial file")
	}
	partial := directory + "/" + entries[0].Name()
	document, err := os.ReadFile(partial)
	if err != nil || len(document) == 0 {
		t.Fatal("fixture did not interrupt an actual partially written upload")
	}
	reconnected := dialPeer(t, peer)
	_, freshArtifact := bootstrapRecoveryArtifact(directory)
	if _, err := reconnected.Provision(t.Context(), request, freshArtifact, nil); !errors.Is(err, ErrExists) {
		t.Fatal("retry reused an existing directory before explicit recovery")
	}
	unchanged, err := os.ReadFile(partial)
	if err != nil || !bytes.Equal(document, unchanged) {
		t.Fatal("retry deleted or replaced a preexisting partial file")
	}
	if err := reconnected.CleanupBootstrap(t.Context(), directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reconnect did not remove the validated partial and exact private directory")
	}
}

func TestBootstrapCleanupRejectsDirectoryOwnerModeAndSymlink(t *testing.T) {
	requireBootstrapFilesystem(t)
	for _, change := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "public_directory", mutate: func(directory string) error { return os.Chmod(directory, 0755) }},
		{name: "special_mode", mutate: func(directory string) error {
			return os.Chmod(directory, 0700|os.ModeSticky)
		}},
		{name: "different_owner", mutate: func(directory string) error { return os.Chown(directory, 1, 1) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			directory := testBootstrapDirectory(t)
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(directory) })
			if err := change.mutate(directory); err != nil {
				t.Fatal(err)
			}
			client := dialPeer(t, newTestPeer(t))
			if err := client.CleanupBootstrap(t.Context(), directory); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unsafe private-directory metadata was accepted: %v", err)
			}
			if _, err := os.Lstat(directory); err != nil {
				t.Fatal("rejected cleanup deleted an untrusted directory")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		directory := testBootstrapDirectory(t)
		target := t.TempDir()
		if err := os.Symlink(target, directory); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(directory) })
		client := dialPeer(t, newTestPeer(t))
		if err := client.CleanupBootstrap(t.Context(), directory); !errors.Is(err, ErrInvalid) {
			t.Fatal("bootstrap symlink was followed during cleanup")
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatal("rejected symlink cleanup changed its target")
		}
	})
}

func requireBootstrapFilesystem(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("root-owned Linux bootstrap filesystem boundary")
	}
}

func bootstrapRecoveryPeer(t *testing.T, directory string, loseCreateReply bool) *testPeer {
	t.Helper()
	peer := newTestPeer(t)
	t.Cleanup(func() {
		entries, _ := os.ReadDir(directory)
		for _, entry := range entries {
			if entry.Name() == "llm-test-runner" || validPartialUploadName(entry.Name()) {
				_ = os.Remove(directory + "/" + entry.Name())
			}
		}
		_ = os.Remove(directory)
	})
	peer.execute = func(command string, channel cryptossh.Channel) {
		status := uint32(1)
		defer func() {
			_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{Status: status}))
		}()
		input, err := io.ReadAll(io.LimitReader(channel, 8192))
		if err != nil || command != "/bin/sh -s" {
			return
		}
		if string(input) == systemProbe {
			_, _ = io.WriteString(channel, "llm-test-system-probe\n0\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\n")
			status = 0
			return
		}
		expected := "set -eu\numask 077\nmkdir -m 0700 -- " + directory + "\n"
		if string(input) != expected || os.Mkdir(directory, 0700) != nil {
			return
		}
		if loseCreateReply {
			disconnectBootstrapPeer(peer)
			return
		}
		status = 0
	}
	return peer
}

func disconnectBootstrapPeer(peer *testPeer) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	for _, connection := range peer.clients {
		_ = connection.Close()
	}
}

func bootstrapRecoveryArtifact(directory string) (runnerinstall.Request, Artifact) {
	program := []byte(strings.Repeat("bootstrap artifact fixture", 32))
	digest := sha256.Sum256(program)
	request := runnerinstall.Request{Contract: jobcontract.CurrentContract,
		OperationID:    strings.TrimPrefix(directory, bootstrapPrefix),
		ArtifactSHA256: hex.EncodeToString(digest[:]), ListenPort: 44917}
	return request, Artifact{OS: "linux", Arch: "amd64", Size: int64(len(program)),
		SHA256: request.ArtifactSHA256, Source: bytes.NewReader(program)}
}

type interruptedBootstrapSource struct {
	first     bool
	interrupt func()
}

func (source *interruptedBootstrapSource) Read(document []byte) (int, error) {
	if source.first {
		source.first = false
		return copy(document, "partial fixture"), nil
	}
	source.interrupt()
	return 0, io.ErrUnexpectedEOF
}

type bootstrapSyncAdvertisement struct {
	cryptossh.Channel
	advertised bool
}

func (channel *bootstrapSyncAdvertisement) Write(document []byte) (int, error) {
	if channel.advertised {
		return channel.Channel.Write(document)
	}
	channel.advertised = true
	if len(document) < 9 || document[4] != 2 {
		return 0, errors.New("expected SFTP version advertisement")
	}
	extension := cryptossh.Marshal(struct{ Name, Version string }{Name: "fsync@openssh.com", Version: "1"})
	updated := append(append([]byte{}, document...), extension...)
	binary.BigEndian.PutUint32(updated[:4], uint32(len(updated)-4))
	if _, err := channel.Channel.Write(updated); err != nil {
		return 0, err
	}
	return len(document), nil
}
