package ssh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
	"github.com/pkg/sftp"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestUpgradeUsesFixedNonSecretCommandsAndRequest(t *testing.T) {
	request, _ := upgradeFixtureArtifact()
	directory := UpgradeDirectory(request.OperationID)
	for _, invalid := range []string{"", "../other", request.OperationID + ";id", request.OperationID + "\n"} {
		if UpgradeDirectory(invalid) != "" || upgradeLaunchCommand(invalid, "upgrade") != "" {
			t.Fatal("untrusted operation ID entered a staging path or command")
		}
	}
	if upgradeLaunchCommand(request.OperationID, "upgrade; id") != "" {
		t.Fatal("arbitrary command entered the upgrade service")
	}
	for _, command := range []string{"upgrade", "retry-upgrade", "recover-upgrade"} {
		wire := upgradeLaunchCommand(request.OperationID, command)
		fixed := strings.HasSuffix(wire, " -- "+directory+"/llm-test-runner "+command) &&
			strings.Contains(wire, " --unit="+upgradeUnitName(request.OperationID)) &&
			strings.Contains(wire, " --property=StandardInput=file:"+directory+"/request.json")
		for _, forbidden := range []string{fakePassword, "node-token", "--wait", "--pipe", "--pty", "Environment=", " stop "} {
			if strings.Contains(wire, forbidden) {
				t.Fatal("upgrade command exposed a credential or tied the service to SSH")
			}
		}
		if !fixed {
			t.Fatal("upgrade command lost its exact operation-derived paths")
		}
	}
	document, _ := json.Marshal(request)
	decoded, err := runnerinstall.DecodeUpgradeRequest(bytes.NewReader(document))
	if err != nil || decoded != request || strings.Contains(string(document), fakePassword) {
		t.Fatal("staged request is not the exact non-secret upgrade contract")
	}
	if err := (runnerinstall.UpgradeProgress{OperationID: request.OperationID, Stage: "stopping"}).Validate(); err != nil {
		t.Fatal("the durable pre-stop checkpoint is missing from progress")
	}
}

func TestUpgradeReadsExistingJournalWithoutExecutingOrConsumingArtifact(t *testing.T) {
	fixture := newUpgradeFixture(t)
	operation := fixture.operation("backed_up")
	operation.Restorable = true
	operation.ErrorCode = runnerinstall.ErrorCode(runnerinstall.ErrService)
	fixture.journal(operation)
	client := dialPeer(t, fixture.peer)
	artifact := fixture.artifact
	artifact.Source = rejectingUpgradeReader{}
	observed, err := client.Upgrade(t.Context(), fixture.request, artifact, nil)
	if err != nil || observed.Stage != operation.Stage || observed.ErrorCode != operation.ErrorCode {
		t.Fatalf("existing journal was not returned unchanged: %v", err)
	}
	if fixture.execs.Load() != 0 {
		t.Fatal("reading an existing operation started an SSH command")
	}
	changed := fixture.request
	changed.NodeID = "33333333-3333-4333-8333-333333333333"
	_, err = client.RetryUpgrade(t.Context(), changed, artifact, nil)
	if !errors.Is(err, runnerinstall.ErrConflict) || fixture.execs.Load() != 0 {
		t.Fatal("a changed same-ID request reached an execution command")
	}
}

func TestUpgradeJournalReaderRejectsUntrustedMetadataAndDocuments(t *testing.T) {
	for _, scenario := range []string{"public", "owner", "symlink", "unknown_field", "wrong_id"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			operation := fixture.operation("checking")
			fixture.journal(operation)
			name := runnerinstall.InstallPath + "/upgrade-" + fixture.request.OperationID + ".json"
			fixture.fs.mu.Lock()
			entry := fixture.fs.entries[name]
			switch scenario {
			case "public":
				entry.mode = 0644
			case "owner":
				entry.owner = 1000
			case "symlink":
				entry.mode = os.ModeSymlink | 0600
			case "unknown_field":
				entry.data = append(entry.data[:len(entry.data)-1], []byte(`,"secret":"not-accepted"}`)...)
			case "wrong_id":
				operation.Request.OperationID = "66666666-6666-4666-8666-666666666666"
				entry.data, _ = json.Marshal(operation)
			}
			fixture.fs.entries[name] = entry
			fixture.fs.mu.Unlock()
			client := dialPeer(t, fixture.peer)
			if _, err := client.UpgradeOperation(t.Context(), fixture.request.OperationID); err == nil {
				t.Fatal("untrusted journal entered the application")
			}
			if fixture.execs.Load() != 0 {
				t.Fatal("journal validation executed an SSH command")
			}
		})
	}
}

