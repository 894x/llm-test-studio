// Package anthropicmessages observes the native Anthropic Messages contract.
package anthropicmessages

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
		ID: protocol.AnthropicMessages, Label: "Anthropic Messages", Path: protocol.AnthropicMessagesPath,
		DefaultBody: json.RawMessage(`{"messages":[{"role":"user","content":"Hello"}],"max_tokens":128}`),
		Headers:     map[string]string{"anthropic-version": "2023-06-01"},
		NewDecoder: func() textapi.Decoder {
			return &decoder{
				root: map[string]any{}, blocks: map[int]map[string]any{},
				arguments: map[int]string{}, open: map[int]bool{},
			}
		},
	}
}

type decoder struct {
	root      map[string]any
	blocks    map[int]map[string]any
	arguments map[int]string
	open      map[int]bool
	started   bool
}

func (state *decoder) JSON(root map[string]any, observation *testspec.Observation) {
	var text strings.Builder
	var toolCalls float64
	blocks, validContent := root["content"].([]any)
	if !validContent {
		runtime.AddIssue(observation, "response", "invalid_response_shape")
	}
	for _, raw := range blocks {
		block := textapi.Object(raw)
		if textapi.String(block["type"]) == "tool_use" {
			toolCalls++
		}
		if textapi.String(block["type"]) == "text" {
			text.WriteString(textapi.String(block["text"]))
		}
	}
	textapi.Observe(root, text.String(), observation)
	observation.Metrics["tool_call_count"] = toolCalls
	usage := textapi.Object(root["usage"])
	input := textapi.Number(usage["input_tokens"])
	read := textapi.Number(usage["cache_read_input_tokens"])
	creation := textapi.Number(usage["cache_creation_input_tokens"])
	output := textapi.Number(usage["output_tokens"])
	observation.Metrics["prompt_tokens"] = input + read + creation
	observation.Metrics["completion_tokens"] = output
	observation.Metrics["total_tokens"] = input + read + creation + output
	observation.Metrics["cached_tokens"] = read
	observation.Metrics["cache_creation_tokens"] = creation
	if observation.HTTPStatus != nil && *observation.HTTPStatus >= 200 && *observation.HTTPStatus < 300 {
		if textapi.String(root["type"]) != "message" || root["stop_reason"] == nil {
			runtime.AddIssue(observation, "response", "response_not_terminal")
		}
	}
}

func (state *decoder) Event(event map[string]any, observation *testspec.Observation) (bool, bool, bool) {
	kind := textapi.String(event["type"])
	index := 0
	if raw, exists := event["index"]; exists {
		var valid bool
		index, valid = textapi.Index(raw)
		if !valid {
			runtime.AddIssue(observation, "stream", "invalid_stream_index")
			return false, false, false
		}
	}
	switch kind {
	case "message_start":
		if state.started {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
			return false, false, false
		}
		if textapi.Object(event["message"]) == nil {
			runtime.AddIssue(observation, "stream", "invalid_event_json")
			return false, false, false
		}
		state.started = true
		if root := textapi.Object(event["message"]); root != nil {
			state.root = root
		}
	case "content_block_start":
		block := textapi.Object(event["content_block"])
		if !state.started || state.blocks[index] != nil || block == nil {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
			return false, false, false
		}
		state.blocks[index] = block
		state.open[index] = true
	case "content_block_delta":
		block, delta := state.blocks[index], textapi.Object(event["delta"])
		if block == nil || delta == nil || !state.open[index] {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
			return false, false, false
		}
		deltaType := textapi.String(delta["type"])
		blockType := textapi.String(block["type"])
		validType := (deltaType == "text_delta" && blockType == "text") ||
			((deltaType == "thinking_delta" || deltaType == "signature_delta") && blockType == "thinking") ||
			(deltaType == "input_json_delta" && blockType == "tool_use")
		if !validType && deltaType != "citations_delta" {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
			return false, false, false
		}
		switch deltaType {
		case "text_delta":
			value := textapi.String(delta["text"])
			block["text"] = textapi.String(block["text"]) + value
			return value != "", value != "", false
		case "thinking_delta":
			value := textapi.String(delta["thinking"])
			block["thinking"] = textapi.String(block["thinking"]) + value
			return value != "", false, false
		case "signature_delta":
			block["signature"] = textapi.String(block["signature"]) + textapi.String(delta["signature"])
		case "input_json_delta":
			value := textapi.String(delta["partial_json"])
			state.arguments[index] += value
			return value != "", false, false
		}
	case "content_block_stop":
		if !state.open[index] {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
			break
		}
		state.open[index] = false
		if raw, exists := state.arguments[index]; exists {
			var input map[string]any
			if json.Unmarshal([]byte(raw), &input) != nil || input == nil {
				runtime.AddIssue(observation, "stream", "invalid_tool_arguments")
			} else {
				state.blocks[index]["input"] = input
			}
		}
	case "message_delta":
		if !state.started {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
		}
		for key, value := range textapi.Object(event["delta"]) {
			state.root[key] = value
		}
		usage := textapi.Object(state.root["usage"])
		if usage == nil {
			usage = map[string]any{}
			state.root["usage"] = usage
		}
		// Message delta token counts are cumulative, never additive.
		for key, value := range textapi.Object(event["usage"]) {
			usage[key] = value
		}
	case "message_stop":
		if !state.started {
			runtime.AddIssue(observation, "stream", "invalid_stream_state")
		}
		for _, open := range state.open {
			if open {
				runtime.AddIssue(observation, "stream", "invalid_stream_state")
				break
			}
		}
		return false, false, true
	case "error":
		runtime.AddIssue(observation, "stream", "provider_error")
		return false, false, true
	}
	return false, false, false
}

func (state *decoder) Finish(observation *testspec.Observation) {
	indices := make([]int, 0, len(state.blocks))
	for index := range state.blocks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	content := []any{}
	for _, index := range indices {
		content = append(content, state.blocks[index])
	}
	state.root["content"] = content
	state.JSON(state.root, observation)
}
