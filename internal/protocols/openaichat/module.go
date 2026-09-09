// Package openaichat executes and observes OpenAI Chat without implicit assertions.
package openaichat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type Module struct{}

func New() *Module { return &Module{} }

func (*Module) Descriptor() runtime.Descriptor {
	return runtime.Descriptor{
		ID: "openai-chat", Label: "OpenAI Chat",
		DefaultSpec: testspec.Spec{
			Inputs: map[string]testspec.Input{}, Assertions: []testspec.Assertion{},
			Request: testspec.Request{Body: json.RawMessage(`{"messages":[{"role":"user","content":"Hello"}]}`)},
		},
		Metrics: []testspec.Metric{
			{ID: "e2e_ms", Label: "E2E", Unit: "ms", Scope: "case", Aggregation: "distribution"},
			{ID: "ttfb_ms", Label: "TTFB", Unit: "ms", Scope: "request", Aggregation: "distribution"},
			{ID: "ttft_ms", Label: "TTFT", Unit: "ms", Scope: "request", Aggregation: "distribution"},
			{ID: "tpot_ms", Label: "TPOT", Unit: "ms/token", Scope: "request", Aggregation: "distribution"},
		},
		Settings: map[string]testspec.Input{},
	}
}

func (*Module) Validate(spec testspec.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.Operation != "" && spec.Operation != "models.list" {
		return errors.New("unsupported openai-chat operation")
	}
	if spec.Operation == "models.list" && spec.Workflow != nil {
		return errors.New("models.list does not accept a workflow")
	}
	if spec.Workflow != nil && spec.Workflow.Mode != "sequence" {
		return errors.New("openai-chat supports only explicit sequence workflows")
	}
	return nil
}

func (*Module) Execute(ctx context.Context, execution runtime.Execution) testspec.Observation {
	started := time.Now()
	observation := runtime.NewObservation("openai-chat")
	steps := []testspec.Step{{ID: "initial", Request: execution.Spec.Request}}
	if execution.Spec.Workflow != nil {
		steps = append(steps, execution.Spec.Workflow.Steps...)
	}
	responses := map[string]json.RawMessage{}
	for _, step := range steps {
		observation.HTTPStatus = nil
		observation.Response = nil
		observation.Text = nil
		observation.Usage = nil
		observation.StreamCompleted = nil
		random := execution.Random
		random.MemberID += "/" + step.ID
		body, err := testspec.Render(step.Request, execution.Inputs, random, responses)
		if err != nil {
			runtime.AddIssue(&observation, step.ID, "request_generation_failed")
			break
		}
		request := runtime.Request{Method: http.MethodPost, Path: "/v1/chat/completions", Body: body}
		if execution.Spec.Operation == "models.list" {
			request = runtime.Request{Method: http.MethodGet, Path: "/v1/models"}
		}
		response, err := execution.Transport.Send(ctx, request)
		if err != nil {
			runtime.AddIssue(&observation, step.ID, runtime.ErrorCode(ctx))
			observation.Exchanges = append(observation.Exchanges, runtime.Exchange(step.ID, request, response))
			break
		}
		status := response.HTTP.StatusCode
		observation.HTTPStatus = &status
		observation.Metrics["ttfb_ms"] = response.TTFBMS
		contentType := response.HTTP.Header.Get("Content-Type")
		events := []json.RawMessage{}
		if strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
			events = parseStream(response, &observation)
		} else {
			parseJSON(response, &observation)
		}
		exchange := runtime.Exchange(step.ID, request, response)
		exchange.Response = append(json.RawMessage{}, observation.Response...)
		exchange.Events = events
		exchange.StreamCompleted = observation.StreamCompleted
		exchange.Metrics = make(map[string]float64, len(observation.Metrics))
		for key, value := range observation.Metrics { exchange.Metrics[key] = value }
		observation.Exchanges = append(observation.Exchanges, exchange)
		responses[step.ID] = append(json.RawMessage{}, observation.Response...)
	}
	observation.Metrics["e2e_ms"] = runtime.Milliseconds(started)
	return observation
}

func parseJSON(response runtime.Response, observation *testspec.Observation) {
	body, err := runtime.Read(response)
	if err != nil {
		runtime.AddIssue(observation, "response", err.Error())
		return
	}
	if !json.Valid(body) {
		runtime.AddIssue(observation, "response", "invalid_json")
		return
	}
	observation.Response = append(json.RawMessage{}, body...)
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		runtime.AddIssue(observation, "response", "response_not_object")
		return
	}
	observation.Usage = append(json.RawMessage{}, root["usage"]...)
	var choices []struct {
		Message struct {
			Content *string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(root["choices"], &choices) == nil && len(choices) > 0 {
		observation.Text = choices[0].Message.Content
	}
	observeTokens(observation)
	observeContentFacts(observation)
}

func observeTokens(observation *testspec.Observation) {
	var usage map[string]json.RawMessage
	if json.Unmarshal(observation.Usage, &usage) != nil {
		return
	}
	for _, name := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		var count float64
		if json.Unmarshal(usage[name], &count) == nil && count >= 0 {
			observation.Metrics[name] = count
		}
	}
	var details map[string]float64
	if json.Unmarshal(usage["prompt_tokens_details"], &details) == nil {
		if count, exists := details["cached_tokens"]; exists && count >= 0 {
			observation.Metrics["cached_tokens"] = count
		}
	}
	var root any
	if json.Unmarshal(observation.Usage, &root) == nil {
		for _, field := range []string{"image_tokens", "reasoning_tokens", "cached_tokens", "cache_read_input_tokens"} {
			if recursivePositive(root, field) { observation.Metrics[field+"_observed"] = 1 } else { observation.Metrics[field+"_observed"] = 0 }
		}
	}
}

func observeContentFacts(observation *testspec.Observation) {
	var root struct { Choices []struct { Message map[string]any `json:"message"` } `json:"choices"` }
	if json.Unmarshal(observation.Response, &root) != nil || len(root.Choices) == 0 { return }
	visible := observation.Metrics["reasoning_tokens_observed"] > 0
	for _, field := range []string{"reasoning_content", "reasoning"} {
		value := root.Choices[0].Message[field]
		switch current := value.(type) { case string: visible = visible || strings.TrimSpace(current) != ""; case []any: visible = visible || len(current)>0; case map[string]any: visible = visible || len(current)>0 }
	}
	if visible { observation.Metrics["reasoning_visible"] = 1 } else { observation.Metrics["reasoning_visible"] = 0 }
}

func recursivePositive(value any, target string) bool {
	switch current := value.(type) {
	case map[string]any:
		if number, ok := current[target].(float64); ok && number > 0 { return true }
		for _, nested := range current { if recursivePositive(nested, target) { return true } }
	case []any:
		for _, nested := range current { if recursivePositive(nested, target) { return true } }
	}
	return false
}
