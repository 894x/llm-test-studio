package casecatalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/google/uuid"
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

func caseJSON(key, name string) string {
	id := uuid.NewSHA1(uuid.MustParse("7680782d-7ae8-558b-9f32-17d13f31a66b"), []byte("builtin.cases/v2/openai-chat/"+key)).String()
	return `{"schema_version":2,"id":"` + id + `","key":"` + key + `","name":"` + name + `","dimension":"compatibility","enabled":true,"default":true,"severity":"normal","execution_mode":"automatic","definitions":{"openai-chat":{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hi"}]}},"assertions":[]}}}`
}

func TestSaveRejectsChangingAnExistingPathIdentityWithoutWriting(t *testing.T) {
	root := t.TempDir()
	original := []byte(caseJSON("same", "Original"))
	service, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{"openai-chat/same/case.json": &fstest.MapFile{Data: original}}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Save(context.Background(), "openai-chat", "same", original); err != nil {
		t.Fatal(err)
	}
	var candidate map[string]any
	_ = json.Unmarshal(original, &candidate)
	candidate["id"] = uuid.NewString()
	raw, _ := json.Marshal(candidate)
	if err := service.Save(context.Background(), "openai-chat", "same", raw); !errors.Is(err, casecatalog.ErrCollision) {
		t.Fatalf("changed identity error = %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(root, "openai-chat", "same", "case.json"))
	if err != nil || !bytes.Equal(saved, original) {
		t.Fatal("rejected save changed the original file")
	}
}
