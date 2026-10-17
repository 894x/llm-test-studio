package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(arguments []string, output, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("runner-manifest", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	directory := flags.String("directory", "", "directory containing both Linux runner executables")
	release := flags.String("release", "", "explicit release tag or development build label")
	commit := flags.String("commit", "", "source checkout commit")
	dirty := flags.Bool("dirty", false, "source checkout contains uncommitted changes")
	if err := flags.Parse(arguments); err != nil {
		return 1
	}
	if flags.NArg() != 0 || *directory == "" {
		_, _ = fmt.Fprintln(diagnostics, runnerartifacts.ErrInvalid)
		return 1
	}
	root, err := filepath.Abs(*directory)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, runnerartifacts.ErrInvalid)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	manifest, err := runnerartifacts.Describe(ctx, root, runnerartifacts.BuildMetadata{
		Release: *release, SourceCommit: *commit, SourceDirty: *dirty,
	})
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 1
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		_, _ = fmt.Fprintln(diagnostics, "runner_manifest_write_failed")
		return 1
	}
	return 0
}
