package credentials

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileCleanupQueuePersistsSortedUniqueCredentialIDsAcrossReload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pending-credential-cleanup.json")
	first := "74000000-0000-4000-8000-000000000002"
	second := "74000000-0000-4000-8000-000000000001"
	queue, err := NewFileCleanupQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(ctx, first, second, first); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewFileCleanupQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{second, first}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
	if err := reloaded.Remove(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, err := queue.List(ctx); err != nil || !reflect.DeepEqual(got, []string{first}) {
		t.Fatalf("List() after cross-instance Remove = %#v, %v", got, err)
	}
	payload, err := os.ReadFile(path)
	if err != nil || !json.Valid(payload) {
		t.Fatalf("queue file = %q, %v; want valid JSON", payload, err)
	}
}

func TestFileCleanupQueueRejectsInvalidAndCorruptStateWithoutOverwritingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pending-credential-cleanup.json")
	queue, err := NewFileCleanupQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(ctx, "not-a-credential-id"); err == nil {
		t.Fatal("Enqueue(invalid) error = nil")
	}
	corrupt := []byte(`{"schema_version":1,"credential_ids":["not-a-uuid"]}`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.List(ctx); err == nil {
		t.Fatal("List(corrupt) error = nil")
	}
	if err := queue.Enqueue(ctx, "74000000-0000-4000-8000-000000000003"); err == nil {
		t.Fatal("Enqueue(corrupt) error = nil")
	}
	if after, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(after, corrupt) {
		t.Fatalf("corrupt queue changed to %q, %v", after, err)
	}
}
