package credentials

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"
)

func TestOSStoreEnforcesCreateReplaceAndDeleteSemantics(t *testing.T) {
	backend := newFakeKeyringBackend()
	store := newOSStoreWithBackend(backend)
	ref := mustStoreRef(t)

	if err := store.Set(context.Background(), ref, []byte("original-secret")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Set(context.Background(), ref, []byte("must-not-overwrite")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Set() error = %v, want ErrAlreadyExists", err)
	}
	if err := store.Replace(context.Background(), ref, []byte("replacement-secret")); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if err := store.Test(context.Background(), ref); err != nil {
		t.Fatalf("Test() error = %v", err)
	}

	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	value, err := lease.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(value), "replacement-secret"; got != want {
		t.Fatalf("Get() = %q, want %q", got, want)
	}
	clear(value)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(context.Background(), ref); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after Delete error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
	if err := store.Replace(context.Background(), ref, []byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Replace() missing error = %v, want ErrNotFound", err)
	}
}

func TestOSStoreUsesStableServiceAndCanonicalAccount(t *testing.T) {
	backend := newFakeKeyringBackend()
	store := newOSStoreWithBackend(backend)
	ref := mustStoreRef(t)
	if err := store.Set(context.Background(), ref, []byte("secret")); err != nil {
		t.Fatal(err)
	}

	const wantService = "com.github.894x.llm-test-studio.credentials.v1"
	const wantAccount = "llm-test-studio/v1/channel_api_key/11111111-2222-4333-8444-555555555555"
	if !backend.has(wantService, wantAccount) {
		t.Fatalf("backend did not receive stable service %q and account %q", wantService, wantAccount)
	}

	invalid := StoreRef{value: wantAccount, purpose: ref.purpose, id: "66666666-7777-4888-8999-aaaaaaaaaaaa"}
	if _, err := store.Get(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get() with internally inconsistent ref error = %v, want ErrInvalid", err)
	}
}

func TestOSStoreConcurrentSetAcrossInstancesHasOneWinner(t *testing.T) {
	backend := newRacingCreateBackend()
	firstStore := newOSStoreWithBackend(backend)
	secondStore := newOSStoreWithBackend(backend)
	ref := mustStoreRef(t)
	type result struct {
		secret string
		err    error
	}
	results := make(chan result, 2)

	go func() {
		const secret = "first-winner-candidate"
		results <- result{secret: secret, err: firstStore.Set(context.Background(), ref, []byte(secret))}
	}()
	<-backend.firstSetStarted
	go func() {
		const secret = "second-winner-candidate"
		results <- result{secret: secret, err: secondStore.Set(context.Background(), ref, []byte(secret))}
	}()

	var winner string
	var successCount, duplicateCount int
	for i := 0; i < 2; i++ {
		got := <-results
		switch {
		case got.err == nil:
			successCount++
			winner = got.secret
		case errors.Is(got.err, ErrAlreadyExists):
			duplicateCount++
		default:
			t.Fatalf("Set() error = %v, want nil or ErrAlreadyExists", got.err)
		}
	}
	if successCount != 1 || duplicateCount != 1 {
		t.Fatalf("successes = %d, duplicates = %d, want 1 and 1", successCount, duplicateCount)
	}
	if got := backend.stored(); got != winner {
		t.Fatalf("backend retained %q, want successful caller's value %q", got, winner)
	}
}

func TestOSStoreMapsBackendFailuresWithoutLeakingDetails(t *testing.T) {
	const leak = "backend-secret-leak-sentinel"
	ref := mustStoreRef(t)

	getBackend := newFakeKeyringBackend()
	getBackend.getErr = errors.New("keyring failed while handling " + leak)
	_, err := newOSStoreWithBackend(getBackend).Get(context.Background(), ref)
	assertUnavailableWithoutLeak(t, err, leak)

	setBackend := newFakeKeyringBackend()
	setBackend.setErr = errors.New("keyring failed while handling " + leak)
	err = newOSStoreWithBackend(setBackend).Set(context.Background(), ref, []byte(leak))
	assertUnavailableWithoutLeak(t, err, leak)

	deleteBackend := newFakeKeyringBackend()
	deleteBackend.put(defaultKeyringService, ref.Value(), "stored")
	deleteBackend.deleteErr = errors.New("keyring failed while handling " + leak)
	err = newOSStoreWithBackend(deleteBackend).Delete(context.Background(), ref)
	assertUnavailableWithoutLeak(t, err, leak)
}

func TestOSStoreRejectsEmptyBackendSecret(t *testing.T) {
	backend := newFakeKeyringBackend()
	ref := mustStoreRef(t)
	backend.put(defaultKeyringService, ref.Value(), "")

	if _, err := newOSStoreWithBackend(backend).Get(context.Background(), ref); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get() error = %v, want ErrInvalid", err)
	}
}

