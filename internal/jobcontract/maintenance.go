package jobcontract

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

type MaintenanceAction string

const (
	MaintenancePause  MaintenanceAction = "pause"
	MaintenanceResume MaintenanceAction = "resume"
)

type MaintenanceCommand struct {
	Contract    int               `json:"contract"`
	OperationID string            `json:"operation_id"`
	Action      MaintenanceAction `json:"action"`
}

type MaintenanceRecord struct {
	Contract           int       `json:"contract"`
	NodeInstallationID string    `json:"node_installation_id"`
	OperationID        string    `json:"operation_id"`
	Paused             bool      `json:"paused"`
	ChangedAt          time.Time `json:"changed_at"`
}

func (command MaintenanceCommand) Validate() error {
	if command.Contract != CurrentContract {
		return ErrUnsupported
	}
	validAction := command.Action == MaintenancePause || command.Action == MaintenanceResume
	if !domain.IsUUID(command.OperationID) || !validAction {
		return invalid("maintenance_command")
	}
	return nil
}

func (record MaintenanceRecord) Validate(installationID string) error {
	if record.Contract != CurrentContract {
		return ErrUnsupported
	}
	identity := domain.IsUUID(record.NodeInstallationID) && record.NodeInstallationID == installationID &&
		domain.IsUUID(record.OperationID)
	_, offset := record.ChangedAt.Zone()
	validTime := !record.ChangedAt.IsZero() && offset == 0
	if !identity || !validTime {
		return invalid("maintenance_record")
	}
	return nil
}

func (command *MaintenanceCommand) UnmarshalJSON(document []byte) error {
	if len(document) > MaxCommandBytes {
		return ErrTooLarge
	}
	if err := checkCollectionBounds(document, scopeMaintenanceCommand); err != nil {
		return err
	}
	type commandDocument MaintenanceCommand
	var decoded commandDocument
	if err := json.Unmarshal(document, &decoded); err != nil {
		return invalid("maintenance_command")
	}
	*command = MaintenanceCommand(decoded)
	return nil
}

func (record *MaintenanceRecord) UnmarshalJSON(document []byte) error {
	if len(document) > MaxCommandBytes {
		return ErrTooLarge
	}
	if err := checkCollectionBounds(document, scopeMaintenanceRecord); err != nil {
		return err
	}
	type recordDocument MaintenanceRecord
	var decoded recordDocument
	if err := json.Unmarshal(document, &decoded); err != nil {
		return invalid("maintenance_record")
	}
	// paused=false is valid, but absence/null is not another stored shape.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return invalid("maintenance_record")
	}
	if bytes.Equal(bytes.TrimSpace(fields["paused"]), []byte("null")) {
		return invalid("maintenance_record")
	}
	if _, present := fields["paused"]; !present {
		return invalid("maintenance_record")
	}
	*record = MaintenanceRecord(decoded)
	return nil
}

func DecodeMaintenanceCommand(reader io.Reader) (MaintenanceCommand, error) {
	if reader == nil {
		return MaintenanceCommand{}, invalid("body")
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxCommandBytes+1))
	if err != nil {
		return MaintenanceCommand{}, invalid("body")
	}
	if len(document) > MaxCommandBytes {
		return MaintenanceCommand{}, ErrTooLarge
	}
	var command MaintenanceCommand
	if err := json.Unmarshal(document, &command); err != nil {
		return MaintenanceCommand{}, err
	}
	return command, command.Validate()
}