func TestUpgradeRejectsUnsafeStagingWithoutReplacingFiles(t *testing.T) {
	for _, scenario := range []string{"public_directory", "special_directory", "foreign_directory", "symlink_directory", "public_program", "wrong_program", "wrong_request", "unknown_file"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			directory := UpgradeDirectory(fixture.request.OperationID)
			fixture.fs.mu.Lock()
			entry := fixture.fs.entries[directory]
			switch scenario {
			case "public_directory":
				entry.mode = os.ModeDir | 0755
			case "special_directory":
				entry.mode |= os.ModeSticky
			case "foreign_directory":
				entry.owner = 1000
			case "symlink_directory":
				entry.mode = os.ModeSymlink | 0700
			case "public_program":
				program := fixture.fs.entries[directory+"/llm-test-runner"]
				program.mode = 0755
				fixture.fs.entries[directory+"/llm-test-runner"] = program
			case "wrong_program", "wrong_request":
				name := directory + "/llm-test-runner"
				if scenario == "wrong_request" {
					name = directory + "/request.json"
				}
				file := fixture.fs.entries[name]
				file.data[0] ^= 1
				fixture.fs.entries[name] = file
			case "unknown_file":
				fixture.fs.entries[directory+"/unrelated"] = upgradeFixtureEntry{data: []byte("retain"), mode: 0600}
			}
			fixture.fs.entries[directory] = entry
			fixture.fs.mu.Unlock()
			before := fixture.fs.snapshot()
			client := dialPeer(t, fixture.peer)
			if _, err := client.Upgrade(t.Context(), fixture.request, fixture.artifact, nil); err == nil {
				t.Fatal("unsafe staged files were accepted")
			}
			if fixture.launches.Load() != 0 || before != fixture.fs.snapshot() {
				t.Fatal("rejected staging was executed, overwritten, or deleted")
			}
		})
	}
}

type rejectingUpgradeReader struct{}

func (rejectingUpgradeReader) Read([]byte) (int, error) {
	return 0, errors.New("artifact must not be read")
}

func upgradeFixtureArtifact() (runnerinstall.UpgradeRequest, Artifact) {
	program := []byte("verified upgrade boundary fixture")
	digest := sha256.Sum256(program)
	request := runnerinstall.UpgradeRequest{
		Contract: jobcontract.CurrentContract, OperationID: provisionOperationID,
		NodeID: "11111111-1111-4111-8111-111111111111", InstallationID: "22222222-2222-4222-8222-222222222222",
		ExpectedBuildID: strings.Repeat("a", 64), ArtifactSHA256: hex.EncodeToString(digest[:]),
	}
	return request, Artifact{OS: "linux", Arch: "amd64", Size: int64(len(program)),
		SHA256: request.ArtifactSHA256, Source: bytes.NewReader(program)}
}

type upgradeFixture struct {
	peer     *testPeer
	fs       *upgradeFixtureFS
	request  runnerinstall.UpgradeRequest
	artifact Artifact
	execs    atomic.Int32
	launches atomic.Int32
	active   atomic.Bool
	onLaunch func(string) uint32
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	t.Helper()
	request, artifact := upgradeFixtureArtifact()
	fixture := &upgradeFixture{peer: newTestPeer(t), fs: &upgradeFixtureFS{entries: map[string]upgradeFixtureEntry{}},
		request: request, artifact: artifact}
	for _, name := range []string{"/", "/var", "/var/tmp", "/var/lib"} {
		fixture.fs.put(name, nil, os.ModeDir|0755)
	}
	fixture.fs.put(runnerinstall.InstallPath, nil, os.ModeDir|0700)
	directory := UpgradeDirectory(request.OperationID)
	fixture.fs.put(directory, nil, os.ModeDir|0700)
	document, _ := json.Marshal(request)
	fixture.fs.put(directory+"/request.json", document, 0600)
	fixture.fs.put(directory+"/llm-test-runner", []byte("verified upgrade boundary fixture"), 0700)
	fixture.peer.sftpSession = func(channel cryptossh.Channel) {
		server := sftp.NewRequestServer(channel, sftp.Handlers{
			FileGet: fixture.fs, FilePut: fixture.fs, FileCmd: fixture.fs, FileList: fixture.fs,
		})
		defer server.Close()
		_ = server.Serve()
	}
	fixture.peer.execute = func(command string, channel cryptossh.Channel) {
		fixture.execs.Add(1)
		status := uint32(0)
		defer func() {
			_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{Status: status}))
		}()
		switch {
		case command == "/bin/sh -s":
			input, _ := io.ReadAll(io.LimitReader(channel, 8192))
			if string(input) != systemProbe {
				status = 1
				return
			}
			_, _ = io.WriteString(channel, "llm-test-system-probe\n0\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\n")
		case strings.HasPrefix(command, "systemctl show "):
			if fixture.active.Load() {
				_, _ = io.WriteString(channel, "LoadState=loaded\nActiveState=active\nSubState=running\n")
				return
			}
			_, _ = io.WriteString(channel, "LoadState=not-found\nActiveState=inactive\nSubState=dead\n")
		case command == "sync -f -- "+directory:
		case command == "sha256sum -- '"+directory+"/request.json'" ||
			command == "sha256sum -- '"+directory+"/llm-test-runner'":
			name := strings.TrimSuffix(strings.TrimPrefix(command, "sha256sum -- '"), "'")
			fixture.fs.mu.Lock()
			entry, exists := fixture.fs.entries[name]
			digest := sha256.Sum256(entry.data)
			fixture.fs.mu.Unlock()
			if !exists {
				status = 1
				return
			}
			_, _ = io.WriteString(channel, hex.EncodeToString(digest[:])+"  "+name+"\n")
		case strings.HasPrefix(command, "systemd-run "):
			valid := false
			for _, verb := range []string{"upgrade", "retry-upgrade", "recover-upgrade"} {
				valid = valid || command == upgradeLaunchCommand(request.OperationID, verb)
			}
			if !valid {
				status = 1
				return
			}
			fixture.launches.Add(1)
			if fixture.onLaunch != nil {
				status = fixture.onLaunch(command)
			}
		default:
			status = 1
		}
	}
	return fixture
}

