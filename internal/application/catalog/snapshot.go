package catalog

import (
	"context"
	"sort"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (service *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	models, err := service.repository.ListModels(ctx)
	if err != nil {
		return Snapshot{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	channels, err := service.repository.ListChannels(ctx)
	if err != nil {
		return Snapshot{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	mappings, err := service.repository.ListChannelModels(ctx)
	if err != nil {
		return Snapshot{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	testCases, err := service.repository.ListTestCases(ctx)
	if err != nil {
		return Snapshot{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	suites, err := service.repository.ListSuites(ctx)
	if err != nil {
		return Snapshot{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	plans, err := service.repository.ListPlans(ctx)
	if err != nil {
		return Snapshot{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	snapshot, err := service.buildSnapshot(ctx, models, channels, mappings, testCases, suites, plans)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.CaseTypes = service.caseTypes.Descriptors()
	return snapshot, nil
}

func (service *Service) ListModels(ctx context.Context) ([]ModelSummary, error) {
	snapshot, err := service.Snapshot(ctx)
	return snapshot.Models, err
}

func (service *Service) ListChannels(ctx context.Context) ([]ChannelSummary, error) {
	snapshot, err := service.Snapshot(ctx)
	return snapshot.Channels, err
}

func (service *Service) ListChannelModels(ctx context.Context) ([]ChannelModelSummary, error) {
	snapshot, err := service.Snapshot(ctx)
	return snapshot.ChannelModels, err
}

func (service *Service) ListTestCases(ctx context.Context) ([]TestCaseSummary, error) {
	snapshot, err := service.Snapshot(ctx)
	return snapshot.TestCases, err
}

func (service *Service) ListSuites(ctx context.Context) ([]SuiteSummary, error) {
	snapshot, err := service.Snapshot(ctx)
	return snapshot.Suites, err
}

func (service *Service) ListPlans(ctx context.Context) ([]PlanSummary, error) {
	snapshot, err := service.Snapshot(ctx)
	return snapshot.Plans, err
}

func (service *Service) buildSnapshot(
	ctx context.Context,
	models []domain.Model,
	channels []domain.Channel,
	mappings []domain.ChannelModel,
	testCases []domain.TestCase,
	suites []domain.Suite,
	plans []domain.Plan,
) (Snapshot, error) {
	modelByID := make(map[string]domain.Model, len(models))
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := model.Validate(); err != nil {
			return Snapshot{}, ErrCorrupt
		}
		if _, duplicate := modelByID[model.ID]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		modelByID[model.ID] = model
	}
	channelByID := make(map[string]domain.Channel, len(channels))
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := channel.Validate(); err != nil {
			return Snapshot{}, ErrCorrupt
		}
		if _, duplicate := channelByID[channel.ID]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		channelByID[channel.ID] = channel
	}
	testCaseByID := make(map[string]domain.TestCase, len(testCases))
	for _, testCase := range testCases {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := testCase.Validate(); err != nil {
			return Snapshot{}, ErrCorrupt
		}
		if _, duplicate := testCaseByID[testCase.ID]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		testCaseByID[testCase.ID] = testCase
	}

	mappingByBinding := make(map[string]domain.ChannelModel, len(mappings))
	mappingIDs := make(map[string]struct{}, len(mappings))
	modelCountByChannel := make(map[string]int, len(channels))
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := mapping.Validate(); err != nil {
			return Snapshot{}, ErrCorrupt
		}
		if _, duplicate := mappingIDs[mapping.ID]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		mappingIDs[mapping.ID] = struct{}{}
		_, channelFound := channelByID[mapping.ChannelID]
		model, modelFound := modelByID[mapping.ModelID]
		if !channelFound || !modelFound {
			return Snapshot{}, ErrCorrupt
		}
		for _, value := range mapping.Protocols {
			if !model.SupportsProtocol(value) {
				return Snapshot{}, ErrCorrupt
			}
		}
		binding := mapping.ChannelID + "\x00" + mapping.ModelID
		if _, duplicate := mappingByBinding[binding]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		mappingByBinding[binding] = mapping
		modelCountByChannel[mapping.ChannelID]++
	}

	suiteByID := make(map[string]domain.Suite, len(suites))
	for _, suite := range suites {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := suite.Validate(); err != nil {
			return Snapshot{}, ErrCorrupt
		}
		if _, duplicate := suiteByID[suite.ID]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		suiteByID[suite.ID] = suite
	}
	planIDs := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := plan.Validate(); err != nil || len(plan.Entries) == 0 {
			return Snapshot{}, ErrCorrupt
		}
		if _, duplicate := planIDs[plan.ID]; duplicate {
			return Snapshot{}, ErrCorrupt
		}
		planIDs[plan.ID] = struct{}{}

	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		SchemaVersion: CurrentSnapshotSchemaVersion,
		CaseTypes:     []casetypes.Descriptor{},
		Models:        make([]ModelSummary, 0, len(models)), Channels: make([]ChannelSummary, 0, len(channels)),
		ChannelModels: make([]ChannelModelSummary, 0, len(mappings)), TestCases: make([]TestCaseSummary, 0, len(testCases)),
		Suites: make([]SuiteSummary, 0, len(suites)), Plans: make([]PlanSummary, 0, len(plans)),
	}
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		snapshot.Models = append(snapshot.Models, ModelSummary{
			ID: model.ID, Revision: model.Revision, Name: model.Name, Protocols: append([]domain.Protocol{}, model.Protocols...),
			Capabilities: append([]string{}, model.Capabilities...),
		})
	}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		snapshot.Channels = append(snapshot.Channels, ChannelSummary{
			ID: channel.ID, Revision: channel.Revision, Name: channel.Name, BaseURL: channel.BaseURL,
			Protocol: channel.Protocol, Enabled: channel.Enabled, CredentialConfigured: channel.CredentialID != "",
			ModelCount: modelCountByChannel[channel.ID],
		})
	}
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		snapshot.ChannelModels = append(snapshot.ChannelModels, ChannelModelSummary{
			ID: mapping.ID, Revision: mapping.Revision, ChannelID: mapping.ChannelID,
			ModelID: mapping.ModelID, Protocols: append([]domain.Protocol{}, mapping.Protocols...),
			UpstreamModelName: mapping.UpstreamModelName,
		})
	}
	for _, testCase := range testCases {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		snapshot.TestCases = append(snapshot.TestCases, TestCaseSummary{
			ID: testCase.ID, Revision: testCase.Revision, Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
			Enabled: testCase.Enabled, Default: testCase.Default,
			Severity: testCase.Severity, ExecutionMode: testCase.ExecutionMode,
			Definitions: testCase.Definitions.Clone(),
		})
	}
	for _, suite := range suites {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		cases := make([]CaseInput, len(suite.Cases))
		for index, ref := range suite.Cases {
			cases[index] = CaseInput{CaseID: ref.CaseID}
		}
		snapshot.Suites = append(snapshot.Suites, SuiteSummary{
			ID: suite.ID, Revision: suite.Revision, Key: suite.Key, Name: suite.Name, Protocol: suite.Protocol,
			Description: suite.Description, CaseCount: len(suite.Cases), Cases: cases, Inputs: domain.CloneSuiteInputs(suite.Inputs),
		})
	}
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		entrySummaries := make([]PlanEntrySummary, 0, len(plan.Entries))
		caseCount := 0
		for _, entry := range plan.Entries {
			summary := planEntrySummary(entry)
			switch entry.TargetKind {
			case domain.PlanTargetCase:
				if target, exists := testCaseByID[entry.TargetID]; exists {
					summary.TargetKey = target.Key
					summary.TargetName = target.Name
					summary.CaseCount = 1
				}
			case domain.PlanTargetSuite:
				if target, exists := suiteByID[entry.TargetID]; exists {
					summary.TargetKey = target.Key
					summary.TargetName = target.Name
					summary.CaseCount = len(target.Cases)
				}
			}
			caseCount += summary.CaseCount
			entrySummaries = append(entrySummaries, summary)
		}
		snapshot.Plans = append(snapshot.Plans, PlanSummary{
			ID: plan.ID, Revision: plan.Revision, Name: plan.Name, Protocol: plan.Protocol, Seed: plan.Seed,
			EntryCount: len(plan.Entries), CaseCount: caseCount, Entries: entrySummaries,
			Performance: clonePerformancePlanProfile(plan.Performance),
		})
	}

	sort.Slice(snapshot.Models, func(left, right int) bool {
		return lessNameID(snapshot.Models[left].Name, snapshot.Models[left].ID, snapshot.Models[right].Name, snapshot.Models[right].ID)
	})
	sort.Slice(snapshot.Channels, func(left, right int) bool {
		return lessNameID(snapshot.Channels[left].Name, snapshot.Channels[left].ID, snapshot.Channels[right].Name, snapshot.Channels[right].ID)
	})
	sort.Slice(snapshot.ChannelModels, func(left, right int) bool {
		leftValue, rightValue := snapshot.ChannelModels[left], snapshot.ChannelModels[right]
		if leftValue.ChannelID != rightValue.ChannelID {
			return leftValue.ChannelID < rightValue.ChannelID
		}
		if leftValue.ModelID != rightValue.ModelID {
			return leftValue.ModelID < rightValue.ModelID
		}
		return leftValue.ID < rightValue.ID
	})
	sort.Slice(snapshot.TestCases, func(left, right int) bool {
		return lessNameID(snapshot.TestCases[left].Name, snapshot.TestCases[left].ID, snapshot.TestCases[right].Name, snapshot.TestCases[right].ID)
	})
	sort.Slice(snapshot.Suites, func(left, right int) bool {
		return lessNameID(snapshot.Suites[left].Name, snapshot.Suites[left].ID, snapshot.Suites[right].Name, snapshot.Suites[right].ID)
	})
	sort.Slice(snapshot.Plans, func(left, right int) bool {
		return lessNameID(snapshot.Plans[left].Name, snapshot.Plans[left].ID, snapshot.Plans[right].Name, snapshot.Plans[right].ID)
	})
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func planEntrySummary(entry domain.PlanEntry) PlanEntrySummary {
	return PlanEntrySummary{PlanEntryInput: PlanEntryInput{
		EntryID: entry.EntryID, TargetKind: entry.TargetKind, TargetID: entry.TargetID,
		LoadMode: entry.Load.Mode, Concurrency: entry.Load.Concurrency, RequestCount: entry.Load.RequestCount,
		RatePerSecond: entry.Load.RatePerSecond, DurationMS: entry.Load.DurationMS, RequestTimeoutMS: entry.Load.RequestTimeoutMS,
		SLAThresholds: cloneThresholds(entry.SLA.Thresholds), Parameters: cloneRawMessages(entry.Parameters), WarmupCount: entry.WarmupCount, Settings: entry.Settings,
	}}
}

func lessNameID(leftName, leftID, rightName, rightID string) bool {
	if leftName == rightName {
		return leftID < rightID
	}
	return leftName < rightName
}
