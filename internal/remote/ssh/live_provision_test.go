package ssh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type liveProvisionInput struct {
	Endpoint               Endpoint `json:"endpoint"`
	Password               []byte   `json:"password"`
	ArtifactPath           string   `json:"artifact_path"`
	OtherArtifactPath      string   `json:"other_artifact_path"`
	OperationID            string   `json:"operation_id"`
	OtherOperationID       string   `json:"other_operation_id"`
	MaintenanceOperationID string   `json:"maintenance_operation_id"`
	CleanupDirectories     []string `json:"cleanup_directories"`
}

func readLiveProvisionInput(t *testing.T) liveProvisionInput {
	t.Helper()
	if os.Getenv("LLM_TEST_LIVE_PROVISION") != "1" {
		t.Skip("explicit physical-node installation validation only")
	}
	var input liveProvisionInput
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("invalid physical installation input")
	}
	return input
}

// This explicitly enabled test installs the product on an authorized root SSH
// node. Inputs containing credentials come only from stdin, never flags or files.
// It does not send requests to any model provider.
func TestLiveProvisionSharedRunner(t *testing.T) {
	input := readLiveProvisionInput(t)
	lease, err := credentials.NewTemporaryLease(input.Password)
	clear(input.Password)
	if err != nil {
		t.Fatal("missing physical installation authentication")
	}
	defer lease.Close()
	client, err := Dial(t.Context(), input.Endpoint, PasswordAuthentication(lease))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, directory := range input.CleanupDirectories {
		if err := client.CleanupBootstrap(t.Context(), directory); err != nil {
			t.Fatal("cannot clean the previous verified bootstrap directory")
		}
		t.Logf("cleaned previous bootstrap directory: %s", directory)
	}
	artifact := liveArtifact(t, input.ArtifactPath)
	request := runnerinstall.Request{
		Contract: jobcontract.CurrentContract, OperationID: input.OperationID,
		ArtifactSHA256: artifact.SHA256, ListenPort: 44917,
	}
	result, err := client.Provision(
		t.Context(),
		request,
		artifact,
		func(progress runnerinstall.Progress) { t.Logf("installation stage: %s", progress.Stage) },
	)
	if err != nil {
		var failure *ProvisionError
		if errors.As(err, &failure) {
			t.Logf(
				"operation=%s bootstrap=%s cleanup_pending=%t cause=%v",
				failure.OperationID,
				failure.TemporaryDirectory,
				failure.CleanupPending,
				failure.Cause,
			)
		}
		if operation, readErr := client.InstallationOperation(t.Context(), request.OperationID); readErr == nil {
			t.Logf("durable installation: stage=%s code=%s", operation.Stage, operation.ErrorCode)
		}
		if errors.Is(err, runnerinstall.ErrService) {
			facts, _ := json.Marshal(liveServiceFacts(t, client))
			t.Logf("service facts: %s", facts)
		}
		t.Fatal("physical installation failed")
	}
	node, info, err := client.ConnectNode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if result.Runner.EngineBuildID != info.EngineBuildID || !info.Ready {
		t.Fatal("live health did not confirm the installed executable")
	}
	operation, err := client.InstallationOperation(t.Context(), request.OperationID)
	confirmedOperation := err == nil && operation.Result != nil &&
		operation.Result.Runner.EngineBuildID == info.EngineBuildID
	if !confirmedOperation {
		t.Fatal("durable operation did not confirm the installed build")
	}
	before := liveServiceFacts(t, client)
	other, err := Dial(t.Context(), input.Endpoint, PasswordAuthentication(lease))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_ = lease.Close()
	otherNode, observed, err := other.ConnectNode(t.Context())
	sharedIdentity := err == nil && observed.NodeID == info.NodeID &&
		observed.NodeInstallationID == info.NodeInstallationID
	if !sharedIdentity {
		t.Fatal("second client did not connect to the shared node installation")
	}
	defer otherNode.Close()
	otherArtifact := liveArtifact(t, input.OtherArtifactPath)
	if otherArtifact.SHA256 == info.EngineBuildID {
		t.Fatal("second-client artifact must have a distinct build")
	}
	otherRequest := request
	otherRequest.OperationID, otherRequest.ArtifactSHA256 = input.OtherOperationID, otherArtifact.SHA256
	reused, err := other.Provision(
		t.Context(),
		otherRequest,
		otherArtifact,
		nil,
	)
	compatibleReuse := err == nil && reused.Reused && reused.Runner.EngineBuildID == info.EngineBuildID
	if !compatibleReuse {
		t.Fatal("a different client build failed to reuse the compatible running service")
	}
	after := liveServiceFacts(t, client)
	if after["MainPID"] != before["MainPID"] || after["NRestarts"] != before["NRestarts"] {
		t.Fatal("compatible connection restarted the shared runner")
	}
	liveMaintenance(
		t,
		node,
		input.MaintenanceOperationID,
		"pause",
	)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := sendLiveMaintenance(
			ctx,
			node,
			input.MaintenanceOperationID,
			"resume",
		); err != nil {
			t.Errorf("restore intake failed: %v", err)
		}
	}()
	paused, err := node.Info(t.Context())
	validPause := paused.Healthy && !paused.Ready && paused.Pending == 0 &&
		paused.MaintenanceOperationID == input.MaintenanceOperationID
	if err != nil || !validPause {
		t.Fatal("cannot verify restart while accepted work or an unconfirmed pause remains")
	}
	if _, err := client.runScript(t.Context(),
		"set -eu\nsystemctl kill --kill-who=main --signal=SIGKILL llm-test-runner.service\n"); err != nil {
		t.Fatal("controlled runner process kill failed")
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		current, err := node.Info(t.Context())
		recoveredPause := err == nil && current.Healthy &&
			current.MaintenanceOperationID == input.MaintenanceOperationID
		if recoveredPause {
			facts := liveServiceFacts(t, client)
			if facts["MainPID"] != before["MainPID"] && facts["ActiveState"] == "active" {
				after = facts
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("systemd did not recover the runner with its paused identity")
		}
		liveWait(t, time.Second)
	}
	liveWait(t, 6*time.Second)
	watchdog := liveServiceFacts(t, client)
	initial, _ := strconv.ParseUint(after["WatchdogTimestampMonotonic"], 10, 64)
	latest, _ := strconv.ParseUint(watchdog["WatchdogTimestampMonotonic"], 10, 64)
	if latest == 0 || latest <= initial {
		t.Fatal("restarted process did not advance its systemd watchdog")
	}
	liveMaintenance(
		t,
		node,
		input.MaintenanceOperationID,
		"resume",
	)
	confirmed, err := node.Info(t.Context())
	intakeRestored := err == nil && confirmed.Ready && confirmed.EngineBuildID == info.EngineBuildID
	if !intakeRestored {
		t.Fatal("restarted shared runner did not restore intake on the same build")
	}
	document, _ := json.Marshal(struct {
		Installation runnerinstall.Result   `json:"installation"`
		SharedReuse  runnerinstall.Result   `json:"shared_reuse"`
		Systemd      map[string]string      `json:"systemd"`
		Runner       jobcontract.RunnerInfo `json:"runner"`
	}{Installation: result, SharedReuse: reused, Systemd: watchdog, Runner: confirmed})
	t.Log(string(document))
}

