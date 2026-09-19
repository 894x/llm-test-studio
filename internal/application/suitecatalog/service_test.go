package suitecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const referencedCaseID = "11111111-1111-4111-8111-111111111111"

func suiteDocument(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := json.Marshal(document{
		SchemaVersion: CurrentSchemaVersion, Key: "smoke", Name: name, Protocol: domain.ProtocolOpenAIChat,
		Cases: []domain.CaseRef{{CaseID: referencedCaseID}}, Inputs: []domain.SuiteInput{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSuiteReferencesLoadWithoutReadingOrRequiringCaseDefinitions(t *testing.T) {
	service, err := New(Options{Builtin: fstest.MapFS{"openai-chat/smoke/suite.json": {Data: suiteDocument(t, "Builtin")}}, UserRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries: %#v, %v", entries, err)
	}
	if entries[0].Suite.Cases[0].CaseID != referencedCaseID {
		t.Fatal("reference was changed")
	}
	original := entries[0].Suite
	entries[0].Suite.Cases[0].CaseID = "22222222-2222-4222-8222-222222222222"
	again, err := service.Find(context.Background(), original.ID)
	if err != nil || again.Suite.Revision != original.Revision || again.Suite.Cases[0].CaseID != referencedCaseID {
		t.Fatalf("read alias: %#v %v", again, err)
	}
}

func TestSuiteSaveRoundTripsReferencesAndExplicitInputMappings(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	raw := suiteDocument(t, "First")
	if err := service.Save(context.Background(), "openai-chat", "smoke", raw); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := entries[0].Suite
	candidate.Name = "Updated"
	candidate.Inputs = []domain.SuiteInput{{Key: "prompt", Label: "Prompt", Input: testspec.Input{Type: "string", Default: json.RawMessage(`"hello"`)}, Bindings: []domain.SuiteInputBinding{{CaseID: referencedCaseID, Input: "message"}}}}
	if err := service.SaveSuite(context.Background(), "openai-chat", "smoke", candidate); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Find(context.Background(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Suite.Name != "Updated" || saved.Suite.Revision == entries[0].Suite.Revision || saved.Suite.Inputs[0].Bindings[0].Input != "message" {
		t.Fatalf("saved %#v", saved)
	}
	bytesOnDisk, err := os.ReadFile(filepath.Join(root, "openai-chat", "smoke", "suite.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"revision"`, `"model_target"`, `"case_keys"`, `"quick_test"`} {
		if bytes.Contains(bytesOnDisk, []byte(forbidden)) {
			t.Fatalf("retired field %s", forbidden)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "openai-chat", "smoke", "revisions")); !os.IsNotExist(err) {
		t.Fatalf("unexpected revision sidecars: %v", err)
	}
}

func TestSuiteRejectsOldAndUnknownFormatsWithoutChangingFiles(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	current := suiteDocument(t, "Current")
	if err := service.Save(context.Background(), "openai-chat", "smoke", current); err != nil {
		t.Fatal(err)
	}
	bad := [][]byte{
		[]byte(`{"schema_version":2,"key":"smoke","name":"old","protocol":"openai-chat","model_target":"model","case_keys":["T001"]}`),
		bytes.Replace(current, []byte(`"schema_version":1`), []byte(`"schema_version":999`), 1),
		append(append([]byte{}, current...), []byte(` {}`)...),
	}
	for _, raw := range bad {
		if err := service.Save(context.Background(), "openai-chat", "smoke", raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "openai-chat", "smoke", "suite.json"))
	if err != nil || !bytes.Equal(after, current) {
		t.Fatalf("rejected write mutated file: %v", err)
	}
}

func TestSuiteUserOverlayHasStableIdentityAndDeletesOnlyItsOwnFile(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{Builtin: fstest.MapFS{"openai-chat/smoke/suite.json": {Data: suiteDocument(t, "Builtin")}}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Save(context.Background(), "openai-chat", "smoke", suiteDocument(t, "User")); err != nil {
		t.Fatal(err)
	}
	after, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Suite.ID != before[0].Suite.ID || after[0].Source != SourceUser {
		t.Fatalf("overlay %#v", after)
	}
	if err := service.Delete(context.Background(), after[0].Suite.ID, after[0].Suite.Revision); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Entries(context.Background())
	if err != nil || restored[0].Suite.Name != "Builtin" {
		t.Fatalf("restore %#v %v", restored, err)
	}
}
