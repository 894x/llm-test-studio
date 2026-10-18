package fileconfig

import (
	"crypto/sha256"
	"sync"
)

// ParsedCache reuses validated authored documents by content, never by mtime.
// Reading the bytes still detects external edits, replacements and corruption.
// Callers own their returned copy; failed decodes are never cached.
type ParsedCache[T any] struct {
	mu      sync.Mutex
	entries map[string]parsedEntry[T]
}

type parsedEntry[T any] struct {
	digest [32]byte
	value  T
}

func (cache *ParsedCache[T]) Read(key string, raw []byte, decode func() (T, error), clone func(T) T) (T, error) {
	digest := sha256.Sum256(raw)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry, found := cache.entries[key]; found && entry.digest == digest {
		return clone(entry.value), nil
	}
	value, err := decode()
	if err != nil {
		return value, err
	}
	if cache.entries == nil || len(cache.entries) >= 2048 {
		cache.entries = map[string]parsedEntry[T]{}
	}
	cache.entries[key] = parsedEntry[T]{digest: digest, value: value}
	return clone(value), nil
}
