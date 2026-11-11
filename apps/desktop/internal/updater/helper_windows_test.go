//go:build windows

package updater

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestMain lets a disposable copy of the test executable exercise exactly the
// same pre-Wails helper entry as the shipped desktop, without any user workspace.
func TestMain(m *testing.M) {
	if os.Getenv("LLM_TEST_UPDATE_CHILD") == "1" {
		if handled, err := RunHelper(os.Args[1:]); handled {
			if err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
		if len(os.Args) == 1 {
			if os.WriteFile(os.Getenv("LLM_TEST_UPDATE_MARKER"), []byte("restarted"), 0600) != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestPrepareInstallChild(t *testing.T) {
	if os.Getenv("LLM_TEST_UPDATE_CHILD") != "1" {
		t.Skip("disposable child process only")
	}
	commit, err := prepareHelper(os.Getenv("LLM_TEST_UPDATE_PACKAGE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	// The helper must wait until this actual process exits before replacing it.
	os.Exit(0)
}

func TestWindowsPortableHelperWaitsReplacesAndRestarts(t *testing.T) {
	testWindowsPortableHelper(t, false)
}

func TestWindowsPortableHelperRestoresUnstartableCandidate(t *testing.T) {
	testWindowsPortableHelper(t, true)
}

func testWindowsPortableHelper(t *testing.T, recoverOld bool) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	installed := filepath.Join(directory, "llm-test-studio.exe")
	if err := copyFile(self, installed); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("llm-test-studio.exe")
	if err != nil {
		t.Fatal(err)
	}
	newBinary := binary
	if recoverOld {
		newBinary = []byte("MZinvalid executable")
	}
	_, _ = entry.Write(newBinary)
	_ = writer.Close()
	downloadDirectory := filepath.Join(directory, "download-test")
	_ = os.Mkdir(downloadDirectory, 0700)
	packagePath := filepath.Join(downloadDirectory, "windows-amd64.zip")
	_ = os.WriteFile(packagePath, buffer.Bytes(), 0600)
	marker := filepath.Join(directory, "restarted")
	child := exec.Command(installed, "-test.run=^TestPrepareInstallChild$")
	child.Env = append(os.Environ(), "LLM_TEST_UPDATE_CHILD=1", "LLM_TEST_UPDATE_MARKER="+marker, "LLM_TEST_UPDATE_PACKAGE="+packagePath)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("prepare helper: %s: %v", output, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "restarted" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("helper did not restart the updated application")
	}
	backups, _ := filepath.Glob(installed + ".previous-*")
	updated, _ := os.ReadFile(installed)
	if recoverOld {
		failed, _ := filepath.Glob(installed + ".failed-*")
		if len(failed) != 1 || len(backups) != 0 || !bytes.Equal(updated, binary) || !failedInstall(directory) {
			t.Fatal("helper failed to restore the previous executable and report failure")
		}
	} else {
		if len(backups) != 1 {
			t.Fatal("helper did not preserve the old executable")
		}
		backup, _ := os.ReadFile(backups[0])
		if !bytes.Equal(backup, binary) || !bytes.Equal(updated, binary) || failedInstall(directory) {
			t.Fatal("helper changed executable bytes or reported failure")
		}
	}
	// Wait for the copied helper to leave its executable before TempDir cleanup.
	helpers, _ := filepath.Glob(filepath.Join(downloadDirectory, "install-*", "update-helper.exe"))
	for _, helper := range helpers {
		for time.Now().Before(deadline) {
			if os.Remove(helper) == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func TestInstallerWaitChild(t *testing.T) {
	switch os.Getenv("LLM_TEST_INSTALLER_WAIT") {
	case "success":
		os.Exit(0)
	case "failure":
		os.Exit(7)
	case "running":
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	default:
		t.Skip("disposable installer process only")
	}
}

func TestInstallerWaitDistinguishesFailureFromStillRunning(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"success", "failure", "running"} {
		t.Run(state, func(t *testing.T) {
			command := exec.Command(self, "-test.run=^TestInstallerWaitChild$")
			command.Env = append(os.Environ(), "LLM_TEST_INSTALLER_WAIT="+state)
			hideHelper(command)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Wait()
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(handle)
			timeout := uint32(5_000)
			if state == "running" {
				timeout = 0
			}
			finished, err := waitForInstaller(handle, timeout)
			if finished != (state != "running") || (err == nil) != (state == "success") {
				t.Fatalf("wait for %s: finished=%v error=%v", state, finished, err)
			}
		})
	}
}
