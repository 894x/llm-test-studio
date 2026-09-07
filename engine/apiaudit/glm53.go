package apiaudit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// These assertions target BigModel's General Model API, not Coding Plan aliases.
var glm53Kinds = []string{
	"glm53_sync", "glm53_stream", "glm53_rejected", "glm53_auth_rejected",
	"glm53_replay", "glm53_tool_roundtrip",
}

func runGLM53Case(
	ctx context.Context,
	doer HTTPDoer,
	config RunConfig,
	definition CaseDefinition,
) (result CaseResult) {
	started := time.Now()
	result = CaseResult{
		ID: definition.ID, Name: definition.Name, Dimension: definition.Dimension,
		Protocol: definition.Protocol, Model: config.Model, Severity: definition.Severity,
		Status: StatusFail, Metrics: map[string]any{}, Exchanges: []HTTPExchange{},
	}
	defer func() {
		result.ElapsedMS = max(1, time.Since(started).Milliseconds())
		result.Metrics["request_count"] = len(result.Exchanges)
	}()
	body, err := cloneBody(definition.Request.Body)
	if err != nil || body == nil {
		result.Evidence = "GLM request must contain a JSON object"
		return result
	}
	if definition.Options["model_mode"] != "preserve" {
		body["model"] = config.Model
	}
	definition.Options = glm53CopyOptions(definition.Options)
	if tools, ok := body["tools"].([]any); ok {
		names := []string{}
		for _, item := range tools {
			tool, _ := item.(map[string]any)
			function, _ := tool["function"].(map[string]any)
			if name, ok := function["name"].(string); ok {
				names = append(names, name)
			}
		}
		definition.Options["declared_tool_names"] = names
	}
	if length, ok := definition.Options["unique_request_id_length"].(float64); ok {
		if length < 6 || length > 64 || math.Trunc(length) != length {
			result.Evidence = "invalid request ID fixture length"
			return result
		}
		body["request_id"] = glm53RequestID(int(length))
	}
	if definition.Options["unicode_request_id"] == true {
		value := []rune(glm53RequestID(6))
		for i := range value {
			value[i] += 0x4e00
		}
		body["request_id"] = string(value)
	}
	if definition.Kind == "glm53_auth_rejected" {
		switch definition.Options["auth_mode"] {
		case "missing":
			config.APIKey = ""
		case "invalid":
			config.APIKey = "glm-audit-invalid-credential"
		default:
			result.Evidence = "missing GLM authentication fixture mode"
			return result
		}
	}
	exchange, raw, err := performRequest(ctx, doer, config, definition.Request, body)
	result.Exchanges = append(result.Exchanges, exchange)
	result.HTTPStatus = exchange.StatusCode
	if err != nil {
		result.Evidence = "GLM transport failed"
		return result
	}
	if definition.Kind == "glm53_rejected" || definition.Kind == "glm53_auth_rejected" {
		if err := glm53Rejection(exchange.StatusCode, raw, definition.Options); err != nil {
			result.Evidence = err.Error()
			return result
		}
		result.Status, result.Evidence = StatusPass, "documented GLM HTTP status and business error code matched"
		return result
	}
	if exchange.StatusCode != 200 {
		result.Evidence = fmt.Sprintf("expected GLM HTTP 200, got %d", exchange.StatusCode)
		return result
	}
	if definition.Kind == "glm53_stream" {
		usage, err := glm53Stream(raw, definition.Options)
		result.Usage = usage
		if err != nil {
			result.Evidence = err.Error()
			return result
		}
		result.Status, result.Evidence = StatusPass, "GLM stream completed with final business output, usage and DONE"
		return result
	}
	response := map[string]any{}
	if err := json.Unmarshal(raw, &response); err != nil {
		result.Evidence = "invalid GLM JSON response"
		return result
	}
	options := definition.Options
	if definition.Kind == "glm53_tool_roundtrip" {
		options = glm53CopyOptions(options)
		options["allow_tools"] = true
	}
	if err := glm53Response(response, options); err != nil {
		result.Evidence = err.Error()
		return result
	}
	result.Usage, _ = response["usage"].(map[string]any)
	if definition.Options["expect_model"] == true && response["model"] != config.Model {
		result.Evidence = "response model does not match the selected upstream model"
		return result
	}
	isReplay := definition.Kind == "glm53_replay" || definition.Kind == "glm53_tool_roundtrip"
	if isReplay {
		if err := glm53Replay(ctx, doer, config, definition, body, response, &result); err != nil {
			result.Evidence = err.Error()
			return result
		}
	}
	result.Status, result.Evidence = StatusPass, "GLM final business output and response contract validated"
	return result
}

