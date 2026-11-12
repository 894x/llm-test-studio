package testspec

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestDecodeCacheOwnsResultsAndRejectsEditedInput(t *testing.T) {
	raw := json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[]}},"assertions":[]}`)
	first, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	first.Request.Body[0] = '['
	first.Inputs["poison"] = Input{Type: "string"}
	second, err := Decode(raw)
	if err != nil || bytes.Equal(first.Request.Body, second.Request.Body) || len(second.Inputs) != 0 {
		t.Fatalf("cache leaked mutable result: %v", err)
	}
	changed := bytes.Replace(raw, []byte(`"inputs":{}`), []byte(`"inputs":[]`), 1)
	copy(raw, changed)
	if _, err := Decode(raw); err == nil {
		t.Fatal("edited bytes inherited cached validity")
	}
}

func TestCloneSpecOwnsEveryMutableBranch(t *testing.T) {
	minimum, maximum := 1.0, 2.0
	original := Spec{Inputs: map[string]Input{"value": {Type: "number", Default: json.RawMessage(`1`),
		Minimum: &minimum, Maximum: &maximum, Enum: []json.RawMessage{json.RawMessage(`1`)},
		File: &FileInput{MediaTypes: []string{"image/png"}}}}, Request: Request{Body: json.RawMessage(`{}`)},
		Assertions: []Assertion{{Value: json.RawMessage(`1`), All: []Assertion{{Value: json.RawMessage(`2`)}},
			Any: []Assertion{{Value: json.RawMessage(`3`)}}, Each: []Assertion{{Value: json.RawMessage(`4`)}},
			ExpectedFrom: &Reference{Source: "response"}}}, Workflow: &Workflow{Steps: []Step{{Request: Request{Body: json.RawMessage(`{}`)}}}}}
	pristine := cloneSpec(original)
	cloned := cloneSpec(original)
	input := cloned.Inputs["value"]
	input.Default[0] = '9'
	input.Enum[0][0] = '9'
	*input.Minimum = 9
	*input.Maximum = 9
	input.File.MediaTypes[0] = "poison"
	delete(cloned.Inputs, "value")
	cloned.Request.Body[0] = '['
	cloned.Assertions[0].Value[0] = '9'
	cloned.Assertions[0].All[0].Value[0] = '9'
	cloned.Assertions[0].Any[0].Value[0] = '9'
	cloned.Assertions[0].Each[0].Value[0] = '9'
	cloned.Assertions[0].ExpectedFrom.Source = "poison"
	cloned.Workflow.Steps[0].Request.Body[0] = '['
	if !reflect.DeepEqual(original, pristine) {
		t.Fatal("deep clone retained mutable aliases")
	}
}

func TestDecodedSpecCacheBoundsRetentionAndConcurrentReaders(t *testing.T) {
	cache := decodedSpecCache{entries: map[[sha256.Size]byte]decodedSpecEntry{}, order: [][sha256.Size]byte{}}
	spec := Spec{Inputs: map[string]Input{}, Assertions: []Assertion{}, Request: Request{Body: json.RawMessage(`{}`)}}
	for index := range decodedSpecCacheEntries + 1 {
		key := sha256.Sum256([]byte(fmt.Sprint(index)))
		cache.remember(key, spec, 16)
	}
	if len(cache.entries) != decodedSpecCacheEntries || cache.used > decodedSpecCacheBytes {
		t.Fatal("unbounded decode cache")
	}
	if _, found := cache.lookup(sha256.Sum256([]byte("0"))); found {
		t.Fatal("oldest key not evicted")
	}
	if cache.remember(sha256.Sum256([]byte("oversized")), spec, decodedSpecCacheBytes) {
		t.Fatal("oversized entry retained")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 20 {
				key := sha256.Sum256([]byte("shared"))
				cache.remember(key, spec, 16)
				if got, found := cache.lookup(key); found {
					got.Request.Body[0] = '['
				}
			}
		}()
	}
	workers.Wait()
}
