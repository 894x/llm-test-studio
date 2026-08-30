package doctor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/894x/llm-test/internal/application/compatibility"
	"github.com/894x/llm-test/internal/application/doctor"
)

type compatibilityListerFunc func(context.Context, compatibility.ListRequest) ([]compatibility.CaseDefinition, error)

func (list compatibilityListerFunc) List(ctx context.Context, request compatibility.ListRequest) ([]compatibility.CaseDefinition, error) {
	return list(ctx, request)
}

func TestOSFileSystemReportsOnlyNonemptyReadableDirectories(t *testing.T) {
	t.Parallel()

	filesystem := doctor.OSFileSystem{}
	empty := t.TempDir()
	hasEntries, err := filesystem.DirectoryHasEntries(context.Background(), empty)
	if err != nil || hasEntries {
		t.Fatalf("empty directory = entries %t err %v", hasEntries, err)
	}
	nonempty := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonempty, "case.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	hasEntries, err = filesystem.DirectoryHasEntries(context.Background(), nonempty)
	if err != nil || !hasEntries {
		t.Fatalf("nonempty directory = entries %t err %v", hasEntries, err)
	}
	if _, err := filesystem.DirectoryHasEntries(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing directory returned no error")
	}
}

func TestCompatibilityCatalogAdaptsListRequestAndCount(t *testing.T) {
	t.Parallel()

	adapter := doctor.CompatibilityCatalog{Lister: compatibilityListerFunc(func(_ context.Context, request compatibility.ListRequest) ([]compatibility.CaseDefinition, error) {
		if request.CasesRoot != "case-root" || request.Suite != "openai-chat" {
			t.Fatalf("request = %#v", request)
		}
		return []compatibility.CaseDefinition{{ID: "C001"}, {ID: "C002"}}, nil
	})}
	count, err := adapter.CountCases(context.Background(), "case-root", "openai-chat")
	if err != nil || count != 2 {
		t.Fatalf("CountCases() = %d, %v", count, err)
	}

	missing := doctor.CompatibilityCatalog{}
	if _, err := missing.CountCases(context.Background(), "case-root", "openai-chat"); err == nil {
		t.Fatal("nil compatibility lister returned no error")
	}
	failing := doctor.CompatibilityCatalog{Lister: compatibilityListerFunc(func(context.Context, compatibility.ListRequest) ([]compatibility.CaseDefinition, error) {
		return nil, errors.New("injected catalog failure")
	})}
	if _, err := failing.CountCases(context.Background(), "case-root", "openai-chat"); err == nil {
		t.Fatal("catalog error was swallowed")
	}
}
