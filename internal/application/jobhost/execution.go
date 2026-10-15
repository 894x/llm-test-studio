package jobhost

import (
	"context"
	"io"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type Clock interface{ Now() time.Time }

// Guard reserves verified assets, disk space and other admission resources.
// The reservation remains owned by the Host until all execution has stopped.
type Guard interface {
	Reserve(context.Context, jobcontract.JobSpec) (io.Closer, error)
}

type Execution struct {
	Spec        jobcontract.JobSpec
	Secrets     map[string]*credentials.Lease
	StopSending <-chan struct{}
	OnProgress  func(quicktest.PerformanceProgress)
}

type Outcome struct {
	Status  domain.RunStatus
	Failure *domain.RunFailure
}

func (outcome Outcome) Validate() error {
	if !jobcontract.Terminal(outcome.Status) {
		return ErrInvalid
	}
	if outcome.Status == domain.RunFailed {
		if outcome.Failure == nil || outcome.Failure.Validate() != nil {
			return ErrInvalid
		}
		return nil
	}
	if outcome.Failure != nil {
		return ErrInvalid
	}
	return nil
}

// Execute must return only after requests, callbacks and Core workers stop.
// Settle seals remaining child facts and reports without sending requests.
// A non-nil error means execution facts could not be safely persisted.
type Executor interface {
	Execute(context.Context, Execution) (Outcome, error)
	Settle(context.Context, jobcontract.JobSpec, Outcome) error
}

func failed(code domain.ErrorCode) Outcome {
	return Outcome{Status: domain.RunFailed, Failure: &domain.RunFailure{Phase: "runner", ErrorCode: code}}
}

func reasonOutcome(cause error) Outcome {
	switch cause {
	case ErrCancelled, ErrLocalShutdown:
		return Outcome{Status: domain.RunCancelled}
	case ErrExecutionLimit:
		return failed("job_execution_timeout")
	case ErrUnavailable:
		return failed("job_storage_unavailable")
	default:
		return failed("runner_interrupted")
	}
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return reflected.IsNil()
	default:
		return false
	}
}
