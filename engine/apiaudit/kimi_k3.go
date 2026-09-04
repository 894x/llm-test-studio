package apiaudit

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const DefaultKimiK3Model = "kimi-k3"

func init() {
	kinds := make(map[string]bool, len(supportedKinds["openai-chat"])+5)
	for kind, supported := range supportedKinds["openai-chat"] {
		kinds[kind] = supported
	}
	kinds["kimi_success"] = true
	kinds["kimi_tool_call"] = true
	kinds["kimi_reasoning_visible"] = true
	kinds["kimi_reasoning_hidden"] = true
	kinds["kimi_error_400"] = true
	supportedKinds["kimi-k3"] = kinds
}

// RunKimiK3Case runs Kimi-K3-specific assertions and delegates shared
// OpenAI-compatible behavior to the regular chat-completions runner.
func RunKimiK3Case(ctx context.Context, doer HTTPDoer, config RunConfig, definition CaseDefinition) (result CaseResult) {
	if definition.Kind != "kimi_success" && definition.Kind != "kimi_tool_call" && definition.Kind != "kimi_reasoning_visible" && definition.Kind != "kimi_reasoning_hidden" && definition.Kind != "kimi_error_400" {
		return RunOpenAIChatCase(ctx, doer, config, definition)
	}

	started := time.Now()
	result = CaseResult{
		ID: definition.ID, Name: definition.Name, Dimension: definition.Dimension,
		Protocol: definition.Protocol, Model: config.Model, Status: StatusUnknown,
		Severity: definition.Severity, Metrics: map[string]any{},
	}
	if result.Severity == "" {
		result.Severity = "normal"
	}
	defer func() {
		result.ElapsedMS = time.Since(started).Milliseconds()
		if result.ElapsedMS == 0 {
			result.ElapsedMS = 1
		}
		result.Metrics["request_count"] = len(result.Exchanges)
	}()

	body, err := cloneBody(definition.Request.Body)
	if err != nil {
		result.Status, result.Evidence = StatusFail, "invalid request body: "+err.Error()
		return result
	}
	if body == nil {
		body = map[string]any{}
	}
	body["model"] = config.Model
	observation, err := observeSync(ctx, doer, config, definition.Request, body)
	result.Exchanges = append(result.Exchanges, observation.Exchange)
	result.HTTPStatus = observation.StatusCode
	result.Usage = observation.Usage
	if err != nil {
		result.Status, result.Evidence = StatusFail, "request failed: "+err.Error()
		return result
	}
	if definition.Kind == "kimi_error_400" {
		if observation.StatusCode != 400 || !hasOpenAIError(observation.Response) {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected OpenAI-style HTTP 400, got HTTP %d", observation.StatusCode)
			return result
		}
		result.Status, result.Evidence = StatusPass, "invalid request returned OpenAI-style HTTP 400"
		return result
	}
	if observation.StatusCode < 200 || observation.StatusCode >= 300 {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("HTTP %d", observation.StatusCode)
		return result
	}

	switch definition.Kind {
	case "kimi_success":
		choices, _ := observation.Response["choices"].([]any)
		if len(choices) == 0 {
			result.Status, result.Evidence = StatusFail, "response choices is empty"
			return result
		}
		if required, _ := definition.Options["require_content"].(bool); required && strings.TrimSpace(observation.Content) == "" {
			reasoningVisible := hasNonEmptyField(observation.Message, "reasoning_content") || hasNonEmptyField(observation.Message, "reasoning") || findPositiveNumber(observation.Usage, "reasoning_tokens")
			result.Metrics["reasoning_visible"] = reasoningVisible
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("assistant final content is empty; finish_reason=%s, reasoning_visible=%t", observation.FinishReason, reasoningVisible)
			return result
		}
		if required, _ := definition.Options["require_image_input"].(bool); required && !findPositiveNumber(observation.Usage, "image_tokens") {
			result.Status, result.Evidence = StatusFail, "response did not account for image input tokens"
			return result
		}
		if required, _ := definition.Options["require_usage"].(bool); required {
			promptTokens, exists := usageNumber(observation.Usage, "prompt_tokens")
			if !exists || promptTokens <= 0 {
				result.Status, result.Evidence = StatusFail, "response prompt usage is missing or zero"
				return result
			}
		}
		if minimum, ok := definition.Options["min_prompt_tokens"].(float64); ok {
			promptTokens, exists := usageNumber(observation.Usage, "prompt_tokens")
			if !exists {
				result.Status, result.Evidence = StatusFail, "response prompt_tokens is missing"
				return result
			}
			result.Metrics["prompt_tokens"] = promptTokens
			if promptTokens < minimum {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("prompt_tokens %.0f is below multimodal threshold %.0f", promptTokens, minimum)
				return result
			}
		}
		if expected, _ := definition.Options["expected_digit_sequence"].(string); expected != "" {
			var actual strings.Builder
			for _, char := range observation.Content {
				if char >= '0' && char <= '9' {
					actual.WriteRune(char)
				}
			}
			result.Metrics["digit_sequence"] = actual.String()
			if actual.String() != expected {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected digit sequence %s, got %s in assistant content %q", expected, actual.String(), observation.Content)
				return result
			}
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("HTTP %d returned expected digit sequence %s", observation.StatusCode, expected)
			return result
		}
		if required, _ := definition.Options["require_content"].(bool); required {
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("HTTP %d returned non-empty assistant content", observation.StatusCode)
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("HTTP %d returned an OpenAI-compatible choice", observation.StatusCode)
		return result

	case "kimi_tool_call":
		toolCalls, _ := observation.Message["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			result.Status, result.Evidence = StatusFail, "assistant returned no tool_calls"
			return result
		}
		expectedName, _ := definition.Options["expected_tool_name"].(string)
		for _, item := range toolCalls {
			toolCall, _ := item.(map[string]any)
			function, _ := toolCall["function"].(map[string]any)
			name, _ := function["name"].(string)
			if strings.TrimSpace(name) == "" {
				result.Status, result.Evidence = StatusFail, "tool call function name is empty"
				return result
			}
			if expectedName != "" && name != expectedName {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected tool %s, got %s", expectedName, name)
				return result
			}
		}
		result.Metrics["tool_call_count"] = len(toolCalls)
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("received %d valid tool call(s)", len(toolCalls))
		return result

	case "kimi_reasoning_visible", "kimi_reasoning_hidden":
		visible := hasNonEmptyField(observation.Message, "reasoning_content") || hasNonEmptyField(observation.Message, "reasoning") || findPositiveNumber(observation.Usage, "reasoning_tokens")
		result.Metrics["reasoning_visible"] = visible
		if definition.Kind == "kimi_reasoning_visible" {
			if !visible {
				result.Status, result.Evidence = StatusFail, "completion succeeded without observable reasoning metadata"
				return result
			}
			result.Status, result.Evidence = StatusPass, "reasoning metadata is observable in the response"
			return result
		}
		if visible {
			result.Status, result.Evidence = StatusFail, "reasoning metadata is visible but this compatibility case expects it to be hidden"
			return result
		}
		result.Status, result.Evidence = StatusPass, "completion succeeded without observable reasoning metadata"
		return result
	}

	result.Status, result.Evidence = StatusUnknown, fmt.Sprintf("unsupported Kimi-K3 evaluator %q", definition.Kind)
	return result
}
