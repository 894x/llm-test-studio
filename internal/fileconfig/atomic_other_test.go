//go:build !windows

package fileconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicallyPreservesExistingDirectoryPermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "application")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomically(context.Background(), filepath.Join(directory, "models.json"), []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("directory mode = %o, want 755", got)
	}
}