func TestOSStoreChecksCancelledContextBeforeCallingBackend(t *testing.T) {
	backend := newFakeKeyringBackend()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := newOSStoreWithBackend(backend).Get(ctx, mustStoreRef(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get() error = %v, want context.Canceled", err)
	}
	if got := backend.calls(); got != 0 {
		t.Fatalf("backend calls = %d, want 0", got)
	}
}

func TestOSStoreCancellationWaitsForUninterruptibleBackendThenWins(t *testing.T) {
	backend := newFakeKeyringBackend()
	ref := mustStoreRef(t)
	backend.put(defaultKeyringService, ref.Value(), "secret")
	backend.getStarted = make(chan struct{})
	backend.releaseGet = make(chan struct{})
	store := newOSStoreWithBackend(backend)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	result := make(chan error, 1)
	go func() {
		lease, err := store.Get(ctx, ref)
		if lease != nil {
			_ = lease.Close()
		}
		result <- err
	}()
	<-backend.getStarted
	cancel()
	select {
	case err := <-result:
		t.Fatalf("Get() returned before uninterruptible backend was released: %v", err)
	default:
	}
	close(backend.releaseGet)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Get() error = %v, want context.Canceled", err)
	}
}

func assertUnavailableWithoutLeak(t *testing.T, err error, leak string) {
	t.Helper()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), leak) {
		t.Fatalf("error exposed backend or credential detail: %v", err)
	}
}

type fakeKeyringBackend struct {
	mu        sync.Mutex
	values    map[string]string
	callCount int
	getErr    error
	setErr    error
	deleteErr error

	getStarted chan struct{}
	releaseGet chan struct{}
	startOnce  sync.Once
}

func newFakeKeyringBackend() *fakeKeyringBackend {
	return &fakeKeyringBackend{values: make(map[string]string)}
}

func (backend *fakeKeyringBackend) Set(service, account, password string) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.callCount++
	if backend.setErr != nil {
		return backend.setErr
	}
	backend.values[service+"\x00"+account] = password
	return nil
}

func (backend *fakeKeyringBackend) Get(service, account string) (string, error) {
	backend.mu.Lock()
	backend.callCount++
	started := backend.getStarted
	release := backend.releaseGet
	err := backend.getErr
	backend.mu.Unlock()

	if started != nil {
		backend.startOnce.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return "", err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	value, exists := backend.values[service+"\x00"+account]
	if !exists {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (backend *fakeKeyringBackend) Delete(service, account string) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.callCount++
	if backend.deleteErr != nil {
		return backend.deleteErr
	}
	key := service + "\x00" + account
	if _, exists := backend.values[key]; !exists {
		return keyring.ErrNotFound
	}
	delete(backend.values, key)
	return nil
}

func (backend *fakeKeyringBackend) put(service, account, secret string) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.values[service+"\x00"+account] = secret
}

func (backend *fakeKeyringBackend) has(service, account string) bool {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	_, exists := backend.values[service+"\x00"+account]
	return exists
}

func (backend *fakeKeyringBackend) calls() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.callCount
}

// racingCreateBackend opens a deterministic check-then-set race for adapters
// that coordinate only per instance. The first Set briefly waits for a second
// missing Get; a process-wide lock prevents that second Get until the winner is
// stored, while per-instance locks allow both callers to observe absence.
type racingCreateBackend struct {
	mu              sync.Mutex
	value           string
	exists          bool
	missingGets     int
	secondMissing   chan struct{}
	firstSetStarted chan struct{}
	secondOnce      sync.Once
	setOnce         sync.Once
}

func newRacingCreateBackend() *racingCreateBackend {
	return &racingCreateBackend{
		secondMissing:   make(chan struct{}),
		firstSetStarted: make(chan struct{}),
	}
}

func (backend *racingCreateBackend) Get(_, _ string) (string, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.exists {
		return backend.value, nil
	}
	backend.missingGets++
	if backend.missingGets == 2 {
		backend.secondOnce.Do(func() { close(backend.secondMissing) })
	}
	return "", keyring.ErrNotFound
}

func (backend *racingCreateBackend) Set(_, _ string, password string) error {
	backend.setOnce.Do(func() { close(backend.firstSetStarted) })
	select {
	case <-backend.secondMissing:
	case <-time.After(20 * time.Millisecond):
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.value = password
	backend.exists = true
	return nil
}

func (backend *racingCreateBackend) Delete(_, _ string) error {
	return keyring.ErrNotFound
}

func (backend *racingCreateBackend) stored() string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.value
}
