package suitecatalog

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

func TestAllAuthoredSuitesUseCurrentReferenceContract(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..", "..", "data", "suites")
	service, err := New(Options{Builtin: os.DirFS(root), UserRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 44 {
		t.Fatalf("suite count %d", len(entries))
	}
	for _, entry := range entries {
		if err := entry.Suite.Validate(); err != nil {
			t.Fatalf("%s: %v", entry.Suite.Key, err)
		}
	}
}

func TestSuiteRejectsDuplicateIdentities(t *testing.T) {
	data := suiteDocument(t, "Same")
	service, err := New(Options{Builtin: fstest.MapFS{"openai-chat/one/suite.json": &fstest.MapFile{Data: data}, "openai-chat/two/suite.json": &fstest.MapFile{Data: data}}, UserRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Entries(context.Background()); err == nil {
		t.Fatal("accepted duplicate identity")
	}
	if _, err := service.Find(context.Background(), "not-uuid"); err == nil {
		t.Fatal("accepted invalid id")
	}
	_ = fs.ErrNotExist
}
