package jobcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/894x/llm-test-studio/internal/canonicaljson"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jsonbudget"
)

type specVerification struct {
	runs        []domain.Run
	fingerprint [sha256.Size]byte
	document    []byte
	once        sync.Once
	canonical   []byte
	digest      string
	err         error
}

// Proofs are process-local and tied to the exact current serialized content.
// Public structs remain mutable: a changed byte invalidates the proof.
func (spec JobSpec) validatedDocument() ([]byte, error) {
	if spec.verification != nil {
		fingerprint, err := specFingerprint(spec)
		if err != nil {
			return nil, invalid("spec")
		}
		if fingerprint == spec.verification.fingerprint {
			return spec.verification.document, nil
		}
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, invalid("spec")
	}
	return raw, nil
}

func sealSpec(spec *JobSpec, runs []domain.Run) error {
	raw, err := json.Marshal(spec)
	if err != nil {
		return invalid("spec")
	}
	fingerprint, err := specFingerprint(*spec)
	if err != nil {
		return invalid("spec")
	}
	spec.verification = &specVerification{fingerprint: fingerprint, document: raw, runs: runs}
	return nil
}

const ownedSpecCacheBytes = 128 << 20
const ownedSpecCacheEntries = 32

type ownedSpecEntry struct {
	spec   JobSpec
	charge int
}
type ownedSpecCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]ownedSpecEntry
	order   [][sha256.Size]byte
	used    int
}

var ownedSpecs = ownedSpecCache{entries: map[[sha256.Size]byte]ownedSpecEntry{}, order: [][sha256.Size]byte{}}

func (cache *ownedSpecCache) lookup(key [sha256.Size]byte) (JobSpec, bool) {
	cache.mu.Lock()
	entry, found := cache.entries[key]
	cache.mu.Unlock()
	if !found {
		return JobSpec{}, false
	}
	return cloneOwnedSpec(entry.spec), true
}
func (cache *ownedSpecCache) remember(key [sha256.Size]byte, spec JobSpec, rawBytes int) bool {
	if spec.Kind == KindPerformance {
		return false
	}
	charge := rawBytes*6 + 4096
	if charge > ownedSpecCacheBytes {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, found := cache.entries[key]; found {
		return true
	}
	for len(cache.order) >= ownedSpecCacheEntries || cache.used+charge > ownedSpecCacheBytes {
		oldest := cache.order[0]
		cache.used -= cache.entries[oldest].charge
		delete(cache.entries, oldest)
		cache.order = cache.order[1:]
	}
	cache.entries[key] = ownedSpecEntry{spec: spec, charge: charge}
	cache.order = append(cache.order, key)
	cache.used += charge
	return true
}

// Parsed snapshots are retained privately and copied with domain-owned cloning,
// avoiding repeated JSON decoding while preserving ownership on every return.
func cloneOwnedSpec(spec JobSpec) JobSpec {
	spec.Assets = slices.Clone(spec.Assets)
	if spec.Spec.Run != nil {
		run := *spec.Spec.Run
		run.Snapshot = run.Snapshot.Clone()
		spec.Spec.Run = &run
	}
	if spec.Spec.Comparison != nil {
		comparison := *spec.Spec.Comparison
		comparison.Runs = slices.Clone(comparison.Runs)
		for index := range comparison.Runs {
			comparison.Runs[index].Snapshot = comparison.Runs[index].Snapshot.Clone()
		}
		spec.Spec.Comparison = &comparison
	}
	return spec
}

func decodeOwnedSpec(raw []byte) (JobSpec, error) {
	if len(raw) > MaxSubmissionBytes {
		return JobSpec{}, ErrTooLarge
	}
	key := sha256.Sum256(raw)
	if cached, found := ownedSpecs.lookup(key); found {
		return cached, nil
	}
	var spec JobSpec
	if err := decode(raw, &spec); err != nil {
		return JobSpec{}, err
	}
	runs := []domain.Run{}
	if err := spec.validateOwned(&runs); err != nil {
		return JobSpec{}, err
	}
	if err := sealSpec(&spec, runs); err != nil {
		return JobSpec{}, err
	}
	if ownedSpecs.remember(key, spec, len(raw)) {
		return cloneOwnedSpec(spec), nil
	}
	return spec, nil
}

func (spec JobSpec) canonicalState() (*specVerification, error) {
	raw, err := spec.validatedDocument()
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxSubmissionBytes {
		return nil, ErrTooLarge
	}
	proof := spec.verification
	if proof == nil || !bytes.Equal(proof.document, raw) {
		fingerprint, err := specFingerprint(spec)
		if err != nil {
			return nil, invalid("spec")
		}
		proof = &specVerification{fingerprint: fingerprint, document: raw}
	}
	proof.once.Do(func() {
		if err := jsonbudget.Check(raw, MaxOwnedItems); err != nil {
			if errors.Is(err, jsonbudget.ErrExceeded) {
				proof.err = ErrTooLarge
			} else {
				proof.err = invalid("spec")
			}
			return
		}
		if err := checkCollectionBounds(raw, scopeRoot); err != nil {
			proof.err = err
			return
		}
		proof.canonical, proof.err = canonicaljson.Normalize(raw)
		if proof.err != nil {
			return
		}
		digest := sha256.Sum256(proof.canonical)
		proof.digest = hex.EncodeToString(digest[:])
	})
	return proof, proof.err
}

// CanonicalJSON returns owned bytes in the unchanged persisted canonical form.
// A verified spec reuses normalization without exposing its private buffer.
func (spec JobSpec) CanonicalJSON() ([]byte, error) {
	proof, err := spec.canonicalState()
	if err != nil {
		return nil, err
	}
	return slices.Clone(proof.canonical), nil
}
