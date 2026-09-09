// Package runtime contains the small shared boundary implemented by protocol modules.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/894x/llm-test-studio/internal/testspec"
)

const MaxResponseBytes int64 = 32 << 20
const MaxExchanges = 1024

type Response struct {
	HTTP        *http.Response
	Started     time.Time
	TTFBMS      float64
	RequestBody json.RawMessage
}

type Transport interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct {
	Method  string
	Path    string
	Body    json.RawMessage
	Headers map[string]string
}

type Execution struct {
	Spec      testspec.Spec
	Inputs    map[string]json.RawMessage
	Random    testspec.RandomContext
	Settings  testspec.RunSettings
	Transport Transport
}

type Descriptor struct {
	ID          string                    `json:"id"`
	Label       string                    `json:"label"`
	DefaultSpec testspec.Spec             `json:"default_spec"`
	Metrics     []testspec.Metric         `json:"metrics"`
	Settings    map[string]testspec.Input `json:"settings"`
}

type Module interface {
	Descriptor() Descriptor
	Validate(testspec.Spec) error
	Execute(context.Context, Execution) testspec.Observation
}

func NewObservation(protocol string) testspec.Observation {
	return testspec.Observation{
		Protocol: protocol, Metrics: map[string]float64{}, Artifacts: []testspec.Artifact{},
		Exchanges: []testspec.Exchange{}, Issues: []testspec.Issue{},
	}
}

func AddIssue(observation *testspec.Observation, stage string, code string) {
	observation.Issues = append(observation.Issues, testspec.Issue{Stage: stage, Code: code})
}

func Read(response Response) ([]byte, error) {
	if response.HTTP == nil || response.HTTP.Body == nil {
		return nil, errors.New("response_unavailable")
	}
	defer response.HTTP.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.HTTP.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, errors.New("response_read_failed")
	}
	if int64(len(body)) > MaxResponseBytes {
		return nil, errors.New("response_limit_exceeded")
	}
	return body, nil
}

func Exchange(step string, request Request, response Response) testspec.Exchange {
	entry := testspec.Exchange{
		Step: step, Method: request.Method, Path: request.Path,
		RequestBody: append(json.RawMessage{}, request.Body...),
	}
	if len(response.RequestBody) > 0 {
		entry.RequestBody = append(json.RawMessage{}, response.RequestBody...)
	}
	if response.HTTP != nil {
		status := response.HTTP.StatusCode
		entry.HTTPStatus = &status
	}
	if !response.Started.IsZero() {
		entry.ElapsedMS = float64(time.Since(response.Started).Microseconds()) / 1000
	}
	return entry
}

func Milliseconds(started time.Time) float64 {
	return float64(time.Since(started).Microseconds()) / 1000
}

func ErrorCode(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if ctx.Err() != nil {
		return "cancelled"
	}
	return "transport_error"
}
