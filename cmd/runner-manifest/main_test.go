package main

import (
	"bytes"
	"testing"
)

func TestManifestCommandRejectsIncompleteArgumentsWithoutOutput(t *testing.T) {
	for _, arguments := range [][]string{
		{}, {"unexpected"}, {"-unknown"}, {"-directory", t.TempDir(), "-release", "v1"},
	} {
		var output, diagnostics bytes.Buffer
		if status := run(arguments, &output, &diagnostics); status != 1 || output.Len() != 0 {
			t.Fatal("incomplete build produced a release manifest")
		}
	}
}
