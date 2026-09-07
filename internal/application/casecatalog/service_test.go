package casecatalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
)

func TestCatalogMergesBuiltinAndUserCasesByGroupWithUserOverride(t *testing.T) {
	builtin := fstest.MapFS{
		"openai-chat/shared/case.json":       &fstest.MapFile{Data: []byte(caseJSON("T001", "builtin"))},
		"openai-chat/builtin-only/case.json": &fstest.MapFile{Data: []byte(caseJSON("T002", "builtin only"))},
	}
	root := t.TempDir()
	writeUserCase(t, root, "openai-chat", "shared", caseJSON("T001", "user override"))
	writeUserCase(t, root, "openai-chat", "user-only", caseJSON("T003", "user only"))
	catalog, err := casecatalog.New(casecatalog.Options{Builtin: builtin, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 1 || len(snapshot.Groups[0].Cases) != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	shared := snapshot.Groups[0].Cases[1]
	if shared.Name != "user override" || shared.Source != casecatalog.SourceUser {
		t.Fatalf("merged shared case = %#v", shared)
	}
}

func TestSaveCreatesExeRelativeGroupCaseLayoutAndReloadsIt(t *testing.T) {
	root := t.TempDir()
	catalog, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root, Now: func() time.Time { return time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(caseJSON("T900", "shared case"))
	if err := catalog.Save(context.Background(), "openai-chat", "T900-shared", raw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	path := filepath.Join(root, "openai-chat", "T900-shared", "case.json")
	if got, err := os.ReadFile(path); err != nil || string(got) != string(raw) {
		t.Fatalf("saved case = %q, %v", got, err)
	}
	snapshot, err := catalog.Snapshot(context.Background())
	if err != nil || len(snapshot.Groups) != 1 || len(snapshot.Groups[0].Cases) != 1 || snapshot.Groups[0].Cases[0].Key != "T900" {
		t.Fatalf("reloaded snapshot = %#v, %v", snapshot, err)
	}
}

func TestSaveSafelyReplacesAnExistingUserCase(t *testing.T) {
	root := t.TempDir()
	catalog, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Save(context.Background(), "openai-chat", "T901-replace", []byte(caseJSON("T901", "first"))); err != nil {
		t.Fatal(err)
	}
	replacement := []byte(caseJSON("T901", "replacement"))
	if err := catalog.Save(context.Background(), "openai-chat", "T901-replace", replacement); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	path := filepath.Join(root, "openai-chat", "T901-replace", "case.json")
	if got, err := os.ReadFile(path); err != nil || string(got) != string(replacement) {
		t.Fatalf("replacement case = %q, %v", got, err)
	}
}

func TestSavePreservesThePreviousRevisionForPinnedPlans(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Save(ctx, "openai-chat", "T902-history", []byte(caseJSON("T902", "first revision"))); err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.Entries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries() = %#v, %v", entries, err)
	}
	first := entries[0].TestCase

	if err := catalog.Save(ctx, "openai-chat", "T902-history", []byte(caseJSON("T902", "second revision"))); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	current, err := catalog.Find(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TestCase.Revision == first.Revision || current.TestCase.Name != "second revision" {
		t.Fatalf("current case = %#v", current.TestCase)
	}

	pinned, err := catalog.FindRevision(ctx, first.ID, first.Revision)
	if err != nil {
		t.Fatalf("FindRevision(old) error = %v", err)
	}
	if pinned.TestCase.ID != first.ID || pinned.TestCase.Revision != first.Revision || pinned.TestCase.Name != "first revision" {
		t.Fatalf("pinned case = %#v, want first revision", pinned.TestCase)
	}
}

func TestStoreRevisionPreservesAnExactLegacyDatabaseRevision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Save(ctx, "openai-chat", "T903-legacy", []byte(caseJSON("T903", "current file"))); err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.Entries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries() = %#v, %v", entries, err)
	}
	legacy := entries[0].TestCase
	legacy.Revision = 7
	legacy.Name = "legacy pinned definition"
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy fixture is invalid: %v", err)
	}

	if err := catalog.StoreRevision(ctx, legacy); err != nil {
		t.Fatalf("StoreRevision() error = %v", err)
	}
	if err := catalog.StoreRevision(ctx, legacy); err != nil {
		t.Fatalf("StoreRevision(idempotent retry) error = %v", err)
	}
	pinned, err := catalog.FindRevision(ctx, legacy.ID, legacy.Revision)
	if err != nil {
		t.Fatalf("FindRevision() error = %v", err)
	}
	if pinned.TestCase.Revision != 7 || pinned.TestCase.Name != legacy.Name {
		t.Fatalf("pinned case = %#v", pinned.TestCase)
	}
	current, err := catalog.Find(ctx, legacy.ID)
	if err != nil || current.TestCase.Name != "current file" {
		t.Fatalf("current case changed = %#v, %v", current.TestCase, err)
	}
	conflict := legacy
	conflict.Name = "different content under the same revision"
	if err := catalog.StoreRevision(ctx, conflict); !errors.Is(err, casecatalog.ErrCollision) {
		t.Fatalf("StoreRevision(conflict) error = %v, want ErrCollision", err)
	}
	pinned, err = catalog.FindRevision(ctx, legacy.ID, legacy.Revision)
	if err != nil || pinned.TestCase.Name != legacy.Name {
		t.Fatalf("conflicting store changed pinned case = %#v, %v", pinned.TestCase, err)
	}
}

func TestStoreRevisionMaterializesTheCurrentRevisionBeforeAnExternalEdit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Save(ctx, "openai-chat", "T904-current", []byte(caseJSON("T904", "current pinned case"))); err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.Entries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries() = %#v, %v", entries, err)
	}
	pinned := entries[0]
	if err := catalog.StoreRevision(ctx, pinned.TestCase); err != nil {
		t.Fatalf("StoreRevision(current) error = %v", err)
	}

	// Simulate a user editing the shareable case.json without going through
	// Save, which cannot archive the previous active document for us.
	writeUserCase(t, root, pinned.Group, pinned.Directory, caseJSON("T904", "externally edited case"))
	current, err := catalog.Find(ctx, pinned.TestCase.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TestCase.Revision == pinned.TestCase.Revision {
		t.Fatal("external edit did not produce a distinct active Case revision")
	}
	exact, err := catalog.FindRevision(ctx, pinned.TestCase.ID, pinned.TestCase.Revision)
	if err != nil {
		t.Fatalf("FindRevision(pinned after external edit) error = %v", err)
	}
	if exact.TestCase.Name != pinned.TestCase.Name {
		t.Fatalf("FindRevision() = %#v, want exact pinned %#v", exact.TestCase, pinned.TestCase)
	}
}

func TestUserRootForExecutableUsesTheExecutableDirectory(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "bin", "llm-test-studio.exe")
	root, err := casecatalog.UserRootForExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(filepath.Dir(executable), "data", "cases") {
		t.Fatalf("user root = %q", root)
	}
	if _, err := casecatalog.UserRootForExecutable("relative.exe"); err == nil {
		t.Fatal("relative executable path was accepted")
	}
}

func writeUserCase(t *testing.T, root, group, name, contents string) {
	t.Helper()
	directory := filepath.Join(root, group, name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "case.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func caseJSON(id, name string) string {
	return `{"schema_version":2,"key":"` + id + `","name":"` + name + `","dimension":"compatibility","protocol":"openai-chat","enabled":true,"default":true,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hi"}]}},"options":{}}}}`
}
