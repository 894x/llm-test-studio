//go:build !windows

package benchmarkruntime

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
