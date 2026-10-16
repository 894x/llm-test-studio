package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
	"github.com/pkg/sftp"
)

const (
	upgradePrefix       = "/var/tmp/llm-test-runner-upgrade."
	upgradeUnitPrefix   = "llm-test-runner-upgrade-"
	upgradePollInterval = 250 * time.Millisecond
	upgradeObserveLimit = 2 * time.Minute
)

var (
	ErrUpgradePending             = errors.New("remote_runner_upgrade_pending")
	ErrUpgradeArtifactUnavailable = errors.New("remote_runner_upgrade_artifact_unavailable")
)

// UpgradeError retains the operation's identity when the remote outcome cannot
// be observed. Its staging directory is retained; disconnecting never rolls back
// the installation or stops its detached service.
type UpgradeError struct {
	OperationID        string
	TemporaryDirectory string
	Pending            bool
	Cause              error
}

func (*UpgradeError) Error() string                { return "remote_runner_upgrade_failed" }
func (err *UpgradeError) Unwrap() error            { return err.Cause }
func (*UpgradeError) GoString() string             { return "ssh.UpgradeError{redacted}" }
func (*UpgradeError) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

// UpgradeDirectory is persisted by the caller before the first remote mutation.
func UpgradeDirectory(operationID string) string {
	if !domain.IsUUID(operationID) {
		return ""
	}
	return upgradePrefix + operationID
}

// UpgradeOperation reads only the fixed, root-owned atomic operation journal.
// In particular, a missing journal is not permission to launch another operation.
func (client *Client) UpgradeOperation(ctx context.Context, id string) (runnerinstall.UpgradeOperation, error) {
	if !domain.IsUUID(id) {
		return runnerinstall.UpgradeOperation{}, ErrInvalid
	}
	name := runnerinstall.InstallPath + "/upgrade-" + id + ".json"
	document, err := client.ReadPrivate(ctx, name, 0)
	if err != nil {
		return runnerinstall.UpgradeOperation{}, err
	}
	defer clear(document)
	operation, err := runnerinstall.DecodeUpgradeOperation(document)
	if err != nil || operation.Request.OperationID != id {
		return runnerinstall.UpgradeOperation{}, ErrIntegrity
	}
	return operation, nil
}

// Upgrade starts one explicit operation. An existing journal is returned without
// rerunning it; continuation and recovery require their separate explicit calls.
func (client *Client) Upgrade(
	ctx context.Context,
	request runnerinstall.UpgradeRequest,
	artifact Artifact,
	progress func(runnerinstall.UpgradeProgress),
) (runnerinstall.UpgradeOperation, error) {
	return client.upgrade(ctx, upgradeCall{request: request, artifact: artifact, progress: progress, command: "upgrade"})
}

func (client *Client) RetryUpgrade(
	ctx context.Context,
	request runnerinstall.UpgradeRequest,
	artifact Artifact,
	progress func(runnerinstall.UpgradeProgress),
) (runnerinstall.UpgradeOperation, error) {
	return client.upgrade(ctx, upgradeCall{request: request, artifact: artifact, progress: progress, command: "retry-upgrade"})
}

// RecoverUpgrade uses the durable operation's safety boundary. For explicit
// retry or recovery only, a nil artifact Source permits verified staged reuse;
// OS, Arch and SHA256 still identify the original release.
func (client *Client) RecoverUpgrade(
	ctx context.Context,
	request runnerinstall.UpgradeRequest,
	artifact Artifact,
	progress func(runnerinstall.UpgradeProgress),
) (runnerinstall.UpgradeOperation, error) {
	return client.upgrade(ctx, upgradeCall{request: request, artifact: artifact, progress: progress, command: "recover-upgrade"})
}

type upgradeCall struct {
	request  runnerinstall.UpgradeRequest
	artifact Artifact
	progress func(runnerinstall.UpgradeProgress)
	command  string
}

