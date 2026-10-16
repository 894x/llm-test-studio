package runs

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func (service *Preparer) BuildQuickTask(
	ctx context.Context,
	command QuickTaskCommand,
) (PreparedRun, *credentials.Lease, error) {
	if service == nil || ctx == nil {
		return PreparedRun{}, nil, ErrInvalid
	}
	invalidTarget := isNil(service.quickTasks) || !domain.IsUUID(command.SuiteID)
	invalidModel := command.Model == "" || command.Model != strings.TrimSpace(command.Model) || len(command.Model) > 256
	if invalidTarget || invalidModel || strings.ContainsFunc(command.Model, unicode.IsControl) {
		return PreparedRun{}, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return PreparedRun{}, nil, err
	}
	caseConcurrency, err := resolveCaseConcurrency(command.CaseConcurrency)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	service.mu.Lock()
	closed := service.closed
	service.mu.Unlock()
	if closed {
		return PreparedRun{}, nil, ErrClosed
	}
	conflictingConnection := command.BaseURL != "" || command.APIKey != "" || command.CredentialRunID != ""
	if command.ChannelID != "" && (!domain.IsUUID(command.ChannelID) || conflictingConnection) {
		return PreparedRun{}, nil, ErrInvalid
	}
	if command.CredentialRunID != "" && (!domain.IsUUID(command.CredentialRunID) || command.APIKey != "") {
		return PreparedRun{}, nil, ErrInvalid
	}
	suite, cases, err := service.quickTaskDefinitions(ctx, command)
	if err != nil {
		return PreparedRun{}, nil, fmt.Errorf("load quick task: %w", err)
	}
	if suite.ID != command.SuiteID || suite.Validate() != nil {
		return PreparedRun{}, nil, ErrNotRunnable
	}
	if err := suite.ValidateCases(cases); err != nil {
		return PreparedRun{}, nil, fmt.Errorf("%w: %v", ErrNotRunnable, err)
	}
	inputs, caseInputs, err := suite.ResolveInputs(command.Inputs)
	if err != nil {
		return PreparedRun{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if suite.HasFileInputs() {
		return PreparedRun{}, nil, fmt.Errorf("%w: %w", ErrNotRunnable, testspec.ErrFileInputsUnsupported)
	}
	for _, testCase := range cases {
		invalidCase := testCase.Validate() != nil || !testCase.Enabled
		if invalidCase || service.caseTypes.Validate(suite.Protocol, testCase.Definitions[suite.Protocol]) != nil {
			return PreparedRun{}, nil, ErrNotRunnable
		}
		spec, err := testCase.SpecFor(suite.Protocol)
		if err != nil {
			return PreparedRun{}, nil, err
		}
		values, err := testspec.ValidateInputs(spec.Inputs, caseInputs[testCase.ID])
		if err != nil {
			return PreparedRun{}, nil, err
		}
		if len(values.Files) != 0 {
			return PreparedRun{}, nil, fmt.Errorf("%w: %w", ErrNotRunnable, testspec.ErrFileInputsUnsupported)
		}
		caseInputs[testCase.ID] = values.SnapshotValues()
	}

	now := service.clock.Now()
	meta, err := service.metaFactory(now)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	modelMeta, err := domain.NewEntityMeta(now)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	channelMeta, err := domain.NewEntityMeta(now)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	channel := domain.Channel{EntityMeta: channelMeta, BaseURL: command.BaseURL, Enabled: true}
	if command.ChannelID != "" {
		channel, err = service.quickTasks.GetChannel(ctx, command.ChannelID)
		if err != nil {
			return PreparedRun{}, nil, fmt.Errorf("load quick task channel: %w", err)
		}
		usableChannel := channel.ID == command.ChannelID && channel.Enabled && channel.CredentialID != ""
		if !usableChannel || channel.Validate() != nil {
			return PreparedRun{}, nil, ErrNotRunnable
		}
		mappings, err := service.quickTasks.ListChannelModels(ctx)
		if err != nil {
			return PreparedRun{}, nil, fmt.Errorf("load quick task mappings: %w", err)
		}
		knownModel := false
		supported := false
		for _, mapping := range mappings {
			if mapping.ChannelID != channel.ID || mapping.UpstreamModelName != command.Model {
				continue
			}
			if mapping.Validate() != nil {
				return PreparedRun{}, nil, ErrNotRunnable
			}
			knownModel = true
			supported = supported || mapping.SupportsProtocol(suite.Protocol)
		}
		if knownModel && !supported {
			return PreparedRun{}, nil, ErrNotRunnable
		}
	} else {
		if len(command.BaseURL) > 4096 || (command.CredentialRunID == "" && !validQuickTaskAPIKey(command.APIKey)) {
			return PreparedRun{}, nil, ErrInvalid
		}
		endpoint, err := url.Parse(command.BaseURL)
		if err != nil {
			return PreparedRun{}, nil, ErrInvalid
		}
		channel.Name = endpoint.Host
		if channel.Validate() != nil {
			return PreparedRun{}, nil, ErrNotRunnable
		}
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
	entry := domain.RunEntrySnapshot{
		EntryID: meta.ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
		Name: suite.Name, Key: suite.Key, Suite: &suite,
		Cases: []domain.CaseRevisionRef{}, CaseDefinitions: cases,
		Parameters: inputs, CaseInputs: caseInputs, Load: load, SLA: sla,
	}
	for _, testCase := range cases {
		entry.Cases = append(entry.Cases, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision})
	}
	plan := domain.Plan{
		EntityMeta: meta, Name: suite.Name, Protocol: suite.Protocol, Seed: command.Seed,
		Entries: []domain.PlanEntry{{
			EntryID: meta.ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
			Parameters: inputs, Load: load, SLA: sla,
		}},
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: meta.ID, Revision: meta.Revision},
		PlanDocument:  &plan, Mapping: &mapping,
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: modelMeta.ID, Revision: modelMeta.Revision},
			Name:              command.Model, Protocol: suite.Protocol,
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision},
			Name:              channel.Name, BaseURL: channel.BaseURL,
			Protocol: suite.Protocol, UpstreamModelName: command.Model,
		},
		Environment: service.environment(), Entries: []domain.RunEntrySnapshot{entry},
		QuickTask: &domain.QuickTaskSnapshot{
			SavedChannelID: command.ChannelID, CredentialRunID: command.CredentialRunID,
		},
	}

	if snapshot.Validate() != nil {
		return PreparedRun{}, nil, ErrNotRunnable
	}
	if err := ctx.Err(); err != nil {
		return PreparedRun{}, nil, err
	}
	var lease *credentials.Lease
	if command.ChannelID != "" {
		ref, refErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, channel.CredentialID)
		if refErr != nil {
			return PreparedRun{}, nil, ErrNotRunnable
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
		return PreparedRun{}, nil, fmt.Errorf("lease quick task credential: %w", err)
	}
	prepared, err := freezePreparedRun(meta, meta.ID, snapshot)
	if err != nil {
		_ = lease.Close()
		return PreparedRun{}, nil, err
	}
	return prepared, lease, nil
}

func (service *Preparer) quickTaskDefinitions(
	ctx context.Context,
	command QuickTaskCommand,
) (domain.Suite, []domain.TestCase, error) {
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
