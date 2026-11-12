package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/894x/llm-test-studio/internal/testspec"
)

// Sequence owns request and response references for one Case execution only.
type Sequence struct {
	execution Execution
	requests  map[string]testspec.RenderedRequest
	responses map[string]json.RawMessage
}

func NewSequence(execution Execution) *Sequence {
	return &Sequence{
		execution: execution,
		requests:  map[string]testspec.RenderedRequest{},
		responses: map[string]json.RawMessage{},
	}
}

func (sequence *Sequence) Prepare(
	ctx context.Context,
	step testspec.Step,
) (testspec.RenderedRequest, float64, error) {
	if err := ctx.Err(); err != nil {
		return testspec.RenderedRequest{}, 0, err
	}
	var waitedMS float64
	if step.DelayMS > 0 {
		started := time.Now()
		timer := time.NewTimer(time.Duration(step.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return testspec.RenderedRequest{}, Milliseconds(started), ctx.Err()
		case <-timer.C:
			waitedMS = Milliseconds(started)
		}
	}
	if err := ctx.Err(); err != nil {
		return testspec.RenderedRequest{}, waitedMS, err
	}
	var body testspec.RenderedRequest
	if step.ReuseRequestFrom != "" {
		previous, exists := sequence.requests[step.ReuseRequestFrom]
		if !exists {
			return body, waitedMS, errors.New("preceding request unavailable")
		}
		body = previous.Clone()
	} else {
		execution := sequence.execution
		random := execution.Random
		random.MemberID += "/" + step.ID
		var err error
		body, err = testspec.Render(testspec.RenderInput{
			Request: step.Request, Declarations: execution.Spec.Inputs,
			Values: execution.Inputs, Random: random, Responses: sequence.responses,
		})
		if err != nil {
			return body, waitedMS, err
		}
	}
	sequence.requests[step.ID] = body.Clone()
	return body, waitedMS, nil
}

func (sequence *Sequence) RecordResponse(step string, response json.RawMessage) {
	sequence.responses[step] = append(json.RawMessage{}, response...)
}

// RequestContext keeps waits out of the HTTP budget for opted-in sequences.
func (sequence *Sequence) RequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if !sequence.execution.Spec.UsesSequenceControls() {
		return context.WithCancel(ctx)
	}
	timeout := sequence.execution.Settings.TimeoutMS
	if timeout == 0 {
		timeout = 600_000
	}
	return context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
}
