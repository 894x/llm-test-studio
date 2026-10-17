package runnerartifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type BuildMetadata struct {
	Release      string
	SourceCommit string
	SourceDirty  bool
}

// Describe reads both previously built executables, checking their ELF target
// and exact content. It neither builds code nor guesses source metadata.
func Describe(ctx context.Context, directory string, metadata BuildMetadata) (Manifest, error) {
	manifest := Manifest{
		Format: CurrentFormat, Contract: jobcontract.CurrentContract, Release: metadata.Release,
		SourceCommit: metadata.SourceCommit, SourceDirty: metadata.SourceDirty, Entries: []Entry{},
	}
	if ctx == nil || !filepath.IsAbs(directory) {
		return Manifest{}, ErrInvalid
	}
	for _, arch := range []string{"amd64", "arm64"} {
		entry, err := describeEntry(ctx, directory, arch)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, manifest.Validate()
}

func describeEntry(ctx context.Context, directory, arch string) (Entry, error) {
	name, err := FileName(arch)
	if err != nil {
		return Entry{}, err
	}
	file, err := openRegular(filepath.Join(directory, name))
	if err != nil {
		return Entry{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Entry{}, err
	}
	if info.Size() <= 0 || info.Size() > MaxArtifactBytes {
		return Entry{}, ErrIntegrity
	}
	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(&contextReader{ctx: ctx, source: file}, info.Size()+1))
	if err != nil {
		return Entry{}, sourceFailure(ctx)
	}
	if read != info.Size() || verifyELF(file, arch) != nil {
		return Entry{}, ErrIntegrity
	}
	return Entry{OS: "linux", Arch: arch, FileName: name, Size: read, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}
