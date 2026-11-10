//go:build windows

package benchmarks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDatasetCacheInspectionAcceptsWindowsShortRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte("prepared"), 0600); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	longPath, err := windows.UTF16PtrFromString(resolvedRoot)
	if err != nil {
		t.Fatal(err)
	}
	required, err := windows.GetShortPathName(longPath, nil, 0)
	if err != nil || required == 0 {
		t.Fatalf("find short path size: %v", err)
	}
	buffer := make([]uint16, required)
	written, err := windows.GetShortPathName(longPath, &buffer[0], uint32(len(buffer)))
	if err != nil || written >= uint32(len(buffer)) {
		t.Fatalf("find short path: %v", err)
	}
	shortPath := windows.UTF16ToString(buffer[:written])
	if strings.EqualFold(shortPath, resolvedRoot) {
		t.Skipf("this volume does not generate Windows short path aliases: %q", root)
	}
	wantSize, wantFiles, wantDigest, err := inspectDatasetCache(t.Context(), resolvedRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	size, files, digest, err := inspectDatasetCache(t.Context(), shortPath, true)
	if err != nil {
		t.Fatalf("short cache root rejected: %v", err)
	}
	if size != wantSize || files != wantFiles || digest != wantDigest {
		t.Fatal("short and long paths disagree about the same cached data")
	}
}
