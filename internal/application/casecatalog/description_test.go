package casecatalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/casecodec"
)

func TestDescribedCaseSavesPortablyAndRejectedReplacementPreservesFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := casecodec.DecodeFilesystemCase("group/case/case.json", []byte(caseJSON("D001", "Explained case")))
	if err != nil {
		t.Fatal(err)
	}
	legacy.Description = "Purpose: check one response.\nPass: current protocol assertions succeed."
	if err := service.SaveCase(ctx, "group", "case", legacy); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "group", "case", "case.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.Snapshot(ctx)
	if err != nil || len(snapshot.Groups) != 1 || snapshot.Groups[0].Cases[0].Description != legacy.Description {
		t.Fatalf("reopened catalog lost the explanation: %#v, %v", snapshot, err)
	}
	for _, invalidVersion := range []int{2, 4} {
		var invalid map[string]any
		if err := json.Unmarshal(before, &invalid); err != nil {
			t.Fatal(err)
		}
		invalid["schema_version"] = invalidVersion
		raw, err := json.Marshal(invalid)
		if err != nil {
			t.Fatal(err)
		}
		if err := reopened.Save(ctx, "group", "case", raw); err == nil {
			t.Fatalf("accepted unsupported version %d", invalidVersion)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("rejected replacement modified authored file: %v", err)
		}
	}
}
