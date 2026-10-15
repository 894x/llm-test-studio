package ssh

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
	cryptossh "golang.org/x/crypto/ssh"
)

const provisionOperationID = "55555555-5555-4555-8555-555555555555"

func runnerMetadata() jobcontract.RunnerInfo {
	return jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: "11111111-1111-4111-8111-111111111111",
		NodeInstallationID: "22222222-2222-4222-8222-222222222222",
		EngineBuildID:      strings.Repeat("a", 64), RunnerVersion: "another-client-build", OS: "linux", Arch: "amd64",
		Capabilities: jobcontract.RunnerCapabilities{
			JobKinds: []jobcontract.Kind{jobcontract.KindRun}, Protocols: []string{"openai-chat"}, Features: []string{},
		},
		Budget: jobcontract.RunnerBudget{
			MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 2000,
			QueueTimeoutNS: int64(time.Hour), ExecutionTimeoutNS: int64(time.Hour),
		},
		Ready: true, Healthy: true,
	}
}

func provisionReceipt() runnerinstall.Result {
	return runnerinstall.Result{
		Contract: jobcontract.CurrentContract, OperationID: provisionOperationID, Runner: runnerMetadata(),
		ListenPort: 44917, ObservedAt: time.Now().UTC(),
	}
}

func TestProvisionOutputHandlesFragmentsAndRejectsUntrustedControlData(t *testing.T) {
	result := provisionReceipt()
	progress := runnerinstall.Progress{OperationID: result.OperationID, Stage: "checking"}
	progressJSON, _ := json.Marshal(map[string]runnerinstall.Progress{"progress": progress})
	resultJSON, _ := json.Marshal(map[string]runnerinstall.Result{"result": result})
	wire := append(append(progressJSON, '\n'), append(resultJSON, '\n')...)
	observed := []runnerinstall.Progress{}
	output := &provisionOutput{
		operationID: result.OperationID, pending: []byte{},
		progress: func(value runnerinstall.Progress) { observed = append(observed, value) },
	}
	for _, value := range wire {
		if _, err := output.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	valid := len(observed) == 1 && output.result != nil && len(output.pending) == 0
	if !valid || output.result.Runner.EngineBuildID != result.Runner.EngineBuildID {
		t.Fatal("fragmented output lost progress or the immutable receipt")
	}
	for _, invalid := range []string{
		`{"progress":{"operation_id":"` + result.OperationID + `","stage":"unknown"}}`,
		`{"progress":null}`, `{"result":null}`, `{"extra":{}}`,
		`{"progress":{"operation_id":"other","stage":"checking"}}`,
		string(resultJSON) + "\n" + string(resultJSON),
		string(progressJSON[:len(progressJSON)-1]) + `,"progress":{}}`,
		strings.Repeat(" ", runnerinstall.MaxRecordBytes+1),
	} {
		output := &provisionOutput{operationID: result.OperationID, pending: []byte{}}
		if _, err := output.Write([]byte(invalid + "\n")); !errors.Is(err, ErrIntegrity) {
			t.Fatal("invalid progress, duplicate receipt, or excessive output was accepted")
		}
	}
	for _, suffix := range []string{"../other", "abcdefghij;id", "abcdefghij\n", "abcdefghij/child", ""} {
		if ValidBootstrapDirectory(bootstrapPrefix + suffix) {
			t.Fatal("untrusted staging directory entered a shell command")
		}
	}
}

func TestProvisionStopsBeforeExecutionWhenSFTPLacksDurableUpload(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private Unix staging modes")
	}
	peer := newTestPeer(t)
	directory := bootstrapPrefix + rand.Text()[:10]
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(directory + "/llm-test-runner"); _ = os.Remove(directory) })
	program := []byte("non executable fixture for the network installation boundary")
	digest := sha256.Sum256(program)
	request := runnerinstall.Request{
		Contract: jobcontract.CurrentContract, OperationID: provisionOperationID,
		ArtifactSHA256: hex.EncodeToString(digest[:]), ListenPort: 44917,
	}
	commands := make(chan string, 4)
	peer.execute = func(command string, channel cryptossh.Channel) {
		commands <- command
		status := uint32(0)
		defer func() {
			_, _ = channel.SendRequest("exit-status", false, cryptossh.Marshal(struct{ Status uint32 }{Status: status}))
		}()
		input, err := io.ReadAll(io.LimitReader(channel, 8192))
		if err != nil {
			status = 1
			return
		}
		switch command {
		case "/bin/sh -s":
			if string(input) == systemProbe {
				_, _ = io.WriteString(channel, "llm-test-system-probe\n0\nLinux\nx86_64\nsystemd 249\nsystemd\nyes\nyes\n")
				return
			}
			expected := "set -eu\numask 077\nmktemp -d " + bootstrapPrefix + "XXXXXXXXXX\n"
			if string(input) != expected {
				status = 1
				return
			}
			_, _ = io.WriteString(channel, directory+"\n")
		default:
			status = 1
		}
	}
	client := dialPeer(t, peer)
	_, err := client.Provision(
		t.Context(),
		request,
		Artifact{
			OS: "linux", Arch: "amd64", Size: int64(len(program)), SHA256: request.ArtifactSHA256,
			Source: bytes.NewReader(program),
		},
		nil,
	)
	var failure *ProvisionError
	if !errors.Is(err, ErrUnsupported) || !errors.As(err, &failure) {
		t.Fatal("unsupported fsync did not prevent installer execution")
	}
	if len(commands) != 2 || failure.CleanupPending {
		t.Fatal("rejected upload executed a program or left unreported temporary files")
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("exact bootstrap artifacts were not cleaned")
	}
}

func TestBootstrapCleanupPreservesUnknownContentsAndRejectsPublicFiles(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("root-owned Linux bootstrap filesystem boundary")
	}
	peer := newTestPeer(t)
	client := dialPeer(t, peer)
	directory := bootstrapPrefix + rand.Text()[:10]
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	unknown := directory + "/unrelated"
	partial := directory + "/.upload-" + rand.Text()
	program := directory + "/llm-test-runner"
	t.Cleanup(func() {
		for _, name := range []string{unknown, partial, program, directory} {
			_ = os.Remove(name)
		}
	})
	if err := os.WriteFile(unknown, []byte("retain this unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := client.CleanupBootstrap(t.Context(), directory); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown bootstrap content was accepted for deletion")
	}
	document, err := os.ReadFile(unknown)
	if err != nil || string(document) != "retain this unrelated data" {
		t.Fatal("rejected cleanup mutated unknown content")
	}
	if err := os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("partial upload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("program fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(program, 0755); err != nil {
		t.Fatal(err)
	}
	if err := client.CleanupBootstrap(t.Context(), directory); !errors.Is(err, ErrInvalid) {
		t.Fatal("public bootstrap file was accepted for deletion")
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatal("cleanup deleted a file before validating all contents")
	}
	if err := os.Chmod(program, 0700); err != nil {
		t.Fatal(err)
	}
	if err := client.CleanupBootstrap(t.Context(), directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("validated private staging files were not removed")
	}
	if err := client.CleanupBootstrap(t.Context(), directory); err != nil {
		t.Fatal("lost cleanup acknowledgment could not be confirmed idempotently")
	}
}
