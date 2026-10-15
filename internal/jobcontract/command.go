package jobcontract

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const MaxCommandBytes = 4 << 10

type CommandKind string

const (
	CommandStopSending CommandKind = "stop_sending"
	CommandCancel      CommandKind = "cancel"
)

// Command identities belong to the whole installation, not one HTTP connection.
type Command struct {
	Contract         int         `json:"contract"`
	CommandID        string      `json:"command_id"`
	JobID            string      `json:"job_id"`
	Kind             CommandKind `json:"kind"`
	ExpectedRevision uint64      `json:"expected_revision"`
}

type CommandConfirmation struct {
	Command    Command   `json:"command"`
	AcceptedAt time.Time `json:"accepted_at"`
	Snapshot   Snapshot  `json:"snapshot"`
}

func (command Command) Validate() error {
	if command.Contract != CurrentContract {
		return ErrUnsupported
	}
	validIdentity := domain.IsUUID(command.CommandID) && domain.IsUUID(command.JobID)
	validRevision := command.ExpectedRevision > 0 && command.ExpectedRevision < math.MaxInt64
	if !validIdentity || !validRevision {
		return invalid("command")
	}
	if command.Kind != CommandStopSending && command.Kind != CommandCancel {
		return invalid("command.kind")
	}
	return nil
}

func (confirmation CommandConfirmation) Validate() error {
	if err := confirmation.Command.Validate(); err != nil {
		return err
	}
	if err := confirmation.Snapshot.ValidateState(); err != nil {
		return err
	}
	command, snapshot := confirmation.Command, confirmation.Snapshot
	_, offset := confirmation.AcceptedAt.Zone()
	validTime := !confirmation.AcceptedAt.IsZero() && offset == 0 && confirmation.AcceptedAt.Equal(snapshot.UpdatedAt)
	if snapshot.Receipt.JobID != command.JobID || !validTime {
		return invalid("command.confirmation")
	}
	revision, status := snapshot.Receipt.Revision, snapshot.Receipt.Status
	advanced := revision == command.ExpectedRevision+1
	switch command.Kind {
	case CommandCancel:
		if status != domain.RunCancelled || !advanced {
			return invalid("command.confirmation")
		}
	case CommandStopSending:
		stoppedQueue := status == domain.RunCancelled && advanced && command.ExpectedRevision == 1
		draining := status == domain.RunDraining && revision >= 3 && (advanced || revision == command.ExpectedRevision)
		if !stoppedQueue && !draining {
			return invalid("command.confirmation")
		}
	}
	return nil
}

func (command *Command) UnmarshalJSON(raw []byte) error {
	if len(raw) > MaxCommandBytes {
		return ErrTooLarge
	}
	if err := checkCollectionBounds(raw, scopeCommand); err != nil {
		return err
	}
	type commandDocument Command
	var decoded commandDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return invalid("command")
	}
	*command = Command(decoded)
	return nil
}

func DecodeCommand(reader io.Reader) (Command, error) {
	if reader == nil {
		return Command{}, invalid("body")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxCommandBytes+1))
	if err != nil {
		return Command{}, invalid("body")
	}
	if len(raw) > MaxCommandBytes {
		return Command{}, ErrTooLarge
	}
	var command Command
	if err := json.Unmarshal(raw, &command); err != nil {
		return Command{}, err
	}
	if err := command.Validate(); err != nil {
		return Command{}, err
	}
	return command, nil
}
