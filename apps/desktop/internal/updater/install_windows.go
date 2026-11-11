//go:build windows

package updater

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hideHelper(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func prepareInstall(path string) (func() error, error) { return prepareHelper(path) }

func waitForParent(id int) (func() error, func(), error) {
	if id <= 0 {
		return nil, nil, errors.New("invalid parent process")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(id))
	if err != nil {
		return nil, nil, err
	}
	return func() error {
		result, err := windows.WaitForSingleObject(handle, 90_000)
		if err != nil || result != windows.WAIT_OBJECT_0 {
			return errors.New("parent process has not exited")
		}
		return nil
	}, func() { _ = windows.CloseHandle(handle) }, nil
}

func applyPlan(plan installPlan) error {
	var err error
	if plan.Installer {
		var finished bool
		finished, err = runInstaller(plan.Payload, filepath.Dir(plan.Target))
		if !finished {
			// The installer may still be replacing application files. Do not
			// race it by starting a possibly half-written executable.
			recordInstallResult(plan, true)
			return err
		}
	} else {
		err = replaceExecutable(plan)
	}
	recordInstallResult(plan, err != nil)
	// A failed installer or a UAC cancellation leaves the existing program usable.
	command := exec.Command(plan.Target)
	command.Dir = filepath.Dir(plan.Target)
	startErr := command.Start()
	if startErr == nil {
		_ = command.Process.Release()
	} else if !plan.Installer && err == nil {
		// Start failure happens before the new binary can migrate data. Restore
		// the old binary only in this case; never roll back after a successful
		// process start because the workspace may already have been upgraded.
		backup := plan.Target + ".previous-" + filepath.Base(filepath.Dir(plan.Payload))
		failed := plan.Target + ".failed-" + filepath.Base(filepath.Dir(plan.Payload))
		if os.Rename(plan.Target, failed) == nil && os.Rename(backup, plan.Target) == nil {
			recordInstallResult(plan, true)
			restart := exec.Command(plan.Target)
			restart.Dir = filepath.Dir(plan.Target)
			if restart.Start() == nil {
				_ = restart.Process.Release()
			}
		}
	}
	return errors.Join(err, startErr)
}

// ShellExecuteEx lets Windows display UAC for the installer itself. The helper
// stays unelevated and waits for that installer before restarting the desktop.
func runInstaller(path, directory string) (bool, error) {
	type shellExecuteInfo struct {
		Size       uint32
		Mask       uint32
		Window     uintptr
		Verb       *uint16
		File       *uint16
		Parameters *uint16
		Directory  *uint16
		Show       int32
		Instance   uintptr
		IDList     uintptr
		Class      *uint16
		ClassKey   uintptr
		HotKey     uint32
		Icon       uintptr
		Process    windows.Handle
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true, err
	}
	parameters, err := windows.UTF16PtrFromString("/S /D=" + directory)
	if err != nil {
		return true, err
	}
	workingDirectory, _ := windows.UTF16PtrFromString(filepath.Dir(path))
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file,
		Parameters: parameters, Directory: workingDirectory, Show: 1}
	info.Size = uint32(unsafe.Sizeof(info))
	procedure := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	result, _, callErr := procedure.Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return true, callErr
	}
	if info.Process == 0 {
		return false, errors.New("installer process unavailable")
	}
	defer windows.CloseHandle(info.Process)
	return waitForInstaller(info.Process, uint32((15*time.Minute)/time.Millisecond))
}

func waitForInstaller(process windows.Handle, timeout uint32) (bool, error) {
	wait, err := windows.WaitForSingleObject(process, timeout)
	if err != nil || wait != windows.WAIT_OBJECT_0 {
		return false, errors.New("installer did not finish")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		return true, err
	}
	if code != 0 {
		return true, errors.New("installer failed")
	}
	return true, nil
}
