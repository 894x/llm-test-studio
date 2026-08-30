package credentials

import (
	"context"
	"sync"
)

var processKeyringGates = newKeyedGateRegistry()

type keyedGateRegistry struct {
	mu      sync.Mutex
	entries map[string]*keyedGateEntry
}

type keyedGateEntry struct {
	token chan struct{}
	refs  int
}

func newKeyedGateRegistry() *keyedGateRegistry {
	return &keyedGateRegistry{entries: make(map[string]*keyedGateEntry)}
}

func (registry *keyedGateRegistry) acquire(ctx context.Context, key string) (func(), error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	registry.mu.Lock()
	entry := registry.entries[key]
	if entry == nil {
		entry = &keyedGateEntry{token: make(chan struct{}, 1)}
		registry.entries[key] = entry
	}
	entry.refs++
	registry.mu.Unlock()

	select {
	case entry.token <- struct{}{}:
		if err := contextError(ctx); err != nil {
			<-entry.token
			registry.drop(key, entry)
			return nil, err
		}
	case <-ctx.Done():
		registry.drop(key, entry)
		return nil, ctx.Err()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-entry.token
			registry.drop(key, entry)
		})
	}, nil
}

func (registry *keyedGateRegistry) drop(key string, entry *keyedGateEntry) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry.refs--
	if entry.refs == 0 && registry.entries[key] == entry {
		delete(registry.entries, key)
	}
}
