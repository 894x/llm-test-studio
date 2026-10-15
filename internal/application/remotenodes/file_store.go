package remotenodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/fileconfig"
)

// FileStore uses the same cross-process advisory lock and durable replacement
// primitives as authored catalogs. nodes.json belongs to the user config root.
type FileStore struct {
	path      string
	rootInfo  os.FileInfo
	writeFile func(context.Context, string, []byte) error
}

func NewFileStore(path string) (*FileStore, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrInvalid
	}
	// Bootstrap owns creation and durability of the application directory.
	// Refuse an absent root instead of implicitly creating an unsynced chain
	// immediately before a credential intent and keyring write.
	root, err := directoryIdentity(filepath.Dir(path))
	if err != nil {
		return nil, ErrInvalid
	}
	return &FileStore{path: path, rootInfo: root, writeFile: fileconfig.WriteAtomically}, nil
}

func (store *FileStore) Read(ctx context.Context) (Configuration, error) {
	var result Configuration
	err := store.withConfiguration(ctx, func(config *Configuration) error { result = *config; return nil })
	return result, err
}

func (store *FileStore) withConfiguration(ctx context.Context, action func(*Configuration) error) error {
	validOperation := store != nil && ctx != nil && action != nil
	if !validOperation {
		return ErrInvalid
	}
	root, err := directoryIdentity(filepath.Dir(store.path))
	if err != nil || !os.SameFile(root, store.rootInfo) {
		return ErrConflict
	}
	return fileconfig.WithExclusiveLock(ctx, store.path+".lock", func() error {
		root, err := directoryIdentity(filepath.Dir(store.path))
		if err != nil || !os.SameFile(root, store.rootInfo) {
			return ErrConflict
		}
		config, err := store.load(ctx)
		if err != nil {
			return err
		}
		return action(&config)
	})
}

func directoryIdentity(path string) (os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() {
		return nil, ErrInvalid
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ErrConflict
	}
	// File.Stat eagerly captures Windows file IDs. Lstat may defer that lookup
	// until SameFile, after a renamed directory's old path has been reused.
	return opened, nil
}

func (store *FileStore) load(ctx context.Context) (Configuration, error) {
	if err := ctx.Err(); err != nil {
		return Configuration{}, err
	}
	document, err := store.readBytes()
	if errors.Is(err, os.ErrNotExist) {
		return emptyConfiguration(), nil
	}
	if err != nil {
		return Configuration{}, err
	}
	return decodeConfiguration(document)
}

func (store *FileStore) readBytes() ([]byte, error) {
	before, err := os.Lstat(store.path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > MaxFileBytes {
		return nil, ErrInvalid
	}
	file, err := os.Open(store.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, ErrConflict
	}
	document, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(document) > MaxFileBytes {
		return nil, ErrInvalid
	}
	return document, nil
}

func (store *FileStore) persist(ctx context.Context, config *Configuration) error {
	config.Revision++
	return store.write(ctx, *config)
}

func (store *FileStore) write(ctx context.Context, config Configuration) error {
	if err := config.Validate(); err != nil {
		return err
	}
	document, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return ErrInvalid
	}
	document = append(document, '\n')
	if len(document) > MaxFileBytes {
		return ErrInvalid
	}
	if err := store.writeFile(ctx, store.path, document); err != nil {
		visible, readErr := store.readBytes()
		if readErr != nil || !bytes.Equal(visible, document) {
			return err
		}
		// Visibility after rename is insufficient proof of persistence. Repeat
		// the same replacement under the lock to confirm file and directory sync.
		confirm, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := store.writeFile(confirm, store.path, document); err != nil {
			return ErrCommitUnconfirmed
		}
	}
	return nil
}
