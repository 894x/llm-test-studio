package credentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestTemporaryLeaseOwnsOnlyItsDefensiveCopy(t *testing.T) {
	input := []byte("temporary-secret")
	lease, err := NewTemporaryLease(input)
	if err != nil {
		t.Fatal(err)
	}
	clear(input)
	got, err := lease.Bytes()
	if err != nil || string(got) != "temporary-secret" {
		t.Fatal("caller mutation affected the lease")
	}
	clear(got)
	if _, err := json.Marshal(lease); !errors.Is(err, ErrInvalid) {
		t.Fatal("temporary lease can be serialized")
	}
	backing := lease.secret
	if err := lease.Close(); err != nil || !bytes.Equal(backing, make([]byte, len(backing))) {
		t.Fatal("temporary secret was not erased")
	}
	if _, err := lease.Bytes(); !errors.Is(err, ErrClosed) {
		t.Fatal("closed lease remains usable")
	}
	if _, err := NewTemporaryLease(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty temporary credential accepted")
	}
}
