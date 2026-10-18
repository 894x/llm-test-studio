package plancatalog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyPlanReadIsNonMutatingAndEditKeepsVerifiedBackup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	service, _ := New(root)
	document := validTargetedDocument("60000000-0000-4000-8000-000000000001", time.Now().UTC())
	old, _ := json.Marshal(authoredDocument{FileSchemaVersion: 1, EntityMeta: document.EntityMeta,
		Name: document.Name, Protocol: document.Protocol, Seed: document.Seed, Entries: document.Entries})
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := service.path(document.ID)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := service.GetDocument(t.Context(), document.ID)
		if err != nil || got.FileSchemaVersion != 2 {
			t.Fatalf("legacy read: %v", err)
		}
		raw, _ := os.ReadFile(path)
		if !bytes.Equal(old, raw) {
			t.Fatal("read rewrote legacy data")
		}
	}
	document.Revision++
	document.UpdatedAt = document.UpdatedAt.Add(time.Second)
	document.Entries[0].OnFailure = "stop"
	// An unavailable backup destination must leave the original untouched.
	if err := os.WriteFile(root+".v1-backups", []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateDocument(t.Context(), 1, document); err == nil {
		t.Fatal("edit ignored failed backup")
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Equal(old, raw) {
		t.Fatal("failed backup changed original")
	}
	if err := os.Remove(root + ".v1-backups"); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateDocument(t.Context(), 1, document); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(root+".v1-backups", "*.json"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(backup, old) {
		t.Fatal("backup lost original bytes")
	}
	got, err := service.GetDocument(t.Context(), document.ID)
	if err != nil || got.Entries[0].OnFailure != "stop" {
		t.Fatalf("edited plan: %+v %v", got, err)
	}
	// Restoring the backup is sufficient to recover the authored v1 document.
	if err := os.WriteFile(path, backup, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetDocument(t.Context(), document.ID); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPlanRejectsNewFieldsAndUnknownVersionsWithoutMutation(t *testing.T) {
	document := validTargetedDocument("60000000-0000-4000-8000-000000000001", time.Now().UTC())
	for _, version := range []int{0, 1, 3} {
		root := filepath.Join(t.TempDir(), "plans")
		service, _ := New(root)
		legacy := authoredDocument{FileSchemaVersion: version, EntityMeta: document.EntityMeta,
			Name: document.Name, Protocol: document.Protocol, Entries: document.Entries}
		legacy.Entries[0].OnFailure = "stop"
		raw, _ := json.Marshal(legacy)
		_ = os.MkdirAll(root, 0700)
		_ = os.WriteFile(service.path(document.ID), raw, 0600)
		if _, err := service.GetDocument(t.Context(), document.ID); err == nil {
			t.Fatalf("accepted invalid version %d", version)
		}
		after, _ := os.ReadFile(service.path(document.ID))
		if !bytes.Equal(raw, after) {
			t.Fatal("rejected data was mutated")
		}
	}
}
