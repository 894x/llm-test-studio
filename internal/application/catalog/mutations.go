package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
	suite := domain.Suite{EntityMeta: meta, Name: command.Name, Cases: cloneCaseRefs(command.Cases)}
	if err := suite.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
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
	suite := domain.Suite{EntityMeta: meta, Name: command.Name, Cases: cloneCaseRefs(command.Cases)}
	if err := suite.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
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
	plan := planFromCreate(meta, command)
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
	if err := current.Validate(); err != nil || current.ID != command.ID {
		return MutationResult{}, ErrCorrupt
	}
	meta, err := service.nextMeta(ctx, current.EntityMeta, command.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	plan := planFromUpdate(meta, command)
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

func (service *Service) validatePlanTarget(ctx context.Context, plan domain.Plan) error {
	targetProtocol := domain.Protocol("")
	for _, ref := range plan.Cases {
		testCase, err := service.repository.GetTestCase(ctx, ref.CaseID)
		if err != nil {
			return service.portError(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := testCase.Validate(); err != nil || testCase.ID != ref.CaseID {
			return ErrCorrupt
		}
		if ref.Revision > testCase.Revision {
			return ErrInvalid
		}
		if targetProtocol == "" {
			targetProtocol = testCase.Protocol
		} else if testCase.Protocol != targetProtocol {
			return ErrInvalid
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
			return ErrInvalid
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
			return ErrInvalid
		}
	}
	return nil
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
		return ErrNotFound
	}
	if errors.Is(err, ErrConflict) || matches(err, service.repositoryErrors.Conflict) {
		return ErrConflict
	}
	if errors.Is(err, ErrCorrupt) || matches(err, service.repositoryErrors.Corrupt) {
		return ErrCorrupt
	}
	return ErrUnavailable
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
		Severity: command.Severity, ExecutionMode: command.ExecutionMode,
		Definition: definitionFromCreate(command),
	}
}

func testCaseFromUpdate(meta domain.EntityMeta, command UpdateTestCaseCommand) domain.TestCase {
	return domain.TestCase{
		EntityMeta: meta, Key: command.Key, Name: command.Name, Dimension: command.Dimension,
		Protocol: command.Protocol, Enabled: command.Enabled, Default: command.Default,
		Severity: command.Severity, ExecutionMode: command.ExecutionMode,
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

func planFromCreate(meta domain.EntityMeta, command CreatePlanCommand) domain.Plan {
	return domain.Plan{
		EntityMeta: meta, Name: command.Name, ModelIDs: append([]string(nil), command.ModelIDs...), ChannelIDs: append([]string(nil), command.ChannelIDs...),
		SuiteID: command.SuiteID, SuiteRevision: command.SuiteRevision, Cases: cloneCaseRefs(command.Cases),
		Load: domain.LoadProfile{Mode: command.LoadMode, Concurrency: command.Concurrency, RequestCount: command.RequestCount, RatePerSecond: command.RatePerSecond, DurationMS: command.DurationMS, RequestTimeoutMS: command.RequestTimeoutMS},
		SLA:  domain.SLAProfile{Thresholds: cloneThresholds(command.SLAThresholds)},
	}
}

func planFromUpdate(meta domain.EntityMeta, command UpdatePlanCommand) domain.Plan {
	return domain.Plan{
		EntityMeta: meta, Name: command.Name, ModelIDs: append([]string(nil), command.ModelIDs...), ChannelIDs: append([]string(nil), command.ChannelIDs...),
		SuiteID: command.SuiteID, SuiteRevision: command.SuiteRevision, Cases: cloneCaseRefs(command.Cases),
		Load: domain.LoadProfile{Mode: command.LoadMode, Concurrency: command.Concurrency, RequestCount: command.RequestCount, RatePerSecond: command.RatePerSecond, DurationMS: command.DurationMS, RequestTimeoutMS: command.RequestTimeoutMS},
		SLA:  domain.SLAProfile{Thresholds: cloneThresholds(command.SLAThresholds)},
	}
}
