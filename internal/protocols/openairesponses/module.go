// Package openairesponses observes the native OpenAI Responses contract.
package openairesponses

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/protocols/textapi"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func New() *textapi.Module {
	return &textapi.Module{
		ID: protocol.OpenAIResponses, Label: "OpenAI Responses", Path: protocol.OpenAIResponsesPath,
		DefaultBody: json.RawMessage(`{"input":"Hello","max_output_tokens":128}`),
		Headers:     map[string]string{},
		NewDecoder: func() textapi.Decoder {
			return &decoder{root: map[string]any{}, items: map[int]map[string]any{}}
		},
	}
}

type decoder struct {
	root              map[string]any
	items             map[int]map[string]any
	started, terminal bool
}

func (state *decoder) JSON(root map[string]any, observation *testspec.Observation) {
	var text strings.Builder
	var toolCalls float64
	items, validOutput := root["output"].([]any)
	if !validOutput {
		runtime.AddIssue(observation, "response", "invalid_response_shape")
	}
	for _, raw := range items {
		item := textapi.Object(raw)
		if textapi.String(item["type"]) == "function_call" {
			toolCalls++
		}
		if textapi.String(item["type"]) != "message" {
			continue
		}
		parts, _ := item["content"].([]any)
		for _, rawPart := range parts {
			part := textapi.Object(rawPart)
			if textapi.String(part["type"]) == "output_text" {
				text.WriteString(textapi.String(part["text"]))
			}
		}
	}
	textapi.Observe(root, text.String(), observation)
	observation.Metrics["tool_call_count"] = toolCalls
	usage := textapi.Object(root["usage"])
	observation.Metrics["prompt_tokens"] = textapi.Number(usage["input_tokens"])
	observation.Metrics["completion_tokens"] = textapi.Number(usage["output_tokens"])
	observation.Metrics["total_tokens"] = textapi.Number(usage["total_tokens"])
	observation.Metrics["cached_tokens"] = textapi.Number(textapi.Object(usage["input_tokens_details"])["cached_tokens"])
	observation.Metrics["reasoning_tokens"] = textapi.Number(textapi.Object(usage["output_tokens_details"])["reasoning_tokens"])
	if observation.HTTPStatus != nil && *observation.HTTPStatus >= 200 && *observation.HTTPStatus < 300 {
		switch textapi.String(root["status"]) {
		case "completed":
		case "failed":
			runtime.AddIssue(observation, "response", "provider_error")
		case "incomplete":
			runtime.AddIssue(observation, "response", "provider_incomplete")
		default:
			runtime.AddIssue(observation, "response", "response_not_terminal")
		}
	}
}

func (state *decoder) Event(event map[string]any, observation *testspec.Observation) (bool, bool, bool) {
	kind := textapi.String(event["type"])
	index := 0
	if raw, exists := event["output_index"]; exists {
		var valid bool
		index, valid = textapi.Index(raw)
		if !valid {
			runtime.AddIssue(observation, "stream", "invalid_stream_index")
			return false, false, false
		}
	}
	if !state.started && kind != "response.created" && kind != "response.in_progress" && kind != "error" {
		runtime.AddIssue(observation, "stream", "invalid_stream_state")
		return false, false, false
	}
	switch kind {
	case "response.created", "response.in_progress":
		state.started = true
		if root := textapi.Object(event["response"]); root != nil {
			state.root = root
		}
	case "response.output_item.added", "response.output_item.done":
		if item := textapi.Object(event["item"]); item != nil {
			state.items[index] = item
		}
	case "response.content_part.added":
		if item := state.items[index]; item != nil {
			partIndex, valid := textapi.Index(event["content_index"])
			if !valid {
				runtime.AddIssue(observation, "stream", "invalid_stream_index")
				break
			}
			parts, _ := item["content"].([]any)
			for len(parts) <= partIndex {
				parts = append(parts, map[string]any{})
			}
			parts[partIndex] = textapi.Object(event["part"])
			item["content"] = parts
		}
	case "response.output_text.delta":
		value := textapi.String(event["delta"])
		partIndex, valid := textapi.Index(event["content_index"])
		if !valid {
			runtime.AddIssue(observation, "stream", "invalid_stream_index")
			return false, false, false
		}
		item := state.items[index]
		parts, _ := item["content"].([]any)
		if partIndex >= len(parts) || textapi.Object(parts[partIndex]) == nil {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
			return false, false, false
		}
		part := textapi.Object(parts[partIndex])
		part["text"] = textapi.String(part["text"]) + value
		return value != "", value != "", false
	case "response.function_call_arguments.delta":
		value := textapi.String(event["delta"])
		if item := state.items[index]; item != nil {
			item["arguments"] = textapi.String(item["arguments"]) + value
		} else {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
		}
		return value != "", false, false
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
		return textapi.String(event["delta"]) != "", false, false
	case "response.completed", "response.failed", "response.incomplete":
		state.terminal = true
		if !state.started {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
		}
		root := textapi.Object(event["response"])
		if root == nil {
			runtime.AddIssue(observation, "stream", "invalid_event_json")
		} else {
			state.root = root
		}
		return false, false, true
	case "error":
		runtime.AddIssue(observation, "stream", "provider_error")
		return false, false, true
	}
	return false, false, false
}

func (state *decoder) Finish(observation *testspec.Observation) {
	if !state.terminal {
		indices := make([]int, 0, len(state.items))
		for index := range state.items {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		output := []any{}
		for _, index := range indices {
			output = append(output, state.items[index])
		}
		state.root["output"] = output
	}
	state.JSON(state.root, observation)
}
