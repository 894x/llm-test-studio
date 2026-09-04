package credentials

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestScopedStoresIsolateSameCredentialID(t *testing.T) {
	ctx := context.Background()
	base := NewMemoryStore()
	storeA, err := NewScopedStore(base, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewScopedStore(base, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := NewStoreRef(domain.CredentialChannelAPIKey, testCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.Set(ctx, ref, []byte("root-a-secret")); err != nil {
		t.Fatal(err)
	}

	if err := storeB.Test(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("root B credential test error = %v, want ErrNotFound", err)
	}
	if err := storeB.Delete(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("root B credential delete error = %v, want ErrNotFound", err)
	}
	if err := storeA.Test(ctx, ref); err != nil {
		t.Fatalf("root A credential was affected by root B: %v", err)
	}
}

func TestNewScopedStoreRejectsNonCanonicalScope(t *testing.T) {
	base := NewMemoryStore()
	for _, scope := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if _, err := NewScopedStore(base, scope); !errors.Is(err, ErrInvalid) {
			t.Fatalf("NewScopedStore(%q) error = %v, want ErrInvalid", scope, err)
		}
	}
	var typedNil *MemoryStore
	if _, err := NewScopedStore(typedNil, strings.Repeat("a", 64)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewScopedStore(typed nil) error = %v, want ErrInvalid", err)
	}
}
