package runs

import (
	"context"
	"errors"

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
	groups := make(map[domain.CaseType][]domain.TestCase)
	order := make([]domain.CaseType, 0, len(request.Cases))
	for _, testCase := range request.Cases {
		caseType := testCase.Definition.Type
		driver, found := router.drivers[caseType]
		if !found || isNil(driver) {
			return ErrNotRunnable
		}
		if err := router.caseTypes.Validate(testCase.Protocol, testCase.Definition); err != nil {
			return errors.Join(ErrNotRunnable, err)
		}
		if _, found := groups[caseType]; !found {
			order = append(order, caseType)
		}
		groups[caseType] = append(groups[caseType], testCase)
	}
	for _, caseType := range order {
		grouped := request
		grouped.Cases = groups[caseType]
		if err := router.drivers[caseType].Execute(ctx, grouped, emit); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
