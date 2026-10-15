package runnerartifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type memorySource struct {
	mu        sync.Mutex
	documents map[string][]byte
	calls     map[string]int
	err       error
}

func (source *memorySource) Fetch(ctx context.Context, release, name string) (io.ReadCloser, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls[name]++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.err != nil {
		return nil, source.err
	}
	document, exists := source.documents[name]
	if !exists {
		return nil, ErrUnavailable
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(document))), nil
}

func fixtureELF(arch string) []byte {
	document := make([]byte, 64)
	copy(document, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(document[16:], 2)
	machine := uint16(62)
	if arch == "arm64" {
		machine = 183
	}
	binary.LittleEndian.PutUint16(document[18:], machine)
	binary.LittleEndian.PutUint32(document[20:], 1)
	binary.LittleEndian.PutUint16(document[52:], 64)
	return document
}

func artifactFixture(t *testing.T) (*Cache, *memorySource, Manifest) {
	t.Helper()
	manifest := Manifest{
		Format: CurrentFormat, Contract: jobcontract.CurrentContract, Release: "v1.2.3",
		SourceCommit: strings.Repeat("a", 40), Entries: []Entry{},
	}
	source := &memorySource{documents: map[string][]byte{}, calls: map[string]int{}}
	for _, arch := range []string{"amd64", "arm64"} {
		document := fixtureELF(arch)
		digest := sha256.Sum256(document)
		name, _ := FileName(arch)
		manifest.Entries = append(manifest.Entries, Entry{OS: "linux", Arch: arch,
			FileName: name, Size: int64(len(document)), SHA256: hex.EncodeToString(digest[:])})
		source.documents[name] = document
	}
	document, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	source.documents[ManifestName] = document
	cache, err := NewCache(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	return cache, source, manifest
}

func TestCacheSharesConcurrentDownloadAndReusesVerifiedArtifactsOffline(t *testing.T) {
	cache, source, manifest := artifactFixture(t)
	other, err := NewCache(cache.root, source)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for index := range 8 {
		current := cache
		if index%2 != 0 {
			current = other
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			artifact, err := current.Open(context.Background(), manifest.Release, "amd64")
			if err == nil {
				document, readErr := io.ReadAll(artifact.Source)
				_ = artifact.Close()
				if readErr != nil || !bytes.Equal(document, fixtureELF("amd64")) {
					err = ErrIntegrity
				}
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if source.calls[ManifestName] != 1 || source.calls[manifest.Entries[0].FileName] != 1 {
		t.Fatalf("concurrent download repeated: %v", source.calls)
	}
	artifact, err := cache.Open(context.Background(), manifest.Release, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	_ = artifact.Close()
	source.err = ErrUnavailable
	artifact, err = other.Open(context.Background(), manifest.Release, "amd64")
	if err != nil || artifact.Manifest.SourceCommit != manifest.SourceCommit {
		t.Fatalf("offline cache: %v", err)
	}
	_ = artifact.Close()
}

func TestCacheRejectsWrongContentWithoutPublishingOrOverwriting(t *testing.T) {
	for _, mutation := range []string{"truncated", "oversized", "digest", "architecture", "not_elf"} {
		t.Run(mutation, func(t *testing.T) {
			cache, source, manifest := artifactFixture(t)
			entry := &manifest.Entries[0]
			document := bytes.Clone(source.documents[entry.FileName])
			switch mutation {
			case "truncated":
				document = document[:len(document)-1]
			case "oversized":
				document = append(document, 0)
			case "digest":
				document[len(document)-1]++
			case "architecture":
				document = fixtureELF("arm64")
			case "not_elf":
				document = bytes.Repeat([]byte{'x'}, 64)
			}
			if mutation == "architecture" || mutation == "not_elf" {
				digest := sha256.Sum256(document)
				entry.SHA256 = hex.EncodeToString(digest[:])
			}
			source.documents[entry.FileName] = document
			source.documents[ManifestName], _ = json.Marshal(manifest)
			artifact, err := cache.Open(context.Background(), manifest.Release, "amd64")
			if artifact != nil || !errors.Is(err, ErrIntegrity) {
				t.Fatalf("invalid artifact accepted: %v", err)
			}
			entries, err := os.ReadDir(cache.releaseDirectory(manifest.Release))
			if err != nil || len(entries) != 1 {
				t.Fatal("invalid artifact or temporary bytes were published")
			}
			if entries[0].Name() != ManifestName {
				t.Fatal("invalid artifact or temporary bytes were published")
			}
		})
	}
	cache, source, manifest := artifactFixture(t)
	artifact, err := cache.Open(context.Background(), manifest.Release, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	path := artifact.Source.Name()
	_ = artifact.Close()
	corrupt := bytes.Repeat([]byte{'z'}, int(manifest.Entries[0].Size))
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Open(context.Background(), manifest.Release, "amd64"); !errors.Is(err, ErrIntegrity) {
		t.Fatal("corrupted cached artifact accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, corrupt) || source.calls[manifest.Entries[0].FileName] != 1 {
		t.Fatal("corrupted cache was silently overwritten or redownloaded")
	}
}

func TestCachedUnsupportedManifestIsRejectedWithoutMutationOrFetch(t *testing.T) {
	cache, source, manifest := artifactFixture(t)
	directory := cache.releaseDirectory(manifest.Release)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	document := bytes.Replace(
		source.documents[ManifestName],
		[]byte(`"format":1`),
		[]byte(`"format":2`),
		1,
	)
	path := filepath.Join(directory, ManifestName)
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Open(context.Background(), manifest.Release, "amd64"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, document) || len(source.calls) != 0 {
		t.Fatal("unsupported cache was changed or retried through a different source")
	}
}

func TestCacheRejectsChangedRootAndCancellation(t *testing.T) {
	cache, _, manifest := artifactFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Open(ctx, manifest.Release, "amd64"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	previous := cache.root + "-previous"
	if err := os.Rename(cache.root, previous); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(previous, cache.root) })
	if err := os.Mkdir(cache.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Open(context.Background(), manifest.Release, "amd64"); !errors.Is(err, ErrIntegrity) {
		t.Fatal("replaced cache root was accepted")
	}
	if err := os.Remove(cache.root); err != nil {
		t.Fatal(err)
	}
}

func TestDescribeAndDirectorySourcePreserveExactBuildMetadata(t *testing.T) {
	cache, source, expected := artifactFixture(t)
	directory := t.TempDir()
	for name, document := range source.documents {
		if err := os.WriteFile(filepath.Join(directory, name), document, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	described, err := Describe(context.Background(), directory, BuildMetadata{
		Release: expected.Release, SourceCommit: expected.SourceCommit, SourceDirty: true,
	})
	validMetadata := described.SourceDirty && described.Entries[1] == expected.Entries[1]
	if err != nil || !validMetadata {
		t.Fatalf("build metadata: %v", err)
	}
	document, _ := json.Marshal(described)
	if err := os.WriteFile(filepath.Join(directory, ManifestName), document, 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := NewDirectorySource(directory)
	if err != nil {
		t.Fatal(err)
	}
	cache.source = local
	artifact, err := cache.Open(context.Background(), expected.Release, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	if artifact.Manifest.SourceCommit != expected.SourceCommit || !artifact.Manifest.SourceDirty {
		t.Fatal("local build lost provenance")
	}
}

func TestCacheReleaseLabelsCannotAliasWindowsPaths(t *testing.T) {
	cache, source, manifest := artifactFixture(t)
	seen := map[string]bool{}
	for _, release := range []string{"v1.2.3", "v1.2.3.", "CON", "con", "NUL.x", "v1-RC", "v1-rc"} {
		manifest.Release = release
		source.documents[ManifestName], _ = json.Marshal(manifest)
		artifact, err := cache.Open(context.Background(), release, "amd64")
		if err != nil {
			t.Fatalf("release %s: %v", release, err)
		}
		path := strings.ToLower(artifact.Source.Name())
		_ = artifact.Close()
		if seen[path] {
			t.Fatal("distinct labels shared a Windows cache path")
		}
		seen[path] = true
	}
}

func TestTargetCheckDoesNotTraverseOrDecompressELFSections(t *testing.T) {
	cache, source, manifest := artifactFixture(t)
	document := fixtureELF("amd64")
	// Deliberately impossible section-table metadata. The check promises only
	// fixed target-header validation, never parsing or executing arbitrary ELF.
	binary.LittleEndian.PutUint64(document[40:], ^uint64(0))
	binary.LittleEndian.PutUint16(document[58:], 64)
	binary.LittleEndian.PutUint16(document[60:], ^uint16(0))
	binary.LittleEndian.PutUint16(document[62:], ^uint16(0))
	digest := sha256.Sum256(document)
	manifest.Entries[0].SHA256 = hex.EncodeToString(digest[:])
	source.documents[manifest.Entries[0].FileName] = document
	source.documents[ManifestName], _ = json.Marshal(manifest)
	artifact, err := cache.Open(context.Background(), manifest.Release, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	read, err := io.ReadAll(artifact.Source)
	if err != nil || !bytes.Equal(read, document) {
		t.Fatal("target check changed bytes or traversed declared section data")
	}
}
