package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type QuickTaskCatalog interface {
	GetSuiteRevision(context.Context, string, uint64) (domain.Suite, error)
	GetChannel(context.Context, string) (domain.Channel, error)
}

// QuickTaskCommand selects an immutable task and either a saved channel or a
// temporary connection. Model is the upstream identifier, not a catalog ID.
type QuickTaskCommand struct {
	SuiteID         string                     `json:"suite_id"`
	SuiteRevision   uint64                     `json:"suite_revision"`
	Model           string                     `json:"model"`
	ChannelID       string                     `json:"channel_id,omitempty"`
	BaseURL         string                     `json:"base_url,omitempty"`
	APIKey          string                     `json:"api_key,omitempty"`
	Inputs          map[string]json.RawMessage `json:"inputs"`
	SourceRunID     string                     `json:"source_run_id,omitempty"`
	CredentialRunID string                     `json:"credential_run_id,omitempty"`
}

func (service *Service) StartQuickTask(ctx context.Context, command QuickTaskCommand) (string, error) {
	id, err := service.PrepareQuickTask(ctx, command)
	return service.activatePrepared(ctx, id, err)
}

func (service *Service) PrepareQuickTask(ctx context.Context, command QuickTaskCommand) (string, error) {
	if service == nil || ctx == nil || isNil(service.quickTasks) || !domain.IsUUID(command.SuiteID) || command.SuiteRevision == 0 ||
		command.Model == "" || command.Model != strings.TrimSpace(command.Model) || len(command.Model) > 256 || strings.ContainsFunc(command.Model, unicode.IsControl) {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	service.mu.Lock()
	closed := service.closed
	service.mu.Unlock()
	if closed {
		return "", ErrClosed
	}
	if command.ChannelID != "" && (!domain.IsUUID(command.ChannelID) || command.BaseURL != "" || command.APIKey != "" || command.CredentialRunID != "") {
		return "", ErrInvalid
	}
	if command.CredentialRunID != "" && (!domain.IsUUID(command.CredentialRunID) || command.APIKey != "") {
		return "", ErrInvalid
	}
	suite, cases, err := service.quickTaskDefinitions(ctx, command)
	if err != nil {
		return "", fmt.Errorf("load quick task: %w", err)
	}
	if suite.ID != command.SuiteID || suite.Revision != command.SuiteRevision || suite.QuickTest == nil || suite.Validate() != nil {
		return "", ErrNotRunnable
	}
	if suite.ModelTarget != "" && suite.ModelTarget != command.Model {
		return "", ErrNotRunnable
	}
	for _, testCase := range cases {
		if !testCase.AppliesToModel(command.Model) {
			return "", ErrNotRunnable
		}
	}
	effective, inputs, err := suite.ApplyInputs(cases, command.Inputs)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, testCase := range effective {
		if testCase.Validate() != nil || service.caseTypes.Validate(testCase.Protocol, testCase.Definition) != nil {
			return "", ErrNotRunnable
		}
	}
	now := service.clock.Now()
	meta, err := service.metaFactory(now)
	if err != nil {
		return "", err
	}
	modelMeta, err := domain.NewEntityMeta(now)
	if err != nil {
		return "", err
	}
	channelMeta, err := domain.NewEntityMeta(now)
	if err != nil {
		return "", err
	}
	channel := domain.Channel{EntityMeta: channelMeta, BaseURL: command.BaseURL, Protocol: suite.Protocol, Enabled: true}
	if command.ChannelID != "" {
		channel, err = service.quickTasks.GetChannel(ctx, command.ChannelID)
		if err != nil {
			return "", fmt.Errorf("load quick task channel: %w", err)
		}
		if channel.ID != command.ChannelID || !channel.Enabled || channel.Protocol != suite.Protocol || channel.CredentialID == "" || channel.Validate() != nil {
			return "", ErrNotRunnable
		}
	} else {
		if len(command.BaseURL) > 4096 || (command.CredentialRunID == "" && !validQuickTaskAPIKey(command.APIKey)) {
			return "", ErrInvalid
		}
		endpoint, err := url.Parse(command.BaseURL)
		if err != nil {
			return "", ErrInvalid
		}
		channel.Name = endpoint.Host
	}
	if !secureCredentialEndpoint(channel.BaseURL, service.allowInsecureLoopback) {
		return "", ErrNotRunnable
	}
	load := domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: uint64(len(cases)), RequestTimeoutMS: suite.QuickTest.TimeoutMS}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": float64(suite.QuickTest.TimeoutMS)}}
	mapping := domain.ChannelModel{EntityMeta: modelMeta, ModelID: modelMeta.ID, ChannelID: channel.ID, UpstreamModelName: command.Model}
	snapshot := domain.RunSnapshot{SchemaVersion: domain.FlatRunSnapshotSchemaVersion,
		Plan: domain.EntityRevisionRef{ID: meta.ID, Revision: meta.Revision}, Mapping: &mapping,
		Model:   domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelMeta.ID, Revision: modelMeta.Revision}, Name: command.Model, Protocol: suite.Protocol},
		Channel: domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision}, Name: channel.Name, BaseURL: channel.BaseURL, Protocol: suite.Protocol, UpstreamModelName: command.Model},
		Cases:   append([]domain.CaseRevisionRef(nil), suite.Cases...), CaseDefinitions: cases, Load: load, SLA: sla, Environment: service.environment(),
		QuickTask: &domain.QuickTaskSnapshot{Suite: suite, Inputs: inputs, SavedChannelID: command.ChannelID, CredentialRunID: command.CredentialRunID},
	}
	if snapshot.Validate() != nil {
		return "", ErrNotRunnable
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var lease *credentials.Lease
	if command.ChannelID != "" {
		ref, refErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
		if refErr != nil {
			return "", ErrNotRunnable
		}
		lease, err = service.credentials.Get(ctx, ref)
	} else if command.CredentialRunID != "" {
		lease, err = service.LeaseQuickTaskCredential(ctx, command.CredentialRunID, channel.BaseURL, suite.Protocol)
	} else {
		secret := []byte(command.APIKey)
		lease, err = credentials.NewTemporaryLease(secret)
		clear(secret)
	}
	if err != nil {
		return "", fmt.Errorf("lease quick task credential: %w", err)
	}
	return service.prepareRun(ctx, meta, meta.ID, snapshot, effective, lease)
}

func (service *Service) quickTaskDefinitions(ctx context.Context, command QuickTaskCommand) (domain.Suite, []domain.TestCase, error) {
	if command.SourceRunID != "" {
		snapshot, err := service.quickTaskSnapshot(ctx, command.SourceRunID)
		if err != nil {
			return domain.Suite{}, nil, err
		}
		return snapshot.QuickTask.Suite, snapshot.CaseDefinitions, nil
	}
	suite, err := service.quickTasks.GetSuiteRevision(ctx, command.SuiteID, command.SuiteRevision)
	if err != nil {
		return domain.Suite{}, nil, err
	}
	cases := make([]domain.TestCase, 0, len(suite.Cases))
	for _, ref := range suite.Cases {
		testCase, err := service.repository.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
		if err != nil {
			return domain.Suite{}, nil, fmt.Errorf("load quick task case: %w", err)
		}
		cases = append(cases, testCase)
	}
	return suite, cases, nil
}
