package bootstrap

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
)

func TestStartupFailureRecognizesWindowsDiskFull(t *testing.T) {
	for _, number := range []syscall.Errno{39, 112} {
		err := &os.PathError{Op: "write", Path: "private", Err: number}
		var failure *bridge.StartupFailure
		if !errors.As(classifyStartupFailure(err), &failure) || failure.Code != "disk_full" {
			t.Fatalf("Windows disk full classification = %+v", failure)
		}
	}
}
