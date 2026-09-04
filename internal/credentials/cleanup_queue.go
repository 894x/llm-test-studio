package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const cleanupQueueSchemaVersion = 1

// CleanupQueue is a durable non-secret registry of credential identifiers.
// Referenced IDs remain registered; an ID is removed only after its keyring
// entry is confirmed deleted once no authored Channel or immutable Plan
// binding references it.
type CleanupQueue interface {
	Enqueue(context.Context, ...string) error
	List(context.Context) ([]string, error)
	Remove(context.Context, ...string) error
}

type cleanupQueueDocument struct {
	SchemaVersion int      `json:"schema_version"`
	CredentialIDs []string `json:"credential_ids"`
}

// FileCleanupQueue stores only credential IDs. Secrets remain exclusively in
// the operating-system keyring.
type FileCleanupQueue struct {
	path     string
	lockPath string
}

func NewFileCleanupQueue(path string) (*FileCleanupQueue, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("create credential cleanup queue: %w", ErrInvalid)
	}
	return &FileCleanupQueue{path: path, lockPath: path + ".lock"}, nil
}

func (queue *FileCleanupQueue) Enqueue(ctx context.Context, ids ...string) error {
	if queue == nil || ctx == nil || !validCredentialIDs(ids) {
		return fmt.Errorf("enqueue credential cleanup: %w", ErrInvalid)
	}
	if len(ids) == 0 {
		return nil
	}
	return fileconfig.WithExclusiveLock(ctx, queue.lockPath, func() error {
		document, err := queue.read(ctx)
		if err != nil {
			return err
		}
		set := make(map[string]struct{}, len(document.CredentialIDs)+len(ids))
		for _, id := range document.CredentialIDs {
			set[id] = struct{}{}
		}
		for _, id := range ids {
			set[id] = struct{}{}
		}
		document.CredentialIDs = sortedCredentialIDs(set)
		return queue.write(ctx, document)
	})
}

func (queue *FileCleanupQueue) List(ctx context.Context) ([]string, error) {
	if queue == nil || ctx == nil {
		return nil, fmt.Errorf("list credential cleanup queue: %w", ErrInvalid)
	}
	var ids []string
	err := fileconfig.WithExclusiveLock(ctx, queue.lockPath, func() error {
		document, err := queue.read(ctx)
		if err != nil {
			return err
		}
		ids = append([]string(nil), document.CredentialIDs...)
		return nil
	})
	return ids, err
}

func (queue *FileCleanupQueue) Remove(ctx context.Context, ids ...string) error {
	if queue == nil || ctx == nil || !validCredentialIDs(ids) {
		return fmt.Errorf("remove credential cleanup: %w", ErrInvalid)
	}
	if len(ids) == 0 {
		return nil
	}
	return fileconfig.WithExclusiveLock(ctx, queue.lockPath, func() error {
		document, err := queue.read(ctx)
		if err != nil {
			return err
		}
		set := make(map[string]struct{}, len(document.CredentialIDs))
		for _, id := range document.CredentialIDs {
			set[id] = struct{}{}
		}
		changed := false
		for _, id := range ids {
			if _, exists := set[id]; exists {
				delete(set, id)
				changed = true
			}
		}
		if !changed {
			return nil
		}
		document.CredentialIDs = sortedCredentialIDs(set)
		return queue.write(ctx, document)
	})
}

func (queue *FileCleanupQueue) read(ctx context.Context) (cleanupQueueDocument, error) {
	if err := ctx.Err(); err != nil {
		return cleanupQueueDocument{}, err
	}
	payload, err := os.ReadFile(queue.path)
	if errors.Is(err, os.ErrNotExist) {
		return cleanupQueueDocument{SchemaVersion: cleanupQueueSchemaVersion, CredentialIDs: []string{}}, nil
	}
	if err != nil {
		return cleanupQueueDocument{}, fmt.Errorf("read credential cleanup queue: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var document cleanupQueueDocument
	if err := decoder.Decode(&document); err != nil {
		return cleanupQueueDocument{}, fmt.Errorf("decode credential cleanup queue: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return cleanupQueueDocument{}, fmt.Errorf("decode credential cleanup queue: %w", err)
	}
	if document.SchemaVersion != cleanupQueueSchemaVersion || !validCanonicalCredentialIDs(document.CredentialIDs) {
		return cleanupQueueDocument{}, errors.New("credential cleanup queue is corrupt")
	}
	return document, nil
}

func (queue *FileCleanupQueue) write(ctx context.Context, document cleanupQueueDocument) error {
	payload, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credential cleanup queue: %w", err)
	}
	if err := fileconfig.WriteAtomically(ctx, queue.path, append(payload, '\n')); err != nil {
		return fmt.Errorf("write credential cleanup queue: %w", err)
	}
	return nil
}

// MemoryCleanupQueue is the process-local implementation used by tests and
// embedders that do not persist application state.
type MemoryCleanupQueue struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func NewMemoryCleanupQueue() *MemoryCleanupQueue {
	return &MemoryCleanupQueue{ids: make(map[string]struct{})}
}

func (queue *MemoryCleanupQueue) Enqueue(ctx context.Context, ids ...string) error {
	if queue == nil || ctx == nil || !validCredentialIDs(ids) {
		return fmt.Errorf("enqueue credential cleanup: %w", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, id := range ids {
		queue.ids[id] = struct{}{}
	}
	return nil
}

func (queue *MemoryCleanupQueue) List(ctx context.Context) ([]string, error) {
	if queue == nil || ctx == nil {
		return nil, fmt.Errorf("list credential cleanup queue: %w", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return sortedCredentialIDs(queue.ids), nil
}

func (queue *MemoryCleanupQueue) Remove(ctx context.Context, ids ...string) error {
	if queue == nil || ctx == nil || !validCredentialIDs(ids) {
		return fmt.Errorf("remove credential cleanup: %w", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, id := range ids {
		delete(queue.ids, id)
	}
	return nil
}

func validCredentialIDs(ids []string) bool {
	for _, id := range ids {
		if !domain.IsUUID(id) {
			return false
		}
	}
	return true
}

func validCanonicalCredentialIDs(ids []string) bool {
	if !validCredentialIDs(ids) {
		return false
	}
	for index := 1; index < len(ids); index++ {
		if ids[index-1] >= ids[index] {
			return false
		}
	}
	return true
}

func sortedCredentialIDs(set map[string]struct{}) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
