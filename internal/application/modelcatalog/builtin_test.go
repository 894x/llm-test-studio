package modelcatalog

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

	"github.com/894x/llm-test-studio/internal/domain"
)

func builtinModelsFixture() fstest.MapFS {
	return fstest.MapFS{
		"first.json": {Data: []byte(`{
			"schema_version":1,"id":"first","display_name":"First","model":"first-model",
			"protocol":"openai-chat","endpoint":"","suites":[],"capabilities":["text"],"enabled":true
		}`)},
		"nested/second.json": {Data: []byte(`{
			"schema_version":1,"id":"second","display_name":"Second","model":"second-model",
			"protocol":"openai-chat","endpoint":"","suites":[],"capabilities":[],"enabled":true
		}`)},
	}
}

func TestBuiltinModelsAreDiscoveredWithoutWritingUserFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "models.json")
	service, err := New(path, WithBuiltin(builtinModelsFixture()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := service.List(ctx)
	if err != nil || len(first) != 2 {
		t.Fatalf("models = %#v, %v", first, err)
	}
	first[0].Protocols[0] = domain.ProtocolWanVideo
	again, err := service.List(ctx)
	if err != nil || !again[0].Builtin || again[0].Protocols[0] != domain.ProtocolOpenAIChat {
		t.Fatalf("built-in models share mutable state: %#v, %v", again, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("reading built-ins wrote user data: %v", err)
	}
	user := validModel("40000000-0000-4000-8000-000000000042", time.Now().UTC())
	if err := service.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted []domain.Model
	if err := json.Unmarshal(raw, &persisted); err != nil || len(persisted) != 1 {
		t.Fatalf("built-in models leaked into user file: %s, %v", raw, err)
	}
}

func TestBuiltinModelUserOverridePreservesIdentityAndOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	service, err := New(path, WithBuiltin(builtinModelsFixture()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	models, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	user := models[0]
	user.Builtin = false
	user.Name = "Existing customer model"
	user.Revision = 7
	original, err := json.Marshal([]domain.Model{user})
	if err != nil {
		t.Fatal(err)
	}
	original = append([]byte("  "), original...)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := service.Get(ctx, user.ID)
		if err != nil || got.Name != user.Name || got.Revision != 7 || got.Builtin {
			t.Fatalf("user override changed: %#v, %v", got, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("opening catalogs rewrote user data: %v", err)
	}
}

func TestBuiltinModelEditCreatesOnlyAnExplicitLocalOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	service, err := New(path, WithBuiltin(builtinModelsFixture()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	models, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	model := models[0]
	if err := service.Delete(ctx, model.ID, model.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deleting a built-in model = %v", err)
	}
	model.Revision++
	model.Name = "Local override"
	if err := service.Update(ctx, 1, model); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, model.ID)
	if err != nil || got.Builtin || got.Name != model.Name {
		t.Fatalf("saved override = %#v, %v", got, err)
	}
	if err := service.Delete(ctx, model.ID, model.Revision); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Get(ctx, model.ID)
	if err != nil || !restored.Builtin || restored.Revision != 1 {
		t.Fatalf("removing an override did not reveal the built-in: %#v, %v", restored, err)
	}
}

func TestBuiltinModelsRejectInvalidDefinitionsWithoutTouchingUserData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	original := []byte("[]\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"schema_version":2}`,
		`{"schema_version":1}`,
		`{"schema_version":1,"unexpected":true}`,
		`{}` + `{}`,
		`broken`,
	} {
		if _, err := New(path, WithBuiltin(fstest.MapFS{"new.json": {Data: []byte(raw)}})); err == nil {
			t.Fatalf("accepted invalid definition: %s", raw)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, original) {
			t.Fatalf("invalid definition modified user data: %v", err)
		}
	}
}
