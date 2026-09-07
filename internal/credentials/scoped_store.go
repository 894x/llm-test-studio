package credentials

import (
	"context"
	"fmt"
	"reflect"
)

// ScopedStore transparently binds otherwise stable credential IDs to one
// storage root. Sharing a channels.json file or Run database therefore shares only
// non-secret metadata; each root must have its own keyring entry.
type ScopedStore struct {
	store Store
	scope string
}

func NewScopedStore(store Store, scope string) (*ScopedStore, error) {
	if nilStore(store) || !validStoreScope(scope) {
		return nil, fmt.Errorf("create scoped credential store: %w", ErrInvalid)
	}
	return &ScopedStore{store: store, scope: scope}, nil
}

func nilStore(store Store) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (store *ScopedStore) Set(ctx context.Context, ref StoreRef, secret []byte) error {
	scoped, err := store.scopeRef(ref)
	if err != nil {
		return fmt.Errorf("set scoped credential: %w", err)
	}
	return store.store.Set(ctx, scoped, secret)
}

func (store *ScopedStore) Replace(ctx context.Context, ref StoreRef, secret []byte) error {
	scoped, err := store.scopeRef(ref)
	if err != nil {
		return fmt.Errorf("replace scoped credential: %w", err)
	}
	return store.store.Replace(ctx, scoped, secret)
}

func (store *ScopedStore) Get(ctx context.Context, ref StoreRef) (*Lease, error) {
	scoped, err := store.scopeRef(ref)
	if err != nil {
		return nil, fmt.Errorf("get scoped credential: %w", err)
	}
	return store.store.Get(ctx, scoped)
}

func (store *ScopedStore) Delete(ctx context.Context, ref StoreRef) error {
	scoped, err := store.scopeRef(ref)
	if err != nil {
		return fmt.Errorf("delete scoped credential: %w", err)
	}
	return store.store.Delete(ctx, scoped)
}

func (store *ScopedStore) Test(ctx context.Context, ref StoreRef) error {
	scoped, err := store.scopeRef(ref)
	if err != nil {
		return fmt.Errorf("test scoped credential: %w", err)
	}
	return store.store.Test(ctx, scoped)
}

func (store *ScopedStore) scopeRef(ref StoreRef) (StoreRef, error) {
	if store == nil || store.store == nil || !validStoreScope(store.scope) {
		return StoreRef{}, ErrInvalid
	}
	if err := ref.validate(); err != nil || ref.scope != "" {
		return StoreRef{}, ErrInvalid
	}
	return newScopedStoreRef(store.scope, ref.purpose, ref.id)
}
