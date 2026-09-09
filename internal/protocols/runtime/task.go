package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// TaskProtocol describes a supported external protocol's lifecycle. It contains
// no expected test outcomes; terminal failure is as observable as terminal success.
type TaskProtocol struct {
	ID               string
	Label            string
	SubmitPath       string
	PollPath         string
	IDPointer        string
	StatusPointer    string
	UsagePointer     string
	ArtifactPointers map[string]string
	TerminalStatuses []string
	DefaultBody      json.RawMessage
	Headers          map[string]string
}

func (protocol TaskProtocol) Descriptor() Descriptor {
	return Descriptor{
		ID: protocol.ID, Label: protocol.Label,
		DefaultSpec: testspec.Spec{
			Inputs: map[string]testspec.Input{}, Assertions: []testspec.Assertion{},
			Request: testspec.Request{Body: append(json.RawMessage{}, protocol.DefaultBody...)},
		},
		Metrics: []testspec.Metric{
			{ID: "e2e_ms", Label: "Task E2E", Unit: "ms", Scope: "case", Aggregation: "distribution"},
			{ID: "submission_ms", Label: "Submission", Unit: "ms", Scope: "request", Aggregation: "distribution"},
			{ID: "poll_count", Label: "Polls", Unit: "count", Scope: "case", Aggregation: "sum"},
		},
		Settings: map[string]testspec.Input{
			"poll_interval_ms": {Type: "integer", Unit: "ms", Default: json.RawMessage(`10000`)},
			"task_timeout_ms":  {Type: "integer", Unit: "ms", Default: json.RawMessage(`600000`)},
		},
	}
}

func (protocol TaskProtocol) Validate(spec testspec.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.Operation != "" {
		return errors.New("task protocol has no named request operations")
	}
	if spec.Workflow != nil && spec.Workflow.Mode == "sequence" {
		return errors.New("task protocol does not support chat sequence steps")
	}
	return nil
}

func (protocol TaskProtocol) Execute(ctx context.Context, execution Execution) testspec.Observation {
	started := time.Now()
	observation := NewObservation(protocol.ID)
	body, err := testspec.Render(execution.Spec.Request, execution.Inputs, execution.Random, map[string]json.RawMessage{})
	if err != nil {
		AddIssue(&observation, "prepare", "request_generation_failed")
		return observation
	}
	request := Request{Method: http.MethodPost, Path: protocol.SubmitPath, Body: body, Headers: protocol.Headers}
	root, completed := taskRequest(ctx, execution.Transport, request, "submit", &observation)
	observation.Metrics["submission_ms"] = Milliseconds(started)
	observation.Metrics["poll_count"] = 0
	if !completed {
		observation.Metrics["e2e_ms"] = Milliseconds(started)
		return observation
	}
	if taskID, exists := jsonpointer.Lookup(root, protocol.IDPointer); exists {
		if id, ok := taskID.(string); ok && id != "" {
			observation.Task = &testspec.Task{ID: id}
		}
	}
	protocol.observe(root, &observation)
	wait := execution.Spec.Workflow != nil && execution.Spec.Workflow.Mode == "wait"
	if wait && observation.Task != nil && !observation.Task.Terminal {
		protocol.poll(ctx, execution, &observation)
	}
	observation.Metrics["e2e_ms"] = Milliseconds(started)
	return observation
}

func (protocol TaskProtocol) poll(ctx context.Context, execution Execution, observation *testspec.Observation) {
	interval := execution.Settings.PollIntervalMS
	if interval == 0 {
		interval = 10_000
	}
	timeout := execution.Settings.TaskTimeoutMS
	if timeout == 0 {
		timeout = 600_000
	}
	pollContext, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	request := Request{Method: http.MethodGet, Path: protocol.PollPath + url.PathEscape(observation.Task.ID)}
	for len(observation.Exchanges) < MaxExchanges {
		if pollContext.Err() != nil {
			AddIssue(observation, "poll", ErrorCode(pollContext))
			return
		}
		observation.Metrics["poll_count"]++
		root, completed := taskRequest(pollContext, execution.Transport, request, "poll", observation)
		if !completed {
			return
		}
		protocol.observe(root, observation)
		if observation.Task.Terminal {
			return
		}
		// A rejected poll is a completed response, not a transport retry candidate.
		if observation.HTTPStatus != nil && (*observation.HTTPStatus < 200 || *observation.HTTPStatus >= 300) {
			return
		}
		timer := time.NewTimer(time.Duration(interval) * time.Millisecond)
		select {
		case <-pollContext.Done():
			timer.Stop()
			AddIssue(observation, "poll", ErrorCode(pollContext))
			return
		case <-timer.C:
		}
	}
	AddIssue(observation, "poll", "exchange_limit_exceeded")
}

func taskRequest(
	ctx context.Context,
	transport Transport,
	request Request,
	step string,
	observation *testspec.Observation,
) (map[string]any, bool) {
	response, err := transport.Send(ctx, request)
	if err != nil {
		observation.Exchanges = append(observation.Exchanges, Exchange(step, request, response))
		AddIssue(observation, step, ErrorCode(ctx))
		return nil, false
	}
	status := response.HTTP.StatusCode
	observation.HTTPStatus = &status
	observation.Response = nil
	body, readErr := Read(response)
	exchange := Exchange(step, request, response)
	defer func() { observation.Exchanges = append(observation.Exchanges, exchange) }()
	if readErr != nil {
		AddIssue(observation, step, readErr.Error())
		return nil, false
	}
	if !json.Valid(body) {
		AddIssue(observation, step, "invalid_json")
		return nil, false
	}
	observation.Response = append(json.RawMessage{}, body...)
	exchange.Response = append(json.RawMessage{}, body...)
	var root map[string]any
	if json.Unmarshal(body, &root) != nil || root == nil {
		AddIssue(observation, step, "response_not_object")
		return nil, false
	}
	return root, true
}

func (protocol TaskProtocol) observe(root map[string]any, observation *testspec.Observation) {
	if observation.Task != nil {
		if status, exists := jsonpointer.Lookup(root, protocol.StatusPointer); exists {
			if value, ok := status.(string); ok {
				observation.Task.Status = value
				for _, terminal := range protocol.TerminalStatuses {
					if strings.EqualFold(value, terminal) {
						observation.Task.Terminal = true
						break
					}
				}
			}
		}
	}
	if usage, exists := jsonpointer.Lookup(root, protocol.UsagePointer); exists {
		observation.Usage, _ = json.Marshal(usage)
	}
	for kind, pointer := range protocol.ArtifactPointers {
		value, exists := jsonpointer.Lookup(root, pointer)
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		parsed, err := url.Parse(text)
		if err != nil || parsed.User != nil || parsed.Hostname() == "" {
			continue
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		observation.Artifacts = append(observation.Artifacts, testspec.Artifact{Kind: kind, URL: text})
	}
}
