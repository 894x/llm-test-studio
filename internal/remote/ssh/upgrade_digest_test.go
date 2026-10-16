package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestUpgradeStagedDigestDoesNotDownloadProgram(t *testing.T) {
	for _, stageOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "local_release_present", true: "local_release_removed"}[stageOnly], func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			program := bytes.Repeat([]byte("staged runner bytes\n"), 1<<17)
			digest := sha256.Sum256(program)
			fixture.request.ArtifactSHA256 = hex.EncodeToString(digest[:])
			fixture.artifact.SHA256 = fixture.request.ArtifactSHA256
			fixture.artifact.Size = int64(len(program))
			directory := UpgradeDirectory(fixture.request.OperationID)
			fixture.fs.put(directory+"/llm-test-runner", program, 0700)
			input, err := json.Marshal(fixture.request)
			if err != nil {
				t.Fatal(err)
			}
			fixture.fs.put(directory+"/request.json", input, 0600)
			fixture.journal(fixture.operation("checking"))
			fixture.onLaunch = func(string) uint32 {
				fixture.journal(fixture.terminal("completed"))
				return 0
			}
			counter := &upgradeDigestFileReads{fs: fixture.fs, name: directory + "/llm-test-runner"}
			fixture.peer.sftpSession = func(channel cryptossh.Channel) {
				server := sftp.NewRequestServer(channel, sftp.Handlers{
					FileGet: counter, FilePut: fixture.fs, FileCmd: fixture.fs, FileList: fixture.fs,
				})
				defer server.Close()
				_ = server.Serve()
			}
			artifact := fixture.artifact
			artifact.Source = rejectingUpgradeReader{}
			if stageOnly {
				artifact.Source, artifact.Size = nil, 0
			}
			client := dialPeer(t, fixture.peer)
			operation, err := client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
			if err != nil || operation.Stage != "completed" || fixture.launches.Load() != 1 {
				t.Fatalf("verified same-operation staging could not continue: %v", err)
			}
			if counter.bytes.Load() != 0 || counter.opens.Load() != 0 {
				t.Fatalf("staged verification downloaded program: opens=%d bytes=%d", counter.opens.Load(), counter.bytes.Load())
			}
		})
	}
}

func TestUpgradeStagedDigestRejectsUntrustedOutput(t *testing.T) {
	for _, scenario := range []string{
		"wrong_digest", "wrong_path", "multiple_lines", "missing_newline", "binary_marker",
		"uppercase_digest", "oversize_output", "stderr", "command_failure",
	} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			fixture.journal(fixture.operation("checking"))
			original := fixture.peer.execute
			fixture.peer.execute = func(command string, channel cryptossh.Channel) {
				if !strings.HasPrefix(command, "sha256sum -- '") {
					original(command, channel)
					return
				}
				name := strings.TrimSuffix(strings.TrimPrefix(command, "sha256sum -- '"), "'")
				fixture.fs.mu.Lock()
				entry := fixture.fs.entries[name]
				digest := sha256.Sum256(entry.data)
				fixture.fs.mu.Unlock()
				output := hex.EncodeToString(digest[:]) + "  " + name + "\n"
				status := uint32(0)
				switch scenario {
				case "wrong_digest":
					output = strings.Repeat("0", 64) + output[64:]
				case "wrong_path":
					output = hex.EncodeToString(digest[:]) + "  /other/file\n"
				case "multiple_lines":
					output += output
				case "missing_newline":
					output = strings.TrimSuffix(output, "\n")
				case "binary_marker":
					output = output[:65] + "*" + output[66:]
				case "uppercase_digest":
					output = strings.ToUpper(output[:64]) + output[64:]
				case "oversize_output":
					output = strings.Repeat("x", 4097)
				case "stderr":
					_, _ = io.WriteString(channel.Stderr(), "unexpected diagnostic")
				case "command_failure":
					status = 1
				}
				_, _ = io.WriteString(channel, output)
				_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{Status: status}))
			}
			before := fixture.fs.snapshot()
			artifact := fixture.artifact
			artifact.Source, artifact.Size = nil, 0
			client := dialPeer(t, fixture.peer)
			_, err := client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
			if err == nil || fixture.launches.Load() != 0 || before != fixture.fs.snapshot() {
				t.Fatal("untrusted digest output launched or modified the operation")
			}
		})
	}
}

func TestUpgradeStagedDigestRejectsMetadataChanges(t *testing.T) {
	for _, scenario := range []string{"uid", "gid", "mode", "size", "mtime", "symlink", "directory_owner"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			fixture.journal(fixture.operation("checking"))
			directory := UpgradeDirectory(fixture.request.OperationID)
			name := directory + "/llm-test-runner"
			listing := &upgradeDigestMetadataFS{fs: fixture.fs, name: name, scenario: scenario}
			if scenario == "directory_owner" {
				listing.name = directory
			}
			fixture.peer.sftpSession = func(channel cryptossh.Channel) {
				server := sftp.NewRequestServer(channel, sftp.Handlers{
					FileGet: fixture.fs, FilePut: fixture.fs, FileCmd: fixture.fs, FileList: listing,
				})
				defer server.Close()
				_ = server.Serve()
			}
			original := fixture.peer.execute
			fixture.peer.execute = func(command string, channel cryptossh.Channel) {
				if command == "sha256sum -- '"+name+"'" {
					// The server hashes the original content; only the subsequent
					// SFTP metadata observation changes.
					listing.changed.Store(true)
				}
				original(command, channel)
			}
			before := fixture.fs.snapshot()
			artifact := fixture.artifact
			artifact.Source, artifact.Size = nil, 0
			client := dialPeer(t, fixture.peer)
			_, err := client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
			if err == nil || !listing.changed.Load() || fixture.launches.Load() != 0 || before != fixture.fs.snapshot() {
				t.Fatal("changed digest metadata launched or modified the operation")
			}
		})
	}
}

