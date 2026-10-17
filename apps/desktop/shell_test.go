package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrontendEmbedMarkerSurvivesCleanCheckoutAndFrontendBuild(t *testing.T) {
	const markerPath = "wails-embed.txt"
	source, err := os.ReadFile(filepath.Join("frontend", "public", markerPath))
	if err != nil {
		t.Fatalf("read public embed marker: %v", err)
	}
	embedded, err := frontendAssets.ReadFile(filepath.ToSlash(filepath.Join("frontend", "dist", markerPath)))
	if err != nil {
		t.Fatalf("read embedded dist marker: %v", err)
	}
	if string(embedded) != string(source) {
		t.Fatalf("embedded marker = %q, want public marker %q", embedded, source)
	}
}
