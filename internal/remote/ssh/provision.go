package ssh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
	"github.com/pkg/sftp"
)

const bootstrapPrefix = "/var/tmp/llm-test-runner-bootstrap."

type Artifact struct {
	OS     string
	Arch   string
	Size   int64
	SHA256 string
	Source io.Reader
}

type ProvisionError struct {
	OperationID        string
	TemporaryDirectory string
	CleanupPending     bool
	Cause              error
}

func (err *ProvisionError) Error() string { return "remote_runner_provision_failed" }
func (err *ProvisionError) Unwrap() error { return err.Cause }

// InstallationOperation resolves an uncertain SSH acknowledgment through the
// server's atomic journal. It does not restart or resubmit the operation.
func (client *Client) InstallationOperation(ctx context.Context, id string) (runnerinstall.Operation, error) {
	if !domain.IsUUID(id) {
		return runnerinstall.Operation{}, ErrInvalid
	}
	name := runnerinstall.InstallPath + "/operation-" + id + ".json"
	document, err := client.ReadPrivate(ctx, name, 0)
	if err != nil {
		return runnerinstall.Operation{}, err
	}
	defer clear(document)
	operation, err := runnerinstall.DecodeOperation(document)
	if err != nil || operation.Request.OperationID != id {
		return runnerinstall.Operation{}, ErrIntegrity
	}
	return operation, nil
}

// Provision uploads a verified Linux program and invokes its fixed installer.
// Retries must retain request.OperationID and the original artifact. The durable
// receipt describes that operation; ConnectNode separately obtains live health.
func (client *Client) Provision(
	ctx context.Context,
	request runnerinstall.Request,
	artifact Artifact,
	progress func(runnerinstall.Progress),
) (result runnerinstall.Result, runErr error) {
	if client == nil || ctx == nil {
		return result, ErrInvalid
	}
	if err := request.Validate(); err != nil {
		return result, err
	}
	validSize := artifact.Size > 0 && artifact.Size <= maxUploadBytes
	validArtifact := artifact.OS == "linux" && artifact.SHA256 == request.ArtifactSHA256
	validUpload := validSize && validArtifact && artifact.Source != nil
	if !validUpload {
		return result, ErrInvalid
	}
	system, err := client.Probe(ctx)
	if err != nil {
		return result, err
	}
	if system.UID != 0 {
		return result, runnerinstall.ErrRootRequired
	}
	supported := system.SystemdPID1 && system.SystemdVersion >= 249 && system.HasSHA256 && system.HasRunuser
	if !supported || system.Arch != artifact.Arch {
		return result, runnerinstall.ErrUnsupported
	}
	directory := BootstrapDirectory(request.OperationID)
	// The caller persists this exact operation-derived path before provisioning.
	// An existing directory must be recovered explicitly, never reused for upload.
	if err := client.withSFTP(ctx, func(remote *sftp.Client) error {
		if err := verifyParents(remote, directory); err != nil {
			return err
		}
		if _, err := remote.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return ErrExists
			}
			return ErrTransfer
		}
		return nil
	}); err != nil {
		return result, &ProvisionError{
			OperationID: request.OperationID, TemporaryDirectory: directory, Cause: err,
		}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err := client.CleanupBootstrap(cleanup, directory)
		if runErr == nil && err != nil {
			runErr = err
		}
		if runErr != nil {
			runErr = &ProvisionError{
				OperationID: request.OperationID, TemporaryDirectory: directory,
				CleanupPending: err != nil, Cause: runErr,
			}
		}
	}()
	document, err := client.runScript(ctx, "set -eu\numask 077\nmkdir -m 0700 -- "+directory+"\n")
	clear(document)
	if err != nil {
		return result, err
	}
	if err := client.UploadNew(ctx, Upload{
		Path: directory + "/llm-test-runner", Mode: 0700, Size: artifact.Size,
		SHA256: artifact.SHA256, Source: artifact.Source,
	}); err != nil {
		return result, err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return result, ErrInvalid
	}
	output := &provisionOutput{operationID: request.OperationID, progress: progress, pending: []byte{}}
	diagnostic := &boundedOutput{limit: 1024}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = client.connection.Close() })
	defer stop()
	session, err := client.connection.NewSession()
	if err != nil {
		return result, connectionError(ctx, err)
	}
	defer session.Close()
	session.Stdin, session.Stdout, session.Stderr = bytes.NewReader(input), output, diagnostic
	commandErr := session.Run(directory + "/llm-test-runner provision")
	defer clear(diagnostic.Bytes())
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if output.invalid || diagnostic.exceeded {
		return result, ErrIntegrity
	}
	if commandErr != nil {
		if diagnostic.buffer.Len() != 0 {
			return result, runnerinstall.ErrorFromCode(strings.TrimSpace(string(diagnostic.Bytes())))
		}
		return result, connectionError(ctx, commandErr)
	}
	complete := output.result != nil && len(output.pending) == 0 && diagnostic.buffer.Len() == 0
	if !complete {
		return result, ErrIntegrity
	}
	return *output.result, nil
}

