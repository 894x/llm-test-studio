package jobcontract

import (
	"bytes"
	"encoding/json"
	"io"
)

const MaxStateBytes = 64 << 10

func stateDocument(reader io.Reader, limit int64) ([]byte, error) {
	if reader == nil {
		return nil, invalid("body")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrTooLarge
	}
	return raw, nil
}

func DecodeReceipt(reader io.Reader) (Receipt, error) {
	raw, err := stateDocument(reader, MaxStateBytes)
	if err != nil {
		return Receipt{}, err
	}
	if _, err := objectFields(raw, []string{
		"contract", "job_id", "kind", "accepted_at", "node_installation_id", "engine_build_id",
		"status", "revision", "run_ids", "report_ids",
	}, []string{"comparison_id"}); err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if json.Unmarshal(raw, &receipt) != nil {
		return Receipt{}, invalid("receipt")
	}
	if receipt.Contract != CurrentContract {
		return Receipt{}, ErrUnsupported
	}
	return receipt, receipt.Validate()
}

func DecodeSnapshot(reader io.Reader) (Snapshot, error) {
	raw, err := stateDocument(reader, MaxStateBytes)
	if err != nil {
		return Snapshot{}, err
	}
	fields, err := objectFields(raw, []string{"receipt", "latest_sequence", "updated_at"}, []string{"failure"})
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := DecodeReceipt(bytes.NewReader(fields["receipt"])); err != nil {
		return Snapshot{}, err
	}
	if failure := fields["failure"]; failure != nil {
		if _, err := objectFields(failure, []string{"phase", "error_code"}, []string{}); err != nil {
			return Snapshot{}, err
		}
	}
	var snapshot Snapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return Snapshot{}, invalid("snapshot")
	}
	return snapshot, snapshot.ValidateState()
}

func DecodeEvent(reader io.Reader) (Event, error) {
	raw, err := stateDocument(reader, MaxEventBytes)
	if err != nil {
		return Event{}, err
	}
	if _, err := objectFields(raw, []string{
		"contract", "job_id", "sequence", "kind", "created_at", "payload",
	}, []string{}); err != nil {
		return Event{}, err
	}
	var event Event
	if json.Unmarshal(raw, &event) != nil {
		return Event{}, invalid("event")
	}
	if event.Contract != CurrentContract {
		return Event{}, ErrUnsupported
	}
	return event, event.Validate()
}

func DecodeCommandConfirmation(reader io.Reader) (CommandConfirmation, error) {
	raw, err := stateDocument(reader, MaxStateBytes)
	if err != nil {
		return CommandConfirmation{}, err
	}
	fields, err := objectFields(raw, []string{"command", "accepted_at", "snapshot"}, []string{})
	if err != nil {
		return CommandConfirmation{}, err
	}
	command, err := DecodeCommand(bytes.NewReader(fields["command"]))
	if err != nil {
		return CommandConfirmation{}, err
	}
	snapshot, err := DecodeSnapshot(bytes.NewReader(fields["snapshot"]))
	if err != nil {
		return CommandConfirmation{}, err
	}
	var confirmation CommandConfirmation
	if json.Unmarshal(raw, &confirmation) != nil {
		return CommandConfirmation{}, invalid("command.confirmation")
	}
	confirmation.Command, confirmation.Snapshot = command, snapshot
	return confirmation, confirmation.Validate()
}
