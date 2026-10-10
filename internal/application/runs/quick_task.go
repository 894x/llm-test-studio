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
	"github.com/894x/llm-test-studio/internal/testspec"
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
	if service == nil || ctx == nil || isNil(service.quickTasks) || !domain.IsUUID(command.SuiteID) ||
		command.Model == "" || command.Model != strings.TrimSpace(command.Model) || len(command.Model) > 256 || strings.ContainsFunc(command.Model, unicode.IsControl) {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	caseConcurrency, err := resolveCaseConcurrency(command.CaseConcurrency)
	if err != nil {
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
	if suite.ID != command.SuiteID || suite.Validate() != nil {
		return "", ErrNotRunnable
	}
	if err := suite.ValidateCases(cases); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotRunnable, err)
	}
	inputs, caseInputs, err := suite.ResolveInputs(command.Inputs)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, testCase := range cases {
		if testCase.Validate() != nil || !testCase.Enabled || service.caseTypes.Validate(suite.Protocol, testCase.Definitions[suite.Protocol]) != nil {
			return "", ErrNotRunnable
		}
		spec, err := testCase.SpecFor(suite.Protocol)
		if err != nil {
			return "", err
		}
		values, err := testspec.ValidateInputs(spec.Inputs, caseInputs[testCase.ID])
		if err != nil {
			return "", err
		}
		caseInputs[testCase.ID] = values
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
	channel := domain.Channel{EntityMeta: channelMeta, BaseURL: command.BaseURL, Enabled: true}
	if command.ChannelID != "" {
		channel, err = service.quickTasks.GetChannel(ctx, command.ChannelID)
		if err != nil {
			return "", fmt.Errorf("load quick task channel: %w", err)
		}
		usableChannel := channel.ID == command.ChannelID && channel.Enabled && channel.CredentialID != ""
		if !usableChannel || channel.Validate() != nil {
			return "", ErrNotRunnable
		}
		mappings, err := service.quickTasks.ListChannelModels(ctx)
		if err != nil {
			return "", fmt.Errorf("load quick task mappings: %w", err)
		}
		knownModel := false
		supported := false
		for _, mapping := range mappings {
			if mapping.ChannelID != channel.ID || mapping.UpstreamModelName != command.Model {
				continue
			}
			if mapping.Validate() != nil {
				return "", ErrNotRunnable
			}
			knownModel = true
			supported = supported || mapping.SupportsProtocol(suite.Protocol)
		}
		if knownModel && !supported {
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
	if channel.Validate() != nil {
		return "", ErrNotRunnable
	}
	timeout := command.RequestTimeoutMS
	if timeout == 0 {
		timeout = 60000
	}
	load := domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: min(caseConcurrency, uint32(len(cases))),
		RequestCount: 1, RequestTimeoutMS: timeout,
	}
	sla := domain.SLAProfile{Thresholds: map[string]float64{}}
	mapping := domain.ChannelModel{
		EntityMeta: modelMeta, ModelID: modelMeta.ID, ChannelID: channel.ID,
		Protocols: []domain.Protocol{suite.Protocol}, UpstreamModelName: command.Model,
	}
	entry := domain.RunEntrySnapshot{EntryID: meta.ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Name: suite.Name, Key: suite.Key, Suite: &suite,
		Cases: []domain.CaseRevisionRef{}, CaseDefinitions: cases, Parameters: inputs, CaseInputs: caseInputs, Load: load, SLA: sla}
	for _, testCase := range cases {
		entry.Cases = append(entry.Cases, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision})
	}
	plan := domain.Plan{EntityMeta: meta, Name: suite.Name, Protocol: suite.Protocol, Seed: command.Seed, Entries: []domain.PlanEntry{{EntryID: meta.ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Parameters: inputs, Load: load, SLA: sla}}}
	snapshot := domain.RunSnapshot{SchemaVersion: domain.CurrentRunSnapshotSchemaVersion, Plan: domain.EntityRevisionRef{ID: meta.ID, Revision: meta.Revision}, PlanDocument: &plan, Mapping: &mapping,
		Model:       domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelMeta.ID, Revision: modelMeta.Revision}, Name: command.Model, Protocol: suite.Protocol},
		Channel:     domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision}, Name: channel.Name, BaseURL: channel.BaseURL, Protocol: suite.Protocol, UpstreamModelName: command.Model},
		Environment: service.environment(), Entries: []domain.RunEntrySnapshot{entry}, QuickTask: &domain.QuickTaskSnapshot{SavedChannelID: command.ChannelID, CredentialRunID: command.CredentialRunID}}

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
	return service.prepareRun(ctx, meta, meta.ID, snapshot, lease)
}

func (service *Service) quickTaskDefinitions(ctx context.Context, command QuickTaskCommand) (domain.Suite, []domain.TestCase, error) {
	if command.SourceRunID != "" {
		snapshot, err := service.quickTaskSnapshot(ctx, command.SourceRunID)
		if err != nil {
			return domain.Suite{}, nil, err
		}
		if snapshot.Entries[0].Suite.ID != command.SuiteID {
			return domain.Suite{}, nil, fmt.Errorf("%w: source quick task uses a different Suite", ErrNotRunnable)
		}
	}
	suite, err := service.quickTasks.GetSuite(ctx, command.SuiteID)
	if err != nil {
		return domain.Suite{}, nil, err
	}
	testCases, err := service.repository.ListTestCases(ctx)
	if err != nil {
		return domain.Suite{}, nil, fmt.Errorf("load quick task Case catalog: %w", err)
	}
	caseByID, err := indexTestCases(testCases)
	if err != nil {
		return domain.Suite{}, nil, err
	}
	cases := make([]domain.TestCase, 0, len(suite.Cases))
	for _, ref := range suite.Cases {
		testCase, found := caseByID[ref.CaseID]
		if !found {
			return domain.Suite{}, nil, fmt.Errorf(
				"%w: quick task references unavailable Case %s",
				ErrNotRunnable,
				ref.CaseID,
			)
		}
		cases = append(cases, testCase)
	}
	return suite, cases, nil
}