// BootstrapDirectory identifies the only staging path owned by an installation
// operation. Persist it before any remote mutation, including directory creation.
func BootstrapDirectory(operationID string) string {
	if !domain.IsUUID(operationID) {
		return ""
	}
	return bootstrapPrefix + operationID
}

func ValidBootstrapDirectory(name string) bool {
	return strings.HasPrefix(name, bootstrapPrefix) && domain.IsUUID(strings.TrimPrefix(name, bootstrapPrefix))
}

// CleanupBootstrap removes only a reported installer staging directory. An SSH
// loss may leave a partial upload; validate its entire bounded namespace before
// removing anything. The canonical installation and operation journal are kept.
func (client *Client) CleanupBootstrap(ctx context.Context, directory string) error {
	if !ValidBootstrapDirectory(directory) {
		return ErrInvalid
	}
	return client.withSFTP(ctx, func(remote *sftp.Client) error {
		if err := verifyParents(remote, directory+"/llm-test-runner"); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		info, err := remote.Lstat(directory)
		if err != nil {
			return ErrTransfer
		}
		stat, ok := info.Sys().(*sftp.FileStat)
		permissions := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
		private := ok && stat.UID == 0 && permissions == 0700
		if !private || !info.IsDir() {
			return ErrInvalid
		}
		entries, err := remote.ReadDir(directory)
		if err != nil || len(entries) > 2 {
			return ErrInvalid
		}
		for _, entry := range entries {
			name := entry.Name()
			validName := name == "llm-test-runner" || validPartialUploadName(name)
			stat, ok := entry.Sys().(*sftp.FileStat)
			owned := ok && stat.UID == 0 && entry.Mode().Perm()&0077 == 0
			bounded := entry.Size() >= 0 && entry.Size() <= maxUploadBytes
			validEntry := validName && owned && bounded && entry.Mode().IsRegular()
			if !validEntry {
				return ErrInvalid
			}
		}
		for _, entry := range entries {
			if err := remote.Remove(directory + "/" + entry.Name()); err != nil {
				return ErrTransfer
			}
		}
		if err := remote.RemoveDirectory(directory); err != nil {
			return ErrTransfer
		}
		return nil
	})
}

func validPartialUploadName(name string) bool {
	if !strings.HasPrefix(name, ".upload-") || len(name) != len(".upload-")+26 {
		return false
	}
	for _, character := range strings.TrimPrefix(name, ".upload-") {
		letter := character >= 'A' && character <= 'Z'
		digit := character >= '2' && character <= '7'
		if !letter && !digit {
			return false
		}
	}
	return true
}

type provisionOutput struct {
	operationID string
	progress    func(runnerinstall.Progress)
	pending     []byte
	total       int
	invalid     bool
	result      *runnerinstall.Result
}

func (output *provisionOutput) Write(document []byte) (int, error) {
	if output.invalid || len(document) > runnerinstall.MaxRecordBytes-output.total {
		output.invalid = true
		return 0, ErrIntegrity
	}
	output.total += len(document)
	output.pending = append(output.pending, document...)
	for {
		line, remaining, present := bytes.Cut(output.pending, []byte("\n"))
		if !present {
			return len(document), nil
		}
		if output.result != nil || output.accept(line) != nil {
			output.invalid = true
			return 0, ErrIntegrity
		}
		output.pending = remaining
	}
}

func (output *provisionOutput) accept(line []byte) error {
	var event map[string]json.RawMessage
	if json.Unmarshal(line, &event) != nil || len(event) != 1 {
		return ErrIntegrity
	}
	if document, ok := event["progress"]; ok {
		var progress runnerinstall.Progress
		if json.Unmarshal(document, &progress) != nil || progress.OperationID != output.operationID {
			return ErrIntegrity
		}
		if err := progress.Validate(); err != nil {
			return ErrIntegrity
		}
		canonical, _ := json.Marshal(map[string]runnerinstall.Progress{"progress": progress})
		if !bytes.Equal(canonical, line) {
			return ErrIntegrity
		}
		if output.progress != nil {
			output.progress(progress)
		}
		return nil
	}
	var result runnerinstall.Result
	if json.Unmarshal(event["result"], &result) != nil || result.OperationID != output.operationID {
		return ErrIntegrity
	}
	if err := result.Validate(); err != nil {
		return ErrIntegrity
	}
	canonical, _ := json.Marshal(map[string]runnerinstall.Result{"result": result})
	if !bytes.Equal(canonical, line) {
		return ErrIntegrity
	}
	output.result = &result
	return nil
}
