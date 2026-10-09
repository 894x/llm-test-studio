// Package textapi shares bounded HTTP/SSE execution between text protocols.
// Each protocol owns its native response shape and streaming state machine.
package textapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type Decoder interface {
	JSON(map[string]any, *testspec.Observation)
	Event(map[string]any, *testspec.Observation) (semantic, text, terminal bool)
	Finish(*testspec.Observation)
}

type Module struct {
	ID, Label, Path string
	DefaultBody     json.RawMessage
	Headers         map[string]string
	NewDecoder      func() Decoder
}

func (module *Module) Descriptor() runtime.Descriptor {
	return runtime.Descriptor{
		ID: module.ID, Label: module.Label,
		DefaultSpec: testspec.Spec{
			Inputs: map[string]testspec.Input{}, Assertions: []testspec.Assertion{},
			Request: testspec.Request{Body: append(json.RawMessage{}, module.DefaultBody...)},
		},
		Metrics: []testspec.Metric{
			{ID: "e2e_ms", Label: "E2E", Unit: "ms", Scope: "case", Aggregation: "distribution"},
			{ID: "ttfb_ms", Label: "TTFB", Unit: "ms", Scope: "request", Aggregation: "distribution"},
			{ID: "ttft_ms", Label: "TTFT", Unit: "ms", Scope: "request", Aggregation: "distribution"},
			{ID: "ttft_text_ms", Label: "Text TTFT", Unit: "ms", Scope: "request", Aggregation: "distribution"},
			{ID: "tpot_ms", Label: "TPOT", Unit: "ms/token", Scope: "request", Aggregation: "distribution"},
		},
		Settings: map[string]testspec.Input{},
	}
}

func (module *Module) Validate(spec testspec.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.Operation != "" {
		return errors.New("text protocol supports only the create operation")
	}
	if spec.Workflow != nil && spec.Workflow.Mode != "sequence" {
		return errors.New("text protocol supports only explicit sequence workflows")
	}
	return nil
}

func (module *Module) Execute(ctx context.Context, execution runtime.Execution) testspec.Observation {
	started := time.Now()
	observation := runtime.NewObservation(module.ID)
	steps := []testspec.Step{{ID: "initial", Request: execution.Spec.Request}}
	if execution.Spec.Workflow != nil {
		steps = append(steps, execution.Spec.Workflow.Steps...)
	}
	responses := map[string]json.RawMessage{}
	for _, step := range steps {
		observation.HTTPStatus, observation.Text, observation.StreamCompleted = nil, nil, nil
		observation.Response, observation.Usage = nil, nil
		observation.Metrics = map[string]float64{}
		random := execution.Random
		random.MemberID += "/" + step.ID
		body, err := testspec.Render(step.Request, execution.Inputs, random, responses)
		if err != nil {
			runtime.AddIssue(&observation, step.ID, "request_generation_failed")
			break
		}
		request := runtime.Request{Method: http.MethodPost, Path: module.Path, Body: body, Headers: module.Headers}
		response, err := execution.Transport.Send(ctx, request)
		if err != nil {
			runtime.AddIssue(&observation, step.ID, runtime.ErrorCode(ctx))
			observation.Exchanges = append(observation.Exchanges, runtime.Exchange(step.ID, request, response))
			break
		}
		if response.HTTP == nil || response.HTTP.Body == nil {
			runtime.AddIssue(&observation, step.ID, "response_unavailable")
			break
		}
		status := response.HTTP.StatusCode
		observation.HTTPStatus = &status
		observation.Metrics["ttfb_ms"] = response.TTFBMS
		decoder := module.NewDecoder()
		events := []json.RawMessage{}
		if strings.HasPrefix(strings.ToLower(response.HTTP.Header.Get("Content-Type")), "text/event-stream") {
			events = readStream(response, decoder, &observation)
		} else {
			parseJSON(response, decoder, &observation)
		}
		exchange := runtime.Exchange(step.ID, request, response)
		exchange.Response = append(json.RawMessage{}, observation.Response...)
		exchange.Events, exchange.StreamCompleted = events, observation.StreamCompleted
		exchange.Metrics = make(map[string]float64, len(observation.Metrics))
		for key, value := range observation.Metrics {
			exchange.Metrics[key] = value
		}
		observation.Exchanges = append(observation.Exchanges, exchange)
		responses[step.ID] = append(json.RawMessage{}, observation.Response...)
		if len(observation.Issues) > 0 {
			break
		}
	}
	observation.Metrics["e2e_ms"] = runtime.Milliseconds(started)
	return observation
}

