package apiaudit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/engine/common"
)

func RunOpenAIChatCase(ctx context.Context, doer HTTPDoer, config RunConfig, definition CaseDefinition) (result CaseResult) {
	if strings.HasPrefix(definition.Kind, "glm53_") {
		return runGLM53Case(ctx, doer, config, definition)
	}
	started := time.Now()
	result = CaseResult{
		ID: definition.ID, Name: definition.Name, Dimension: definition.Dimension,
		Protocol: definition.Protocol, Model: config.Model, Status: StatusUnknown,
		Severity: definition.Severity,
	}
	if result.Severity == "" {
		result.Severity = "normal"
	}
	defer func() {
		result.ElapsedMS = time.Since(started).Milliseconds()
		if result.ElapsedMS == 0 {
			result.ElapsedMS = 1
		}
	}()
	if definition.Kind == "manual_unknown" {
		reason, _ := definition.Options["reason"].(string)
		if strings.TrimSpace(reason) == "" {
			reason = "this case requires an external baseline or manual review"
		}
		result.Status, result.Evidence = StatusUnknown, reason
		return result
	}
	if blackBoxEvaluatorKinds[definition.Kind] {
		return runOpenAIBlackBoxEvaluator(ctx, doer, config, definition)
	}

	body, err := cloneBody(definition.Request.Body)
	if err != nil {
		result.Status, result.Evidence = StatusFail, "invalid request body: "+err.Error()
		return result
	}
	if body != nil && definition.Kind != "models_contains" {
		body["model"] = config.Model
	}

	if definition.Kind == "id_consistency" || definition.Kind == "response_id" {
		repetitions := 5
		if value, ok := definition.Options["repetitions"].(float64); ok && value >= 2 && value <= 20 {
			repetitions = int(value)
		}
		maxChannels := 1
		if definition.Kind == "response_id" {
			maxChannels = 2
			if value, ok := definition.Options["max_channels"].(float64); ok && value >= 1 && value <= 20 {
				maxChannels = int(value)
			}
		}
		prefixes := make(map[string]int)
		for i := 0; i < repetitions; i++ {
			exchange, responseBody, requestErr := performRequest(ctx, doer, config, definition.Request, body)
			result.Exchanges = append(result.Exchanges, exchange)
			if requestErr != nil {
				result.Status, result.Evidence = StatusFail, "request failed: "+requestErr.Error()
				return result
			}
			result.HTTPStatus = exchange.StatusCode
			if exchange.StatusCode < 200 || exchange.StatusCode >= 300 {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("request %d returned HTTP %d", i+1, exchange.StatusCode)
				return result
			}
			var response map[string]any
			if err := common.Unmarshal(responseBody, &response); err != nil {
				result.Status, result.Evidence = StatusFail, "invalid JSON response: "+err.Error()
				return result
			}
			id, _ := response["id"].(string)
			if id == "" {
				result.Status, result.Evidence = StatusFail, "response id is missing"
				return result
			}
			prefix := "(non-letter)"
			for index := 0; index < len(id); index++ {
				character := id[index]
				if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
					if index > 0 {
						prefix = id[:index]
					}
					break
				}
				if index == len(id)-1 {
					prefix = id
				}
			}
			prefixes[prefix]++
		}
		keys := make([]string, 0, len(prefixes))
		for prefix := range prefixes {
			keys = append(keys, prefix)
		}
		sort.Strings(keys)
		if definition.Kind == "response_id" {
			result.Evidence = fmt.Sprintf("%d responses indicate %d possible upstream channels from ID patterns: %s", repetitions, len(prefixes), strings.Join(keys, ", "))
			if len(prefixes) <= maxChannels {
				result.Status = StatusPass
			} else {
				result.Status = StatusFail
			}
			return result
		}
		if len(prefixes) == 1 {
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d responses used one id prefix: %s", repetitions, keys[0])
		} else {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("%d prefixes observed across %d responses: %s", len(prefixes), repetitions, strings.Join(keys, ", "))
		}
		return result
	}

	exchange, responseBody, requestErr := performRequest(ctx, doer, config, definition.Request, body)
	result.Exchanges = append(result.Exchanges, exchange)
	if requestErr != nil {
		result.Status, result.Evidence = StatusFail, "request failed: "+requestErr.Error()
		return result
	}
	result.HTTPStatus = exchange.StatusCode
	if definition.Kind == "error_schema" {
		var response map[string]any
		if err := common.Unmarshal(responseBody, &response); err != nil {
			result.Status, result.Evidence = StatusFail, "invalid JSON error response: "+err.Error()
			return result
		}
		errorObject, _ := response["error"].(map[string]any)
		message, _ := errorObject["message"].(string)
		if expected, ok := definition.Options["expected_http_status"].(float64); ok && exchange.StatusCode != int(expected) {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected HTTP %.0f, got HTTP %d", expected, exchange.StatusCode)
			return result
		}
		if exchange.StatusCode >= 400 && strings.TrimSpace(message) != "" {
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("HTTP %d returned OpenAI-style error.message", exchange.StatusCode)
		} else {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("HTTP %d did not return OpenAI-style error.message", exchange.StatusCode)
		}
		return result
	}
	if exchange.StatusCode < 200 || exchange.StatusCode >= 300 {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("HTTP %d", exchange.StatusCode)
		return result
	}

	if definition.Kind == "stream_usage" || definition.Kind == "chat_stream" {
		scanner := bufio.NewScanner(bytes.NewReader(responseBody))
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		frames := 0
		content := strings.Builder{}
		finishReason := ""
		seenDone := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			frames++
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				seenDone = true
				continue
			}
			var chunk map[string]any
			if err := common.Unmarshal([]byte(data), &chunk); err != nil {
				result.Status, result.Evidence = StatusFail, "invalid SSE JSON: "+err.Error()
				return result
			}
			if usage, ok := chunk["usage"].(map[string]any); ok {
				result.Usage = usage
			}
			choices, _ := chunk["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			if value, ok := choice["finish_reason"].(string); ok {
				finishReason = value
			}
			delta, _ := choice["delta"].(map[string]any)
			if value, ok := delta["content"].(string); ok {
				content.WriteString(value)
			}
			if value, ok := delta["reasoning_content"].(string); ok {
				content.WriteString(value)
			}
		}
		if err := scanner.Err(); err != nil {
			result.Status, result.Evidence = StatusFail, "read SSE: "+err.Error()
			return result
		}
		if frames < 2 || content.Len() == 0 || !seenDone {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("incomplete SSE: frames=%d content=%d done=%t", frames, content.Len(), seenDone)
			return result
		}
		if definition.Kind == "stream_usage" && result.Usage == nil {
			result.Status, result.Evidence = StatusFail, "stream completed without usage"
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d SSE frames, %d content bytes, finish_reason=%s", frames, content.Len(), finishReason)
		return result
	}

	var response map[string]any
	if err := common.Unmarshal(responseBody, &response); err != nil {
		result.Status, result.Evidence = StatusFail, "invalid JSON response: "+err.Error()
		return result
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		result.Usage = usage
	}
	if definition.Kind == "models_contains" {
		models, _ := response["data"].([]any)
		for _, item := range models {
			model, _ := item.(map[string]any)
			if model["id"] == config.Model {
				result.Status, result.Evidence = StatusPass, fmt.Sprintf("model %s appears in %d listed models", config.Model, len(models))
				return result
			}
		}
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("model %s is absent from %d listed models", config.Model, len(models))
		return result
	}

	choices, _ := response["choices"].([]any)
	if len(choices) == 0 {
		result.Status, result.Evidence = StatusFail, "response choices is empty"
		return result
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	content, _ := message["content"].(string)
	finishReason, _ := choice["finish_reason"].(string)
	if definition.Kind == "stop_parameter" {
		stopText, _ := definition.Options["stop_text"].(string)
		if stopText == "" {
			stopText = "STOP_AUDIT"
		}
		if strings.Contains(content, stopText) || finishReason != "stop" {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("stop text present=%t, finish_reason=%s", strings.Contains(content, stopText), finishReason)
		} else {
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("stop text %q excluded and finish_reason=stop", stopText)
		}
		return result
	}
	if definition.Kind == "structured_json" {
		var object map[string]any
		if err := common.Unmarshal([]byte(content), &object); err != nil {
			result.Status, result.Evidence = StatusFail, "assistant content is not a JSON object: "+err.Error()
			return result
		}
		required, _ := definition.Options["required_keys"].([]any)
		keys := make([]string, 0, len(required))
		for _, item := range required {
			key, _ := item.(string)
			if key == "" {
				continue
			}
			keys = append(keys, key)
			if _, exists := object[key]; !exists {
				result.Status, result.Evidence = StatusFail, "structured output is missing key "+key
				return result
			}
		}
		result.Status, result.Evidence = StatusPass, "structured output contains keys: "+strings.Join(keys, ", ")
		return result
	}
	if definition.Kind == "tool_call" {
		toolCalls, _ := message["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			result.Status, result.Evidence = StatusFail, "assistant returned no tool_calls"
			return result
		}
		expectedName, _ := definition.Options["expected_tool_name"].(string)
		for _, item := range toolCalls {
			call, _ := item.(map[string]any)
			function, _ := call["function"].(map[string]any)
			name, _ := function["name"].(string)
			if strings.TrimSpace(name) == "" || (expectedName != "" && name != expectedName) {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("unexpected tool name %q; expected %q", name, expectedName)
				return result
			}
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d valid tool calls returned with finish_reason=%s", len(toolCalls), finishReason)
		return result
	}
	constraintCount := 0
	if required, _ := definition.Options["require_image_input"].(bool); required {
		constraintCount++
		if !findPositiveNumber(result.Usage, "image_tokens") {
			result.Status, result.Evidence = StatusFail, "response did not account for image input tokens"
			return result
		}
	}
	if minimum, ok := definition.Options["min_prompt_tokens"].(float64); ok {
		constraintCount++
		tokens, exists := usageNumber(result.Usage, "prompt_tokens")
		if !exists || tokens < minimum {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("prompt_tokens must be at least %.0f", minimum)
			return result
		}
	}
	if expected, _ := definition.Options["expected_digit_sequence"].(string); expected != "" {
		constraintCount++
		var actual strings.Builder
		for _, char := range content {
			if char >= '0' && char <= '9' {
				actual.WriteRune(char)
			}
		}
		if actual.String() != expected {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected digit sequence %s, got %s", expected, actual.String())
			return result
		}
	}
	if expected, ok := definition.Options["expected_exact"].(string); ok && expected != "" {
		constraintCount++
		if strings.TrimSpace(content) != expected {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected exact content %q, got %q", expected, strings.TrimSpace(content))
			return result
		}
	}
	if values, ok := definition.Options["required_substrings"].([]any); ok && len(values) > 0 {
		constraintCount++
		for _, item := range values {
			value, _ := item.(string)
			if value != "" && !strings.Contains(strings.ToLower(content), strings.ToLower(value)) {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("assistant content is missing required text %q", value)
				return result
			}
		}
	}
	if values, ok := definition.Options["forbidden_substrings"].([]any); ok && len(values) > 0 {
		constraintCount++
		for _, item := range values {
			value, _ := item.(string)
			if value != "" && strings.Contains(strings.ToLower(content), strings.ToLower(value)) {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("assistant content contains forbidden text %q", value)
				return result
			}
		}
	}
	if required, _ := definition.Options["require_usage"].(bool); required {
		constraintCount++
		if result.Usage == nil {
			result.Status, result.Evidence = StatusFail, "response usage is missing"
			return result
		}
	}
	if maximum, ok := definition.Options["max_completion_tokens"].(float64); ok && maximum >= 0 {
		constraintCount++
		completion, exists := result.Usage["completion_tokens"].(float64)
		if !exists {
			result.Status, result.Evidence = StatusUnknown, "completion_tokens is unavailable"
			return result
		}
		if completion > maximum {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("completion_tokens %.0f exceeds maximum %.0f", completion, maximum)
			return result
		}
	}
	if maximum, ok := definition.Options["max_elapsed_ms"].(float64); ok && maximum > 0 {
		constraintCount++
		elapsed := time.Since(started).Milliseconds()
		if float64(elapsed) > maximum {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("elapsed time %dms exceeds maximum %.0fms", elapsed, maximum)
			return result
		}
	}
	if forbidden, _ := definition.Options["forbid_tool_calls"].(bool); forbidden {
		constraintCount++
		if toolCalls, ok := message["tool_calls"].([]any); ok && len(toolCalls) > 0 {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("assistant returned %d forbidden tool_calls", len(toolCalls))
			return result
		}
	}
	if required, exists := definition.Options["require_content"].(bool); strings.TrimSpace(content) == "" && (!exists || required) {
		result.Status, result.Evidence = StatusFail, "assistant content is empty"
		return result
	}
	if constraintCount > 0 {
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d constraints passed; HTTP %d, finish_reason=%s", constraintCount, exchange.StatusCode, finishReason)
	} else {
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("HTTP %d, %d content bytes, finish_reason=%s", exchange.StatusCode, len(content), finishReason)
	}
	return result
}

var _ HTTPDoer = (*http.Client)(nil)
