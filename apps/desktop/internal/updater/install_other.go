//go:build !windows

package updater

import (
	"errors"
	"os/exec"
	"runtime"
)

func hideHelper(_ *exec.Cmd) {}

func waitForParent(_ int) (func() error, func(), error) {
	return nil, nil, errors.New("Windows update helper required")
}

func prepareInstall(path string) (func() error, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("desktop update installation unsupported")
	}
	command := exec.Command("/usr/bin/open", path)
	if err := command.Start(); err != nil {
		return nil, err
	}
	_ = command.Process.Release()
	return nil, nil
}

func applyPlan(_ installPlan) error { return errors.New("Windows update helper required") }
