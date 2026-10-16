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

type UpgradeRequest struct {
	Contract        int    `json:"contract"`
	OperationID     string `json:"operation_id"`
	NodeID          string `json:"node_id"`
	InstallationID  string `json:"installation_id"`
	ExpectedBuildID string `json:"expected_build_id"`
	ArtifactSHA256  string `json:"artifact_sha256"`
}

type UpgradeResult struct {
	Contract    int                    `json:"contract"`
	OperationID string                 `json:"operation_id"`
	Runner      jobcontract.RunnerInfo `json:"runner"`
	ObservedAt  time.Time              `json:"observed_at"`
}

type UpgradeOperation struct {
	Request         UpgradeRequest `json:"request"`
	Stage           string         `json:"stage"`
	ChangedAt       time.Time      `json:"changed_at"`
	ErrorCode       string         `json:"error_code"`
	ResumeAttempted bool           `json:"resume_attempted"`
	Restorable      bool           `json:"restorable"`
	Result          *UpgradeResult `json:"result"`
}

type UpgradeProgress struct {
	OperationID string `json:"operation_id"`
	Stage       string `json:"stage"`
}

var upgradeStages = []string{
	"checking", "pausing", "stopping", "stopped", "backing_up", "backed_up", "cutover", "verifying",
	"resume_attempted", "completed", "rolling_back", "rolled_back",
}

func (request UpgradeRequest) Validate() error {
	if request.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	identities := domain.IsUUID(request.OperationID) && domain.IsUUID(request.NodeID) &&
		domain.IsUUID(request.InstallationID)
	builds := jobcontract.IsSHA256(request.ExpectedBuildID) && jobcontract.IsSHA256(request.ArtifactSHA256) &&
		request.ExpectedBuildID != request.ArtifactSHA256
	if !identities || !builds {
		return ErrInvalid
	}
	return nil
}

func (progress UpgradeProgress) Validate() error {
	if !domain.IsUUID(progress.OperationID) || !slices.Contains(upgradeStages, progress.Stage) {
		return ErrInvalid
	}
	return nil
}

func DecodeUpgradeRequest(reader io.Reader) (UpgradeRequest, error) {
	if reader == nil {
		return UpgradeRequest{}, ErrInvalid
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxRequestBytes+1))
	if err != nil || len(document) > MaxRequestBytes {
		return UpgradeRequest{}, ErrInvalid
	}
	fields := []string{"contract", "operation_id", "node_id", "installation_id", "expected_build_id", "artifact_sha256"}
	if err := exactUpgradeFields(document, fields); err != nil {
		return UpgradeRequest{}, err
	}
	var request UpgradeRequest
	if json.Unmarshal(document, &request) != nil {
		return UpgradeRequest{}, ErrInvalid
	}
	return request, request.Validate()
}

func exactUpgradeFields(document []byte, fields []string) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		valid := err == nil && ok && slices.Contains(fields, name) && !seen[name]
		if !valid {
			return ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
		seen[name] = true
	}
	if _, err := decoder.Token(); err != nil || len(seen) != len(fields) {
		return ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func DecodeUpgradeOperation(document []byte) (UpgradeOperation, error) {
	if len(document) > MaxRecordBytes {
		return UpgradeOperation{}, ErrInvalid
	}
	var operation UpgradeOperation
	if json.Unmarshal(document, &operation) != nil {
		return UpgradeOperation{}, ErrInvalid
	}
	if err := operation.Validate(); err != nil {
		return UpgradeOperation{}, err
	}
	canonical, err := json.Marshal(operation)
	if err != nil || !bytes.Equal(canonical, document) {
		return UpgradeOperation{}, ErrInvalid
	}
	return operation, nil
}

func (result UpgradeResult) Validate() error {
	if result.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	_, offset := result.ObservedAt.Zone()
	valid := domain.IsUUID(result.OperationID) && !result.ObservedAt.IsZero() && offset == 0 &&
		result.Runner.Healthy && result.Runner.Ready
	if !valid {
		return ErrInvalid
	}
	return result.Runner.Validate()
}

func (operation UpgradeOperation) Validate() error {
	if err := operation.Request.Validate(); err != nil {
		return err
	}
	_, offset := operation.ChangedAt.Zone()
	validTime := !operation.ChangedAt.IsZero() && offset == 0
	validError := operation.ErrorCode == "" || operation.ErrorCode == ErrorCode(ErrorFromCode(operation.ErrorCode))
	validState := slices.Contains(upgradeStages, operation.Stage) && validTime && validError
	if !validState || operation.ResumeAttempted && operation.Restorable {
		return ErrInvalid
	}
	if operation.Stage == "resume_attempted" && !operation.ResumeAttempted {
		return ErrInvalid
	}
	if operation.ResumeAttempted && operation.Stage != "resume_attempted" && operation.Stage != "completed" {
		return ErrInvalid
	}
	canRestoreStage := slices.Contains([]string{"backed_up", "cutover", "verifying", "rolling_back"}, operation.Stage)
	if operation.Restorable && !canRestoreStage {
		return ErrInvalid
	}
	if operation.Result != nil {
		result := operation.Result
		expectedBuild := operation.Request.ArtifactSHA256
		validTerminal := operation.Stage == "completed" && operation.ResumeAttempted
		if operation.Stage == "rolled_back" {
			expectedBuild = operation.Request.ExpectedBuildID
			validTerminal = !operation.ResumeAttempted && !operation.Restorable
		}
		validIdentity := result.Contract == jobcontract.CurrentContract && result.OperationID == operation.Request.OperationID &&
			result.Runner.NodeID == operation.Request.NodeID && result.Runner.NodeInstallationID == operation.Request.InstallationID &&
			result.Runner.EngineBuildID == expectedBuild
		validResult := validIdentity && result.Validate() == nil
		if !validTerminal || operation.ErrorCode != "" || !validResult {
			return ErrInvalid
		}
	} else if operation.Stage == "completed" || operation.Stage == "rolled_back" {
		return ErrInvalid
	}
	return nil
}

func upgradeOperationName(id string) string { return "upgrade-" + id + ".json" }

func upgradeDataName(id string) string { return "upgrade-data-" + id }
