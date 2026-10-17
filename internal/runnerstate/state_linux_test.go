package runnerstate_test

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func TestRuntimeLockProcessHelper(t *testing.T) {
	mode := os.Getenv("LLM_TEST_STATE_LOCK_CHILD")
	if mode == "" {
		return
	}
	state, err := runnerstate.Open(os.Getenv("LLM_TEST_STATE_LOCK_PATH"))
	if errors.Is(err, runnerstate.ErrLocked) {
		os.Exit(73)
	}
	if err != nil {
		os.Exit(74)
	}
	defer state.Close()
	if mode == "hold" {
		_, _ = os.Stdout.WriteString("READY\n")
		time.Sleep(time.Minute)
	}
}

func lockChild(t *testing.T, mode, path string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestRuntimeLockProcessHelper$")
	command.Env = append(os.Environ(), "LLM_TEST_STATE_LOCK_CHILD="+mode, "LLM_TEST_STATE_LOCK_PATH="+path)
	return command
}

func TestRuntimeLockExcludesAnotherProcessAndSurvivesStaleLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	state, err := runnerstate.Open(path)
	if err != nil {
		info, statErr := os.Lstat(path)
		t.Fatalf(
			"open state %q: %v; stat=%+v statErr=%v euid=%d",
			path,
			err,
			info,
			statErr,
			os.Geteuid(),
		)
	}
	defer state.Close()
	before, err := os.Stat(filepath.Join(path, "runner.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lockChild(t, "try", path).Run(); err == nil {
		t.Fatal("second process acquired an active runtime lock")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 73 {
			t.Fatalf("unexpected child result: %v", err)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lockChild(t, "try", path).Run(); err != nil {
		t.Fatalf("stale lock file prevented a new runner: %v", err)
	}
	after, err := os.Stat(filepath.Join(path, "runner.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("lock release replaced its inode")
	}
}

func TestRuntimeLockIsReleasedAfterProcessIsKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	child := lockChild(t, "hold", path)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "READY" {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("child did not acquire its lock")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	state, err := runnerstate.Open(path)
	if err != nil {
		t.Fatalf("killed process retained runtime lock: %v", err)
	}
	defer state.Close()
}

func TestPrivateIdentityWriteDoesNotOverwriteAndRejectsUnsafeReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	state, err := runnerstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	document := []byte(`{"identity":"stable"}`)
	if err := state.WriteNew("identity.json", document); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteNew("identity.json", []byte(`{"identity":"replacement"}`)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing identity could be overwritten: %v", err)
	}
	read, err := state.ReadPrivate("identity.json", int64(len(document)))
	if err != nil || !bytes.Equal(read, document) {
		t.Fatalf("identity changed: %v", err)
	}
	if _, err := state.ReadPrivate("identity.json", int64(len(document)-1)); !errors.Is(err, runnerstate.ErrInvalid) {
		t.Fatal("oversized private file was accepted")
	}
	for _, name := range []string{"", ".", "..", "../identity.json", "/identity.json", "dir/file", "dir\\file"} {
		if err := state.WriteNew(name, document); !errors.Is(err, runnerstate.ErrInvalid) {
			t.Fatalf("unsafe write name %q accepted", name)
		}
		if _, err := state.ReadPrivate(name, 100); !errors.Is(err, runnerstate.ErrInvalid) {
			t.Fatalf("unsafe read name %q accepted", name)
		}
	}
	if err := os.Symlink("identity.json", filepath.Join(path, "alias.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadPrivate("alias.json", 100); err == nil {
		t.Fatal("private identity followed a symlink")
	}
	if err := os.Chmod(filepath.Join(path, "identity.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadPrivate("identity.json", 100); !errors.Is(err, runnerstate.ErrInvalid) {
		t.Fatal("private read accepted world-readable state")
	}
	retained, err := os.ReadFile(filepath.Join(path, "identity.json"))
	if err != nil || !bytes.Equal(retained, document) {
		t.Fatal("rejected private read changed the file")
	}
	available, err := state.AvailableBytes()
	if err != nil || available == 0 {
		t.Fatalf("filesystem availability: %v", err)
	}
}

func TestStateRejectsPublicDirectoriesAndSymlinkAncestorsBeforeCreatingChildren(t *testing.T) {
	base := t.TempDir()
	public := filepath.Join(base, "public")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := runnerstate.Open(public); !errors.Is(err, runnerstate.ErrInvalid) {
		t.Fatalf("public state directory accepted: %v", err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := runnerstate.Open(filepath.Join(alias, "child")); !errors.Is(err, runnerstate.ErrInvalid) {
		t.Fatalf("symlink ancestor accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "child")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected state path created files through the symlink")
	}
}

func TestMutablePrivateControlReplacementRejectsLinksAndRetainsRejectedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	state, err := runnerstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first, second := []byte(`{"paused":true}`), []byte(`{"paused":false}`)
	if err := state.ReplacePrivate("control.json", first); err != nil {
		t.Fatal(err)
	}
	if err := state.ReplacePrivate("control.json", second); err != nil {
		t.Fatal(err)
	}
	if err := state.SyncPrivate("control.json"); err != nil {
		t.Fatal(err)
	}
	read, err := state.ReadPrivate("control.json", 100)
	if err != nil || !bytes.Equal(read, second) {
		t.Fatal("mutable control did not replace its complete document")
	}
	if err := os.Symlink("control.json", filepath.Join(path, "alias.json")); err != nil {
		t.Fatal(err)
	}
	if err := state.ReplacePrivate("alias.json", first); err == nil {
		t.Fatal("mutable replacement accepted a private-file symlink")
	}
	if err := state.SyncPrivate("alias.json"); err == nil {
		t.Fatal("durability confirmation accepted a private-file symlink")
	}
	read, err = state.ReadPrivate("control.json", 100)
	if err != nil || !bytes.Equal(read, second) {
		t.Fatal("rejected replacement changed the link target")
	}
}
