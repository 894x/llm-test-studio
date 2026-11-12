package canonicaljson

import (
	"bytes"
	"testing"
)

func TestNormalizationRetainsPrecisionAndDoesNotTrustEdits(t *testing.T) {
	raw := []byte(`{"z":9007199254740993,"a":{"y":2,"x":1}}`)
	canonical, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"a":{"x":1,"y":2},"z":9007199254740993}`)
	if !bytes.Equal(canonical, want) {
		t.Fatal("canonical form or integer precision changed")
	}
	if !Known(canonical) {
		t.Fatal("normalization proof not retained")
	}
	copy(canonical, []byte(`{"`))
	canonical[2] = 'q'
	if Known(canonical) {
		t.Fatal("changed document reused canonical proof")
	}
	if _, err := Normalize([]byte(`{} {}`)); err == nil {
		t.Fatal("multiple documents accepted")
	}
}
