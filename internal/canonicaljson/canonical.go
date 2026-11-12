// Package canonicaljson preserves the current UseNumber JSON canonical form.
package canonicaljson

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

const knownLimit = 256

var known = struct {
	sync.Mutex
	keys  map[[sha256.Size]byte]struct{}
	order [][sha256.Size]byte
	next  int
}{keys: map[[sha256.Size]byte]struct{}{}, order: [][sha256.Size]byte{}}

// Remember marks output assembled from already canonical children. Only content
// fingerprints are retained, so edits cannot inherit this encoding proof.
func Remember(raw []byte) {
	key := sha256.Sum256(raw)
	known.Lock()
	defer known.Unlock()
	if _, ok := known.keys[key]; ok {
		return
	}
	if len(known.order) < knownLimit {
		known.order = append(known.order, key)
	} else {
		delete(known.keys, known.order[known.next])
		known.order[known.next] = key
		known.next = (known.next + 1) % knownLimit
	}
	known.keys[key] = struct{}{}
}
func Known(raw []byte) bool {
	key := sha256.Sum256(raw)
	known.Lock()
	defer known.Unlock()
	_, ok := known.keys[key]
	return ok
}
func Normalize(raw []byte) ([]byte, error) {
	if Known(raw) {
		return bytes.Clone(raw), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("multiple JSON values")
	}
	out, err := json.Marshal(value)
	if err == nil {
		Remember(out)
	}
	return out, err
}
