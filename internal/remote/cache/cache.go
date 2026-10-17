// Package cache stores verified remote documents independently of SQLite.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrInvalid   = errors.New("invalid_remote_document_cache_request")
	ErrIntegrity = errors.New("remote_document_cache_integrity_failed")
)

type Cache struct {
	root     string
	identity os.FileInfo
	publish  func(string, string) error
}

// New requires an already prepared private directory. Names come exclusively
// from validated content hashes, never remote paths or display names.
func New(root string) (*Cache, error) {
	validPath := filepath.IsAbs(root) && filepath.Clean(root) == root && filepath.Dir(root) != root
	if !validPath {
		return nil, ErrInvalid
	}
	info, err := directoryIdentity(root)
	if err != nil {
		return nil, ErrInvalid
	}
	return &Cache{root: root, identity: info, publish: publishFile}, nil
}

// Read resumes a private partial file and returns bytes only after validation,
// file sync and atomic durable publication. Existing complete files are checked
// without calling fetch. Errors preserve partials and never reset corrupt data.
func (cache *Cache) Read(
	ctx context.Context,
	reference jobcontract.ReportReference,
	fetch func(context.Context, io.ReadWriteSeeker) error,
) ([]byte, error) {
	validIdentity := domain.IsUUID(reference.ID) && jobcontract.IsSHA256(reference.SHA256)
	validKind := reference.Kind == jobcontract.KindRun && domain.IsUUID(reference.RunID)
	limit := int64(jobcontract.MaxRunReportBytes)
	if reference.Kind == jobcontract.KindPerformance {
		validKind = reference.RunID == ""
		limit = jobcontract.MaxPerformanceResultBytes
	}
	validSize := reference.Size > 0 && reference.Size <= limit
	validReference := validIdentity && validKind && validSize
	if ctx == nil || !validReference {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cache.checkRoot(); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(cache.root, reference.SHA256+".lock")
	if _, err := regularInfo(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var result []byte
	err := fileconfig.WithExclusiveLock(ctx, lockPath, func() error {
		if err := cache.checkRoot(); err != nil {
			return err
		}
		path := filepath.Join(cache.root, reference.SHA256+".json")
		document, err := cache.readComplete(ctx, path, reference)
		if err == nil {
			result = document
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if fetch == nil {
			return os.ErrNotExist
		}
		partialPath := filepath.Join(cache.root, reference.SHA256+".partial")
		_, err = cache.download(
			ctx,
			partialPath,
			reference,
			fetch,
		)
		if err != nil {
			return err
		}
		if err := cache.checkRoot(); err != nil {
			return err
		}
		// No other cache writer can publish this hash while its lock is held.
		// An unexpected existing object is retained and explicitly rejected.
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return ErrIntegrity
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := cache.publish(partialPath, path); err != nil {
			return err
		}
		result, err = cache.readComplete(ctx, path, reference)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (cache *Cache) checkRoot() error {
	info, err := directoryIdentity(cache.root)
	if err != nil || !os.SameFile(info, cache.identity) {
		return ErrIntegrity
	}
	return nil
}

func directoryIdentity(path string) (os.FileInfo, error) {
	before, err := os.Lstat(path)
	validDirectory := err == nil && before.IsDir() && private(before)
	if !validDirectory {
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

func private(info os.FileInfo) bool {
	// On Windows, callers must prepare a directory under the private user
	// profile. Unix permission bits cannot establish Windows ACL guarantees.
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o077 == 0
}

func regularInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !private(info) {
		return nil, ErrIntegrity
	}
	return info, nil
}

func openPrivate(path string, create bool) (*os.File, error) {
	before, err := regularInfo(path)
	if errors.Is(err, os.ErrNotExist) && create {
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	}
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, ErrIntegrity
	}
	return file, nil
}

func (cache *Cache) readComplete(
	ctx context.Context,
	path string,
	reference jobcontract.ReportReference,
) ([]byte, error) {
	file, err := openPrivate(path, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	document, err := verify(ctx, file, reference)
	if err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := cache.checkRoot(); err != nil {
		return nil, err
	}
	if err := confirmDirectory(cache.root); err != nil {
		return nil, err
	}
	return document, nil
}

func (cache *Cache) download(
	ctx context.Context,
	path string,
	reference jobcontract.ReportReference,
	fetch func(context.Context, io.ReadWriteSeeker) error,
) ([]byte, error) {
	file, err := openPrivate(path, true)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > reference.Size {
		return nil, ErrIntegrity
	}
	bounded := &partialFile{file: file, limit: reference.Size}
	if info.Size() < reference.Size {
		if err := fetch(ctx, bounded); err != nil {
			return nil, errors.Join(err, file.Sync())
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	document, err := verify(ctx, file, reference)
	if err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return document, nil
}

type partialFile struct {
	file  *os.File
	limit int64
}

func (file *partialFile) Read(value []byte) (int, error) { return file.file.Read(value) }

func (file *partialFile) Seek(offset int64, whence int) (int64, error) {
	return file.file.Seek(offset, whence)
}

func (file *partialFile) Write(value []byte) (int, error) {
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	validPosition := position >= 0 && position <= file.limit
	if !validPosition || int64(len(value)) > file.limit-position {
		return 0, ErrIntegrity
	}
	return file.file.Write(value)
}

func verify(ctx context.Context, file *os.File, reference jobcontract.ReportReference) ([]byte, error) {
	info, err := file.Stat()
	if err != nil || info.Size() != reference.Size {
		return nil, ErrIntegrity
	}
	document, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, source: file}, reference.Size+1))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(document)
	if int64(len(document)) != reference.Size || hex.EncodeToString(digest[:]) != reference.SHA256 {
		return nil, ErrIntegrity
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return document, nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(value []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(value)
}
