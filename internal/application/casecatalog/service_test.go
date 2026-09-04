package casecatalog_test

import (
	"context"
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

func TestUserRootForExecutableUsesTheExecutableDirectory(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "bin", "llm-test-studio.exe")
	root, err := casecatalog.UserRootForExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(filepath.Dir(executable), "cases") {
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