func glm53RequestID(length int) string {
	// Combine enough independent text for the longest (64-character) fixture.
	return (rand.Text() + rand.Text() + rand.Text())[:length]
}

func glm53CopyOptions(options map[string]any) map[string]any {
	copy := make(map[string]any, len(options))
	for key, value := range options {
		copy[key] = value
	}
	return copy
}

func glm53Rejection(status int, raw []byte, options map[string]any) error {
	expected, ok := options["expected_http_status"].(float64)
	if !ok || float64(status) != expected {
		return fmt.Errorf("GLM error HTTP status mismatch: got %d", status)
	}
	response := map[string]any{}
	if err := json.Unmarshal(raw, &response); err != nil {
		return errors.New("GLM rejection must be a JSON error object")
	}
	errorObject, _ := response["error"].(map[string]any)
	code, _ := errorObject["code"].(string)
	message, _ := errorObject["message"].(string)
	if strings.TrimSpace(message) == "" {
		return errors.New("GLM rejection is missing error.message")
	}
	codes, _ := options["error_codes"].([]any)
	for _, allowed := range codes {
		if allowed == code && code != "" {
			return nil
		}
	}
	return errors.New("GLM business error code is outside the expected contract class")
}

func glm53Integer(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok && number >= 0 && !math.IsInf(number, 0) && math.Trunc(number) == number
}

func glm53Usage(usage map[string]any, options map[string]any) error {
	prompt, pOK := glm53Integer(usage["prompt_tokens"])
	completion, cOK := glm53Integer(usage["completion_tokens"])
	total, tOK := glm53Integer(usage["total_tokens"])
	valid := pOK && cOK && tOK && prompt > 0 && total == prompt+completion
	if !valid {
		return errors.New("GLM usage must contain nonnegative integer counters with correct arithmetic")
	}
	if maximum, ok := options["max_completion_tokens"].(float64); ok && completion > maximum {
		return errors.New("GLM completion usage exceeds the requested output budget")
	}
	details, present := usage["prompt_tokens_details"].(map[string]any)
	if options["require_cached_tokens"] == true || present {
		cached, ok := glm53Integer(details["cached_tokens"])
		if !ok || cached > prompt {
			return errors.New("GLM cached_tokens must be within the prompt token budget")
		}
	}
	return nil
}

func glm53Response(response map[string]any, options map[string]any) error {
	for _, field := range []string{"id", "request_id", "model"} {
		if value, ok := response[field].(string); !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("GLM response %s is missing", field)
		}
	}
	if created, ok := glm53Integer(response["created"]); !ok || created == 0 {
		return errors.New("GLM response created must be a positive Unix timestamp")
	}
	choices, _ := response["choices"].([]any)
	if len(choices) != 1 {
		return errors.New("GLM response must have one choice")
	}
	choice, _ := choices[0].(map[string]any)
	if index, ok := glm53Integer(choice["index"]); !ok || index != 0 {
		return errors.New("GLM choice index must be zero")
	}
	message, _ := choice["message"].(map[string]any)
	if err := glm53Business(message, choice["finish_reason"], options); err != nil {
		return err
	}
	usage, _ := response["usage"].(map[string]any)
	return glm53Usage(usage, options)
}

func glm53Business(message map[string]any, finish any, options map[string]any) error {
	if message["role"] != "assistant" {
		return errors.New("GLM response role must be assistant")
	}
	if options["allow_length"] == true {
		if finish != "length" {
			return errors.New("GLM tiny output budget must terminate with length")
		}
		return nil
	}
	reasoning, _ := message["reasoning_content"].(string)
	if strings.TrimSpace(reasoning) == "" {
		return errors.New("GLM reasoning_content is missing")
	}
	toolName, _ := options["expected_tool_name"].(string)
	calls, _ := message["tool_calls"].([]any)
	if toolName != "" || len(calls) > 0 {
		if finish != "tool_calls" || len(calls) == 0 {
			return errors.New("GLM tool call must terminate with tool_calls and include calls")
		}
		if options["allow_tools"] != true && toolName == "" {
			return errors.New("GLM returned unexpected tool calls")
		}
		return glm53Tools(calls, options)
	}
	content, _ := message["content"].(string)
	if finish != "stop" || strings.TrimSpace(content) == "" {
		return errors.New("GLM final answer must contain content and terminate with stop")
	}
	if expected, ok := options["expected_exact"].(string); ok && strings.TrimSpace(content) != expected {
		return errors.New("GLM final answer does not match the expected fixture")
	}
	if forbidden, ok := options["forbidden_text"].(string); ok && strings.Contains(content, forbidden) {
		return errors.New("GLM output contains the configured stop delimiter")
	}
	if expected, ok := options["expected_json"].(map[string]any); ok {
		object := map[string]any{}
		if json.Unmarshal([]byte(content), &object) != nil || !glm53EqualJSON(object, expected) {
			return errors.New("GLM structured output does not match the expected JSON object")
		}
	}
	return nil
}

