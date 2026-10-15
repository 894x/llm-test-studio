package runs

import (
	"context"
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/domain"
)

type QuickTaskCatalog interface {
	GetSuite(context.Context, string) (domain.Suite, error)
	GetChannel(context.Context, string) (domain.Channel, error)
	ListChannelModels(context.Context) ([]domain.ChannelModel, error)
}

// QuickTaskCommand selects the current task definition and either a saved channel
// or a temporary connection. Model is the upstream identifier, not a catalog ID.
type QuickTaskCommand struct {
	SuiteID          string                     `json:"suite_id"`
	Seed             uint64                     `json:"seed"`
	RequestTimeoutMS uint64                     `json:"request_timeout_ms"`
	CaseConcurrency  uint32                     `json:"case_concurrency,omitempty"`
	Model            string                     `json:"model"`
	ChannelID        string                     `json:"channel_id,omitempty"`
	BaseURL          string                     `json:"base_url,omitempty"`
	APIKey           string                     `json:"api_key,omitempty"`
	Inputs           map[string]json.RawMessage `json:"inputs"`
	SourceRunID      string                     `json:"source_run_id,omitempty"`
	CredentialRunID  string                     `json:"credential_run_id,omitempty"`
}

func (service *Service) StartQuickTask(ctx context.Context, command QuickTaskCommand) (string, error) {
	id, err := service.PrepareQuickTask(ctx, command)
	return service.activatePrepared(ctx, id, err)
}

func (service *Service) PrepareQuickTask(ctx context.Context, command QuickTaskCommand) (string, error) {
	if service == nil {
		return "", ErrInvalid
	}
	prepared, lease, err := service.Preparer.BuildQuickTask(ctx, command)
	if err != nil {
		return "", err
	}
	return service.AcceptPrepared(ctx, prepared, lease)
}