func TestUpgradeStagedDigestPreservesCancellation(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.journal(fixture.operation("checking"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	original := fixture.peer.execute
	fixture.peer.execute = func(command string, channel cryptossh.Channel) {
		if strings.HasPrefix(command, "sha256sum -- '") {
			cancel()
			<-ctx.Done()
			return
		}
		original(command, channel)
	}
	before := fixture.fs.snapshot()
	artifact := fixture.artifact
	artifact.Source, artifact.Size = nil, 0
	client := dialPeer(t, fixture.peer)
	_, err := client.RetryUpgrade(ctx, fixture.request, artifact, nil)
	if !errors.Is(err, context.Canceled) || fixture.launches.Load() != 0 || before != fixture.fs.snapshot() {
		t.Fatalf("cancelled digest lost cancellation or changed the operation: %v", err)
	}
}

func TestUpgradeStagedDigestRejectsUnownedCommandPaths(t *testing.T) {
	fixture := newUpgradeFixture(t)
	directory := UpgradeDirectory(fixture.request.OperationID)
	for _, name := range []string{
		"/tmp/llm-test-runner", directory + "/other", directory + "/request.json/extra",
		directory + "/../llm-test-runner", directory + ";id/llm-test-runner", directory + "/llm-test-runner'",
	} {
		client := dialPeer(t, fixture.peer)
		err := client.withSFTP(t.Context(), sftpMetadataTimeout, func(remote *sftp.Client) error {
			return client.verifyUpgradeArtifact(t.Context(), remote, Upload{
				Path: name, Mode: 0700, Size: fixture.artifact.Size, SHA256: fixture.artifact.SHA256,
			})
		})
		if !errors.Is(err, ErrInvalid) || fixture.execs.Load() != 0 {
			t.Fatal("unowned path reached an SSH digest command")
		}
	}
}

type upgradeDigestMetadataFS struct {
	fs       *upgradeFixtureFS
	name     string
	scenario string
	changed  atomic.Bool
}

func (fs *upgradeDigestMetadataFS) Lstat(request *sftp.Request) (sftp.ListerAt, error) {
	return fs.Filelist(request)
}

func (fs *upgradeDigestMetadataFS) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	listing, err := fs.fs.Filelist(request)
	if err != nil || request.Method == "List" || request.Filepath != fs.name || !fs.changed.Load() {
		return listing, err
	}
	entries := listing.(upgradeFixtureListing)
	info := entries[0]
	metadata := upgradeDigestFileInfo{
		FileInfo: info, mode: info.Mode(), size: info.Size(), modified: info.ModTime(),
	}
	switch fs.scenario {
	case "uid", "directory_owner":
		metadata.uid = 1000
	case "gid":
		metadata.gid = 1000
	case "mode":
		metadata.mode |= os.ModeSetuid
	case "size":
		metadata.size++
	case "mtime":
		metadata.modified = metadata.modified.Add(time.Second)
	case "symlink":
		metadata.mode = os.ModeSymlink | 0700
	}
	return upgradeFixtureListing{metadata}, nil
}

type upgradeDigestFileInfo struct {
	os.FileInfo
	uid      uint32
	gid      uint32
	mode     os.FileMode
	size     int64
	modified time.Time
}

func (info upgradeDigestFileInfo) Uid() uint32        { return info.uid }
func (info upgradeDigestFileInfo) Gid() uint32        { return info.gid }
func (info upgradeDigestFileInfo) Mode() os.FileMode  { return info.mode }
func (info upgradeDigestFileInfo) Size() int64        { return info.size }
func (info upgradeDigestFileInfo) ModTime() time.Time { return info.modified }

type upgradeDigestFileReads struct {
	fs    *upgradeFixtureFS
	name  string
	opens atomic.Int64
	bytes atomic.Int64
}

func (counter *upgradeDigestFileReads) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	reader, err := counter.fs.Fileread(request)
	if err != nil || request.Filepath != counter.name {
		return reader, err
	}
	counter.opens.Add(1)
	return upgradeDigestCountedReader{ReaderAt: reader, bytes: &counter.bytes}, nil
}

type upgradeDigestCountedReader struct {
	io.ReaderAt
	bytes *atomic.Int64
}

func (reader upgradeDigestCountedReader) ReadAt(destination []byte, offset int64) (int, error) {
	n, err := reader.ReaderAt.ReadAt(destination, offset)
	reader.bytes.Add(int64(n))
	return n, err
}
