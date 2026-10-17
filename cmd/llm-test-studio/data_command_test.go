package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestDataHistoryExportUsesReadOnlySourceAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	database, output := filepath.Join(directory, "workspace.db"), filepath.Join(directory, "history.jsonl")
	if err := persistence.Migrate(ctx, database, persistence.MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"data", "export-upgrade-history", "--database", database, "--output", output}
	if code := run(ctx, args, nil, &stdout, &stderr, defaultDependencies()); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	exported, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(exported, []byte(`"type":"complete"`)) {
		t.Fatal("incomplete export")
	}
	if code := run(ctx, args, nil, &stdout, &stderr, defaultDependencies()); code != 1 {
		t.Fatal("overwrote existing output")
	}
	after, err := os.ReadFile(database)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("export changed database")
	}
	again, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(exported, again) {
		t.Fatal("existing output changed")
	}
	if stdout.Len() != 0 {
		t.Fatal("historical document leaked to stdout")
	}
}

func TestDataHistoryExportRejectsMissingSourceAndInvalidArguments(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	for _, args := range [][]string{{"data"}, {"data", "export-upgrade-history"},
		{"data", "export-upgrade-history", "--database", missing, "--output", missing + ".jsonl"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, nil, &stdout, &stderr, defaultDependencies()); code == 0 {
			t.Fatal("invalid command succeeded")
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing source was created")
	}
}