func (call upgradeCall) failure(cause error, pending bool) error {
	return &UpgradeError{
		OperationID: call.request.OperationID, TemporaryDirectory: UpgradeDirectory(call.request.OperationID),
		Pending: pending, Cause: cause,
	}
}

func (call upgradeCall) notify(operation runnerinstall.UpgradeOperation) {
	if call.progress != nil && operation.Stage != "" {
		call.progress(runnerinstall.UpgradeProgress{OperationID: call.request.OperationID, Stage: operation.Stage})
	}
}

func (client *Client) upgrade(ctx context.Context, call upgradeCall) (runnerinstall.UpgradeOperation, error) {
	var operation runnerinstall.UpgradeOperation
	if client == nil || ctx == nil {
		return operation, ErrInvalid
	}
	if err := call.request.Validate(); err != nil {
		return operation, err
	}
	stageOnly := call.artifact.Source == nil
	validSize := call.artifact.Size > 0 && call.artifact.Size <= maxUploadBytes || stageOnly && call.artifact.Size == 0
	validArtifact := call.artifact.OS == "linux" && call.artifact.SHA256 == call.request.ArtifactSHA256
	missingInitialArtifact := stageOnly && call.command == "upgrade"
	validInput := validSize && validArtifact && !missingInitialArtifact
	if !validInput {
		return operation, ErrInvalid
	}

	// Resolve the durable receipt before any upload, unit reset, or launch.
	operation, err := client.UpgradeOperation(ctx, call.request.OperationID)
	present := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return operation, call.failure(err, true)
	}
	if present && operation.Request != call.request {
		return operation, call.failure(runnerinstall.ErrConflict, false)
	}
	if present && call.command == "upgrade" {
		call.notify(operation)
		return operation, nil
	}
	if !present && call.command == "recover-upgrade" {
		return operation, call.failure(os.ErrNotExist, false)
	}
	if !present && call.command == "retry-upgrade" {
		if stageOnly {
			return operation, call.failure(ErrUpgradeArtifactUnavailable, false)
		}
		// An interrupted upload has no server operation yet. The explicit retry
		// creates that same operation, without inventing another identity.
		call.command = "upgrade"
	}
	if present && operation.Stage == "completed" {
		call.notify(operation)
		return operation, nil
	}

	unit, err := client.upgradeUnit(ctx, call.request.OperationID)
	if err != nil {
		return operation, call.failure(err, true)
	}
	if unit.running() {
		call.notify(operation)
		return operation, call.failure(ErrUpgradePending, true)
	}
	if err := client.prepareUpgrade(ctx, call); err != nil {
		return operation, call.failure(err, true)
	}

	// Failed transient units must be released before the explicit continuation.
	// reset-failed does not stop a concurrently started unit. systemd's exact
	// same-operation name and the installer's lock arbitrate that remaining race.
	if unit.active == "failed" {
		output, err := client.runUpgradeCommand(ctx, "systemctl reset-failed -- "+upgradeUnitName(call.request.OperationID))
		clear(output)
		if err != nil {
			return operation, call.failure(err, true)
		}
	}
	command := upgradeLaunchCommand(call.request.OperationID, call.command)
	output, launchErr := client.runUpgradeCommand(ctx, command)
	clear(output)
	// Even a successful SSH exec ACK is not the operation receipt. A lost ACK
	// is resolved through the journal first; this call never sends a second exec.
	observed, readErr := client.UpgradeOperation(ctx, call.request.OperationID)
	if readErr == nil {
		if observed.Request != call.request {
			return observed, call.failure(runnerinstall.ErrConflict, true)
		}
		if observed.Stage == "completed" || observed.Stage == "rolled_back" {
			call.notify(observed)
			return observed, upgradeOperationError(observed)
		}
	}
	if launchErr != nil {
		if readErr == nil {
			operation = observed
		}
		return client.finishUpgradeObservation(ctx, call, operation, launchErr)
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return operation, call.failure(readErr, true)
	}
	return client.observeUpgrade(ctx, call, operation)
}

