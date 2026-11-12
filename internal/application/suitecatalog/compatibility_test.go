package suitecatalog

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestRetiredSuiteLookupPreservesUserOverridesWithoutListingTheBuiltin(t *testing.T) {
	ctx := context.Background()
	raw := suiteDocument(t, "Retired builtin")
	original, err := decodeDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := "compatibility/" + original.ID + "/openai-chat/smoke.json"
	service, err := New(Options{
		Builtin: fstest.MapFS{path: {Data: raw}}, UserRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCount := func(want int) {
		t.Helper()
		entries, err := service.Entries(ctx)
		if err != nil || len(entries) != want {
			t.Fatalf(
				"entries=%d, want=%d, error=%v",
				len(entries),
				want,
				err,
			)
		}
		count, err := service.Count(ctx)
		if err != nil || count != want {
			t.Fatalf(
				"count=%d, want=%d, error=%v",
				count,
				want,
				err,
			)
		}
	}
	assertCount(0)
	entry, err := service.Find(ctx, original.ID)
	if err != nil || entry.Source != SourceBuiltin || entry.Directory != "smoke" ||
		entry.Suite.Revision != original.Revision || entry.Suite.Cases[0].CaseID != referencedCaseID {
		t.Fatalf("legacy reference failed: %+v, %v", entry, err)
	}
	if err := service.Save(ctx, entry.Group, entry.Directory, suiteDocument(t, "User override")); err != nil {
		t.Fatal(err)
	}
	assertCount(1)
	overlay, err := service.Find(ctx, original.ID)
	if err != nil || overlay.Source != SourceUser || overlay.Suite.Name != "User override" {
		t.Fatalf("user override lost: %+v, %v", overlay, err)
	}
	if err := service.Delete(ctx, overlay.Suite.ID, overlay.Suite.Revision); err != nil {
		t.Fatal(err)
	}
	assertCount(0)
	restored, err := service.Find(ctx, original.ID)
	if err != nil || restored.Suite.Revision != original.Revision || restored.Suite.Name != original.Name {
		t.Fatalf("retired fallback changed after deleting overlay: %+v, %v", restored, err)
	}
	if _, err := service.Find(ctx, "00000000-0000-4000-8000-000000000001"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unknown ID resolved: %v", err)
	}
}

func TestRetiredSuiteRejectsCorruptMismatchedAndAmbiguousDocuments(t *testing.T) {
	raw := suiteDocument(t, "Retired")
	original, err := decodeDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "compatibility/" + original.ID + "/"
	for _, test := range []struct {
		name  string
		files fstest.MapFS
		want  error
	}{
		{
			name: "corrupt", want: ErrInvalid,
			files: fstest.MapFS{prefix + "openai-chat/smoke.json": {Data: []byte(`{"schema_version":99}`)}},
		},
		{
			name: "protocol_mismatch", want: ErrInvalid,
			files: fstest.MapFS{prefix + "anthropic-messages/smoke.json": {Data: raw}},
		},
		{
			name: "ambiguous", want: ErrCollision,
			files: fstest.MapFS{
				prefix + "openai-chat/one.json": {Data: raw},
				prefix + "openai-chat/two.json": {Data: raw},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := New(Options{Builtin: test.files, UserRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Find(context.Background(), original.ID); !errors.Is(err, test.want) {
				t.Fatalf("invalid compatibility document accepted: %v", err)
			}
		})
	}
	wrongID := "00000000-0000-4000-8000-000000000001"
	service, err := New(Options{
		Builtin:  fstest.MapFS{"compatibility/" + wrongID + "/openai-chat/smoke.json": {Data: raw}},
		UserRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Find(context.Background(), wrongID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched published ID accepted: %v", err)
	}
}
