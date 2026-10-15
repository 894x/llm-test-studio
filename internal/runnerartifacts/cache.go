package runnerartifacts

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/fileconfig"
)

type Cache struct {
	root     string
	rootInfo os.FileInfo
	source   Source
}

// Artifact owns the already verified open file. Its bytes remain subject to the
// independent upload digest check; callers must Close it after the operation.
type Artifact struct {
	Manifest Manifest
	Entry    Entry
	Source   *os.File
}

func (artifact *Artifact) Close() error {
	if artifact == nil || artifact.Source == nil {
		return nil
	}
	return artifact.Source.Close()
}

func NewCache(root string, source Source) (*Cache, error) {
	validRoot := filepath.IsAbs(root) && filepath.Clean(root) == root
	if !validRoot || source == nil {
		return nil, ErrInvalid
	}
	info, err := directoryIdentity(root)
	if err != nil {
		return nil, ErrInvalid
	}
	return &Cache{root: root, rootInfo: info, source: source}, nil
}

func (cache *Cache) Open(ctx context.Context, release, arch string) (*Artifact, error) {
	if ctx == nil || !ValidRelease(release) {
		return nil, ErrInvalid
	}
	name, err := FileName(arch)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if err := cache.checkRoot(); err != nil {
		return nil, err
	}
	var result *Artifact
	directory := cache.releaseDirectory(release)
	err = fileconfig.WithExclusiveLock(ctx, directory+".lock", func() error {
		if err := cache.checkRoot(); err != nil {
			return err
		}
		if err := ensureReleaseDirectory(directory); err != nil {
			return err
		}
		manifest, err := cache.manifest(ctx, directory, release)
		if err != nil {
			return err
		}
		entry := manifest.Entries[0]
		if arch == "arm64" {
			entry = manifest.Entries[1]
		}
		path := filepath.Join(directory, entry.SHA256)
		file, err := verifyFile(ctx, path, entry)
		if errors.Is(err, os.ErrNotExist) {
			if err := cache.download(
				ctx,
				path,
				release,
				entry,
			); err != nil {
				return err
			}
			file, err = verifyFile(ctx, path, entry)
		}
		if err != nil {
			return err
		}
		if entry.FileName != name {
			_ = file.Close()
			return ErrIntegrity
		}
		result = &Artifact{Manifest: manifest, Entry: entry, Source: file}
		return nil
	})
	if err != nil {
		if result != nil {
			_ = result.Close()
		}
		return nil, err
	}
	return result, nil
}

func (cache *Cache) releaseDirectory(release string) string {
	digest := sha256.Sum256([]byte(release))
	return filepath.Join(cache.root, hex.EncodeToString(digest[:]))
}

func directoryIdentity(path string) (os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() {
		return nil, ErrIntegrity
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ErrIntegrity
	}
	return opened, nil
}

func (cache *Cache) checkRoot() error {
	root, err := directoryIdentity(cache.root)
	if err != nil || !os.SameFile(root, cache.rootInfo) {
		return ErrIntegrity
	}
	return nil
}

func (cache *Cache) manifest(ctx context.Context, directory, release string) (Manifest, error) {
	path := filepath.Join(directory, ManifestName)
	file, err := openRegular(path)
	if err == nil {
		defer file.Close()
		return readManifest(ctx, file, release)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	input, err := cache.source.Fetch(ctx, release, ManifestName)
	if err != nil {
		return Manifest{}, err
	}
	defer input.Close()
	manifest, err := readManifest(ctx, input, release)
	if err != nil {
		return Manifest{}, err
	}
	document, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, ErrInvalid
	}
	if err := fileconfig.WriteAtomically(ctx, path, append(document, '\n')); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readManifest(ctx context.Context, reader io.Reader, release string) (Manifest, error) {
	document, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, source: reader}, MaxManifestBytes+1))
	if err != nil {
		return Manifest{}, sourceFailure(ctx)
	}
	manifest, err := DecodeManifest(document)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Release != release {
		return Manifest{}, ErrIntegrity
	}
	return manifest, nil
}

func (cache *Cache) download(ctx context.Context, path, release string, entry Entry) error {
	input, err := cache.source.Fetch(ctx, release, entry.FileName)
	if err != nil {
		return err
	}
	defer input.Close()
	file, err := os.CreateTemp(filepath.Dir(path), ".runner-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	digest := sha256.New()
	bounded := io.LimitReader(&contextReader{ctx: ctx, source: input}, entry.Size+1)
	written, err := io.Copy(io.MultiWriter(file, digest), bounded)
	if err != nil {
		return sourceFailure(ctx)
	}
	if written != entry.Size || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
		return ErrIntegrity
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	verified, err := verifyFile(ctx, temporary, entry)
	if err != nil {
		return err
	}
	if err := verified.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishFile(temporary, path)
}

func verifyFile(ctx context.Context, path string, entry Entry) (*os.File, error) {
	file, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || info.Size() != entry.Size {
		return nil, ErrIntegrity
	}
	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(&contextReader{ctx: ctx, source: file}, entry.Size+1))
	if err != nil {
		return nil, sourceFailure(ctx)
	}
	if read != entry.Size || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
		return nil, ErrIntegrity
	}
	if err := verifyELF(file, entry.Arch); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	valid = true
	return file, nil
}

func verifyELF(file *os.File, arch string) error {
	// Only target metadata is needed. Do not parse sections or decompress an
	// untrusted ELF string table, whose expanded size is outside the byte budget.
	var header [64]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return ErrIntegrity
	}
	magic := string(header[:4]) == "\x7fELF"
	encoding := header[4] == 2 && header[5] == 1 && header[6] == 1
	version := binary.LittleEndian.Uint32(header[20:24]) == 1 && binary.LittleEndian.Uint16(header[52:54]) == 64
	fileType := binary.LittleEndian.Uint16(header[16:18])
	executable := fileType == 2 || fileType == 3
	linux := header[7] == 0 || header[7] == 3
	expectedMachine := uint16(62)
	if arch == "arm64" {
		expectedMachine = 183
	}
	validMachine := binary.LittleEndian.Uint16(header[18:20]) == expectedMachine
	validTarget := magic && encoding && version && executable && linux && validMachine
	if !validTarget {
		return ErrIntegrity
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}