func upgradeOperationError(operation runnerinstall.UpgradeOperation) error {
	if operation.ErrorCode != "" {
		return runnerinstall.ErrorFromCode(operation.ErrorCode)
	}
	return nil
}

func (client *Client) observeUpgrade(
	ctx context.Context,
	call upgradeCall,
	baseline runnerinstall.UpgradeOperation,
) (runnerinstall.UpgradeOperation, error) {
	ctx, cancel := context.WithTimeout(ctx, upgradeObserveLimit)
	defer cancel()
	latest := baseline
	stage := ""
	for {
		operation, err := client.UpgradeOperation(ctx, call.request.OperationID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return latest, call.failure(err, true)
		}
		if err == nil {
			if operation.Request != call.request {
				return latest, call.failure(runnerinstall.ErrConflict, true)
			}
			latest = operation
			if stage != operation.Stage {
				call.notify(operation)
				stage = operation.Stage
			}
			terminal := operation.Stage == "completed" || operation.Stage == "rolled_back"
			freshFailure := operation.ErrorCode != "" && !operation.ChangedAt.Equal(baseline.ChangedAt)
			if terminal {
				return operation, upgradeOperationError(operation)
			}
			if freshFailure {
				return client.finishUpgradeObservation(ctx, call, operation, ErrUpgradePending)
			}
		}
		unit, err := client.upgradeUnit(ctx, call.request.OperationID)
		if err != nil {
			return client.finishUpgradeObservation(ctx, call, latest, err)
		}
		if !unit.running() {
			return client.finishUpgradeObservation(ctx, call, latest, ErrUpgradePending)
		}
		timer := time.NewTimer(upgradePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return latest, call.failure(ctx.Err(), true)
		case <-timer.C:
		}
	}
}

// A progress callback or unit-state query can span the final server write. Read
// the complete journal again before turning an old failure into a return value.
func (client *Client) finishUpgradeObservation(
	ctx context.Context,
	call upgradeCall,
	previous runnerinstall.UpgradeOperation,
	cause error,
) (runnerinstall.UpgradeOperation, error) {
	operation, err := client.UpgradeOperation(ctx, call.request.OperationID)
	if err != nil {
		return previous, call.failure(err, true)
	}
	if operation.Request != call.request {
		return previous, call.failure(runnerinstall.ErrConflict, true)
	}
	if operation.Stage != previous.Stage {
		call.notify(operation)
	}
	terminal := operation.Stage == "completed" || operation.Stage == "rolled_back"
	if terminal || operation.ErrorCode != "" {
		return operation, upgradeOperationError(operation)
	}
	return operation, call.failure(cause, true)
}

