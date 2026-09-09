package runs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// resolveEntries reads each current reference once and freezes those same values.
// No provider request or credential access occurs during resolution.
func (service *Service) resolveEntries(ctx context.Context, plan domain.Plan) ([]domain.RunEntrySnapshot, error) {
	cases := map[string]domain.TestCase{}
	suites := map[string]domain.Suite{}
	getCase := func(id string) (domain.TestCase, error) {
		if value, found := cases[id]; found {
			return value, nil
		}
		value, err := service.repository.GetTestCase(ctx, id)
		if err == nil {
			cases[id] = value
		}
		return value, err
	}
	entries := make([]domain.RunEntrySnapshot, 0, len(plan.Entries))
	for _, item := range plan.Entries {
		entry := domain.RunEntrySnapshot{
			EntryID: item.EntryID, WarmupCount: item.WarmupCount, Settings: item.Settings, TargetKind: item.TargetKind, TargetID: item.TargetID,
			Parameters: map[string]json.RawMessage{}, CaseInputs: map[string]map[string]json.RawMessage{},
			Cases: []domain.CaseRevisionRef{}, CaseDefinitions: []domain.TestCase{}, Load: item.Load, SLA: item.SLA,
		}
		definitions := []domain.TestCase{}
		supplied := map[string]map[string]json.RawMessage{}
		switch item.TargetKind {
		case domain.PlanTargetCase:
			testCase, err := getCase(item.TargetID)
			if err != nil {
				return nil, fmt.Errorf("resolve entry %s Case: %w", item.EntryID, err)
			}
			entry.Name, entry.Key = testCase.Name, testCase.Key
			definitions = append(definitions, testCase)
			supplied[testCase.ID] = item.Parameters
		case domain.PlanTargetSuite:
			suite, found := suites[item.TargetID]
			if !found {
				var err error
				suite, err = service.repository.GetSuite(ctx, item.TargetID)
				if err != nil {
					return nil, fmt.Errorf("resolve entry %s Suite: %w", item.EntryID, err)
				}
				suites[item.TargetID] = suite
			}
			if suite.Protocol != plan.Protocol {
				return nil, fmt.Errorf("%w: entry %s has a different protocol", ErrNotRunnable, item.EntryID)
			}
			entry.Suite, entry.Name, entry.Key = &suite, suite.Name, suite.Key
			for _, ref := range suite.Cases {
				testCase, err := getCase(ref.CaseID)
				if err != nil {
					return nil, fmt.Errorf("resolve entry %s member: %w", item.EntryID, err)
				}
				definitions = append(definitions, testCase)
			}
			if err := suite.ValidateCases(definitions); err != nil {
				return nil, fmt.Errorf("%w: entry %s: %v", ErrNotRunnable, item.EntryID, err)
			}
			var err error
			entry.Parameters, supplied, err = suite.ResolveInputs(item.Parameters)
			if err != nil {
				return nil, fmt.Errorf("entry %s inputs: %w", item.EntryID, err)
			}
		default:
			return nil, ErrNotRunnable
		}
		for _, testCase := range definitions {
			if !testCase.Enabled || testCase.ExecutionMode != domain.CaseExecutionAutomatic || testCase.Protocol != plan.Protocol {
				return nil, fmt.Errorf("%w: Case %s is disabled, nonautomatic or belongs to another protocol", ErrNotRunnable, testCase.Key)
			}
			if err := service.caseTypes.Validate(testCase.Protocol, testCase.Definition); err != nil {
				return nil, fmt.Errorf("Case %s: %w", testCase.Key, err)
			}
			spec, err := testspec.Decode(testCase.Definition.Spec)
			if err != nil {
				return nil, err
			}
			inputs, err := testspec.ValidateInputs(spec.Inputs, supplied[testCase.ID])
			if err != nil {
				return nil, fmt.Errorf("entry %s Case %s inputs: %w", item.EntryID, testCase.Key, err)
			}
			entry.CaseInputs[testCase.ID] = inputs
			if item.TargetKind == domain.PlanTargetCase {
				entry.Parameters = inputs
			}
			entry.Cases = append(entry.Cases, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision})
			entry.CaseDefinitions = append(entry.CaseDefinitions, testCase)
		}
		if err := entry.Validate(plan.Protocol); err != nil {
			return nil, fmt.Errorf("%w: entry %s: %v", ErrNotRunnable, item.EntryID, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
