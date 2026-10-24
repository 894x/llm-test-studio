package jobcontract

import (
	"encoding/json"
	"io"

	"github.com/894x/llm-test-studio/internal/domain"
)

const FeatureTaskConcurrency = "task_concurrency"

// TaskConcurrencyCommand changes the shared node's task slots using its last
// observed value. It does not change any task's request concurrency.
type TaskConcurrencyCommand struct {
	Contract              int    `json:"contract"`
	NodeID                string `json:"node_id"`
	NodeInstallationID    string `json:"node_installation_id"`
	ExpectedMaxActiveJobs uint32 `json:"expected_max_active_jobs"`
	MaxActiveJobs         uint32 `json:"max_active_jobs"`
}

func (command TaskConcurrencyCommand) Validate() error {
	if command.Contract != CurrentContract {
		return ErrUnsupported
	}
	validIdentity := domain.IsUUID(command.NodeID) && domain.IsUUID(command.NodeInstallationID)
	validExpected := command.ExpectedMaxActiveJobs >= 1 && command.ExpectedMaxActiveJobs <= 128
	validLimit := command.MaxActiveJobs >= 1 && command.MaxActiveJobs <= 128
	if !validIdentity || !validExpected || !validLimit {
		return invalid("task_concurrency")
	}
	return nil
}

func DecodeTaskConcurrencyCommand(reader io.Reader) (TaskConcurrencyCommand, error) {
	if reader == nil {
		return TaskConcurrencyCommand{}, invalid("body")
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxCommandBytes+1))
	if err != nil {
		return TaskConcurrencyCommand{}, invalid("body")
	}
	if len(document) > MaxCommandBytes {
		return TaskConcurrencyCommand{}, ErrTooLarge
	}
	if err := checkCollectionBounds(document, scopeTaskConcurrencyCommand); err != nil {
		return TaskConcurrencyCommand{}, err
	}
	var command TaskConcurrencyCommand
	if err := json.Unmarshal(document, &command); err != nil {
		return TaskConcurrencyCommand{}, invalid("task_concurrency")
	}
	return command, command.Validate()
}