func (client *Client) prepareUpgrade(ctx context.Context, call upgradeCall) error {
	system, err := client.Probe(ctx)
	if err != nil {
		return err
	}
	if system.UID != 0 && !system.HasSudo {
		return runnerinstall.ErrRootRequired
	}
	supported := system.SystemdPID1 && system.SystemdVersion >= 249 && system.HasSHA256 && system.HasRunuser
	if !supported || system.Arch != call.artifact.Arch {
		return runnerinstall.ErrUnsupported
	}
	if call.artifact.Source == nil {
		return client.reuseStagedUpgrade(ctx, call)
	}
	directory := UpgradeDirectory(call.request.OperationID)
	exists := false
	if err := client.withSFTP(ctx, sftpMetadataTimeout, func(remote *sftp.Client) error {
		if err := verifyParents(remote, directory); err != nil {
			return err
		}
		_, err := remote.Lstat(directory)
		exists = err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrTransfer
		}
		return nil
	}); err != nil {
		return err
	}
	if !exists {
		output, err := client.runAdministrativeScript(ctx, "set -eu\numask 077\nmkdir -m 0700 -- "+directory+"\n")
		clear(output)
		if err != nil {
			return err
		}
	}
	input, err := json.Marshal(call.request)
	if err != nil {
		return ErrInvalid
	}
	defer clear(input)
	digest := sha256.Sum256(input)
	uploads := []Upload{
		{Path: directory + "/request.json", Mode: 0600, Size: int64(len(input)),
			SHA256: hex.EncodeToString(digest[:]), Source: bytes.NewReader(input)},
		{Path: directory + "/llm-test-runner", Mode: 0700, Size: call.artifact.Size,
			SHA256: call.artifact.SHA256, Source: call.artifact.Source},
	}
	present := map[string]bool{}
	partials := []Upload{}
	if err := client.withSFTP(ctx, sftpArtifactTimeout, func(remote *sftp.Client) error {
		if err := verifyUpgradeDirectory(remote, directory); err != nil {
			return err
		}
		entries, err := remote.ReadDir(directory)
		if err != nil || len(entries) > len(uploads)+2 {
			return ErrExists
		}
		for _, entry := range entries {
			known := entry.Name() == "request.json" || entry.Name() == "llm-test-runner"
			if !known {
				stat, ok := entry.Sys().(*sftp.FileStat)
				mode := entry.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
				owned := ok && stat.UID == 0 && (mode == 0600 || mode == 0700)
				bounded := entry.Size() >= 0 && entry.Size() <= maxUploadBytes
				valid := validPartialUploadName(entry.Name()) && owned && bounded && entry.Mode().IsRegular()
				if !valid || len(partials) == 2 {
					return ErrExists
				}
				partials = append(partials, Upload{Path: directory + "/" + entry.Name(), Size: entry.Size(), Mode: mode})
				continue
			}
			present[entry.Name()] = true
		}
		for _, upload := range uploads {
			name := strings.TrimPrefix(upload.Path, directory+"/")
			if !present[name] {
				continue
			}
			if err := client.verifyUpgradeArtifact(ctx, remote, upload); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if len(partials) != 0 {
		// Only an operation which has never reached its durable server journal
		// may discard interrupted uploads. A running same-operation unit always
		// wins; completed artifacts and unknown names are never overwritten.
		_, err := client.UpgradeOperation(ctx, call.request.OperationID)
		if !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return ErrExists
			}
			return err
		}
		unit, err := client.upgradeUnit(ctx, call.request.OperationID)
		if err != nil {
			return err
		}
		if unit.running() {
			return ErrUpgradePending
		}
		if err := client.withSFTP(ctx, sftpMetadataTimeout, func(remote *sftp.Client) error {
			if err := verifyUpgradeDirectory(remote, directory); err != nil {
				return err
			}
			for _, partial := range partials {
				info, err := remote.Lstat(partial.Path)
				if err != nil {
					return ErrTransfer
				}
				stat, ok := info.Sys().(*sftp.FileStat)
				mode := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
				unchanged := ok && stat.UID == 0 && info.Mode().IsRegular() && mode == partial.Mode && info.Size() == partial.Size
				if !unchanged {
					return ErrIntegrity
				}
			}
			for _, partial := range partials {
				if err := remote.Remove(partial.Path); err != nil {
					return ErrTransfer
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, upload := range uploads {
		if present[strings.TrimPrefix(upload.Path, directory+"/")] {
			continue
		}
		if err := client.UploadNew(ctx, upload); err != nil {
			return err
		}
	}
	// UploadNew fsyncs each file; syncfs makes the directory entries durable too.
	output, err := client.runUpgradeCommand(ctx, "sync -f -- "+directory)
	clear(output)
	return err
}

// Explicit retry and recovery can survive removal of the local release cache.
// This path requires the same operation's journal and both complete staged files;
// it never creates a directory, uploads a replacement, or cleans partial files.
func (client *Client) reuseStagedUpgrade(ctx context.Context, call upgradeCall) error {
	directory := UpgradeDirectory(call.request.OperationID)
	input, err := json.Marshal(call.request)
	if err != nil {
		return ErrInvalid
	}
	defer clear(input)
	digest := sha256.Sum256(input)
	request := Upload{Path: directory + "/request.json", Mode: 0600, Size: int64(len(input)),
		SHA256: hex.EncodeToString(digest[:])}
	err = client.withSFTP(ctx, sftpArtifactTimeout, func(remote *sftp.Client) error {
		if err := verifyUpgradeDirectory(remote, directory); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrUpgradeArtifactUnavailable
			}
			return err
		}
		entries, err := remote.ReadDir(directory)
		if err != nil {
			return ErrTransfer
		}
		if len(entries) != 2 {
			return ErrUpgradeArtifactUnavailable
		}
		for _, entry := range entries {
			if entry.Name() != "request.json" && entry.Name() != "llm-test-runner" {
				return ErrExists
			}
		}
		if err := client.verifyUpgradeArtifact(ctx, remote, request); err != nil {
			return err
		}
		name := directory + "/llm-test-runner"
		info, err := remote.Lstat(name)
		if err != nil {
			return ErrUpgradeArtifactUnavailable
		}
		validSize := info.Size() > 0 && info.Size() <= maxUploadBytes
		expectedSize := call.artifact.Size == 0 || call.artifact.Size == info.Size()
		if !validSize || !expectedSize {
			return ErrIntegrity
		}
		return client.verifyUpgradeArtifact(ctx, remote, Upload{
			Path: name, Mode: 0700, Size: info.Size(), SHA256: call.request.ArtifactSHA256,
		})
	})
	if err != nil {
		return err
	}
	output, err := client.runUpgradeCommand(ctx, "sync -f -- "+directory)
	clear(output)
	return err
}

func verifyUpgradeDirectory(remote *sftp.Client, directory string) error {
	if err := verifyParents(remote, directory+"/request.json"); err != nil {
		return err
	}
	info, err := remote.Lstat(directory)
	if err != nil {
		return ErrTransfer
	}
	stat, ok := info.Sys().(*sftp.FileStat)
	mode := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if !ok || stat.UID != 0 || !info.IsDir() || mode != 0700 {
		return ErrInvalid
	}
	return nil
}

// Existing staging is hashed by the authenticated host rather than downloaded
// again. New uploads retain UploadNew's independent SFTP readback before rename.
func (client *Client) verifyUpgradeArtifact(ctx context.Context, remote *sftp.Client, upload Upload) error {
	relative, prefixed := strings.CutPrefix(upload.Path, upgradePrefix)
	id, name, separated := strings.Cut(relative, "/")
	directory := UpgradeDirectory(id)
	validPath := prefixed && separated && directory != "" && upload.Path == directory+"/"+name
	validFile := name == "request.json" && upload.Mode == 0600 || name == "llm-test-runner" && upload.Mode == 0700
	validSize := upload.Size > 0 && upload.Size <= maxUploadBytes
	if !validPath || !validFile || !validSize || !jobcontract.IsSHA256(upload.SHA256) {
		return ErrInvalid
	}
	before, err := stagedUpgradeMetadata(remote, upload)
	if err != nil {
		return err
	}
	// UUID validation and the two fixed basenames exclude shell metacharacters.
	output, err := client.runUpgradeCommand(ctx, "sha256sum -- '"+upload.Path+"'")
	if err != nil {
		return err
	}
	defer clear(output)
	if string(output) != upload.SHA256+"  "+upload.Path+"\n" {
		return ErrIntegrity
	}
	after, err := stagedUpgradeMetadata(remote, upload)
	if err != nil {
		return err
	}
	if before != after {
		return ErrIntegrity
	}
	return nil
}

type stagedUpgradeFileMetadata struct {
	uid      uint32
	gid      uint32
	mode     os.FileMode
	size     int64
	modified int64
}

func stagedUpgradeMetadata(remote *sftp.Client, upload Upload) (stagedUpgradeFileMetadata, error) {
	var metadata stagedUpgradeFileMetadata
	directory := upload.Path[:strings.LastIndex(upload.Path, "/")]
	if err := verifyUpgradeDirectory(remote, directory); err != nil {
		return metadata, err
	}
	info, err := remote.Lstat(upload.Path)
	if err != nil {
		return metadata, ErrTransfer
	}
	stat, ok := info.Sys().(*sftp.FileStat)
	mode := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	valid := ok && stat.UID == 0 && info.Mode().IsRegular() && mode == upload.Mode && info.Size() == upload.Size
	if !valid {
		return metadata, ErrIntegrity
	}
	return stagedUpgradeFileMetadata{
		uid: stat.UID, gid: stat.GID, mode: info.Mode(), size: info.Size(), modified: info.ModTime().UnixNano(),
	}, nil
}

type upgradeUnitState struct {
	load   string
	active string
	sub    string
}

func (unit upgradeUnitState) running() bool {
	switch unit.active {
	case "activating", "active", "reloading", "deactivating":
		return true
	default:
		return false
	}
}

func upgradeUnitName(id string) string { return upgradeUnitPrefix + id + ".service" }

func (client *Client) upgradeUnit(ctx context.Context, id string) (upgradeUnitState, error) {
	command := "systemctl show --property=LoadState --property=ActiveState --property=SubState -- " + upgradeUnitName(id)
	output, err := client.runUpgradeCommand(ctx, command)
	if err != nil {
		return upgradeUnitState{}, err
	}
	defer clear(output)
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || values[key] != "" {
			return upgradeUnitState{}, ErrIntegrity
		}
		values[key] = value
	}
	if len(values) != 3 {
		return upgradeUnitState{}, ErrIntegrity
	}
	unit := upgradeUnitState{load: values["LoadState"], active: values["ActiveState"], sub: values["SubState"]}
	validLoad := unit.load == "not-found" || unit.load == "loaded"
	validActive := unit.running() || unit.active == "inactive" || unit.active == "failed"
	validSub := unit.sub == "dead" || unit.sub == "failed" || unit.sub == "running" || unit.sub == "start" ||
		unit.sub == "start-pre" || unit.sub == "start-post" || unit.sub == "stop" || unit.sub == "stop-sigterm" ||
		unit.sub == "stop-sigkill" || unit.sub == "stop-post" || unit.sub == "final-sigterm" || unit.sub == "final-sigkill"
	if !validLoad || !validActive || !validSub {
		return upgradeUnitState{}, ErrIntegrity
	}
	return unit, nil
}

func upgradeLaunchCommand(id, command string) string {
	validCommand := command == "upgrade" || command == "retry-upgrade" || command == "recover-upgrade"
	if !domain.IsUUID(id) || !validCommand {
		return ""
	}
	directory := UpgradeDirectory(id)
	return "systemd-run --quiet --no-block --unit=" + upgradeUnitName(id) +
		" --service-type=exec --property=UMask=0077 --property=Restart=no" +
		" --property=StandardInput=file:" + directory + "/request.json" +
		" --property=StandardOutput=null --property=StandardError=null -- " +
		directory + "/llm-test-runner " + command
}

// Only fixed package-owned commands reach this helper. It does not expose a
// shell to the application. Closing this observer channel never stops a unit.
func (client *Client) runUpgradeCommand(ctx context.Context, command string) ([]byte, error) {
	command, err := client.administrativeCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = client.connection.Close() })
	defer stop()
	session, err := client.connection.NewSession()
	if err != nil {
		return nil, connectionError(ctx, err)
	}
	defer session.Close()
	output := &boundedOutput{limit: 4 << 10}
	diagnostic := &boundedOutput{limit: 1 << 10}
	session.Stdout, session.Stderr = output, diagnostic
	err = session.Run(command)
	defer clear(diagnostic.Bytes())
	if err != nil || ctx.Err() != nil {
		clear(output.Bytes())
		return nil, connectionError(ctx, err)
	}
	if output.exceeded || diagnostic.exceeded || diagnostic.buffer.Len() != 0 {
		clear(output.Bytes())
		return nil, ErrIntegrity
	}
	return output.Bytes(), nil
}
