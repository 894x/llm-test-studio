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
	testCases, err := service.repository.ListTestCases(ctx)
	if err != nil {
		return nil, fmt.Errorf("load Case catalog: %w", err)
	}
	cases, err := indexTestCases(testCases)
	if err != nil {
		return nil, err
	}

	suites := map[string]domain.Suite{}
	if planContainsSuites(plan) {
		catalogSuites, listErr := service.repository.ListSuites(ctx)
		if listErr != nil {
			return nil, fmt.Errorf("load Suite catalog: %w", listErr)
		}
		suites, err = indexSuites(catalogSuites)
		if err != nil {
			return nil, err
		}
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
			testCase, found := cases[item.TargetID]
			if !found {
				return nil, fmt.Errorf(
					"%w: entry %s references unavailable Case %s",
					ErrNotRunnable,
					item.EntryID,
					item.TargetID,
				)
			}
			entry.Name, entry.Key = testCase.Name, testCase.Key
			definitions = append(definitions, testCase)
			supplied[testCase.ID] = item.Parameters
		case domain.PlanTargetSuite:
			suite, found := suites[item.TargetID]
			if !found {
				return nil, fmt.Errorf(
					"%w: entry %s references unavailable Suite %s",
					ErrNotRunnable,
					item.EntryID,
					item.TargetID,
				)
			}
			if suite.Protocol != plan.Protocol {
				return nil, fmt.Errorf("%w: entry %s has a different protocol", ErrNotRunnable, item.EntryID)
			}
			entry.Suite, entry.Name, entry.Key = &suite, suite.Name, suite.Key
			for _, ref := range suite.Cases {
				testCase, exists := cases[ref.CaseID]
				if !exists {
					return nil, fmt.Errorf(
						"%w: entry %s references unavailable Case %s",
						ErrNotRunnable,
						item.EntryID,
						ref.CaseID,
					)
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

func indexTestCases(testCases []domain.TestCase) (map[string]domain.TestCase, error) {
	indexed := make(map[string]domain.TestCase, len(testCases))
	for _, testCase := range testCases {
		if !domain.IsUUID(testCase.ID) {
			return nil, fmt.Errorf("%w: Case catalog contains an invalid identifier", ErrNotRunnable)
		}
		if _, duplicate := indexed[testCase.ID]; duplicate {
			return nil, fmt.Errorf("%w: Case catalog contains duplicate identifier %s", ErrNotRunnable, testCase.ID)
		}
		indexed[testCase.ID] = testCase
	}
	return indexed, nil
}

func indexSuites(suites []domain.Suite) (map[string]domain.Suite, error) {
	indexed := make(map[string]domain.Suite, len(suites))
	for _, suite := range suites {
		if !domain.IsUUID(suite.ID) {
			return nil, fmt.Errorf("%w: Suite catalog contains an invalid identifier", ErrNotRunnable)
		}
		if _, duplicate := indexed[suite.ID]; duplicate {
			return nil, fmt.Errorf("%w: Suite catalog contains duplicate identifier %s", ErrNotRunnable, suite.ID)
		}
		indexed[suite.ID] = suite
	}
	return indexed, nil
}

func planContainsSuites(plan domain.Plan) bool {
	for _, entry := range plan.Entries {
		if entry.TargetKind == domain.PlanTargetSuite {
			return true
		}
	}
	return false
}
