package credentials

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const testCredentialID = "11111111-2222-4333-8444-555555555555"

func TestNewStoreRefBuildsStableNamespacedReference(t *testing.T) {
	ref, err := NewStoreRef(domain.CredentialChannelAPIKey, testCredentialID)
	if err != nil {
		t.Fatalf("NewStoreRef() error = %v", err)
	}

	const want = "llm-test-studio/v1/channel_api_key/11111111-2222-4333-8444-555555555555"
	if got := ref.Value(); got != want {
		t.Fatalf("Value() = %q, want %q", got, want)
	}
	if ref.Purpose() != domain.CredentialChannelAPIKey {
		t.Fatalf("Purpose() = %q", ref.Purpose())
	}
	if ref.ID() != testCredentialID {
		t.Fatalf("ID() = %q", ref.ID())
	}

	parsed, err := ParseStoreRef(want)
	if err != nil {
		t.Fatalf("ParseStoreRef() error = %v", err)
	}
	if parsed != ref {
		t.Fatalf("ParseStoreRef() = %#v, want %#v", parsed, ref)
	}
}

func TestParseStoreRefRejectsNamespaceConfusion(t *testing.T) {
	invalid := []string{
		"",
		"llm-test-studio/v2/channel_api_key/" + testCredentialID,
		"other/v1/channel_api_key/" + testCredentialID,
		"llm-test-studio/v1/channel_api_key/../" + testCredentialID,
		"llm-test-studio/v1/channel_api_key/" + testCredentialID + "/extra",
		"llm-test-studio/v1/CHANNEL_API_KEY/" + testCredentialID,
		"llm-test-studio/v1/channel_api_key/00000000-0000-0000-0000-000000000000",
	}
	for _, value := range invalid {
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			if _, err := ParseStoreRef(value); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseStoreRef(%q) error = %v, want ErrInvalid", value, err)
			}
		})
	}
}

func TestStoreRefFromCredentialRequiresMatchingPurpose(t *testing.T) {
	meta, err := domain.NewEntityMeta(time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.CredentialRef{
		EntityMeta:   meta,
		StoreRef:     "llm-test-studio/v1/channel_api_key/" + testCredentialID,
		Purpose:      domain.CredentialIntegrationAdmin,
		MaskedSuffix: "abcd",
		Fingerprint:  "sha256:" + strings.Repeat("a", 64),
	}

	if _, err := StoreRefFromCredential(ref); !errors.Is(err, ErrInvalid) {
		t.Fatalf("StoreRefFromCredential() error = %v, want ErrInvalid", err)
	}
}

func TestStoreRefFromCredentialRequiresMatchingCredentialID(t *testing.T) {
	meta, err := domain.NewEntityMeta(time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.CredentialRef{
		EntityMeta:   meta,
		StoreRef:     "llm-test-studio/v1/channel_api_key/" + testCredentialID,
		Purpose:      domain.CredentialChannelAPIKey,
		MaskedSuffix: "abcd",
		Fingerprint:  "sha256:" + strings.Repeat("a", 64),
	}

	if _, err := StoreRefFromCredential(ref); !errors.Is(err, ErrInvalid) {
		t.Fatalf("StoreRefFromCredential() error = %v, want ErrInvalid", err)
	}
}

func TestStoreRefFromCredentialAcceptsFullyBoundReference(t *testing.T) {
	meta, err := domain.NewEntityMeta(time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.CredentialRef{
		EntityMeta:   meta,
		StoreRef:     "llm-test-studio/v1/channel_api_key/" + meta.ID,
		Purpose:      domain.CredentialChannelAPIKey,
		MaskedSuffix: "abcd",
		Fingerprint:  "sha256:" + strings.Repeat("a", 64),
	}

	storeRef, err := StoreRefFromCredential(ref)
	if err != nil {
		t.Fatalf("StoreRefFromCredential() error = %v", err)
	}
	if storeRef.ID() != meta.ID || storeRef.Purpose() != ref.Purpose {
		t.Fatalf("StoreRefFromCredential() = %#v, want ID and purpose bound to domain ref", storeRef)
	}
}
