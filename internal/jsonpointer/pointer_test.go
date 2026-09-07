package jsonpointer

import "testing"

func TestReplaceUpdatesOnlyAnExistingField(t *testing.T) {
	root := map[string]any{"items": []any{map[string]any{"text": "original"}}}
	if !Replace(root, "/items/0/text", "edited") {
		t.Fatal("existing field not updated")
	}
	if got, _ := Lookup(root, "/items/0/text"); got != "edited" {
		t.Fatal(got)
	}
	for _, pointer := range []string{"", "/missing", "/items/1", "/items/00/text", "/items/0/missing"} {
		if Replace(root, pointer, "wrong") {
			t.Fatalf("invalid replacement accepted: %s", pointer)
		}
	}
	if got, _ := Lookup(root, "/items/0/text"); got != "edited" {
		t.Fatal("invalid replacement changed valid field")
	}
}

func TestLookupEscapesObjectsAndRejectsArrayAliases(t *testing.T) {
	root := map[string]any{"items": []any{"first"}, "a/b": map[string]any{"~1": "escaped"}, "00": "key"}
	for _, test := range []struct {
		pointer string
		want    any
		found   bool
	}{
		{"/items/0", "first", true},
		{"/a~1b/~01", "escaped", true},
		{"/00", "key", true},
		{"/items/00", nil, false},
		{"/items/+0", nil, false},
		{"/items/-0", nil, false},
		{"/items/-", nil, false},
		{"/items/1", nil, false},
		{"/items/0/missing", nil, false},
		{"/a~2b", nil, false},
		{"items", nil, false},
	} {
		t.Run(test.pointer, func(t *testing.T) {
			got, found := Lookup(root, test.pointer)
			if got != test.want || found != test.found {
				t.Fatalf("Lookup = %v, %v; want %v, %v", got, found, test.want, test.found)
			}
		})
	}
}
