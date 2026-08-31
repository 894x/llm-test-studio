package runs

import "context"

// ExecutorRouter keeps imported compatibility semantics and native load-test
// semantics explicit. A single run may not mix evaluator families because that
// would make request-count and pass/fail aggregation ambiguous.
type ExecutorRouter struct {
	legacy Executor
	load   Executor
}

func NewExecutorRouter(legacy Executor, load Executor) *ExecutorRouter {
	return &ExecutorRouter{legacy: legacy, load: load}
}

func (router *ExecutorRouter) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	if router == nil || isNil(router.legacy) || isNil(router.load) || len(request.Cases) == 0 {
		return ErrInvalid
	}
	legacyCount := 0
	for _, testCase := range request.Cases {
		if _, err := legacyCaseDefinition(testCase); err == nil {
			legacyCount++
		}
	}
	switch legacyCount {
	case 0:
		return router.load.Execute(ctx, request, emit)
	case len(request.Cases):
		return router.legacy.Execute(ctx, request, emit)
	default:
		return ErrNotRunnable
	}
}