func parseJSON(response runtime.Response, decoder Decoder, observation *testspec.Observation) {
	body, err := runtime.Read(response)
	if err != nil {
		runtime.AddIssue(observation, "response", err.Error())
		return
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil || root == nil {
		runtime.AddIssue(observation, "response", "invalid_json")
		return
	}
	observation.Response = append(json.RawMessage{}, body...)
	decoder.JSON(root, observation)
}

func readStream(response runtime.Response, decoder Decoder, observation *testspec.Observation) []json.RawMessage {
	defer response.HTTP.Body.Close()
	completed := false
	observation.StreamCompleted = &completed
	events := []json.RawMessage{}
	limited := &io.LimitedReader{R: response.HTTP.Body, N: runtime.MaxResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data bytes.Buffer
	flush := func() {
		if data.Len() == 0 {
			return
		}
		payload := append(json.RawMessage{}, bytes.TrimSpace(data.Bytes())...)
		data.Reset()
		if _, exists := observation.Metrics["first_frame_ms"]; !exists {
			observation.Metrics["first_frame_ms"] = runtime.Milliseconds(response.Started)
		}
		if len(events) < 256 {
			events = append(events, payload)
		}
		var event map[string]any
		if json.Unmarshal(payload, &event) != nil || event == nil {
			runtime.AddIssue(observation, "stream", "invalid_event_json")
			return
		}
		observation.Metrics["stream_frames"]++
		if completed {
			runtime.AddIssue(observation, "stream", "stream_data_after_terminal")
			return
		}
		semantic, text, terminal := decoder.Event(event, observation)
		if semantic {
			elapsed := runtime.Milliseconds(response.Started)
			observation.Metrics["semantic_chunk_count"]++
			if _, exists := observation.Metrics["ttft_ms"]; !exists {
				observation.Metrics["ttft_ms"] = elapsed
			}
			if observation.Metrics["semantic_chunk_count"] == 2 {
				observation.Metrics["second_semantic_ms"] = elapsed
			}
			observation.Metrics["last_semantic_ms"] = elapsed
		}
		if text {
			if _, exists := observation.Metrics["ttft_text_ms"]; !exists {
				observation.Metrics["ttft_text_ms"] = runtime.Milliseconds(response.Started)
			}
		}
		completed = terminal
	}
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > 32768 {
			runtime.AddIssue(observation, "stream", "stream_line_limit_exceeded")
			break
		}
		line := scanner.Text()
		if line == "" {
			flush()
		} else if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line[5:], " ")
			if data.Len()+len(payload)+1 > 1<<20 {
				runtime.AddIssue(observation, "stream", "stream_event_limit_exceeded")
				break
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(payload)
		}
	}
	flush()
	if scanner.Err() != nil {
		runtime.AddIssue(observation, "stream", "stream_read_failed")
	}
	if limited.N <= 0 {
		runtime.AddIssue(observation, "stream", "response_limit_exceeded")
	}
	decoder.Finish(observation)
	if !completed {
		runtime.AddIssue(observation, "stream", "stream_incomplete")
	}
	if tokens := observation.Metrics["completion_tokens"]; tokens > 1 {
		if first, exists := observation.Metrics["ttft_ms"]; exists {
			observation.Metrics["tpot_ms"] = (observation.Metrics["last_semantic_ms"] - first) / (tokens - 1)
		}
	}
	return events
}

func Object(value any) map[string]any { object, _ := value.(map[string]any); return object }
func String(value any) string         { text, _ := value.(string); return text }

// Index validates provider-controlled indexes before any integer conversion.
func Index(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	if number < 0 || number > 1023 || number != math.Trunc(number) {
		return 0, false
	}
	return int(number), true
}

func Number(value any) float64 {
	number, _ := value.(float64)
	if number < 0 {
		return 0
	}
	return number
}

func Observe(root map[string]any, text string, observation *testspec.Observation) {
	observation.Response, _ = json.Marshal(root)
	observation.Usage, _ = json.Marshal(root["usage"])
	observation.Text = &text
}
