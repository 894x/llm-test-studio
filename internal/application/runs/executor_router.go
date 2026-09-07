package runs

import (
	"context"
	"errors"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

type ExecutorRouter struct {
	caseTypes *casetypes.Registry
	drivers   map[domain.CaseType]Executor
}

func NewExecutorRouter(caseTypes *casetypes.Registry, drivers map[domain.CaseType]Executor) (*ExecutorRouter, error) {
	if caseTypes == nil || len(drivers) == 0 {
		return nil, ErrInvalid
	}
	cloned := make(map[domain.CaseType]Executor, len(drivers))
	for caseType, driver := range drivers {
		if caseType == "" || isNil(driver) {
			return nil, ErrInvalid
		}
		cloned[caseType] = driver
	}
	return &ExecutorRouter{caseTypes: caseTypes, drivers: cloned}, nil
}

func MustExecutorRouter(caseTypes *casetypes.Registry, drivers map[domain.CaseType]Executor) *ExecutorRouter {
	router, err := NewExecutorRouter(caseTypes, drivers)
	if err != nil {
		panic(err)
	}
	return router
}

func (router *ExecutorRouter) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	if router == nil || router.caseTypes == nil || len(request.Cases) == 0 {
		return ErrInvalid
	}
	type group struct {
		caseType domain.CaseType
		cases    []domain.TestCase
	}
	groups := make([]group, 0, len(request.Cases))
	byType := make(map[domain.CaseType]int)
	quick := request.Run.Snapshot().QuickTask != nil
	for _, testCase := range request.Cases {
		caseType := testCase.Definition.Type
		driver, found := router.drivers[caseType]
		if !found || isNil(driver) {
			return ErrNotRunnable
		}
		if err := router.caseTypes.Validate(testCase.Protocol, testCase.Definition); err != nil {
			return errors.Join(ErrNotRunnable, err)
		}
		index, found := byType[caseType]
		if !found || quick {
			index = len(groups)
			byType[caseType] = index
			groups = append(groups, group{caseType: caseType})
		}
		groups[index].cases = append(groups[index].cases, testCase)
	}
	started := time.Now()
	for _, group := range groups {
		grouped := request
		grouped.Cases = group.cases
		groupEmit := emit
		if len(groups) > 1 {
			offsetMS := milliseconds(time.Since(started))
			prefix := string(group.caseType)
			if quick {
				prefix = group.cases[0].ID
			}
			groupEmit = func(draft ResultDraft) error {
				if draft.RequestID != "" {
					draft.RequestID = prefix + ":" + draft.RequestID
				}
				// Drivers measure offsets from their own invocation. Sequential
				// groups must share one origin in the Run timeline and metrics.
				draft.Metrics = cloneMetrics(draft.Metrics)
				for _, key := range []string{"scheduled_offset_ms", "started_offset_ms", "finished_offset_ms"} {
					if value, exists := draft.Metrics[key]; exists {
						draft.Metrics[key] = value + offsetMS
					}
				}
				return emit(draft)
			}
		}
		if err := router.drivers[group.caseType].Execute(ctx, grouped, groupEmit); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
