//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
)

func TestTerminationSignalsOnUnixIncludeInterruptAndSIGTERM(t *testing.T) {
	t.Parallel()

	signals := terminationSignals()
	if len(signals) != 2 || signals[0] != os.Interrupt || signals[1] != syscall.SIGTERM {
		t.Fatalf("termination signals = %#v, want os.Interrupt and SIGTERM", signals)
	}
}
