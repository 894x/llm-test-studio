// Package runnerinstall owns shared-node installation operations, independently
// of the runner's process and task locks.
package runnerinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrInvalid         = errors.New("invalid_installation_request")
	ErrRootRequired    = errors.New("installation_root_required")
	ErrUnsupported     = errors.New("unsupported_installation_platform")
	ErrBusy            = errors.New("installation_busy")
	ErrConflict        = errors.New("installation_operation_conflict")
	ErrUnownedPath     = errors.New("installation_path_not_owned")
	ErrPairingRequired = errors.New("node_pairing_repair_required")
	ErrUpgradeRequired = errors.New("runner_upgrade_required")
	ErrService         = errors.New("runner_service_unavailable")
	ErrIntegrity       = errors.New("installation_integrity_failed")
)

const (
	MaxRequestBytes = 4 << 10
	MaxRecordBytes  = 64 << 10
	PairingName     = "node-token"
	ServiceName     = "llm-test-runner.service"
	ProgramPath     = "/opt/llm-test-runner"
	StatePath       = "/var/lib/llm-test-runner"
	InstallPath     = "/var/lib/llm-test-runner-install"
	IdentityPath    = InstallPath + "/identity.json"
	PairingPath     = InstallPath + "/" + PairingName
)

type Request struct {
	Contract       int    `json:"contract"`
	OperationID    string `json:"operation_id"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ListenPort     uint16 `json:"listen_port"`
}

type Result struct {
	Contract    int                    `json:"contract"`
	OperationID string                 `json:"operation_id"`
	Runner      jobcontract.RunnerInfo `json:"runner"`
	ListenPort  uint16                 `json:"listen_port"`
	Reused      bool                   `json:"reused"`
	ObservedAt  time.Time              `json:"observed_at"`
}

type Operation struct {
	Request   Request   `json:"request"`
	Stage     string    `json:"stage"`
	ChangedAt time.Time `json:"changed_at"`
	ErrorCode string    `json:"error_code"`
	Result    *Result   `json:"result"`
}

type Progress struct {
	OperationID string `json:"operation_id"`
	Stage       string `json:"stage"`
}

func (progress Progress) Validate() error {
	stages := []string{"checking", "account", "program", "configuration", "service", "verifying", "completed"}
	if !domain.IsUUID(progress.OperationID) || !slices.Contains(stages, progress.Stage) {
		return ErrInvalid
	}
	return nil
}

type Identity struct {
	Contract       int    `json:"contract"`
	NodeID         string `json:"node_id"`
	InstallationID string `json:"installation_id"`
	ListenPort     uint16 `json:"listen_port"`
	TokenSHA256    string `json:"token_sha256"`
}

func (identity Identity) Validate() error {
	if identity.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	valid := domain.IsUUID(identity.NodeID) && domain.IsUUID(identity.InstallationID) &&
		identity.ListenPort >= 1024 && jobcontract.IsSHA256(identity.TokenSHA256)
	if !valid {
		return ErrInvalid
	}
	return nil
}

func DecodeIdentity(document []byte) (Identity, error) {
	var identity Identity
	if len(document) > MaxRequestBytes || json.Unmarshal(document, &identity) != nil {
		return Identity{}, ErrInvalid
	}
	if err := identity.Validate(); err != nil {
		return Identity{}, err
	}
	canonical, err := json.Marshal(identity)
	if err != nil || !bytes.Equal(document, canonical) {
		return Identity{}, ErrInvalid
	}
	return identity, nil
}

func (result Result) Validate() error {
	if result.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	validIdentity := domain.IsUUID(result.OperationID) && result.ListenPort >= 1024
	_, offset := result.ObservedAt.Zone()
	validTime := !result.ObservedAt.IsZero() && offset == 0
	valid := validIdentity && validTime && result.Runner.Healthy
	if !valid {
		return ErrInvalid
	}
	return result.Runner.Validate()
}

func (request Request) Validate() error {
	if request.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	validIdentity := domain.IsUUID(request.OperationID) && jobcontract.IsSHA256(request.ArtifactSHA256)
	if !validIdentity || request.ListenPort < 1024 {
		return ErrInvalid
	}
	return nil
}

func DecodeRequest(reader io.Reader) (Request, error) {
	if reader == nil {
		return Request{}, ErrInvalid
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxRequestBytes+1))
	if err != nil || len(document) > MaxRequestBytes {
		return Request{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return Request{}, ErrInvalid
	}
	allowed := []string{"contract", "operation_id", "artifact_sha256", "listen_port"}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		valid := err == nil && ok && slices.Contains(allowed, name) && !seen[name]
		if !valid {
			return Request{}, ErrInvalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Request{}, ErrInvalid
		}
		seen[name] = true
	}
	if _, err := decoder.Token(); err != nil || len(seen) != len(allowed) {
		return Request{}, ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Request{}, ErrInvalid
	}
	var request Request
	if err := json.Unmarshal(document, &request); err != nil {
		return Request{}, ErrInvalid
	}
	return request, request.Validate()
}

func DecodeOperation(document []byte) (Operation, error) {
	if len(document) > MaxRecordBytes {
		return Operation{}, ErrInvalid
	}
	var operation Operation
	if err := json.Unmarshal(document, &operation); err != nil {
		return Operation{}, ErrInvalid
	}
	if err := operation.Request.Validate(); err != nil {
		return Operation{}, err
	}
	validStage := slices.Contains([]string{
		"checking", "account", "program", "configuration", "service", "verifying", "completed",
	}, operation.Stage)
	_, offset := operation.ChangedAt.Zone()
	validTime := !operation.ChangedAt.IsZero() && offset == 0
	if !validStage || !validTime {
		return Operation{}, ErrInvalid
	}
	validError := operation.ErrorCode == "" || operation.ErrorCode == ErrorCode(ErrorFromCode(operation.ErrorCode))
	if !validError {
		return Operation{}, ErrInvalid
	}
	if operation.Result != nil {
		result := operation.Result
		valid := operation.Stage == "completed" && operation.ErrorCode == "" &&
			result.OperationID == operation.Request.OperationID && result.Validate() == nil
		if !valid {
			return Operation{}, ErrInvalid
		}
	} else if operation.Stage == "completed" {
		return Operation{}, ErrInvalid
	}
	// These are application-owned canonical records, not authored JSON. Exact
	// re-encoding rejects missing, duplicate, mis-cased and unknown stored fields.
	canonical, err := json.Marshal(operation)
	if err != nil || !bytes.Equal(canonical, document) {
		return Operation{}, ErrInvalid
	}
	return operation, nil
}

// ErrorFromCode accepts only the installer's fixed public diagnostics. SSH
// stderr is never returned verbatim to a desktop caller or written into a report.
func ErrorFromCode(code string) error {
	switch code {
	case "installation_root_required":
		return ErrRootRequired
	case "unsupported_installation_platform":
		return ErrUnsupported
	case "installation_busy":
		return ErrBusy
	case "installation_operation_conflict":
		return ErrConflict
	case "installation_path_not_owned":
		return ErrUnownedPath
	case "node_pairing_repair_required":
		return ErrPairingRequired
	case "runner_upgrade_required":
		return ErrUpgradeRequired
	case "installation_integrity_failed":
		return ErrIntegrity
	case "invalid_installation_request":
		return ErrInvalid
	default:
		return ErrService
	}
}

func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrRootRequired):
		return "installation_root_required"
	case errors.Is(err, ErrUnsupported):
		return "unsupported_installation_platform"
	case errors.Is(err, ErrBusy):
		return "installation_busy"
	case errors.Is(err, ErrConflict):
		return "installation_operation_conflict"
	case errors.Is(err, ErrUnownedPath):
		return "installation_path_not_owned"
	case errors.Is(err, ErrPairingRequired):
		return "node_pairing_repair_required"
	case errors.Is(err, ErrUpgradeRequired), errors.Is(err, jobcontract.ErrUnsupported):
		return "runner_upgrade_required"
	case errors.Is(err, ErrIntegrity):
		return "installation_integrity_failed"
	case errors.Is(err, ErrInvalid):
		return "invalid_installation_request"
	default:
		return "runner_installation_failed"
	}
}
