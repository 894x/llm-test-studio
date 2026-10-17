package runnerinstall

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type UninstallRequest struct {
	Contract        int    `json:"contract"`
	OperationID     string `json:"operation_id"`
	NodeID          string `json:"node_id"`
	InstallationID  string `json:"installation_id"`
	ExpectedBuildID string `json:"expected_build_id"`
}

// UninstallManifest freezes the only program files eligible for removal.
// State, pairing, content, backups, accounts, and lock inodes are retained.
type UninstallManifest struct {
	BuildIDs    []string `json:"build_ids"`
	UnitSHA256  string   `json:"unit_sha256"`
	OwnerSHA256 string   `json:"owner_sha256"`
}

type UninstallOperation struct {
	Request        UninstallRequest  `json:"request"`
	Manifest       UninstallManifest `json:"manifest"`
	Stage          string            `json:"stage"`
	PauseConfirmed bool              `json:"pause_confirmed"`
	RestoreService bool              `json:"restore_service"`
	ChangedAt      time.Time         `json:"changed_at"`
	ErrorCode      string            `json:"error_code"`
	Result         *UninstallResult  `json:"result"`
}

type UninstallResult struct {
	Contract     int       `json:"contract"`
	OperationID  string    `json:"operation_id"`
	Uninstalled  bool      `json:"uninstalled"`
	DataRetained bool      `json:"data_retained"`
	ObservedAt   time.Time `json:"observed_at"`
}

type UninstallProgress struct {
	OperationID string `json:"operation_id"`
	Stage       string `json:"stage"`
}

var uninstallStages = []string{
	"checking", "pausing", "paused", "stopping", "stopped", "disabling", "removing", "completed", "aborting", "aborted",
}

func (request UninstallRequest) Validate() error {
	if request.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	validIdentity := domain.IsUUID(request.OperationID) && domain.IsUUID(request.NodeID) &&
		domain.IsUUID(request.InstallationID)
	if !validIdentity || !jobcontract.IsSHA256(request.ExpectedBuildID) {
		return ErrInvalid
	}
	return nil
}

func DecodeUninstallRequest(reader io.Reader) (UninstallRequest, error) {
	if reader == nil {
		return UninstallRequest{}, ErrInvalid
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxRequestBytes+1))
	if err != nil || len(document) > MaxRequestBytes {
		return UninstallRequest{}, ErrInvalid
	}
	fields := []string{"contract", "operation_id", "node_id", "installation_id", "expected_build_id"}
	if err := exactOperationFields(document, fields); err != nil {
		return UninstallRequest{}, err
	}
	var request UninstallRequest
	if json.Unmarshal(document, &request) != nil {
		return UninstallRequest{}, ErrInvalid
	}
	return request, request.Validate()
}

func (progress UninstallProgress) Validate() error {
	if !domain.IsUUID(progress.OperationID) || !slices.Contains(uninstallStages, progress.Stage) {
		return ErrInvalid
	}
	return nil
}

func (operation UninstallOperation) Validate() error {
	if err := operation.Request.Validate(); err != nil {
		return err
	}
	manifest := operation.Manifest
	validManifest := len(manifest.BuildIDs) > 0 && len(manifest.BuildIDs) <= 128 &&
		jobcontract.IsSHA256(manifest.UnitSHA256) && jobcontract.IsSHA256(manifest.OwnerSHA256)
	if !validManifest || !slices.Contains(manifest.BuildIDs, operation.Request.ExpectedBuildID) {
		return ErrInvalid
	}
	for index, build := range manifest.BuildIDs {
		if !jobcontract.IsSHA256(build) || index > 0 && manifest.BuildIDs[index-1] >= build {
			return ErrInvalid
		}
	}
	_, offset := operation.ChangedAt.Zone()
	validTime := !operation.ChangedAt.IsZero() && offset == 0
	validError := operation.ErrorCode == "" || operation.ErrorCode == ErrorCode(ErrorFromCode(operation.ErrorCode))
	if !slices.Contains(uninstallStages, operation.Stage) || !validTime || !validError {
		return ErrInvalid
	}
	pausedStages := []string{"paused", "stopping", "stopped", "disabling", "removing", "completed"}
	needsPause := slices.Contains(pausedStages, operation.Stage)
	if needsPause && !operation.PauseConfirmed {
		return ErrInvalid
	}
	if (operation.Stage == "checking" || operation.Stage == "pausing") && operation.PauseConfirmed {
		return ErrInvalid
	}
	aborting := operation.Stage == "aborting" || operation.Stage == "aborted"
	if operation.RestoreService && !aborting || aborting && operation.PauseConfirmed && !operation.RestoreService {
		return ErrInvalid
	}
	terminal := operation.Stage == "completed" || operation.Stage == "aborted"
	if terminal != (operation.Result != nil) {
		return ErrInvalid
	}
	if operation.Result != nil {
		result := operation.Result
		_, offset := result.ObservedAt.Zone()
		validIdentity := result.Contract == jobcontract.CurrentContract && result.OperationID == operation.Request.OperationID
		validResult := validIdentity && result.Uninstalled == (operation.Stage == "completed") && result.DataRetained &&
			!result.ObservedAt.IsZero() && offset == 0 && operation.ErrorCode == ""
		if !validResult {
			return ErrInvalid
		}
	}
	return nil
}

func DecodeUninstallOperation(document []byte) (UninstallOperation, error) {
	var operation UninstallOperation
	if len(document) > MaxRecordBytes || json.Unmarshal(document, &operation) != nil {
		return UninstallOperation{}, ErrInvalid
	}
	if err := operation.Validate(); err != nil {
		return UninstallOperation{}, err
	}
	canonical, err := json.Marshal(operation)
	if err != nil || !bytes.Equal(canonical, document) {
		return UninstallOperation{}, ErrInvalid
	}
	return operation, nil
}

func uninstallOperationName(id string) string { return "uninstall-" + id + ".json" }
