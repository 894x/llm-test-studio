package fileconfig

import (
	"encoding/json"
	"testing"
)

func TestParsedCacheTracksContentAndIsolatesReturnedValues(t *testing.T) {
	var cache ParsedCache[[]string]
	decodes := 0
	read := func(raw string) ([]string, error) {
		return cache.Read("document", []byte(raw), func() ([]string, error) {
			decodes++
			var value []string
			err := json.Unmarshal([]byte(raw), &value)
			return value, err
		}, func(value []string) []string { return append([]string{}, value...) })
	}
	first, err := read(`["one"]`)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = "mutated"
	again, err := read(`["one"]`)
	if err != nil || again[0] != "one" || decodes != 1 {
		t.Fatalf("cached read: %v, decodes=%d, error=%v", again, decodes, err)
	}
	changed, err := read(`["two"]`)
	if err != nil || changed[0] != "two" || decodes != 2 {
		t.Fatalf("same-length replacement: %v, decodes=%d, error=%v", changed, decodes, err)
	}
	for range 2 {
		if _, err := read(`invalid`); err == nil {
			t.Fatal("corrupt content reused an earlier parsed value")
		}
	}
	if decodes != 4 {
		t.Fatalf("failed parses must not be cached: %d", decodes)
	}
	if _, err := read(`["two"]`); err != nil || decodes != 4 {
		t.Fatalf("restored content: decodes=%d, error=%v", decodes, err)
	}
}
