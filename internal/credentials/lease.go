package credentials

import (
	"fmt"
	"sync"
)

// Lease gives short-lived access to a defensive copy of a secret. Close must
// be called as soon as the caller no longer needs the secret.
type Lease struct {
	mu     sync.Mutex
	secret []byte
	closed bool
}

func newLease(secret []byte) *Lease {
	return &Lease{secret: append([]byte(nil), secret...)}
}

// NewTemporaryLease copies a transient credential without creating a keyring
// entry. The caller retains ownership of its input bytes and must close the
// returned lease when the operation ends.
func NewTemporaryLease(secret []byte) (*Lease, error) {
	if len(secret) == 0 {
		return nil, ErrInvalid
	}
	return newLease(secret), nil
}

// Bytes returns a defensive copy that the caller should zero after use.
func (lease *Lease) Bytes() ([]byte, error) {
	if lease == nil {
		return nil, ErrClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil, ErrClosed
	}
	return append([]byte(nil), lease.secret...), nil
}

// Close zeroes the lease-owned bytes. It is safe to call more than once.
func (lease *Lease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	clear(lease.secret)
	lease.secret = nil
	lease.closed = true
	return nil
}

func (*Lease) String() string {
	return "[REDACTED]"
}

func (*Lease) GoString() string {
	return "[REDACTED]"
}

// MarshalJSON deliberately refuses to turn secret material into a transport
// value. Domain objects, reports, events, and logs must carry StoreRef only.
func (*Lease) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("serialize secret lease: %w", ErrInvalid)
}