func glm53Tools(calls []any, options map[string]any) error {
	seen := map[string]bool{}
	for _, item := range calls {
		call, _ := item.(map[string]any)
		id, _ := call["id"].(string)
		if strings.TrimSpace(id) == "" || seen[id] || call["type"] != "function" {
			return errors.New("GLM function calls require unique IDs and type=function")
		}
		seen[id] = true
		function, _ := call["function"].(map[string]any)
		name, _ := function["name"].(string)
		if strings.TrimSpace(name) == "" {
			return errors.New("GLM function call name is missing")
		}
		if expected, ok := options["expected_tool_name"].(string); ok && name != expected {
			return errors.New("GLM returned an unexpected fixture function")
		}
		if declared, ok := options["declared_tool_names"].([]string); ok && !slices.Contains(declared, name) {
			return errors.New("GLM returned a function absent from the request")
		}
		arguments, ok := function["arguments"].(string)
		object := map[string]any{}
		if !ok || json.Unmarshal([]byte(arguments), &object) != nil || object == nil {
			return errors.New("GLM function arguments must encode a JSON object")
		}
		if expected, ok := options["expected_arguments"].(map[string]any); ok && !glm53EqualJSON(object, expected) {
			return errors.New("GLM fixture function arguments do not match")
		}
	}
	return nil
}

