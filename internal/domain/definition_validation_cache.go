package domain

import (
	"crypto/sha256"
	"sync"
)

const definitionValidationCacheLimit = 1024

type definitionValidationKey struct {
	protocol Protocol
	digest   [sha256.Size]byte
}

// Only successful validation is memoized. Keys cover the exact current bytes;
// editing a mutable RawMessage always requires validation again. Neither native
// request bodies nor parsed mutable specs are retained in this bounded cache.
type definitionValidationCache struct {
	mu    sync.Mutex
	keys  map[definitionValidationKey]struct{}
	order []definitionValidationKey
	next  int
}

var validatedDefinitions = definitionValidationCache{
	keys:  map[definitionValidationKey]struct{}{},
	order: make([]definitionValidationKey, 0, definitionValidationCacheLimit),
}

func (cache *definitionValidationCache) contains(key definitionValidationKey) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	_, found := cache.keys[key]
	return found
}

func (cache *definitionValidationCache) remember(key definitionValidationKey) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, found := cache.keys[key]; found {
		return
	}
	if len(cache.order) < definitionValidationCacheLimit {
		cache.order = append(cache.order, key)
	} else {
		delete(cache.keys, cache.order[cache.next])
		cache.order[cache.next] = key
		cache.next = (cache.next + 1) % definitionValidationCacheLimit
	}
	cache.keys[key] = struct{}{}
}
