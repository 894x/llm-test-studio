//go:build windows

package main

import (
	"os"
	"testing"
)

func TestTerminationSignalsOnWindowsUseInterrupt(t *testing.T) {
	t.Parallel()

	signals := terminationSignals()
	if len(signals) != 1 || signals[0] != os.Interrupt {
		t.Fatalf("termination signals = %#v, want only os.Interrupt", signals)
	}
}
