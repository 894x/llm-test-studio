package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
)

// QuickTaskDetail exposes only fields needed to restore a task form. Credentials,
// request definitions, response bodies, and environment details stay in Core.
type QuickTaskDetail struct {
	SchemaVersion   int                        `json:"schema_version"`
	RunID           string                     `json:"run_id"`
	Suite           QuickTaskSuite             `json:"suite"`
	Model           string                     `json:"model"`
	BaseURL         string                     `json:"base_url"`
	ChannelID       string                     `json:"channel_id,omitempty"`
	CredentialRunID string                     `json:"credential_run_id,omitempty"`
	Inputs          map[string]json.RawMessage `json:"inputs"`
}

// QuickTaskPerformancePath resolves the unique chat endpoint from the selected
// definitions; performance inputs remain owned by the performance service.
func (service *Service) QuickTaskPerformancePath(ctx context.Context, command QuickTaskCommand) (string, error) {
	if service == nil || ctx == nil || !domain.IsUUID(command.SuiteID) || command.SuiteRevision == 0 || isNil(service.quickTasks) {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	suite, cases, err := service.quickTaskDefinitions(ctx, command)
	if err != nil {
		return "", err
	}
	if suite.ID != command.SuiteID || suite.Revision != command.SuiteRevision || suite.Validate() != nil || suite.ValidateCases(cases) != nil || suite.QuickTest == nil || suite.Protocol != domain.ProtocolOpenAIChat || (suite.ModelTarget != "" && suite.ModelTarget != command.Model) {
		return "", ErrNotRunnable
	}
	path := ""
	for _, testCase := range cases {
		var spec struct {
			Request struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"request"`
		}
		if !testCase.AppliesToModel(command.Model) || json.Unmarshal(testCase.Definition.Spec, &spec) != nil || spec.Request.Method != "POST" || !strings.HasSuffix(spec.Request.Path, "/chat/completions") {
			continue
		}
		if path != "" && path != spec.Request.Path {
			return "", ErrNotRunnable
		}
		path = spec.Request.Path
	}
	if path == "" {
		return "", ErrNotRunnable
	}
	return path, nil
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
	return QuickTaskDetail{SchemaVersion: 1, RunID: runID, Suite: QuickTaskSuite{Suite: task.Suite, CaseCount: len(task.Suite.Cases)}, Model: snapshot.Channel.UpstreamModelName,
		BaseURL: snapshot.Channel.BaseURL, ChannelID: task.SavedChannelID, CredentialRunID: service.rememberedQuickTaskCredential(ctx, runID, snapshot), Inputs: task.Inputs}, nil
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
