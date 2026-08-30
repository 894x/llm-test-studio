package credentials

import (
	"context"
	"errors"
	"fmt"

	keyring "github.com/zalando/go-keyring"
)

const defaultKeyringService = "com.github.894x.llm-studio.credentials.v1"

type keyringBackend interface {
	Set(service, account, password string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

type systemKeyringBackend struct{}

func (systemKeyringBackend) Set(service, account, password string) error {
	return keyring.Set(service, account, password)
}

func (systemKeyringBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeyringBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

// OSStore persists credentials in the operating-system credential service.
// The gate preserves create-only Set and existing-only Replace semantics among
// callers in this process; operating-system keyrings do not offer a portable
// atomic create-if-absent primitive across processes.
type OSStore struct {
	service string
	backend keyringBackend
}

func NewOSStore() *OSStore {
	return newOSStoreWithBackend(systemKeyringBackend{})
}

func newOSStoreWithBackend(backend keyringBackend) *OSStore {
	return &OSStore{
		service: defaultKeyringService,
		backend: backend,
	}
}

func (store *OSStore) Set(ctx context.Context, ref StoreRef, secret []byte) error {
	if err := validateOperation(ctx, ref, secret); err != nil {
		return fmt.Errorf("set credential: %w", err)
	}
	release, err := store.acquire(ctx, ref)
	if err != nil {
		return err
	}
	defer release()

	_, err = store.lookup(ctx, ref)
	switch {
	case err == nil:
		return fmt.Errorf("set credential: %w", ErrAlreadyExists)
	case !errors.Is(err, ErrNotFound):
		return err
	}

	// go-keyring's platform API accepts string passwords. Keep that unavoidable
	// conversion inside this adapter and never propagate the string upward.
	password := string(secret)
	backendErr := store.backend.Set(store.service, ref.Value(), password)
	if err := contextError(ctx); err != nil {
		return err
	}
	if backendErr != nil {
		return unavailable("set")
	}
	return nil
}

func (store *OSStore) Replace(ctx context.Context, ref StoreRef, secret []byte) error {
	if err := validateOperation(ctx, ref, secret); err != nil {
		return fmt.Errorf("replace credential: %w", err)
	}
	release, err := store.acquire(ctx, ref)
	if err != nil {
		return err
	}
	defer release()

	if _, err := store.lookup(ctx, ref); err != nil {
		return err
	}
	password := string(secret)
	backendErr := store.backend.Set(store.service, ref.Value(), password)
	if err := contextError(ctx); err != nil {
		return err
	}
	if backendErr != nil {
		return unavailable("replace")
	}
	return nil
}

func (store *OSStore) Get(ctx context.Context, ref StoreRef) (*Lease, error) {
	if err := validateReferenceOperation(ctx, ref); err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}
	release, err := store.acquire(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer release()

	password, err := store.lookup(ctx, ref)
	if err != nil {
		return nil, err
	}
	temporary := []byte(password)
	lease := newLease(temporary)
	clear(temporary)
	return lease, nil
}

func (store *OSStore) Delete(ctx context.Context, ref StoreRef) error {
	if err := validateReferenceOperation(ctx, ref); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	release, err := store.acquire(ctx, ref)
	if err != nil {
		return err
	}
	defer release()

	backendErr := store.backend.Delete(store.service, ref.Value())
	if err := contextError(ctx); err != nil {
		return err
	}
	if errors.Is(backendErr, keyring.ErrNotFound) {
		return fmt.Errorf("delete credential: %w", ErrNotFound)
	}
	if backendErr != nil {
		return unavailable("delete")
	}
	return nil
}

func (store *OSStore) Test(ctx context.Context, ref StoreRef) error {
	lease, err := store.Get(ctx, ref)
	if err != nil {
		return err
	}
	return lease.Close()
}

func (store *OSStore) lookup(ctx context.Context, ref StoreRef) (string, error) {
	password, backendErr := store.backend.Get(store.service, ref.Value())
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if errors.Is(backendErr, keyring.ErrNotFound) {
		return "", fmt.Errorf("get credential: %w", ErrNotFound)
	}
	if backendErr != nil {
		return "", unavailable("get")
	}
	if password == "" {
		return "", fmt.Errorf("get credential: %w", ErrInvalid)
	}
	return password, nil
}

func (store *OSStore) acquire(ctx context.Context, ref StoreRef) (func(), error) {
	return processKeyringGates.acquire(ctx, store.service+"\x00"+ref.Value())
}

func unavailable(operation string) error {
	// Backend errors are intentionally discarded: platform implementations may
	// include account or provider-specific detail that must not reach logs.
	return fmt.Errorf("%s credential in operating-system store: %w", operation, ErrUnavailable)
}
