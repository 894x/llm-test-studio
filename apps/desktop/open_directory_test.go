package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenDirectoryRejectsInvalidTargetsBeforeLaunchingSystemShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	for _, target := range []string{filepath.Join(root, "missing"), filePath} {
		if err := openDirectory(target); err == nil {
			t.Fatalf("openDirectory(%q) error = nil, want validation failure", target)
		}
	}
}

func TestDirectoryOpenerWaitsForExitAndBoundsHungCommands(t *testing.T) {
	if strings.Contains(strings.Join(os.Args, " "), "helper-exit") {
		os.Exit(7)
	}
	if strings.Contains(strings.Join(os.Args, " "), "helper-hang") {
		time.Sleep(time.Second)
		return
	}

	err := runDirectoryOpener(os.Args[0], time.Second, "-test.run=TestDirectoryOpenerWaitsForExitAndBoundsHungCommands", "helper-exit")
	if err == nil {
		t.Fatal("runDirectoryOpener(exit) error = nil, want non-zero exit")
	}
	err = runDirectoryOpener(os.Args[0], 20*time.Millisecond, "-test.run=TestDirectoryOpenerWaitsForExitAndBoundsHungCommands", "helper-hang")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runDirectoryOpener(hang) error = %v, want deadline exceeded", err)
	}
}

func TestStartedDirectoryOpenerReportsFastFailureWithoutKillingLongRunningHandler(t *testing.T) {
	arguments := strings.Join(os.Args, " ")
	if strings.Contains(arguments, "helper-start-exit") {
		os.Exit(7)
	}
	if index := indexOfArgument(os.Args, "helper-start-complete"); index >= 0 {
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(os.Args[index+1], []byte("completed"), 0o600); err != nil {
			os.Exit(8)
		}
		return
	}

	err := startDirectoryOpener(os.Args[0], time.Second, "-test.run=TestStartedDirectoryOpenerReportsFastFailureWithoutKillingLongRunningHandler", "helper-start-exit")
	if err == nil {
		t.Fatal("startDirectoryOpener(exit) error = nil, want fast non-zero exit")
	}
	marker := filepath.Join(t.TempDir(), "completed")
	started := time.Now()
	err = startDirectoryOpener(os.Args[0], 20*time.Millisecond, "-test.run=TestStartedDirectoryOpenerReportsFastFailureWithoutKillingLongRunningHandler", "helper-start-complete", marker)
	if err != nil {
		t.Fatalf("startDirectoryOpener(long running) error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("startDirectoryOpener(long running) took %v, want bounded initial wait", elapsed)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("long-running helper was killed before completing")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func indexOfArgument(arguments []string, target string) int {
	for index, argument := range arguments {
		if argument == target && index+1 < len(arguments) {
			return index
		}
	}
	return -1
}
