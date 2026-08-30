package credentials

import (
	"context"
	"fmt"
	"sync"
)

// MemoryStore is a concurrent in-memory Store intended for tests and
// short-lived processes. It never serializes its contents.
type MemoryStore struct {
	mu      sync.RWMutex
	secrets map[StoreRef][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{secrets: make(map[StoreRef][]byte)}
}

func (store *MemoryStore) Set(ctx context.Context, ref StoreRef, secret []byte) error {
	if err := validateOperation(ctx, ref, secret); err != nil {
		return fmt.Errorf("set credential: %w", err)
	}
	copyOfSecret := append([]byte(nil), secret...)
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := contextError(ctx); err != nil {
		clear(copyOfSecret)
		return err
	}
	if _, exists := store.secrets[ref]; exists {
		clear(copyOfSecret)
		return fmt.Errorf("set credential: %w", ErrAlreadyExists)
	}
	store.secrets[ref] = copyOfSecret
	return nil
}

func (store *MemoryStore) Replace(ctx context.Context, ref StoreRef, secret []byte) error {
	if err := validateOperation(ctx, ref, secret); err != nil {
		return fmt.Errorf("replace credential: %w", err)
	}
	copyOfSecret := append([]byte(nil), secret...)
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := contextError(ctx); err != nil {
		clear(copyOfSecret)
		return err
	}
	previous, exists := store.secrets[ref]
	if !exists {
		clear(copyOfSecret)
		return fmt.Errorf("replace credential: %w", ErrNotFound)
	}
	store.secrets[ref] = copyOfSecret
	clear(previous)
	return nil
}

func (store *MemoryStore) Get(ctx context.Context, ref StoreRef) (*Lease, error) {
	if err := validateReferenceOperation(ctx, ref); err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	secret, exists := store.secrets[ref]
	if !exists {
		return nil, fmt.Errorf("get credential: %w", ErrNotFound)
	}
	return newLease(secret), nil
}

func (store *MemoryStore) Delete(ctx context.Context, ref StoreRef) error {
	if err := validateReferenceOperation(ctx, ref); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return err
	}
	secret, exists := store.secrets[ref]
	if !exists {
		return fmt.Errorf("delete credential: %w", ErrNotFound)
	}
	delete(store.secrets, ref)
	clear(secret)
	return nil
}

func (store *MemoryStore) Test(ctx context.Context, ref StoreRef) error {
	lease, err := store.Get(ctx, ref)
	if err != nil {
		return err
	}
	return lease.Close()
}

func validateOperation(ctx context.Context, ref StoreRef, secret []byte) error {
	if err := validateReferenceOperation(ctx, ref); err != nil {
		return err
	}
	if len(secret) == 0 {
		return ErrInvalid
	}
	return nil
}

func validateReferenceOperation(ctx context.Context, ref StoreRef) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := ref.validate(); err != nil {
		return ErrInvalid
	}
	return nil
}
