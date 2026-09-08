package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func (service *Service) CreateModel(ctx context.Context, command CreateModelCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := service.newMeta(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	model := domain.Model{EntityMeta: meta, Name: command.Name, Protocol: command.Protocol, Capabilities: append([]string(nil), command.Capabilities...)}
	if err := model.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.repository.CreateModel(ctx, model); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) UpdateModel(ctx context.Context, command UpdateModelCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetModel(ctx, command.ID)
	if err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if err := current.Validate(); err != nil || current.ID != command.ID {
		return MutationResult{}, ErrCorrupt
	}
	if command.Protocol != current.Protocol {
		return MutationResult{}, ErrInvalid
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	model := domain.Model{EntityMeta: meta, Name: command.Name, Protocol: command.Protocol, Capabilities: append([]string(nil), command.Capabilities...)}
	if err := model.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.repository.UpdateModel(ctx, command.ExpectedRevision, model); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) CreateChannel(ctx context.Context, command CreateChannelCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := service.newMeta(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	channel := domain.Channel{EntityMeta: meta, Name: command.Name, BaseURL: command.BaseURL, Protocol: command.Protocol, Enabled: command.Enabled, CredentialID: command.CredentialID}
	if err := channel.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.repository.CreateChannel(ctx, channel); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) UpdateChannel(ctx context.Context, command UpdateChannelCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetChannel(ctx, command.ID)
	if err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if err := current.Validate(); err != nil || current.ID != command.ID {
		return MutationResult{}, ErrCorrupt
	}
	if command.Protocol != current.Protocol {
		return MutationResult{}, ErrInvalid
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	channel := domain.Channel{EntityMeta: meta, Name: command.Name, BaseURL: command.BaseURL, Protocol: command.Protocol, Enabled: command.Enabled, CredentialID: current.CredentialID}
	if err := channel.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.repository.UpdateChannel(ctx, command.ExpectedRevision, channel); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) CreateChannelModel(ctx context.Context, command CreateChannelModelCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := service.newMeta(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	mapping := domain.ChannelModel{EntityMeta: meta, ChannelID: command.ChannelID, ModelID: command.ModelID, UpstreamModelName: command.UpstreamModelName}
	if err := mapping.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.validateBinding(ctx, mapping.ChannelID, mapping.ModelID); err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.CreateChannelModel(ctx, mapping); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) UpdateChannelModel(ctx context.Context, command UpdateChannelModelCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetChannelModel(ctx, command.ID)
	if err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if err := current.Validate(); err != nil || current.ID != command.ID {
		return MutationResult{}, ErrCorrupt
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	mapping := domain.ChannelModel{EntityMeta: meta, ChannelID: current.ChannelID, ModelID: current.ModelID, UpstreamModelName: command.UpstreamModelName}
	if err := mapping.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.validateBinding(ctx, mapping.ChannelID, mapping.ModelID); err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.UpdateChannelModel(ctx, command.ExpectedRevision, mapping); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) CreateTestCase(ctx context.Context, command CreateTestCaseCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := service.newMeta(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	testCase := testCaseFromCreate(meta, command)
	if err := testCase.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.caseTypes.Validate(testCase.Protocol, testCase.Definition); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if descriptor, found := service.caseTypes.Descriptor(testCase.Definition.Type, testCase.Definition.TypeVersion); !found || !descriptor.Creatable {
		return MutationResult{}, ErrInvalid
	}
	if err := service.repository.CreateTestCase(ctx, testCase); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) UpdateTestCase(ctx context.Context, command UpdateTestCaseCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetTestCase(ctx, command.ID)
	if err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if err := current.Validate(); err != nil || current.ID != command.ID {
		return MutationResult{}, ErrCorrupt
	}
	if command.Protocol != current.Protocol || command.Key != current.Key {
		return MutationResult{}, ErrInvalid
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	testCase := testCaseFromUpdate(meta, command)
	if err := testCase.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.caseTypes.Validate(testCase.Protocol, testCase.Definition); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if testCase.Definition.Type != current.Definition.Type || testCase.Definition.TypeVersion != current.Definition.TypeVersion {
		if descriptor, found := service.caseTypes.Descriptor(testCase.Definition.Type, testCase.Definition.TypeVersion); !found || !descriptor.Creatable {
			return MutationResult{}, ErrInvalid
		}
	}
	if err := service.repository.UpdateTestCase(ctx, command.ExpectedRevision, testCase); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) CreateSuite(ctx context.Context, command CreateSuiteCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := service.newMeta(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	suite := domain.Suite{
		EntityMeta: meta, Key: command.Key, Name: command.Name, Protocol: command.Protocol,
		ModelTarget: command.ModelTarget, Cases: cloneCaseRefs(command.Cases), QuickTest: command.QuickTest.Clone(),
	}
	if err := service.validateSuiteCases(ctx, suite); err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.CreateSuite(ctx, suite); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) UpdateSuite(ctx context.Context, command UpdateSuiteCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetSuite(ctx, command.ID)
	if err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if err := current.Validate(); err != nil || current.ID != command.ID {
		return MutationResult{}, ErrCorrupt
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	suite := domain.Suite{
		EntityMeta: meta, Key: command.Key, Name: command.Name, Protocol: command.Protocol,
		ModelTarget: command.ModelTarget, Cases: cloneCaseRefs(command.Cases), QuickTest: command.QuickTest.Clone(),
	}
	if err := service.validateSuiteCases(ctx, suite); err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.UpdateSuite(ctx, command.ExpectedRevision, suite); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) CreatePlan(ctx context.Context, command CreatePlanCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := service.newMeta(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	entries, err := service.resolvePlanSuiteEntries(ctx, meta.ID, meta.Revision, nil, command.Suites)
	if err != nil {
		return MutationResult{}, err
	}
	plan := planFromCreate(meta, command, entries)
	if err := plan.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.validatePlanTarget(ctx, plan); err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.CreatePlan(ctx, plan); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) UpdatePlan(ctx context.Context, command UpdatePlanCommand) (MutationResult, error) {
	ctx, err := service.ready(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetPlan(ctx, command.ID)
	if err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	if err := current.Validate(); err != nil || current.ID != command.ID || len(current.Suites) == 0 {
		return MutationResult{}, ErrCorrupt
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	existingEntries := make(map[string]struct{}, len(current.Suites))
	for _, entry := range current.Suites {
		existingEntries[entry.EntryID] = struct{}{}
	}
	entries, err := service.resolvePlanSuiteEntries(ctx, meta.ID, meta.Revision, existingEntries, command.Suites)
	if err != nil {
		return MutationResult{}, err
	}
	plan := planFromUpdate(meta, command, entries)
	if err := plan.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.validatePlanTarget(ctx, plan); err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.UpdatePlan(ctx, command.ExpectedRevision, plan); err != nil {
		return MutationResult{}, service.portError(ctx, err)
	}
	return service.completedMutation(ctx, meta)
}

func (service *Service) DeleteModel(ctx context.Context, command DeleteCommand) error {
	return service.delete(ctx, command, func(ctx context.Context, id string, expectedRevision uint64) error {
		return service.repository.DeleteModel(ctx, id, expectedRevision)
	})
}

func (service *Service) DeleteChannel(ctx context.Context, command DeleteCommand) error {
	return service.delete(ctx, command, func(ctx context.Context, id string, expectedRevision uint64) error {
		return service.repository.DeleteChannel(ctx, id, expectedRevision)
	})
}

func (service *Service) DeleteChannelModel(ctx context.Context, command DeleteCommand) error {
	return service.delete(ctx, command, func(ctx context.Context, id string, expectedRevision uint64) error {
		return service.repository.DeleteChannelModel(ctx, id, expectedRevision)
	})
}

func (service *Service) DeleteTestCase(ctx context.Context, command DeleteCommand) error {
	return service.delete(ctx, command, func(ctx context.Context, id string, expectedRevision uint64) error {
		return service.repository.DeleteTestCase(ctx, id, expectedRevision)
	})
}

func (service *Service) DeleteSuite(ctx context.Context, command DeleteCommand) error {
	return service.delete(ctx, command, func(ctx context.Context, id string, expectedRevision uint64) error {
		return service.repository.DeleteSuite(ctx, id, expectedRevision)
	})
}

func (service *Service) DeletePlan(ctx context.Context, command DeleteCommand) error {
	return service.delete(ctx, command, func(ctx context.Context, id string, expectedRevision uint64) error {
		return service.repository.DeletePlan(ctx, id, expectedRevision)
	})
}

func (service *Service) delete(ctx context.Context, command DeleteCommand, operation func(context.Context, string, uint64) error) error {
	ctx, err := service.ready(ctx)
	if err != nil {
		return err
	}
	if !validUpdateIdentity(command.ID, command.ExpectedRevision) {
		return ErrInvalid
	}
	if err := operation(ctx, command.ID, command.ExpectedRevision); err != nil {
		return service.portError(ctx, err)
	}
	return ctx.Err()
}

func (service *Service) ready(ctx context.Context) (context.Context, error) {
	if service == nil || isNilInterface(service.repository) || isNilInterface(service.clock) || service.metaFactory == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ctx, nil
}

func (service *Service) newMeta(ctx context.Context) (domain.EntityMeta, error) {
	if err := ctx.Err(); err != nil {
		return domain.EntityMeta{}, err
	}
	now := service.clock.Now()
	if err := ctx.Err(); err != nil {
		return domain.EntityMeta{}, err
	}
	if now.IsZero() {
		return domain.EntityMeta{}, ErrUnavailable
	}
	meta, err := service.metaFactory(now)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return domain.EntityMeta{}, contextErr
		}
		return domain.EntityMeta{}, ErrUnavailable
	}
	if err := meta.Validate(); err != nil || meta.Revision != 1 || !meta.CreatedAt.Equal(now.UTC()) || !meta.UpdatedAt.Equal(now.UTC()) {
		return domain.EntityMeta{}, ErrCorrupt
	}
	return meta, nil
}

func (service *Service) nextMeta(ctx context.Context, current domain.EntityMeta, expected uint64) (domain.EntityMeta, error) {
	if err := ctx.Err(); err != nil {
		return domain.EntityMeta{}, err
	}
	if current.Revision != expected || expected == math.MaxUint64 {
		return domain.EntityMeta{}, ErrConflict
	}
	now := service.clock.Now()
	if err := ctx.Err(); err != nil {
		return domain.EntityMeta{}, err
	}
	if now.IsZero() || now.Before(current.UpdatedAt) {
		return domain.EntityMeta{}, ErrUnavailable
	}
	now = now.UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
		if !now.After(current.UpdatedAt) {
			return domain.EntityMeta{}, ErrConflict
		}
	}
	meta, err := current.NextRevision(now)
	if err != nil {
		return domain.EntityMeta{}, ErrConflict
	}
	return meta, nil
}

func (service *Service) completedMutation(ctx context.Context, meta domain.EntityMeta) (MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{ID: meta.ID, Revision: meta.Revision}, nil
}

func (service *Service) validateBinding(ctx context.Context, channelID, modelID string) error {
	channel, err := service.repository.GetChannel(ctx, channelID)
	if err != nil {
		return service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	model, err := service.repository.GetModel(ctx, modelID)
	if err != nil {
		return service.portError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := channel.Validate(); err != nil || channel.ID != channelID {
		return ErrCorrupt
	}
	if err := model.Validate(); err != nil || model.ID != modelID {
		return ErrCorrupt
	}
	if channel.Protocol != model.Protocol {
		return ErrInvalid
	}
	return nil
}

func (service *Service) validateSuiteCases(ctx context.Context, suite domain.Suite) error {
	if suite.Validate() != nil {
		return ErrInvalid
	}
	definitions := make([]domain.TestCase, 0, len(suite.Cases))
	for _, ref := range suite.Cases {
		testCase, err := service.repository.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
		if err != nil {
			return service.portError(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		definitions = append(definitions, testCase)
	}
	if suite.ValidateCases(definitions) != nil {
		return ErrInvalid
	}
	return nil
}

func (service *Service) resolvePlanSuiteEntries(
	ctx context.Context,
	planID string,
	planRevision uint64,
	existingEntries map[string]struct{},
	inputs []PlanSuiteInput,
) ([]domain.PlanSuiteEntry, error) {
	if len(inputs) == 0 {
		return nil, ErrInvalid
	}
	entries := make([]domain.PlanSuiteEntry, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entryID := input.EntryID
		if entryID == "" {
			entryID = generatedPlanSuiteEntryID(planID, planRevision, index, input.SuiteID, input.SuiteRevision)
		} else if !domain.IsUUID(entryID) {
			return nil, ErrInvalid
		} else if _, exists := existingEntries[entryID]; !exists {
			return nil, ErrInvalid
		}
		if _, duplicate := seen[entryID]; duplicate {
			return nil, ErrInvalid
		}
		seen[entryID] = struct{}{}

		suite, err := service.repository.GetSuiteRevision(ctx, input.SuiteID, input.SuiteRevision)
		if err != nil {
			return nil, service.portError(ctx, err)
		}
		if suite.ID != input.SuiteID || suite.Revision != input.SuiteRevision || suite.Validate() != nil {
			return nil, ErrCorrupt
		}
		definitions := make([]domain.TestCase, 0, len(suite.Cases))
		for _, ref := range suite.Cases {
			testCase, loadErr := service.repository.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision)
			if loadErr != nil {
				return nil, service.portError(ctx, loadErr)
			}
			if testCase.Protocol != suite.Protocol {
				return nil, ErrPlanProtocolMismatch
			}
			definitions = append(definitions, testCase)
		}
		if suite.ValidateCases(definitions) != nil {
			return nil, ErrCorrupt
		}
		parameters := cloneRawMessages(input.Parameters)
		if parameters == nil {
			parameters = map[string]json.RawMessage{}
		}
		if suite.QuickTest == nil {
			if len(parameters) != 0 {
				return nil, ErrInvalid
			}
		} else {
			_, resolved, applyErr := suite.ApplyInputs(definitions, parameters)
			if applyErr != nil {
				return nil, ErrInvalid
			}
			parameters = resolved
		}
		entry := domain.PlanSuiteEntry{
			EntryID: entryID, SuiteID: suite.ID, SuiteRevision: suite.Revision,
			Cases: append([]domain.CaseRevisionRef(nil), suite.Cases...), Parameters: parameters,
			Load: domain.LoadProfile{
				Mode: input.LoadMode, Concurrency: input.Concurrency, RequestCount: input.RequestCount,
				RatePerSecond: input.RatePerSecond, DurationMS: input.DurationMS, RequestTimeoutMS: input.RequestTimeoutMS,
			},
			SLA: domain.SLAProfile{Thresholds: cloneThresholds(input.SLAThresholds)},
		}
		if entry.Validate() != nil {
			return nil, ErrInvalid
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func generatedPlanSuiteEntryID(planID string, planRevision uint64, index int, suiteID string, suiteRevision uint64) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("plan-suite-entry:%s:%d:%d:%s:%d", planID, planRevision, index, suiteID, suiteRevision)))
	digest[6] = digest[6]&0x0f | 0x50
	digest[8] = digest[8]&0x3f | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16],
	)
}

func (service *Service) validatePlanTarget(ctx context.Context, plan domain.Plan) error {
	targetProtocol := domain.Protocol("")
	requiredModelTarget := ""
	conflictingModelTargets := false
	for _, entry := range plan.Suites {
		suite, err := service.repository.GetSuiteRevision(ctx, entry.SuiteID, entry.SuiteRevision)
		if err != nil {
			return service.portError(ctx, err)
		}
		if suite.ID != entry.SuiteID || suite.Revision != entry.SuiteRevision || suite.Validate() != nil || !sameCaseRefs(suite.Cases, entry.Cases) {
			return ErrCorrupt
		}
		if targetProtocol == "" {
			targetProtocol = suite.Protocol
		} else if suite.Protocol != targetProtocol {
			return ErrPlanProtocolMismatch
		}
		if suite.ModelTarget != "" {
			if requiredModelTarget == "" {
				requiredModelTarget = suite.ModelTarget
			} else if suite.ModelTarget != requiredModelTarget {
				conflictingModelTargets = true
			}
		}
		if err := service.validateSuiteCases(ctx, suite); err != nil {
			if errors.Is(err, ErrInvalid) {
				return ErrCorrupt
			}
			return err
		}
	}
	for _, modelID := range plan.ModelIDs {
		model, err := service.repository.GetModel(ctx, modelID)
		if err != nil {
			return service.portError(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := model.Validate(); err != nil || model.ID != modelID {
			return ErrCorrupt
		}
		if model.Protocol != targetProtocol {
			return ErrPlanProtocolMismatch
		}
	}
	for _, channelID := range plan.ChannelIDs {
		channel, err := service.repository.GetChannel(ctx, channelID)
		if err != nil {
			return service.portError(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := channel.Validate(); err != nil || channel.ID != channelID {
			return ErrCorrupt
		}
		if channel.Protocol != targetProtocol {
			return ErrPlanProtocolMismatch
		}
	}
	if conflictingModelTargets {
		return ErrInvalid
	}
	if len(plan.ModelIDs) == 0 {
		return nil
	}
	mappings, err := service.repository.ListChannelModels(ctx)
	if err != nil {
		return service.portError(ctx, err)
	}
	mappingByBinding := make(map[string]domain.ChannelModel, len(mappings))
	for _, mapping := range mappings {
		key := mapping.ChannelID + "\x00" + mapping.ModelID
		if !sliceContains(plan.ChannelIDs, mapping.ChannelID) {
			continue
		}
		if !sliceContains(plan.ModelIDs, mapping.ModelID) {
			continue
		}
		if mapping.Validate() != nil {
			return ErrCorrupt
		}
		if _, duplicate := mappingByBinding[key]; duplicate {
			return ErrCorrupt
		}
		mappingByBinding[key] = mapping
	}
	for _, channelID := range plan.ChannelIDs {
		for _, modelID := range plan.ModelIDs {
			mapping, found := mappingByBinding[channelID+"\x00"+modelID]
			if !found || requiredModelTarget != "" && mapping.UpstreamModelName != requiredModelTarget {
				return ErrInvalid
			}
		}
	}
	return nil
}

func sliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (service *Service) portError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, ErrNotFound) || matches(err, service.repositoryErrors.NotFound) {
		return repositoryError(ErrNotFound, err)
	}
	if errors.Is(err, ErrConflict) || matches(err, service.repositoryErrors.Conflict) {
		return repositoryError(ErrConflict, err)
	}
	if errors.Is(err, ErrCorrupt) || matches(err, service.repositoryErrors.Corrupt) {
		return repositoryError(ErrCorrupt, err)
	}
	return repositoryError(ErrUnavailable, err)
}

func repositoryError(publicError, portError error) error {
	var diagnostic SafeDiagnosticCause
	if errors.As(portError, &diagnostic) {
		detail := strings.TrimSpace(diagnostic.SafeDiagnosticCause())
		if detail != "" {
			return fmt.Errorf("%w: %s", publicError, detail)
		}
	}
	return publicError
}

func matches(err, target error) bool {
	return err != nil && !isNilInterface(target) && errors.Is(err, target)
}

func validUpdateIdentity(id string, revision uint64) bool {
	return domain.IsUUID(id) && revision > 0
}

func definitionFromCreate(command CreateTestCaseCommand) domain.TestCaseDefinition {
	return newDefinition(command.DefinitionSchemaVersion, command.Type, command.TypeVersion, command.Spec)
}

func testCaseFromCreate(meta domain.EntityMeta, command CreateTestCaseCommand) domain.TestCase {
	return domain.TestCase{
		EntityMeta: meta, Key: command.Key, Name: command.Name, Dimension: command.Dimension,
		Protocol: command.Protocol, Enabled: command.Enabled, Default: command.Default,
		ModelTargets: append([]string(nil), command.ModelTargets...),
		Severity:     command.Severity, ExecutionMode: command.ExecutionMode,
		Definition: definitionFromCreate(command),
	}
}

func testCaseFromUpdate(meta domain.EntityMeta, command UpdateTestCaseCommand) domain.TestCase {
	return domain.TestCase{
		EntityMeta: meta, Key: command.Key, Name: command.Name, Dimension: command.Dimension,
		Protocol: command.Protocol, Enabled: command.Enabled, Default: command.Default,
		ModelTargets: append([]string(nil), command.ModelTargets...),
		Severity:     command.Severity, ExecutionMode: command.ExecutionMode,
		Definition: definitionFromUpdate(command),
	}
}

func definitionFromUpdate(command UpdateTestCaseCommand) domain.TestCaseDefinition {
	return newDefinition(command.DefinitionSchemaVersion, command.Type, command.TypeVersion, command.Spec)
}

func newDefinition(schemaVersion int, caseType domain.CaseType, typeVersion uint32, spec json.RawMessage) domain.TestCaseDefinition {
	return domain.TestCaseDefinition{
		SchemaVersion: schemaVersion,
		Type:          caseType,
		TypeVersion:   typeVersion,
		Spec:          append(json.RawMessage(nil), spec...),
	}
}

func cloneCaseRefs(values []CaseRevisionInput) []domain.CaseRevisionRef {
	result := make([]domain.CaseRevisionRef, len(values))
	for index, value := range values {
		result[index] = domain.CaseRevisionRef{CaseID: value.CaseID, Revision: value.Revision}
	}
	return result
}

func cloneThresholds(values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}

func cloneRawMessages(values map[string]json.RawMessage) map[string]json.RawMessage {
	if values == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func sameCaseRefs(left, right []domain.CaseRevisionRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func planFromCreate(meta domain.EntityMeta, command CreatePlanCommand, entries []domain.PlanSuiteEntry) domain.Plan {
	return domain.Plan{
		EntityMeta: meta, Name: command.Name, ModelIDs: append([]string(nil), command.ModelIDs...), ChannelIDs: append([]string(nil), command.ChannelIDs...),
		Suites: clonePlanSuiteEntries(entries),
	}
}

func planFromUpdate(meta domain.EntityMeta, command UpdatePlanCommand, entries []domain.PlanSuiteEntry) domain.Plan {
	return domain.Plan{
		EntityMeta: meta, Name: command.Name, ModelIDs: append([]string(nil), command.ModelIDs...), ChannelIDs: append([]string(nil), command.ChannelIDs...),
		Suites: clonePlanSuiteEntries(entries),
	}
}

func clonePlanSuiteEntries(entries []domain.PlanSuiteEntry) []domain.PlanSuiteEntry {
	result := make([]domain.PlanSuiteEntry, len(entries))
	for index, entry := range entries {
		result[index] = entry
		result[index].Cases = append([]domain.CaseRevisionRef(nil), entry.Cases...)
		result[index].Parameters = cloneRawMessages(entry.Parameters)
		result[index].SLA = domain.SLAProfile{Thresholds: cloneThresholds(entry.SLA.Thresholds)}
	}
	return result
}
