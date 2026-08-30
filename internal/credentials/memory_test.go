package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/894x/llm-test/internal/domain"
)

func TestMemoryStoreSetAndGetUseDefensiveCopies(t *testing.T) {
	store := NewMemoryStore()
	ref := mustStoreRef(t)
	input := []byte("top-secret-1234")
	if err := store.Set(context.Background(), ref, input); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	for i := range input {
		input[i] = 'x'
	}

	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })

	first, err := lease.Bytes()
	if err != nil {
		t.Fatalf("Bytes() error = %v", err)
	}
	if got, want := string(first), "top-secret-1234"; got != want {
		t.Fatalf("Bytes() = %q, want %q", got, want)
	}
	first[0] = 'X'
	second, err := lease.Bytes()
	if err != nil {
		t.Fatalf("second Bytes() error = %v", err)
	}
	if got, want := string(second), "top-secret-1234"; got != want {
		t.Fatalf("second Bytes() = %q, want %q", got, want)
	}
}

func TestLeaseCloseZeroesMemoryAndBlocksReuse(t *testing.T) {
	lease := newLease([]byte("erase-this-secret"))
	backing := lease.secret

	if err := lease.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !bytes.Equal(backing, make([]byte, len(backing))) {
		t.Fatalf("Close() left non-zero bytes in its backing storage")
	}
	if _, err := lease.Bytes(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Bytes() after Close error = %v, want ErrClosed", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestLeaseFormattingAndJSONNeverRevealSecret(t *testing.T) {
	const secret = "format-leak-sentinel"
	lease := newLease([]byte(secret))
	t.Cleanup(func() { _ = lease.Close() })

	for name, formatted := range map[string]string{
		"String":   lease.String(),
		"GoString": lease.GoString(),
		"fmt-v":    fmt.Sprintf("%v", lease),
		"fmt-go-v": fmt.Sprintf("%#v", lease),
	} {
		if strings.Contains(formatted, secret) || formatted != "[REDACTED]" {
			t.Fatalf("%s exposed or did not redact the lease: %q", name, formatted)
		}
	}
	encoded, err := json.Marshal(lease)
	if err == nil {
		t.Fatalf("json.Marshal() = %q, want serialization error", encoded)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("json.Marshal() error exposed secret: %v", err)
	}
}

func TestMemoryStoreSetRejectsDuplicateWithoutOverwriting(t *testing.T) {
	store := NewMemoryStore()
	ref := mustStoreRef(t)
	if err := store.Set(context.Background(), ref, []byte("first-secret")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), ref, []byte("second-secret")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Set() error = %v, want ErrAlreadyExists", err)
	}

	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	value, _ := lease.Bytes()
	if got, want := string(value), "first-secret"; got != want {
		t.Fatalf("duplicate Set() overwrote value: got %q, want %q", got, want)
	}
}

func TestMemoryStoreReplaceRequiresExistingCredential(t *testing.T) {
	store := NewMemoryStore()
	ref := mustStoreRef(t)
	if err := store.Replace(context.Background(), ref, []byte("replacement")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Replace() missing error = %v, want ErrNotFound", err)
	}
	if err := store.Set(context.Background(), ref, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(context.Background(), ref, []byte("replacement")); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	value, _ := lease.Bytes()
	if got, want := string(value), "replacement"; got != want {
		t.Fatalf("Get() = %q, want %q", got, want)
	}
}

func TestMemoryStoreDeleteAndTestReportMissingCredential(t *testing.T) {
	store := NewMemoryStore()
	ref := mustStoreRef(t)
	if err := store.Test(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Test() before Set error = %v, want ErrNotFound", err)
	}
	if err := store.Set(context.Background(), ref, []byte("available")); err != nil {
		t.Fatal(err)
	}
	if err := store.Test(context.Background(), ref); err != nil {
		t.Fatalf("Test() error = %v", err)
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
}

func TestMemoryStoreHonorsCancelledContextWithoutMutation(t *testing.T) {
	store := NewMemoryStore()
	ref := mustStoreRef(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Set(ctx, ref, []byte("must-not-be-stored")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set() error = %v, want context.Canceled", err)
	}
	if _, err := store.Get(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled Set mutated store; Get() error = %v", err)
	}
	if err := store.Test(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("Test() error = %v, want context.Canceled", err)
	}
}

func TestMemoryStoreErrorsNeverContainSecret(t *testing.T) {
	store := NewMemoryStore()
	const secret = "never-echo-this-secret"
	err := store.Set(context.Background(), StoreRef{}, []byte(secret))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Set() error = %v, want ErrInvalid", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Set() error exposed secret: %v", err)
	}
	ref := mustStoreRef(t)
	if err := store.Set(context.Background(), ref, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Set(nil) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryStoreConcurrentSetHasOneWinner(t *testing.T) {
	store := NewMemoryStore()
	ref := mustStoreRef(t)
	var successes atomic.Int32
	var failures atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := store.Set(context.Background(), ref, []byte("shared-secret"))
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrAlreadyExists):
				failures.Add(1)
			default:
				t.Errorf("Set() error = %v", err)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || failures.Load() != 31 {
		t.Fatalf("successes = %d, duplicates = %d, want 1 and 31", successes.Load(), failures.Load())
	}
}

func mustStoreRef(t *testing.T) StoreRef {
	t.Helper()
	ref, err := NewStoreRef(domain.CredentialChannelAPIKey, testCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
