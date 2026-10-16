package jobs

import (
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// ControlView describes delivery of one fixed instruction. Its historical
// confirmation never replaces the job's independently synchronized execution.
type ControlView struct {
	SchemaVersion    int                     `json:"schema_version"`
	CommandID        string                  `json:"command_id"`
	JobID            string                  `json:"job_id"`
	Kind             jobcontract.CommandKind `json:"kind"`
	ExpectedRevision uint64                  `json:"expected_revision"`
	DeliveryRevision uint64                  `json:"delivery_revision"`
	State            ControlState            `json:"state"`
	Observation      Observation             `json:"observation"`
	CreatedAt        string                  `json:"created_at"`
	UpdatedAt        string                  `json:"updated_at"`
	ConfirmedAt      *string                 `json:"confirmed_at"`
	Execution        *ExecutionView          `json:"execution"`
}

type ControlPageView struct {
	SchemaVersion int           `json:"schema_version"`
	JobID         string        `json:"job_id"`
	Commands      []ControlView `json:"commands"`
	NextCursor    string        `json:"next_cursor"`
	HasMore       bool          `json:"has_more"`
}

// ProjectControl receives a record whose fixed submission owner has already
// been verified by Controller or the repository's read transaction.
func ProjectControl(record ControlRecord) (ControlView, error) {
	if record.Validate() != nil {
		return ControlView{}, ErrInvalid
	}
	command := record.Command
	view := ControlView{SchemaVersion: 1, CommandID: command.CommandID, JobID: command.JobID, Kind: command.Kind,
		ExpectedRevision: command.ExpectedRevision, DeliveryRevision: record.Revision,
		State: record.State, Observation: record.Observation,
		CreatedAt: record.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano)}
	if confirmation := record.Confirmation; confirmation != nil {
		at := confirmation.AcceptedAt.Format(time.RFC3339Nano)
		view.ConfirmedAt = &at
		view.Execution = executionView(confirmation.Snapshot)
	}
	return view, nil
}
