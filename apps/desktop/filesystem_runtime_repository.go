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

func (repository filesystemRuntimeRepository) GetTestCaseRevision(ctx context.Context, id string, revision uint64) (domain.TestCase, error) {
	testCase, err := repository.catalog.GetTestCaseRevision(ctx, id, revision)
	if err != nil {
		return domain.TestCase{}, err
	}
	// SQLite persists Run snapshots as canonical JSON and compares a later
	// state transition with the decoded snapshot. Normalize filesystem JSON
	// first so insignificant formatting and object-key order cannot make those
	// otherwise identical Run values compare differently.
	decoder := json.NewDecoder(bytes.NewReader(testCase.Definition.Spec))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return domain.TestCase{}, err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return domain.TestCase{}, err
	}
	testCase.Definition.Spec = append(json.RawMessage(nil), canonical...)
	return testCase, nil
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
	if len(plan.ModelIDs) > 0 && (!containsRuntimeID(plan.ModelIDs, modelID) || !containsRuntimeID(plan.ChannelIDs, channelID)) {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrNotFound
	}
	if len(plan.ModelIDs) > 0 {
		for _, binding := range document.TargetBindings {
			if binding.Model.ID != modelID || binding.Channel.ID != channelID {
				continue
			}
			if !binding.Channel.Enabled || binding.Model.Protocol != binding.Channel.Protocol ||
				binding.Mapping.ModelID != binding.Model.ID || binding.Mapping.ChannelID != binding.Channel.ID {
				return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrCorrupt
			}
			return binding.Model, binding.Channel, binding.Mapping, nil
		}
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
	if !channel.Enabled || model.Protocol != channel.Protocol || selected.ModelID != model.ID || selected.ChannelID != channel.ID {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, catalog.ErrNotFound
	}
	return model, channel, selected, nil
}

func containsRuntimeID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
