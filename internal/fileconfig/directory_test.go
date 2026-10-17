package fileconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestEnsurePrivateDirectoryCreatesNestedRootOnceAndKeepsExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configuration", "application")
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := EnsurePrivateDirectory(context.Background(), path); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatal("created directory is not private")
	}
	retained := filepath.Join(path, "retained")
	if err := os.WriteFile(retained, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(retained)
	if err != nil || string(content) != "preserved" {
		t.Fatal("existing data was replaced")
	}
}

func TestEnsurePrivateDirectoryRejectsFilesLinksAndCancellation(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "existing-file")
	if err := os.WriteFile(file, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(context.Background(), file); err == nil {
		t.Fatal("regular file accepted as directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(root, "cancelled")
	if err := EnsurePrivateDirectory(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled operation created directory")
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(root, link); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if err := EnsurePrivateDirectory(context.Background(), link); err == nil {
		t.Fatal("symlink accepted as application directory")
	}
}

func TestEnsurePrivateDirectoryRetryConfirmsVisibleAncestorAfterSyncFailure(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "published-before-failure")
	path := filepath.Join(parent, "application")
	failure := errors.New("injected parent sync failure")
	confirmations := 0
	confirm := func(path string) error {
		return syncDirectoryChain(path, func(name string) error {
			if name == root {
				confirmations++
				if confirmations == 1 {
					return failure
				}
			}
			return nil
		})
	}
	if err := ensurePrivateDirectory(context.Background(), path, confirm); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := os.Stat(parent); err != nil {
		t.Fatal("test did not retain the directory published before sync failed")
	}
	if err := ensurePrivateDirectory(context.Background(), path, confirm); err != nil {
		t.Fatal(err)
	}
	if confirmations != 2 {
		t.Fatal("retry skipped the visible ancestor's failed durability confirmation")
	}
}
