package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

// filesystemRuntimeRepository resolves authored configuration from files and
// delegates only operational Run/Result/Report persistence to SQLite.
type filesystemRuntimeRepository struct {
	*sqlite.Repository
	catalog filesystemCatalogRepository
}

func (repository filesystemRuntimeRepository) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	return repository.catalog.ListPlans(ctx)
}

func (repository filesystemRuntimeRepository) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	return repository.catalog.ListSuites(ctx)
}

func (repository filesystemRuntimeRepository) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	return repository.catalog.GetPlan(ctx, id)
}

func (repository filesystemRuntimeRepository) GetPlanRevision(ctx context.Context, id string, revision uint64) (domain.Plan, error) {
	plan, err := repository.catalog.GetPlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if plan.Revision != revision {
		return domain.Plan{}, catalog.ErrNotFound
	}
	return plan, nil
}

func (repository filesystemRuntimeRepository) GetTestCase(ctx context.Context, id string) (domain.TestCase, error) {
	testCase, err := repository.catalog.GetTestCase(ctx, id)
	if err != nil {
		return domain.TestCase{}, err
	}
	return normalizeRuntimeTestCase(testCase)
}

func (repository filesystemRuntimeRepository) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	testCases, err := repository.catalog.ListTestCases(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.TestCase, len(testCases))
	for index, testCase := range testCases {
		result[index], err = normalizeRuntimeTestCase(testCase)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func normalizeRuntimeTestCase(testCase domain.TestCase) (domain.TestCase, error) {
	// SQLite persists Run snapshots as canonical JSON and compares a later
	// state transition with the decoded snapshot. Normalize filesystem JSON
	// first so insignificant formatting and object-key order cannot make those
	// otherwise identical Run values compare differently.
	testCase.Definitions = testCase.Definitions.Clone()
	for protocol, raw := range testCase.Definitions {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var normalized any
		if err := decoder.Decode(&normalized); err != nil {
			return domain.TestCase{}, err
		}
		canonical, err := json.Marshal(normalized)
		if err != nil {
			return domain.TestCase{}, err
		}
		testCase.Definitions[protocol] = canonical
	}
	return testCase, nil
}

func (repository filesystemRuntimeRepository) GetSuite(ctx context.Context, id string) (domain.Suite, error) {
	suite, err := repository.catalog.GetSuite(ctx, id)
	if err != nil {
		return domain.Suite{}, err
	}
	return suite, nil
}

func (repository filesystemRuntimeRepository) ResolvePlanTargetSelection(
	ctx context.Context,
	plan domain.Plan,
	modelID string,
	channelID string,
) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	if ctx == nil || plan.Validate() != nil || !domain.IsUUID(modelID) || !domain.IsUUID(channelID) {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrInvalid
	}
	document, err := repository.catalog.GetPlanDocument(ctx, plan.ID)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	if !reflect.DeepEqual(document.Plan, plan) {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrNotFound
	}

	model, err := repository.catalog.GetModel(ctx, modelID)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	channel, err := repository.catalog.GetChannel(ctx, channelID)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	mappings, err := repository.catalog.ListChannelModels(ctx)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	var selected domain.ChannelModel
	for _, mapping := range mappings {
		if mapping.ModelID != modelID || mapping.ChannelID != channelID {
			continue
		}
		if selected.ID != "" {
			return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrCorrupt
		}
		selected = mapping
	}
	if selected.ID == "" {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrNotFound
	}
	protocolSupported := model.SupportsProtocol(plan.Protocol) && selected.SupportsProtocol(plan.Protocol)
	mappingMatches := selected.ModelID == model.ID && selected.ChannelID == channel.ID
	if !protocolSupported || !mappingMatches {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrPlanProtocolMismatch
	}
	return model, channel, selected, nil
}
