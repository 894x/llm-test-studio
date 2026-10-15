package runnerartifacts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Opt-in validation of the same two real build outputs used by release CI.
// This gate contains no provider or SSH credentials and performs no remote I/O.
func TestBuiltReleaseArtifacts(t *testing.T) {
	directory := os.Getenv("LLM_TEST_RUNNER_RELEASE_DIRECTORY")
	if directory == "" {
		t.Skip("explicit local build directory not supplied")
	}
	document, err := os.ReadFile(filepath.Join(directory, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeManifest(document)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewDirectorySource(directory)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewCache(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Entries {
		artifact, err := cache.Open(context.Background(), manifest.Release, entry.Arch)
		if err != nil {
			t.Fatal(err)
		}
		count, err := io.Copy(io.Discard, artifact.Source)
		_ = artifact.Close()
		if err != nil || count != entry.Size {
			t.Fatal("real artifact was not returned from its beginning with exact size")
		}
		t.Logf(
			"linux/%s size=%d sha256=%s dirty=%v",
			entry.Arch,
			count,
			entry.SHA256,
			manifest.SourceDirty,
		)
	}
}