func TestLiveRunnerInstallationDiagnostics(t *testing.T) {
	input := readLiveProvisionInput(t)
	lease, err := credentials.NewTemporaryLease(input.Password)
	clear(input.Password)
	if err != nil {
		t.Fatal("missing physical installation authentication")
	}
	defer lease.Close()
	client, err := Dial(t.Context(), input.Endpoint, PasswordAuthentication(lease))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	document, err := client.runScript(t.Context(), `set -eu
getent passwd llm-test-runner || true
getent group llm-test-runner || true
id llm-test-runner || true
stat -c '%n uid=%u gid=%g mode=%a type=%F' \
 /opt/llm-test-runner /var/lib/llm-test-runner /var/lib/llm-test-runner-install \
 /etc/systemd/system/llm-test-runner.service 2>/dev/null || true
ls -lan /var/lib/llm-test-runner 2>/dev/null || true
systemctl show llm-test-runner.service \
 --property=MainPID,ActiveState,SubState,NeedDaemonReload,Type,Restart,WatchdogUSec,RestartUSec,\
StartLimitIntervalUSec,StartLimitBurst,User,Group,WorkingDirectory,LimitCORE 2>/dev/null || true
`)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(document))
	node, info, err := client.ConnectNode(t.Context())
	if err != nil {
		t.Logf("current runner connection status: %v", err)
		return
	}
	defer node.Close()
	metadata, _ := json.Marshal(info)
	t.Logf("current runner metadata: %s", metadata)
}

func liveArtifact(t *testing.T, path string) Artifact {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("missing local runner artifact")
	}
	t.Cleanup(func() { _ = file.Close() })
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(file, maxUploadBytes+1))
	validArtifact := err == nil && size > 0 && size <= maxUploadBytes
	if !validArtifact {
		t.Fatal("invalid local runner artifact")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return Artifact{
		OS: "linux", Arch: "amd64", Source: file, Size: size, SHA256: hex.EncodeToString(digest.Sum(nil)),
	}
}

func liveServiceFacts(t *testing.T, client *Client) map[string]string {
	t.Helper()
	document, err := client.runScript(t.Context(), "set -eu\nsystemctl show llm-test-runner.service "+
		"--property=MainPID,NRestarts,ActiveState,SubState,User,Group,WatchdogTimestampMonotonic,WatchdogUSec,"+
		"Restart,Result,ExecMainStatus,ExecMainCode,StartLimitIntervalUSec,StartLimitBurst,NeedDaemonReload\n")
	if err != nil {
		t.Fatal("cannot read product service facts")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(document)), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || fields[key] != "" {
			t.Fatal("invalid product service facts")
		}
		fields[key] = value
	}
	return fields
}

func liveMaintenance(t *testing.T, node *Node, operationID string, action jobcontract.MaintenanceAction) {
	t.Helper()
	if err := sendLiveMaintenance(
		t.Context(),
		node,
		operationID,
		action,
	); err != nil {
		t.Fatal(err)
	}
}

func sendLiveMaintenance(
	ctx context.Context,
	node *Node,
	operationID string,
	action jobcontract.MaintenanceAction,
) error {
	document, err := json.Marshal(jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: operationID, Action: action,
	})
	if err != nil {
		return ErrInvalid
	}
	response, err := node.Do(
		ctx,
		http.MethodPost,
		"/runner/maintenance",
		strings.NewReader(string(document)),
	)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return runnerinstall.ErrService
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, runnerinstall.MaxRecordBytes+1))
	return err
}

func liveWait(t *testing.T, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal("physical validation interrupted")
	case <-timer.C:
	}
}