func glm53EqualJSON(left, right map[string]any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func glm53Replay(
	ctx context.Context,
	doer HTTPDoer,
	config RunConfig,
	definition CaseDefinition,
	body, response map[string]any,
	result *CaseResult,
) error {
	// Each exchange owns its request snapshot; the second turn must not mutate the first.
	body, err := cloneBody(body)
	if err != nil {
		return errors.New("GLM replay could not clone the first request")
	}
	choices, _ := response["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	messages, _ := body["messages"].([]any)
	// Replay the complete original assistant message, including reasoning and call IDs.
	messages = append(messages, message)
	expected := "GLM_REPLAY_OK"
	prompt := "Reply only with GLM_REPLAY_OK."
	if definition.Kind == "glm53_tool_roundtrip" {
		calls, _ := message["tool_calls"].([]any)
		if len(calls) != 1 {
			return errors.New("GLM deterministic tool roundtrip requires exactly one call")
		}
		call, _ := calls[0].(map[string]any)
		fixture, _ := definition.Options["tool_result"].(string)
		expected, _ = definition.Options["expected_final"].(string)
		if fixture == "" || expected == "" {
			return errors.New("GLM roundtrip fixture is missing")
		}
		messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": fixture})
		prompt = "Reply only with the fixture value returned by the tool, without explanation."
	}
	messages = append(messages, map[string]any{"role": "user", "content": prompt})
	body["messages"] = messages
	delete(body, "tools")
	delete(body, "tool_choice")
	if _, exists := body["request_id"]; exists {
		body["request_id"] = glm53RequestID(32)
	}
	exchange, raw, err := performRequest(ctx, doer, config, definition.Request, body)
	result.Exchanges = append(result.Exchanges, exchange)
	result.HTTPStatus = exchange.StatusCode
	if err != nil || exchange.StatusCode != 200 {
		return errors.New("GLM replay request failed")
	}
	final := map[string]any{}
	if err := json.Unmarshal(raw, &final); err != nil {
		return errors.New("GLM replay response is invalid JSON")
	}
	if err := glm53Response(final, map[string]any{"expected_exact": expected}); err != nil {
		return err
	}
	finalUsage, _ := final["usage"].(map[string]any)
	combined := map[string]any{}
	for _, field := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		first, _ := result.Usage[field].(float64)
		last, _ := finalUsage[field].(float64)
		combined[field] = first + last
	}
	result.Usage = combined
	return nil
}

func glm53Stream(raw []byte, options map[string]any) (map[string]any, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	message := map[string]any{"role": "assistant"}
	usage := map[string]any{}
	calls := map[int]map[string]any{}
	var content, reasoning strings.Builder
	var finish, id, model string
	done, finished := false, false
	frames := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if done {
			return usage, errors.New("GLM SSE data arrived after DONE")
		}
		if data == "[DONE]" {
			done = true
			continue
		}
		frame := map[string]any{}
		if json.Unmarshal([]byte(data), &frame) != nil {
			return usage, errors.New("GLM SSE contains invalid JSON")
		}
		frames++
		if _, exists := frame["error"]; exists {
			return usage, errors.New("GLM SSE contains a provider error")
		}
		if value, exists := frame["usage"]; exists && value != nil {
			var ok bool
			usage, ok = value.(map[string]any)
			if !ok {
				return usage, errors.New("GLM SSE usage must be an object")
			}
		}
		choices, _ := frame["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		if len(choices) != 1 {
			return usage, errors.New("GLM SSE has unexpected choice count")
		}
		frameID, _ := frame["id"].(string)
		frameModel, _ := frame["model"].(string)
		if frameID == "" || frameModel == "" || (id != "" && id != frameID) || (model != "" && model != frameModel) {
			return usage, errors.New("GLM SSE identity is missing or changed")
		}
		id, model = frameID, frameModel
		if created, ok := glm53Integer(frame["created"]); !ok || created == 0 {
			return usage, errors.New("GLM SSE created must be a positive Unix timestamp")
		}
		choice, _ := choices[0].(map[string]any)
		if index, ok := glm53Integer(choice["index"]); !ok || index != 0 {
			return usage, errors.New("GLM SSE choice index must be zero")
		}
		delta, _ := choice["delta"].(map[string]any)
		for _, field := range []string{"content", "reasoning_content"} {
			if value, exists := delta[field]; exists && value != nil {
				if _, ok := value.(string); !ok {
					return usage, errors.New("GLM SSE text delta has the wrong type")
				}
			}
		}
		text, _ := delta["content"].(string)
		thought, _ := delta["reasoning_content"].(string)
		fragments, _ := delta["tool_calls"].([]any)
		if finished && (text != "" || thought != "" || len(fragments) > 0) {
			return usage, errors.New("GLM SSE content arrived after finish_reason")
		}
		if role, exists := delta["role"]; exists && role != "assistant" {
			return usage, errors.New("GLM SSE returned a non-assistant role")
		}
		content.WriteString(text)
		reasoning.WriteString(thought)
		if err := glm53ToolFragments(calls, fragments); err != nil {
			return usage, err
		}
		if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
			if finished {
				return usage, errors.New("GLM SSE contains multiple finish reasons")
			}
			finish, finished = reason, true
		}
	}
	if scanner.Err() != nil || !done || !finished || frames < 2 {
		return usage, errors.New("GLM SSE is incomplete: requires frames, terminal finish and DONE")
	}
	message["content"], message["reasoning_content"] = content.String(), reasoning.String()
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	assembled := make([]any, 0, len(indexes))
	for index, value := range indexes {
		if index != value {
			return usage, errors.New("GLM SSE tool call indexes are not contiguous")
		}
		assembled = append(assembled, calls[value])
	}
	message["tool_calls"] = assembled
	if err := glm53Business(message, finish, options); err != nil {
		return usage, err
	}
	return usage, glm53Usage(usage, options)
}

func glm53ToolFragments(calls map[int]map[string]any, fragments []any) error {
	for _, item := range fragments {
		fragment, _ := item.(map[string]any)
		index, ok := glm53Integer(fragment["index"])
		if !ok || index > 127 {
			return errors.New("GLM SSE tool index is invalid")
		}
		call, exists := calls[int(index)]
		if !exists {
			call = map[string]any{"function": map[string]any{"name": "", "arguments": ""}}
			calls[int(index)] = call
		}
		for _, field := range []string{"id", "type"} {
			if value, exists := fragment[field]; exists && value != nil && value != "" {
				text, ok := value.(string)
				if !ok || (call[field] != nil && call[field] != text) {
					return errors.New("GLM SSE tool identity changed or has the wrong type")
				}
				call[field] = text
			}
		}
		function, _ := fragment["function"].(map[string]any)
		assembled := call["function"].(map[string]any)
		for _, field := range []string{"name", "arguments"} {
			if value, exists := function[field]; exists && value != nil {
				text, ok := value.(string)
				if !ok {
					return errors.New("GLM SSE function fragment must be a string")
				}
				assembled[field] = assembled[field].(string) + text
			}
		}
	}
	return nil
}
