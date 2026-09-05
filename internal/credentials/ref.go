package credentials

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	storeRefNamespace       = "llm-test-studio/v1"
	scopedStoreRefNamespace = "llm-test-studio/v2"
	storeScopeHexLength     = 64
)

// StoreRef is a stable, non-secret identifier for an operating-system
// credential. Its fields are private so only canonical references can exist.
type StoreRef struct {
	value   string
	scope   string
	purpose domain.CredentialPurpose
	id      string
}

func NewStoreRef(purpose domain.CredentialPurpose, id string) (StoreRef, error) {
	if err := purpose.Validate(); err != nil || !domain.IsUUID(id) {
		return StoreRef{}, fmt.Errorf("create store reference: %w", ErrInvalid)
	}
	return StoreRef{
		value:   storeRefNamespace + "/" + string(purpose) + "/" + id,
		purpose: purpose,
		id:      id,
	}, nil
}

func newScopedStoreRef(scope string, purpose domain.CredentialPurpose, id string) (StoreRef, error) {
	if !validStoreScope(scope) {
		return StoreRef{}, fmt.Errorf("create scoped store reference: %w", ErrInvalid)
	}
	if err := purpose.Validate(); err != nil || !domain.IsUUID(id) {
		return StoreRef{}, fmt.Errorf("create scoped store reference: %w", ErrInvalid)
	}
	return StoreRef{
		value:   scopedStoreRefNamespace + "/" + scope + "/" + string(purpose) + "/" + id,
		scope:   scope,
		purpose: purpose,
		id:      id,
	}, nil
}

func ParseStoreRef(value string) (StoreRef, error) {
	parts := strings.Split(value, "/")
	var (
		ref StoreRef
		err error
	)
	switch {
	case len(parts) == 4 && parts[0]+"/"+parts[1] == storeRefNamespace:
		ref, err = NewStoreRef(domain.CredentialPurpose(parts[2]), parts[3])
	case len(parts) == 5 && parts[0]+"/"+parts[1] == scopedStoreRefNamespace:
		ref, err = newScopedStoreRef(parts[2], domain.CredentialPurpose(parts[3]), parts[4])
	default:
		return StoreRef{}, fmt.Errorf("parse store reference: %w", ErrInvalid)
	}
	if err != nil || ref.value != value {
		return StoreRef{}, fmt.Errorf("parse store reference: %w", ErrInvalid)
	}
	return ref, nil
}

func StoreRefFromCredential(ref domain.CredentialRef) (StoreRef, error) {
	if err := ref.Validate(); err != nil {
		return StoreRef{}, fmt.Errorf("convert credential reference: %w", ErrInvalid)
	}
	storeRef, err := ParseStoreRef(ref.StoreRef)
	if err != nil || storeRef.scope != "" || storeRef.purpose != ref.Purpose || storeRef.id != ref.ID {
		return StoreRef{}, fmt.Errorf("convert credential reference: %w", ErrInvalid)
	}
	return storeRef, nil
}

func (ref StoreRef) Value() string {
	return ref.value
}

func (ref StoreRef) String() string {
	return ref.value
}

func (ref StoreRef) Purpose() domain.CredentialPurpose {
	return ref.purpose
}

// Scope is empty for legacy references and contains the canonical authored
// catalog scope for references owned by a ScopedStore.
func (ref StoreRef) Scope() string {
	return ref.scope
}

func (ref StoreRef) ID() string {
	return ref.id
}

func (ref StoreRef) validate() error {
	parsed, err := ParseStoreRef(ref.value)
	if err != nil || parsed != ref {
		return ErrInvalid
	}
	return nil
}

func validStoreScope(scope string) bool {
	if len(scope) != storeScopeHexLength || scope != strings.ToLower(scope) {
		return false
	}
	_, err := hex.DecodeString(scope)
	return err == nil
}
