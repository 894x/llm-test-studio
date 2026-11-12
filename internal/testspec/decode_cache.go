package testspec

import (
	"bytes"
	"crypto/sha256"
	"sync"
)

const decodedSpecCacheEntries = 1024
const decodedSpecCacheBytes = 32 << 20

type decodedSpecEntry struct {
	spec   Spec
	charge int
}

type decodedSpecCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]decodedSpecEntry
	order   [][sha256.Size]byte
	used    int
}

var decodedSpecs = decodedSpecCache{entries: map[[sha256.Size]byte]decodedSpecEntry{}, order: [][sha256.Size]byte{}}

func (cache *decodedSpecCache) lookup(key [sha256.Size]byte) (Spec, bool) {
	cache.mu.Lock()
	entry, found := cache.entries[key]
	cache.mu.Unlock()
	if !found {
		return Spec{}, false
	}
	return cloneSpec(entry.spec), true
}

// Retain only valid, decoder-owned specs. Charge generously for decoded maps
// and strings; oversized individual documents remain supported without caching.
func (cache *decodedSpecCache) remember(key [sha256.Size]byte, spec Spec, rawBytes int) bool {
	charge := rawBytes*8 + 4096
	if charge > decodedSpecCacheBytes {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, found := cache.entries[key]; found {
		return true
	}
	for len(cache.order) >= decodedSpecCacheEntries || cache.used+charge > decodedSpecCacheBytes {
		oldest := cache.order[0]
		cache.used -= cache.entries[oldest].charge
		delete(cache.entries, oldest)
		cache.order = cache.order[1:]
	}
	cache.entries[key] = decodedSpecEntry{spec: spec, charge: charge}
	cache.order = append(cache.order, key)
	cache.used += charge
	return true
}

// Every cache hit returns independently mutable data. No caller can mutate the
// cached request, inputs, assertions, file declarations or workflow steps.
func cloneSpec(spec Spec) Spec {
	inputs := make(map[string]Input, len(spec.Inputs))
	for name, input := range spec.Inputs {
		cloned := input.Clone()
		if input.Enum == nil {
			cloned.Enum = nil
		}
		inputs[name] = cloned
	}
	spec.Inputs = inputs
	spec.Request.Body = bytes.Clone(spec.Request.Body)
	spec.Assertions = cloneAssertions(spec.Assertions)
	if spec.Workflow != nil {
		workflow := *spec.Workflow
		if workflow.Steps != nil {
			workflow.Steps = append([]Step{}, workflow.Steps...)
			for index := range workflow.Steps {
				workflow.Steps[index].Request.Body = bytes.Clone(workflow.Steps[index].Request.Body)
			}
		}
		spec.Workflow = &workflow
	}
	return spec
}

func cloneAssertions(values []Assertion) []Assertion {
	if values == nil {
		return nil
	}
	cloned := append([]Assertion{}, values...)
	for index := range cloned {
		value := &cloned[index]
		value.Value = bytes.Clone(value.Value)
		value.All = cloneAssertions(value.All)
		value.Any = cloneAssertions(value.Any)
		value.Each = cloneAssertions(value.Each)
		if value.ExpectedFrom != nil {
			reference := *value.ExpectedFrom
			value.ExpectedFrom = &reference
		}
	}
	return cloned
}
