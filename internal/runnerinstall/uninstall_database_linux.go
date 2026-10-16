package runnerinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func confirmUninstallIdle(ctx context.Context, paths layout, owner account, request UninstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := openOwned(filepath.Join(paths.state, "maintenance.json"), owner.uid, true)
	if err != nil {
		return ErrIntegrity
	}
	document, readErr := io.ReadAll(io.LimitReader(file, jobcontract.MaxCommandBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(document) > jobcontract.MaxCommandBytes {
		return ErrIntegrity
	}
	var record jobcontract.MaintenanceRecord
	if json.Unmarshal(document, &record) != nil || record.Validate(request.InstallationID) != nil ||
		!record.Paused || record.OperationID != request.OperationID {
		return ErrConflict
	}
	canonical, _ := json.Marshal(record)
	if !bytes.Equal(canonical, document) {
		return ErrIntegrity
	}
	path := filepath.Join(paths.state, "runner.db")
	// Verification is read-only and sees committed WAL facts. Do not migrate,
	// checkpoint, delete sidecars, or infer settlement from the runtime lock.
	verify, cancelVerify := context.WithTimeout(ctx, 30*time.Second)
	_, err = sqlite.VerifyRunnerDatabase(verify, path)
	cancelVerify()
	if err != nil {
		return err
	}
	location := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)"}}
	location.RawQuery = query.Encode()
	read, cancelRead := context.WithTimeout(ctx, 30*time.Second)
	defer cancelRead()
	repository, err := sqlite.OpenRepository(read, location.String(), sqlite.RepositoryOptions{})
	if err != nil {
		return err
	}
	jobs, readErr := repository.RecoveryJobs(read)
	if err := errors.Join(readErr, repository.Close()); err != nil {
		return err
	}
	if len(jobs) != 0 {
		return ErrBusy
	}
	return read.Err()
}
