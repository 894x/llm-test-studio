package openaichat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const maxEventBytes = 256 << 10

type streamState struct {
	root       map[string]any
	choices    map[int]map[string]any
	text       strings.Builder
	firstToken bool
	events     []json.RawMessage
	ids        map[string]bool
	models     map[string]bool
	finished   bool
}

func parseStream(response runtime.Response, observation *testspec.Observation) []json.RawMessage {
	defer response.HTTP.Body.Close()
	completed := false
	observation.StreamCompleted = &completed
	state := streamState{root: map[string]any{}, choices: map[int]map[string]any{}, events: []json.RawMessage{}, ids: map[string]bool{}, models: map[string]bool{}}
	for _, key := range []string{"stream_frames", "stream_data_after_done", "stream_content_after_finish", "stream_finish_count", "stream_invalid_delta_count", "stream_provider_error_count", "stream_identity_missing_count"} {
		observation.Metrics[key] = 0
	}
	limited := &io.LimitedReader{R: response.HTTP.Body, N: runtime.MaxResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	var data bytes.Buffer
	flush := func() {
		if data.Len() == 0 {
			return
		}
		payload := bytes.TrimSpace(data.Bytes())
		if _, exists := observation.Metrics["first_frame_ms"]; !exists {
			observation.Metrics["first_frame_ms"] = runtime.Milliseconds(response.Started)
		}
		if completed {
			observation.Metrics["stream_data_after_done"]++
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			completed = true
			data.Reset()
			return
		}
		if len(state.events) < 256 {
			state.events = append(state.events, append(json.RawMessage{}, payload...))
		}
		var event map[string]any
		if json.Unmarshal(payload, &event) != nil {
			runtime.AddIssue(observation, "stream", "invalid_event_json")
			data.Reset()
			return
		}
		observation.Metrics["stream_frames"]++
		state.consume(event, response, observation)
		data.Reset()
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
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimPrefix(line[5:], " ")
		if data.Len()+len(payload)+1 > maxEventBytes {
			runtime.AddIssue(observation, "stream", "stream_event_limit_exceeded")
			break
		}
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.WriteString(payload)
	}
	flush()
	if scanner.Err() != nil {
		runtime.AddIssue(observation, "stream", "stream_read_failed")
	}
	if limited.N <= 0 {
		runtime.AddIssue(observation, "stream", "response_limit_exceeded")
	}
	indices := make([]int, 0, len(state.choices))
	for index := range state.choices {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	choices := make([]any, 0, len(indices))
	for _, index := range indices {
		choices = append(choices, state.choices[index])
	}
	state.root["choices"] = choices
	observation.Response, _ = json.Marshal(state.root)
	text := state.text.String()
	observation.Text = &text
	observeTokens(observation)
	observeContentFacts(observation)
	observation.Metrics["stream_response_ids"] = float64(len(state.ids))
	observation.Metrics["stream_model_ids"] = float64(len(state.models))
	if tokens, exists := observation.Metrics["completion_tokens"]; exists && tokens > 1 && state.firstToken {
		observation.Metrics["tpot_ms"] = (runtime.Milliseconds(response.Started) - observation.Metrics["ttft_ms"]) / (tokens - 1)
	}
	return state.events
}

func (state *streamState) consume(event map[string]any, response runtime.Response, observation *testspec.Observation) {
	if _, exists := event["error"]; exists {
		observation.Metrics["stream_provider_error_count"]++
	}
	for name, value := range event {
		if name == "choices" {
			continue
		}
		state.root[name] = value
	}
	if usage, exists := event["usage"]; exists && usage != nil {
		observation.Usage, _ = json.Marshal(usage)
	}
	choices, _ := event["choices"].([]any)
	if len(choices) > 0 {
		id, _ := event["id"].(string)
		model, _ := event["model"].(string)
		if id == "" || model == "" {
			observation.Metrics["stream_identity_missing_count"]++
		}
		if id != "" {
			state.ids[id] = true
		}
		if model != "" {
			state.models[model] = true
		}
	}
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		indexFloat, _ := choice["index"].(float64)
		index := int(indexFloat)
		target, exists := state.choices[index]
		if !exists {
			target = map[string]any{"index": index, "message": map[string]any{}}
			state.choices[index] = target
		}
		for name, value := range choice {
			if name != "delta" && name != "index" {
				target[name] = value
			}
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		message := target["message"].(map[string]any)
		if state.finished && hasContent(delta) {
			observation.Metrics["stream_content_after_finish"]++
		}
		if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
			state.finished = true
			observation.Metrics["stream_finish_count"]++
		}
		for _, name := range []string{"content", "reasoning_content"} {
			if value, exists := delta[name]; exists && value != nil {
				if _, ok := value.(string); !ok {
					observation.Metrics["stream_invalid_delta_count"]++
				}
			}
		}
		if hasContent(delta) && !state.firstToken {
			state.firstToken = true
			observation.Metrics["ttft_ms"] = runtime.Milliseconds(response.Started)
		}
		if text, ok := delta["content"].(string); ok && index == 0 {
			state.text.WriteString(text)
		}
		mergeDelta(message, delta)
	}
}

func hasContent(delta map[string]any) bool {
	for _, key := range []string{"content", "reasoning_content", "reasoning"} {
		if text, ok := delta[key].(string); ok && text != "" {
			return true
		}
	}
	tools, _ := delta["tool_calls"].([]any)
	return len(tools) > 0
}

func mergeDelta(target map[string]any, delta map[string]any) {
	for key, raw := range delta {
		switch value := raw.(type) {
		case string:
			if key == "role" || key == "id" || key == "type" {
				target[key] = value
				continue
			}
			previous, _ := target[key].(string)
			target[key] = previous + value
		case map[string]any:
			nested, _ := target[key].(map[string]any)
			if nested == nil {
				nested = map[string]any{}
				target[key] = nested
			}
			mergeDelta(nested, value)
		case []any:
			items, _ := target[key].([]any)
			if items == nil {
				items = []any{}
			}
			for _, rawItem := range value {
				item, ok := rawItem.(map[string]any)
				if !ok {
					continue
				}
				indexFloat, _ := item["index"].(float64)
				index := int(indexFloat)
				if index < 0 || index > 255 {
					continue
				}
				for len(items) <= index {
					items = append(items, map[string]any{})
				}
				mergeDelta(items[index].(map[string]any), item)
			}
			target[key] = items
		default:
			target[key] = value
		}
	}
}
