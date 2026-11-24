package runs

import (
	"context"

	"github.com/894x/llm-test-studio/internal/domain"
)

type ProtocolComparisonPreview struct {
	Targets []ProtocolComparisonTarget `json:"targets"`
}

type ProtocolComparisonTarget struct {
	Protocol    domain.Protocol `json:"protocol"`
	Cases       int             `json:"cases"`
	Requests    *int            `json:"requests"`
	Available   bool            `json:"available"`
	MissingItem string          `json:"missing_item,omitempty"`
}

func (service *Preparer) PreviewProtocolComparison(
	ctx context.Context,
	planID, suiteID string,
) (ProtocolComparisonPreview, error) {
	isPlan := planID != "" && suiteID == "" && domain.IsUUID(planID)
	isSuite := suiteID != "" && planID == "" && domain.IsUUID(suiteID)
	if service == nil || ctx == nil || (!isPlan && !isSuite) {
		return ProtocolComparisonPreview{}, ErrInvalid
	}
	allSuites, err := service.repository.ListSuites(ctx)
	if err != nil {
		return ProtocolComparisonPreview{}, err
	}
	suites, err := indexSuites(allSuites)
	if err != nil {
		return ProtocolComparisonPreview{}, err
	}

	var plan domain.Plan
	if isPlan {
		plan, err = service.repository.GetPlan(ctx, planID)
		if err != nil {
			return ProtocolComparisonPreview{}, err
		}
	} else {
		suite, exists := suites[suiteID]
		if !exists {
			return ProtocolComparisonPreview{}, ErrNotRunnable
		}
		plan = domain.Plan{
			Name: suite.Name, Protocol: suite.Protocol,
			Entries: []domain.PlanEntry{{
				TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
				Load: domain.LoadProfile{Mode: domain.LoadSingle},
			}},
		}
	}
	if !domain.IsComparisonProtocol(plan.Protocol) {
		return ProtocolComparisonPreview{}, ErrNotRunnable
	}

	allCases, err := service.repository.ListTestCases(ctx)
	if err != nil {
		return ProtocolComparisonPreview{}, err
	}
	cases, err := indexTestCases(allCases)
	if err != nil {
		return ProtocolComparisonPreview{}, err
	}

	preview := ProtocolComparisonPreview{Targets: make([]ProtocolComparisonTarget, 0, 3)}
	for _, protocol := range comparisonProtocols() {
		target := ProtocolComparisonTarget{Protocol: protocol, Available: true}
		requests, countKnown := 0, true
		for _, entry := range plan.Entries {
			members, name, ok := protocolPreviewMembers(entry, protocol, suites, cases)
			if !ok {
				target.Available, target.MissingItem = false, name
				break
			}
			if entry.TargetKind == domain.PlanTargetPerformance || entry.Load.Mode != domain.LoadSingle || entry.WarmupCount != 0 {
				countKnown = false
			}
			for _, testCase := range members {
				definition, err := testCase.SpecFor(protocol)
				if err != nil {
					target.Available, target.MissingItem = false, testCase.Name
					break
				}
				target.Cases++
				requests++
				if definition.Workflow != nil {
					requests += len(definition.Workflow.Steps)
				}
			}
			if !target.Available {
				break
			}
		}
		if countKnown && target.Available {
			target.Requests = &requests
		}
		preview.Targets = append(preview.Targets, target)
	}
	return preview, nil
}

func protocolPreviewMembers(
	entry domain.PlanEntry,
	protocol domain.Protocol,
	suites map[string]domain.Suite,
	cases map[string]domain.TestCase,
) ([]domain.TestCase, string, bool) {
	switch entry.TargetKind {
	case domain.PlanTargetPerformance:
		return nil, entry.Name, true
	case domain.PlanTargetCase:
		testCase, exists := cases[entry.TargetID]
		valid := exists && testCase.Enabled && testCase.ExecutionMode == domain.CaseExecutionAutomatic
		return []domain.TestCase{testCase}, entry.Name, valid && testCase.SupportsProtocol(protocol)
	case domain.PlanTargetSuite:
		source, exists := suites[entry.TargetID]
		if !exists {
			return nil, entry.Name, false
		}
		target, err := protocolSuite(source, protocol, suites)
		if err != nil || target.HasFileInputs() {
			return nil, source.Name, false
		}
		members := make([]domain.TestCase, 0, len(target.Cases))
		for _, ref := range target.Cases {
			testCase, exists := cases[ref.CaseID]
			if !exists || !testCase.Enabled || testCase.ExecutionMode != domain.CaseExecutionAutomatic {
				return nil, source.Name, false
			}
			members = append(members, testCase)
		}
		return members, source.Name, target.ValidateCases(members) == nil
	}
	return nil, entry.Name, false
}
