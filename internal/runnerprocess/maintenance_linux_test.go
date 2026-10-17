package runnerprocess_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
)

func TestMaintenancePersistsAcrossRunnerRestartAndKeepsWatchdogHealthy(t *testing.T) {
	path, _ := newProcessConfig(t)
	child := startRunner(t, path, "")
	command := jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		Action: jobcontract.MaintenancePause,
	}
	document, _ := json.Marshal(command)
	response := processRequest(
		t,
		child.ready,
		"/runner/maintenance",
		document,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatal("runner did not persist the upgrade gate")
	}
	_ = response.Body.Close()
	if err := child.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.done:
		if child.err != nil {
			t.Fatal("paused runner failed normal shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("paused runner did not stop")
	}
	socket := filepath.Join(t.TempDir(), "s")
	connection, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	restarted := startRunner(t, path, socket)
	response = processRequest(
		t,
		restarted.ready,
		"/runner",
		nil,
	)
	var info jobcontract.RunnerInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	valid := info.Validate() == nil && !info.Ready && info.Healthy && info.Pending == 0 &&
		info.MaintenanceOperationID == command.OperationID
	if !valid {
		t.Fatal("restart reopened intake or lost upgrade ownership")
	}
	submission := processSubmission(t, restarted.ready, "http://127.0.0.1:1")
	document, _ = json.Marshal(submission)
	defer clear(document)
	response = processRequest(
		t,
		restarted.ready,
		"/jobs",
		document,
	)
	if response.StatusCode != http.StatusLocked {
		t.Fatal("restarted paused runner accepted new work")
	}
	_ = response.Body.Close()
	for _, expected := range []string{"READY=1", "WATCHDOG=1"} {
		if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		message := make([]byte, 128)
		size, _, err := connection.ReadFromUnix(message)
		if err != nil || string(message[:size]) != expected {
			t.Fatalf(
				"paused notification=%q, want %s: %v",
				message[:size],
				expected,
				err,
			)
		}
	}
	command.Action = jobcontract.MaintenanceResume
	document, _ = json.Marshal(command)
	response = processRequest(
		t,
		restarted.ready,
		"/runner/maintenance",
		document,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatal("upgrade owner could not resume after restart")
	}
	_ = response.Body.Close()
}

func TestUnsupportedMaintenanceFormatIsRejectedBeforeDatabaseAccess(t *testing.T) {
	path, config := newProcessConfig(t)
	record := jobcontract.MaintenanceRecord{
		Contract: jobcontract.CurrentContract + 1, NodeInstallationID: config.InstallationID,
		OperationID: "55555555-5555-4555-8555-555555555555", Paused: true, ChangedAt: time.Now().UTC(),
	}
	document, _ := json.Marshal(record)
	controlPath := filepath.Join(path, "maintenance.json")
	if err := os.WriteFile(controlPath, document, 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(path, "runner.db")
	sentinel := []byte("preserve unknown existing data")
	if err := os.WriteFile(database, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	err := runnerprocess.Run(t.Context(), runnerprocess.Options{StatePath: path, Version: "preflight-test"})
	if !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatalf("unsupported maintenance record was not the first rejected input: %v", err)
	}
	retained, err := os.ReadFile(controlPath)
	if err != nil || !bytes.Equal(retained, document) {
		t.Fatal("rejected maintenance record was changed")
	}
	retained, err = os.ReadFile(database)
	if err != nil || !bytes.Equal(retained, sentinel) {
		t.Fatal("rejected maintenance record reached database writes")
	}
}
