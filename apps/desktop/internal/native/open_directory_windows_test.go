//go:build windows

package native

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDirectoryOpenerExitTreatsExplorerExitOneAsSuccess(t *testing.T) {
	arguments := strings.Join(os.Args, " ")
	if strings.Contains(arguments, "helper-explorer-exit-one") {
		os.Exit(1)
	}
	if strings.Contains(arguments, "helper-explorer-exit-seven") {
		os.Exit(7)
	}

	one := runDirectoryOpener(
		os.Args[0], time.Second,
		"-test.run=TestDirectoryOpenerExitTreatsExplorerExitOneAsSuccess", "helper-explorer-exit-one",
	)
	if one == nil {
		t.Fatal("runDirectoryOpener(exit 1) error = nil, want exit error")
	}
	if tolerated := directoryOpenerExit(one); tolerated != nil {
		t.Fatalf("directoryOpenerExit(exit 1) error = %v, want nil", tolerated)
	}

	seven := runDirectoryOpener(
		os.Args[0], time.Second,
		"-test.run=TestDirectoryOpenerExitTreatsExplorerExitOneAsSuccess", "helper-explorer-exit-seven",
	)
	if seven == nil {
		t.Fatal("runDirectoryOpener(exit 7) error = nil, want exit error")
	}
	if passed := directoryOpenerExit(seven); passed == nil || passed != seven {
		t.Fatalf("directoryOpenerExit(exit 7) error = %v, want the original error", passed)
	}
}
