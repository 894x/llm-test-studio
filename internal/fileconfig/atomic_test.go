package fileconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomicallyKeepsOldFileWhenInstallFails(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.json")
	if err := os.WriteFile(path, []byte("old payload\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installFailure := errors.New("injected install failure")

	err := writeAtomically(context.Background(), path, []byte("new payload\n"), func(_, _ string) error {
		return installFailure
	})
	if !errors.Is(err, installFailure) {
		t.Fatalf("writeAtomically() error = %v, want wrapped %v", err, installFailure)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old payload\n" {
		t.Fatalf("destination contents = %q, want old payload", raw)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config-") {
			t.Fatalf("temporary file %q was not cleaned up", entry.Name())
		}
	}
}

func TestWriteAtomicallyReplacesCompletePayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "channels.json")
	if err := WriteAtomically(context.Background(), path, []byte("complete payload\n")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "complete payload\n" {
		t.Fatalf("destination contents = %q", raw)
	}
}

func TestWriteAtomicallyCancelledContextKeepsOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	if err := os.WriteFile(path, []byte("old payload\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WriteAtomically(ctx, path, []byte("new payload\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteAtomically() error = %v, want %v", err, context.Canceled)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old payload\n" {
		t.Fatalf("destination contents = %q, want old payload", raw)
	}
}

func TestWriteAtomicallyRejectsInvalidInputs(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "models.json")
	for _, test := range []struct {
		name    string
		ctx     context.Context
		path    string
		payload []byte
	}{
		{name: "nil context", path: absolute, payload: []byte("payload")},
		{name: "relative path", ctx: context.Background(), path: "models.json", payload: []byte("payload")},
		{name: "empty payload", ctx: context.Background(), path: absolute},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := WriteAtomically(test.ctx, test.path, test.payload); err == nil {
				t.Fatal("WriteAtomically() error = nil, want invalid input error")
			}
		})
	}
}
