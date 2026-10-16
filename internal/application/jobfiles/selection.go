// Package jobfiles owns temporary file selections for one desktop session.
// Selections are private memory snapshots, never filesystem paths or job assets.
package jobfiles

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const (
	MaxSelectionBytes         = testspec.MaxFileInputBytes
	MaxSelections             = 8
	MaxSelectionTotalBytes    = 8 << 20
	MaxSelectionDocumentBytes = 1_400_000
)

var (
	ErrInvalid  = errors.New("file_input_invalid")
	ErrTooLarge = errors.New("file_input_too_large")
	ErrLimit    = errors.New("file_input_limit")
	ErrBusy     = errors.New("file_input_busy")
	ErrNotFound = errors.New("file_input_not_found")
	ErrClosed   = errors.New("file_input_unavailable")
)

type Selection struct {
	ID        string `json:"selection_id"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
}

type selectionEntry struct {
	metadata Selection
	data     []byte
	owned    bool
	readers  map[*selectionReader]struct{}
}

// Registry serializes imports and charges snapshots until their last reader
// closes. Close also closes borrowed readers; no reader can outlive its owner.
type Registry struct {
	mu        sync.Mutex
	drained   *sync.Cond
	closed    bool
	importing bool
	usedBytes int64
	entries   map[string]*selectionEntry
}

func NewRegistry() *Registry {
	registry := &Registry{entries: map[string]*selectionEntry{}}
	registry.drained = sync.NewCond(&registry.mu)
	return registry
}

// Import accepts the one current bounded DTO. It publishes only validated bytes
// and never retains the base64 document, a filename, or the caller's buffer.
func (registry *Registry) Import(ctx context.Context, document string) (Selection, error) {
	if registry == nil || ctx == nil {
		return Selection{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return Selection{}, ErrClosed
	}
	if registry.importing {
		registry.mu.Unlock()
		return Selection{}, ErrBusy
	}
	registry.importing = true
	registry.mu.Unlock()
	defer func() {
		registry.mu.Lock()
		registry.importing = false
		registry.drained.Broadcast()
		registry.mu.Unlock()
	}()

	command, size, err := decodeSelection(document)
	if err != nil {
		return Selection{}, err
	}
	if err := registry.reserve(size); err != nil {
		return Selection{}, err
	}
	published := false
	defer func() {
		if !published {
			registry.mu.Lock()
			registry.usedBytes -= size
			registry.mu.Unlock()
		}
	}()
	data, digest, err := decodeImage(ctx, command, size)
	if err != nil {
		return Selection{}, err
	}
	defer func() {
		if !published {
			clear(data)
		}
	}()
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		return Selection{}, ErrInvalid
	}
	selection := Selection{ID: meta.ID, SHA256: digest, Size: size, MediaType: command.MediaType}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return Selection{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	registry.entries[selection.ID] = &selectionEntry{
		metadata: selection, data: data, owned: true, readers: map[*selectionReader]struct{}{},
	}
	published = true
	return selection, nil
}

func (registry *Registry) reserve(size int64) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return ErrClosed
	}
	if len(registry.entries) >= MaxSelections || registry.usedBytes+size > MaxSelectionTotalBytes {
		return ErrLimit
	}
	registry.usedBytes += size
	return nil
}

// Borrow returns an independently positioned reader. Release prevents new
// borrows; readers already borrowed keep the same bytes until they close.
func (registry *Registry) Borrow(ctx context.Context, id string) (io.ReadCloser, error) {
	if registry == nil || ctx == nil || !domain.IsUUID(id) {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return nil, ErrClosed
	}
	entry, exists := registry.entries[id]
	if !exists || !entry.owned {
		return nil, ErrNotFound
	}
	reader := &selectionReader{registry: registry, entry: entry, ctx: ctx}
	entry.readers[reader] = struct{}{}
	return reader, nil
}

// Release is idempotent for valid selection IDs, including a lost import reply.
func (registry *Registry) Release(id string) error {
	if registry == nil || !domain.IsUUID(id) {
		return ErrInvalid
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return ErrClosed
	}
	entry, exists := registry.entries[id]
	if !exists {
		return nil
	}
	entry.owned = false
	registry.discardLocked(entry)
	return nil
}

func (registry *Registry) discardLocked(entry *selectionEntry) {
	if entry.owned || len(entry.readers) != 0 {
		return
	}
	clear(entry.data)
	entry.data = nil
	registry.usedBytes -= entry.metadata.Size
	delete(registry.entries, entry.metadata.ID)
}

func (registry *Registry) Close() error {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.closed = true
	for registry.importing {
		registry.drained.Wait()
	}
	for _, entry := range registry.entries {
		for reader := range entry.readers {
			reader.closed = true
		}
		entry.readers = map[*selectionReader]struct{}{}
		entry.owned = false
		registry.discardLocked(entry)
	}
	return nil
}
