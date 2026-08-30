package credentials

import (
	"fmt"
	"strings"

	"github.com/894x/llm-test/internal/domain"
)

const storeRefNamespace = "llm-test/v1"

// StoreRef is a stable, non-secret identifier for an operating-system
// credential. Its fields are private so only canonical references can exist.
type StoreRef struct {
	value   string
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

func ParseStoreRef(value string) (StoreRef, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 4 || parts[0]+"/"+parts[1] != storeRefNamespace {
		return StoreRef{}, fmt.Errorf("parse store reference: %w", ErrInvalid)
	}
	ref, err := NewStoreRef(domain.CredentialPurpose(parts[2]), parts[3])
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
	if err != nil || storeRef.purpose != ref.Purpose || storeRef.id != ref.ID {
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