func (fixture *upgradeFixture) operation(stage string) runnerinstall.UpgradeOperation {
	return runnerinstall.UpgradeOperation{Request: fixture.request, Stage: stage, ChangedAt: time.Now().UTC()}
}

func (fixture *upgradeFixture) terminal(stage string) runnerinstall.UpgradeOperation {
	operation := fixture.operation(stage)
	runner := runnerMetadata()
	runner.EngineBuildID = fixture.request.ArtifactSHA256
	operation.ResumeAttempted = stage == "completed"
	if stage == "rolled_back" {
		runner.EngineBuildID = fixture.request.ExpectedBuildID
	}
	operation.Result = &runnerinstall.UpgradeResult{Contract: jobcontract.CurrentContract,
		OperationID: fixture.request.OperationID, Runner: runner, ObservedAt: time.Now().UTC()}
	return operation
}

func (fixture *upgradeFixture) journal(operation runnerinstall.UpgradeOperation) {
	document, _ := json.Marshal(operation)
	fixture.fs.put(runnerinstall.InstallPath+"/upgrade-"+fixture.request.OperationID+".json", document, 0600)
}

// The fixture has no access to host paths. Both the fake SSH executor and SFTP
// share this private in-memory filesystem, including ownership and mode facts.
type upgradeFixtureFS struct {
	mu      sync.Mutex
	entries map[string]upgradeFixtureEntry
}

type upgradeFixtureEntry struct {
	data  []byte
	mode  os.FileMode
	owner uint32
}

func (fs *upgradeFixtureFS) put(name string, data []byte, mode os.FileMode) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.entries[name] = upgradeFixtureEntry{data: bytes.Clone(data), mode: mode}
}

func (fs *upgradeFixtureFS) snapshot() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	values := map[string]any{}
	for name, entry := range fs.entries {
		values[name] = []any{entry.data, uint32(entry.mode), entry.owner}
	}
	document, _ := json.Marshal(values)
	return string(document)
}

func (fs *upgradeFixtureFS) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	entry, ok := fs.entries[request.Filepath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return bytes.NewReader(bytes.Clone(entry.data)), nil
}

func (*upgradeFixtureFS) Filewrite(*sftp.Request) (io.WriterAt, error) { return nil, os.ErrPermission }

func (fs *upgradeFixtureFS) Filecmd(request *sftp.Request) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if request.Method != "Remove" {
		return os.ErrPermission
	}
	if _, ok := fs.entries[request.Filepath]; !ok {
		return os.ErrNotExist
	}
	delete(fs.entries, request.Filepath)
	return nil
}

func (fs *upgradeFixtureFS) Lstat(request *sftp.Request) (sftp.ListerAt, error) {
	return fs.Filelist(request)
}

func (fs *upgradeFixtureFS) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	entry, ok := fs.entries[request.Filepath]
	if !ok {
		return nil, os.ErrNotExist
	}
	entries := upgradeFixtureListing{}
	if request.Method == "List" {
		for name, child := range fs.entries {
			if name != request.Filepath && path.Dir(name) == request.Filepath {
				entries = append(entries, upgradeFixtureInfo{name: path.Base(name), entry: child})
			}
		}
		return entries, nil
	}
	return append(entries, upgradeFixtureInfo{name: path.Base(request.Filepath), entry: entry}), nil
}

type upgradeFixtureListing []os.FileInfo

func (entries upgradeFixtureListing) ListAt(destination []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(entries)) {
		return 0, io.EOF
	}
	n := copy(destination, entries[offset:])
	if int(offset)+n == len(entries) {
		return n, io.EOF
	}
	return n, nil
}

type upgradeFixtureInfo struct {
	name  string
	entry upgradeFixtureEntry
}

func (info upgradeFixtureInfo) Name() string       { return info.name }
func (info upgradeFixtureInfo) Size() int64        { return int64(len(info.entry.data)) }
func (info upgradeFixtureInfo) Mode() os.FileMode  { return info.entry.mode }
func (info upgradeFixtureInfo) ModTime() time.Time { return time.Unix(1, 0).UTC() }
func (info upgradeFixtureInfo) IsDir() bool        { return info.entry.mode.IsDir() }
func (upgradeFixtureInfo) Sys() any                { return nil }
func (info upgradeFixtureInfo) Uid() uint32        { return info.entry.owner }
func (upgradeFixtureInfo) Gid() uint32             { return 0 }
