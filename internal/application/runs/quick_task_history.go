package runs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/domain"
)

// QuickTaskDetail exposes only fields needed to restore a task form. Credentials,
// request definitions, response bodies, and environment details stay in Core.
type QuickTaskDetail struct {
	Seed             uint64                     `json:"seed"`
	RequestTimeoutMS uint64                     `json:"request_timeout_ms"`
	SchemaVersion    int                        `json:"schema_version"`
	RunID            string                     `json:"run_id"`
	Suite            QuickTaskSuite             `json:"suite"`
	Model            string                     `json:"model"`
	BaseURL          string                     `json:"base_url"`
	ChannelID        string                     `json:"channel_id,omitempty"`
	CredentialRunID  string                     `json:"credential_run_id,omitempty"`
	Inputs           map[string]json.RawMessage `json:"inputs"`
}

// QuickTaskPerformancePath resolves the unique chat endpoint from the selected
// definitions; performance inputs remain owned by the performance service.
func (service *Service) QuickTaskPerformancePath(ctx context.Context, command QuickTaskCommand) (string, error) {
	if service == nil || ctx == nil || !domain.IsUUID(command.SuiteID) || isNil(service.quickTasks) {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	suite, cases, err := service.quickTaskDefinitions(ctx, command)
	if err != nil {
		return "", err
	}
	if suite.ID != command.SuiteID || suite.Protocol != domain.ProtocolOpenAIChat || suite.ValidateCases(cases) != nil {
		return "", ErrNotRunnable
	}
	return "/v1/chat/completions", nil
}

type QuickTaskSuite struct {
	domain.Suite
	CaseCount int `json:"case_count"`
}

func (service *Service) QuickTask(ctx context.Context, runID string) (QuickTaskDetail, error) {
	snapshot, err := service.quickTaskSnapshot(ctx, runID)
	if err != nil {
		return QuickTaskDetail{}, err
	}
	task := snapshot.QuickTask
	return QuickTaskDetail{SchemaVersion: 1, RunID: runID, Suite: QuickTaskSuite{Suite: *snapshot.Entries[0].Suite, CaseCount: len(snapshot.Entries[0].Cases)}, Model: snapshot.Channel.UpstreamModelName,
		BaseURL: snapshot.Channel.BaseURL, ChannelID: task.SavedChannelID, CredentialRunID: service.rememberedQuickTaskCredential(ctx, runID, snapshot), Inputs: snapshot.Entries[0].Parameters, Seed: snapshot.PlanDocument.Seed, RequestTimeoutMS: snapshot.Entries[0].Load.RequestTimeoutMS}, nil
}

func (service *Service) quickTaskSnapshot(ctx context.Context, runID string) (domain.RunSnapshot, error) {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return domain.RunSnapshot{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return domain.RunSnapshot{}, err
	}
	run, err := service.repository.GetRun(ctx, runID)
	if err != nil {
		return domain.RunSnapshot{}, fmt.Errorf("load quick task history: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.RunSnapshot{}, err
	}
	snapshot := run.Snapshot()
	if run.Meta().ID != runID || snapshot.QuickTask == nil || snapshot.Validate() != nil {
		return domain.RunSnapshot{}, ErrNotRunnable
	}
	return snapshot, nil
}
