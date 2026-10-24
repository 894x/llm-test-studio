package runnerprocess_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
)

func TestTaskConcurrencyPersistsAcrossRealRunnerRestart(t *testing.T) {
	path, original := newProcessConfig(t)
	child := startRunner(t, path, "")
	command := jobcontract.TaskConcurrencyCommand{Contract: jobcontract.CurrentContract,
		NodeID: original.NodeID, NodeInstallationID: original.InstallationID,
		ExpectedMaxActiveJobs: original.Budget.MaxActiveJobs, MaxActiveJobs: 4}
	document, _ := json.Marshal(command)
	response := processRequest(t, child.ready, "/runner/task-concurrency", document)
	var info jobcontract.RunnerInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || info.Validate() != nil || info.Budget.MaxActiveJobs != 4 {
		t.Fatal("running process did not confirm its new task limit")
	}
	stored, err := os.ReadFile(filepath.Join(path, runnerprocess.ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(stored)
	config, err := runnerconfig.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	defer config.Clear()
	expected := original
	expected.Budget.MaxActiveJobs = 4
	if !reflect.DeepEqual(config, expected) {
		t.Fatal("updating task slots changed identity, authorization or other budgets")
	}
	if err := child.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.done:
		if child.err != nil {
			t.Fatal("runner did not shut down cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner failed to stop")
	}
	restarted := startRunner(t, path, "")
	response = processRequest(t, restarted.ready, "/runner", nil)
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if info.Validate() != nil || info.Budget.MaxActiveJobs != 4 || info.NodeID != original.NodeID ||
		info.NodeInstallationID != original.InstallationID {
		t.Fatal("restart lost the setting or changed the node identity")
	}
}

func TestTaskConcurrencyRejectsChangedPrivateConfigWithoutMutation(t *testing.T) {
	path, config := newProcessConfig(t)
	child := startRunner(t, path, "")
	command := jobcontract.TaskConcurrencyCommand{Contract: jobcontract.CurrentContract,
		NodeID: config.NodeID, NodeInstallationID: config.InstallationID,
		ExpectedMaxActiveJobs: config.Budget.MaxActiveJobs, MaxActiveJobs: 4}
	config.NodeID = "99999999-9999-4999-8999-999999999999"
	changed, _ := json.Marshal(config)
	defer clear(changed)
	name := filepath.Join(path, runnerprocess.ConfigName)
	if err := os.WriteFile(name, changed, 0600); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(command)
	response := processRequest(t, child.ready, "/runner/task-concurrency", document)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatal("replaced private identity was not rejected")
	}
	actual, err := os.ReadFile(name)
	defer clear(actual)
	if err != nil || !bytes.Equal(actual, changed) {
		t.Fatal("rejected configuration was modified")
	}
}
