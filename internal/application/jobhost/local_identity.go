package jobhost

import (
	"bytes"

	"github.com/894x/llm-test-studio/internal/domain"
)

// LocalIdentity belongs to the desktop's operational database. The commitment
// key authenticates idempotent submissions; it is never a channel credential.
type LocalIdentity struct {
	NodeID         string
	InstallationID string
	CommitmentKey  []byte
}

func (LocalIdentity) String() string               { return "local_executor_identity{redacted}" }
func (LocalIdentity) GoString() string             { return "local_executor_identity{redacted}" }
func (LocalIdentity) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

func (identity LocalIdentity) Validate() error {
	validIDs := domain.IsUUID(identity.NodeID) && domain.IsUUID(identity.InstallationID)
	var empty [32]byte
	validKey := len(identity.CommitmentKey) == 32 && !bytes.Equal(identity.CommitmentKey, empty[:])
	if !validIDs || !validKey {
		return ErrInvalid
	}
	return nil
}

func (identity *LocalIdentity) Clear() {
	if identity != nil {
		clear(identity.CommitmentKey)
		identity.CommitmentKey = []byte{}
	}
}
