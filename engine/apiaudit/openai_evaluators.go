package apiaudit

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test/engine/common"
)

var blackBoxEvaluatorKinds = map[string]bool{
	"usage_baseline": true, "stream_parity": true, "sse_integrity": true,
	"reasoning_visibility": true, "logprobs_contract": true,
	"deterministic_stability": true, "multi_turn_usage": true,
	"structured_stability": true, "concurrency_probe": true,
	"stream_ttft": true, "long_stream_stability": true,
	"cache_visibility": true, "stream_throughput": true,
	"sampling_effect": true, "parameter_boundaries": true,
	"usage_growth": true, "needle_retrieval": true,
	"error_no_usage": true, "padding_ratio": true,
}

func runOpenAIBlackBoxEvaluator(ctx context.Context, doer HTTPDoer, config RunConfig, definition CaseDefinition) (result CaseResult) {
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

	observeOneSync := func(requestBody map[string]any) (syncObservation, bool) {
		observation, observeErr := observeSync(ctx, doer, config, definition.Request, requestBody)
		result.Exchanges = append(result.Exchanges, observation.Exchange)
		result.HTTPStatus = observation.StatusCode
		if observeErr != nil {
			result.Status, result.Evidence = StatusFail, "request failed: "+observeErr.Error()
			return observation, false
		}
		result.Usage = observation.Usage
		return observation, true
	}
	observeOneStream := func(requestBody map[string]any) (streamObservation, bool) {
		observation, observeErr := observeStream(ctx, doer, config, definition.Request, requestBody)
		result.Exchanges = append(result.Exchanges, observation.Exchange)
		result.HTTPStatus = observation.StatusCode
		if observeErr != nil {
			result.Status, result.Evidence = StatusFail, "stream request failed: "+observeErr.Error()
			return observation, false
		}
		result.Usage = observation.Usage
		return observation, true
	}

	switch definition.Kind {
	case "usage_baseline", "usage_growth":
		shortPrompt := stringOption(definition.Options, "short_prompt", "仅输出 USAGE_OK")
		longPrompt := stringOption(definition.Options, "long_prompt", strings.Repeat("black-box usage probe ", 128)+"仅输出 USAGE_OK")
		shortBody := bodyWithUserPrompt(body, shortPrompt)
		longBody := bodyWithUserPrompt(body, longPrompt)
		shortObservation, ok := observeOneSync(shortBody)
		if !ok || !requireSuccessfulSync(&result, shortObservation, "short prompt") {
			return result
		}
		longObservation, ok := observeOneSync(longBody)
		if !ok || !requireSuccessfulSync(&result, longObservation, "long prompt") {
			return result
		}
		shortUsage, shortOK := usageNumber(shortObservation.Usage, "prompt_tokens")
		longUsage, longOK := usageNumber(longObservation.Usage, "prompt_tokens")
		result.Metrics["short_prompt_usage"] = shortUsage
		result.Metrics["long_prompt_usage"] = longUsage
		if !shortOK || !longOK {
			result.Status, result.Evidence = StatusUnknown, "prompt usage is not exposed for both black-box requests"
			return result
		}
		if shortUsage < 0 || longUsage < 0 || longUsage <= shortUsage {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("long prompt usage %.0f did not exceed short prompt usage %.0f", longUsage, shortUsage)
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("prompt usage increased from %.0f to %.0f for a longer request", shortUsage, longUsage)
		return result

	case "stream_parity":
		syncBody, _ := cloneBody(body)
		delete(syncBody, "stream")
		delete(syncBody, "stream_options")
		streamBody, _ := cloneBody(body)
		streamBody["stream"] = true
		streamBody["stream_options"] = map[string]any{"include_usage": true}
		syncObservation, ok := observeOneSync(syncBody)
		if !ok || !requireSuccessfulSync(&result, syncObservation, "non-stream request") {
			return result
		}
		streamObservation, ok := observeOneStream(streamBody)
		if !ok || !requireValidStream(&result, streamObservation) {
			return result
		}
		result.Metrics["sync_content_bytes"] = len(syncObservation.Content)
		result.Metrics["stream_content_bytes"] = len(streamObservation.Content)
		if syncObservation.Content != streamObservation.Content {
			result.Status, result.Evidence = StatusFail, "stream and non-stream deterministic content differ"
			return result
		}
		syncTotal, syncOK := usageNumber(syncObservation.Usage, "total_tokens")
		streamTotal, streamOK := usageNumber(streamObservation.Usage, "total_tokens")
		if !syncOK || !streamOK {
			result.Status, result.Evidence = StatusUnknown, "stream/non-stream content matched but comparable total usage is unavailable"
			return result
		}
		result.Metrics["sync_total_usage"] = syncTotal
		result.Metrics["stream_total_usage"] = streamTotal
		if syncTotal != streamTotal {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("stream total usage %.0f differs from non-stream %.0f", streamTotal, syncTotal)
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("stream/non-stream content and total usage match at %.0f", syncTotal)
		return result

	case "sse_integrity":
		streamBody, _ := cloneBody(body)
		streamBody["stream"] = true
		observation, ok := observeOneStream(streamBody)
		if !ok || !requireValidStream(&result, observation) {
			return result
		}
		result.Metrics["frames"] = observation.Frames
		result.Metrics["response_ids"] = len(observation.IDs)
		if len(observation.IDs) > 1 {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("stream used %d response IDs", len(observation.IDs))
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("SSE stream completed with %d frames, one finish reason, and [DONE]", observation.Frames)
		return result

	case "stream_ttft", "long_stream_stability", "stream_throughput":
		repetitions := repetitionsOption(definition.Options, 3)
		firstFrames := make([]int64, 0, repetitions)
		throughputs := make([]float64, 0, repetitions)
		for i := 0; i < repetitions; i++ {
			streamBody, _ := cloneBody(body)
			streamBody["stream"] = true
			observation, ok := observeOneStream(streamBody)
			if !ok || !requireValidStream(&result, observation) {
				return result
			}
			firstFrames = append(firstFrames, observation.FirstFrameMS)
			seconds := math.Max(float64(observation.ElapsedMS)/1000, 0.001)
			throughputs = append(throughputs, float64(len(observation.Content)+len(observation.Reasoning))/seconds)
		}
		result.Metrics["first_frame_ms"] = firstFrames
		result.Metrics["characters_per_second"] = throughputs
		if definition.Kind == "stream_ttft" {
			sorted := append([]int64(nil), firstFrames...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
			median := sorted[len(sorted)/2]
			maximum := int64(numberOption(definition.Options, "max_first_frame_ms", 5000))
			result.Metrics["median_first_frame_ms"] = median
			result.Metrics["max_first_frame_ms"] = maximum
			if median > maximum {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("median first SSE frame %dms exceeds %dms", median, maximum)
				return result
			}
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("median first SSE frame %dms is within %dms", median, maximum)
			return result
		}
		if definition.Kind == "stream_throughput" {
			for _, throughput := range throughputs {
				if throughput <= 0 {
					result.Status, result.Evidence = StatusFail, "stream produced no observable character throughput"
					return result
				}
			}
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d streams exposed positive character delivery rates", repetitions)
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d long streams completed with content, finish reason, and [DONE]", repetitions)
		return result

	case "reasoning_visibility":
		observation, ok := observeOneSync(body)
		if !ok {
			return result
		}
		if observation.StatusCode < 200 || observation.StatusCode >= 300 {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("reasoning visibility request returned HTTP %d", observation.StatusCode)
			return result
		}
		visible := hasNonEmptyField(observation.Message, "reasoning_content") || hasNonEmptyField(observation.Message, "reasoning") || findPositiveNumber(observation.Usage, "reasoning_tokens")
		result.Metrics["reasoning_visible"] = visible
		if visible {
			result.Status, result.Evidence = StatusPass, "reasoning metadata is observable in the black-box response"
		} else {
			result.Status, result.Evidence = StatusWarning, "completion succeeded but no reasoning metadata is exposed"
		}
		return result

	case "logprobs_contract":
		requestBody, _ := cloneBody(body)
		requestBody["logprobs"] = true
		requestBody["top_logprobs"] = 1
		observation, ok := observeOneSync(requestBody)
		if !ok {
			return result
		}
		if observation.StatusCode >= 400 && observation.StatusCode < 500 {
			result.Status, result.Evidence = StatusWarning, fmt.Sprintf("logprobs is not supported: HTTP %d", observation.StatusCode)
			return result
		}
		if observation.StatusCode < 200 || observation.StatusCode >= 300 {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("logprobs request returned HTTP %d", observation.StatusCode)
			return result
		}
		choices, _ := observation.Response["choices"].([]any)
		choice, _ := choices[0].(map[string]any)
		logprobs, ok := choice["logprobs"].(map[string]any)
		content, contentOK := logprobs["content"].([]any)
		if !ok || !contentOK || len(content) == 0 {
			result.Status, result.Evidence = StatusFail, "successful logprobs response has no logprobs.content entries"
			return result
		}
		result.Metrics["logprob_entries"] = len(content)
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("logprobs contract returned %d content entries", len(content))
		return result

	case "cache_visibility":
		repetitions := repetitionsOption(definition.Options, 2)
		cached := false
		for i := 0; i < repetitions; i++ {
			observation, ok := observeOneSync(body)
			if !ok || !requireSuccessfulSync(&result, observation, "cache probe") {
				return result
			}
			cached = cached || findPositiveNumber(observation.Usage, "cached_tokens") || findPositiveNumber(observation.Usage, "cache_read_input_tokens")
		}
		result.Metrics["cache_usage_visible"] = cached
		if cached {
			result.Status, result.Evidence = StatusPass, "repeated prompt exposed positive cache usage"
		} else {
			result.Status, result.Evidence = StatusWarning, "repeated prompts succeeded but no standardized cache usage field is exposed"
		}
		return result

	case "deterministic_stability", "structured_stability", "sampling_effect", "padding_ratio":
		runRepeatedContentEvaluator(definition, &result, body, observeOneSync)
		return result

	case "concurrency_probe":
		return runConcurrencyEvaluator(ctx, doer, config, definition, result, body)

	case "multi_turn_usage":
		needle := stringOption(definition.Options, "needle", "AUDIT_MEMORY_7F3A")
		firstBody := bodyWithUserPrompt(body, "记住验证码 "+needle+"，仅输出该验证码")
		first, ok := observeOneSync(firstBody)
		if !ok || !requireSuccessfulSync(&result, first, "first turn") {
			return result
		}
		secondBody, _ := cloneBody(body)
		secondBody["messages"] = []any{
			map[string]any{"role": "user", "content": "记住验证码 " + needle + "，仅输出该验证码"},
			map[string]any{"role": "assistant", "content": first.Content},
			map[string]any{"role": "user", "content": "刚才的验证码是什么？仅输出验证码"},
		}
		second, ok := observeOneSync(secondBody)
		if !ok || !requireSuccessfulSync(&result, second, "second turn") {
			return result
		}
		firstUsage, firstOK := usageNumber(first.Usage, "prompt_tokens")
		secondUsage, secondOK := usageNumber(second.Usage, "prompt_tokens")
		result.Metrics["first_prompt_usage"] = firstUsage
		result.Metrics["second_prompt_usage"] = secondUsage
		if strings.TrimSpace(first.Content) != needle || strings.TrimSpace(second.Content) != needle {
			result.Status, result.Evidence = StatusFail, "multi-turn nonce was not preserved exactly"
			return result
		}
		if !firstOK || !secondOK {
			result.Status, result.Evidence = StatusUnknown, "multi-turn content is consistent but prompt usage is unavailable"
			return result
		}
		if secondUsage <= firstUsage {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("second-turn prompt usage %.0f did not exceed first-turn %.0f", secondUsage, firstUsage)
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("multi-turn nonce preserved and prompt usage increased from %.0f to %.0f", firstUsage, secondUsage)
		return result

	case "parameter_boundaries":
		zeroBody, _ := cloneBody(body)
		zeroBody["max_tokens"] = 0
		zero, ok := observeOneSync(zeroBody)
		if !ok || !validBoundaryResponse(zero) {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("max_tokens=0 returned invalid boundary response HTTP %d", zero.StatusCode)
			return result
		}
		normalBody, _ := cloneBody(body)
		normalBody["temperature"] = 0.5
		normal, ok := observeOneSync(normalBody)
		if !ok || !requireSuccessfulSync(&result, normal, "normal sampling request") {
			return result
		}
		invalidToolBody, _ := cloneBody(body)
		invalidToolBody["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "missing_audit_tool"}}
		invalidTool, ok := observeOneSync(invalidToolBody)
		if !ok || invalidTool.StatusCode < 400 || invalidTool.StatusCode >= 500 || !hasOpenAIError(invalidTool.Response) {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("invalid tool_choice did not return OpenAI-style 4xx; HTTP %d", invalidTool.StatusCode)
			return result
		}
		result.Metrics["statuses"] = []int{zero.StatusCode, normal.StatusCode, invalidTool.StatusCode}
		result.Status, result.Evidence = StatusPass, "max_tokens, sampling, and invalid tool_choice boundaries behaved consistently"
		return result

	case "needle_retrieval":
		needle := stringOption(definition.Options, "needle", "AUDIT_NEEDLE_7F3A")
		repetitions := repetitionsOption(definition.Options, 3)
		filler := strings.Repeat("alpha beta gamma delta ", 512)
		positions := []int{0, len(filler) / 2, len(filler)}
		for i := 0; i < repetitions; i++ {
			position := positions[i%len(positions)]
			payload := filler[:position] + "\n审计验证码：" + needle + "\n" + filler[position:]
			requestBody := bodyWithUserPrompt(body, payload+"\n仅输出审计验证码")
			observation, ok := observeOneSync(requestBody)
			if !ok || !requireSuccessfulSync(&result, observation, "needle request") {
				return result
			}
			if strings.TrimSpace(observation.Content) != needle {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("needle retrieval failed at sample %d", i+1)
				return result
			}
		}
		result.Metrics["payload_characters"] = len(filler)
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("literal nonce was retrieved at %d payload positions", repetitions)
		return result

	case "error_no_usage":
		invalidBody, _ := cloneBody(body)
		invalidBody["max_tokens"] = -1
		observation, ok := observeOneSync(invalidBody)
		if !ok {
			return result
		}
		if observation.StatusCode < 400 || observation.StatusCode >= 500 || !hasOpenAIError(observation.Response) {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("invalid request did not return OpenAI-style 4xx; HTTP %d", observation.StatusCode)
			return result
		}
		if observation.Usage != nil {
			result.Status, result.Evidence = StatusWarning, "invalid request returned usage; external charging cannot be observed"
			return result
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("invalid request returned OpenAI-style HTTP %d without usage", observation.StatusCode)
		return result
	}

	result.Status, result.Evidence = StatusUnknown, "black-box evaluator is not implemented for kind "+definition.Kind
	return result
}

func runRepeatedContentEvaluator(definition CaseDefinition, result *CaseResult, body map[string]any, observeOne func(map[string]any) (syncObservation, bool)) {
	repetitions := repetitionsOption(definition.Options, 5)
	contents := make([]string, 0, repetitions)
	typeSignature := ""
	for i := 0; i < repetitions; i++ {
		requestBody, _ := cloneBody(body)
		if definition.Kind == "sampling_effect" {
			if i < repetitions/2 {
				requestBody["temperature"] = 0
			} else {
				requestBody["temperature"] = 1
			}
		}
		if definition.Kind == "padding_ratio" {
			requestBody = bodyWithUserPrompt(requestBody, strings.Repeat("padding ", 1+i%3*16)+"仅输出 PAD_OK")
		}
		observation, ok := observeOne(requestBody)
		if !ok || !requireSuccessfulSync(result, observation, "repeated request") {
			return
		}
		content := strings.TrimSpace(observation.Content)
		contents = append(contents, content)
		if expected, ok := definition.Options["expected_exact"].(string); ok && expected != "" && content != expected {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected exact content %q, got %q", expected, content)
			return
		}
		if definition.Kind == "structured_stability" {
			var object map[string]any
			if err := common.Unmarshal([]byte(content), &object); err != nil {
				result.Status, result.Evidence = StatusFail, "structured sample is not a JSON object: "+err.Error()
				return
			}
			required, _ := definition.Options["required_keys"].([]any)
			parts := make([]string, 0, len(required))
			for _, item := range required {
				key, _ := item.(string)
				value, exists := object[key]
				if !exists {
					result.Status, result.Evidence = StatusFail, "structured sample is missing key "+key
					return
				}
				parts = append(parts, fmt.Sprintf("%s:%T", key, value))
			}
			signature := strings.Join(parts, ",")
			if typeSignature != "" && signature != typeSignature {
				result.Status, result.Evidence = StatusFail, "structured value types changed across samples"
				return
			}
			typeSignature = signature
		}
		if definition.Kind == "padding_ratio" {
			maximum := numberOption(definition.Options, "max_completion_tokens", 3)
			completion, exists := usageNumber(observation.Usage, "completion_tokens")
			if !exists {
				result.Status, result.Evidence = StatusUnknown, "completion usage is unavailable for padding samples"
				return
			}
			if completion > maximum {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("completion usage %.0f exceeds %.0f", completion, maximum)
				return
			}
		}
	}
	unique := make(map[string]bool)
	for _, content := range contents {
		unique[content] = true
	}
	result.Metrics["unique_outputs"] = len(unique)
	if definition.Kind == "sampling_effect" {
		zeroUnique := make(map[string]bool)
		for _, content := range contents[:repetitions/2] {
			zeroUnique[content] = true
		}
		if len(zeroUnique) > 1 {
			result.Status, result.Evidence = StatusFail, "temperature-zero outputs changed across samples"
			return
		}
		if len(unique) == 1 {
			result.Status, result.Evidence = StatusWarning, "sampling parameters were accepted but produced no observable variation"
			return
		}
		result.Status, result.Evidence = StatusPass, fmt.Sprintf("temperature-zero output was stable and sampling produced %d variants", len(unique))
		return
	}
	if definition.Kind == "deterministic_stability" && len(unique) != 1 {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("temperature-zero requests produced %d outputs", len(unique))
		return
	}
	result.Status, result.Evidence = StatusPass, fmt.Sprintf("%d repeated samples satisfied %s", repetitions, definition.Kind)
	return
}

func runConcurrencyEvaluator(ctx context.Context, doer HTTPDoer, config RunConfig, definition CaseDefinition, result CaseResult, body map[string]any) CaseResult {
	repetitions := repetitionsOption(definition.Options, 5)
	type outcome struct {
		observation syncObservation
		err         error
	}
	outcomes := make(chan outcome, repetitions)
	for i := 0; i < repetitions; i++ {
		go func() {
			requestBody, _ := cloneBody(body)
			observation, err := observeSync(ctx, doer, config, definition.Request, requestBody)
			outcomes <- outcome{observation: observation, err: err}
		}()
	}
	statusCounts := make(map[int]int)
	for i := 0; i < repetitions; i++ {
		current := <-outcomes
		result.Exchanges = append(result.Exchanges, current.observation.Exchange)
		result.HTTPStatus = current.observation.StatusCode
		if current.err != nil {
			result.Status, result.Evidence = StatusFail, "concurrent request failed: "+current.err.Error()
			return result
		}
		statusCounts[current.observation.StatusCode]++
	}
	result.Metrics["status_counts"] = statusCounts
	if statusCounts[http.StatusTooManyRequests] > 0 {
		result.Status, result.Evidence = StatusWarning, fmt.Sprintf("%d of %d concurrent requests returned HTTP 429", statusCounts[http.StatusTooManyRequests], repetitions)
		return result
	}
	for status := range statusCounts {
		if status < 200 || status >= 300 {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("concurrent probe returned HTTP %d", status)
			return result
		}
	}
	result.Status, result.Evidence = StatusPass, fmt.Sprintf("all %d concurrent requests returned 2xx", repetitions)
	return result
}

func requireSuccessfulSync(result *CaseResult, observation syncObservation, label string) bool {
	if observation.StatusCode < 200 || observation.StatusCode >= 300 {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("%s returned HTTP %d", label, observation.StatusCode)
		return false
	}
	if strings.TrimSpace(observation.Content) == "" && strings.TrimSpace(observation.Reasoning) == "" {
		result.Status, result.Evidence = StatusFail, label+" returned no assistant content or reasoning"
		return false
	}
	return true
}

func requireValidStream(result *CaseResult, observation streamObservation) bool {
	if observation.StatusCode < 200 || observation.StatusCode >= 300 {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("stream returned HTTP %d", observation.StatusCode)
		return false
	}
	if observation.Frames < 2 || (strings.TrimSpace(observation.Content) == "" && strings.TrimSpace(observation.Reasoning) == "") || observation.FinishCount != 1 || !observation.Done {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("incomplete SSE: frames=%d content=%d reasoning=%d finish=%d done=%t", observation.Frames, len(observation.Content), len(observation.Reasoning), observation.FinishCount, observation.Done)
		return false
	}
	return true
}

func validBoundaryResponse(observation syncObservation) bool {
	if observation.StatusCode >= 200 && observation.StatusCode < 300 {
		return observation.Content == ""
	}
	return observation.StatusCode >= 400 && observation.StatusCode < 500 && hasOpenAIError(observation.Response)
}

func hasOpenAIError(response map[string]any) bool {
	errorObject, _ := response["error"].(map[string]any)
	message, _ := errorObject["message"].(string)
	return strings.TrimSpace(message) != ""
}

func usageNumber(usage map[string]any, key string) (float64, bool) {
	if usage == nil {
		return 0, false
	}
	value, ok := usage[key].(float64)
	return value, ok
}

func findPositiveNumber(value any, target string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == target {
				if number, ok := item.(float64); ok && number > 0 {
					return true
				}
			}
			if findPositiveNumber(item, target) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if findPositiveNumber(item, target) {
				return true
			}
		}
	}
	return false
}

func hasNonEmptyField(value map[string]any, key string) bool {
	if value == nil {
		return false
	}
	switch typed := value[key].(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return false
	}
}

func bodyWithUserPrompt(body map[string]any, prompt string) map[string]any {
	cloned, _ := cloneBody(body)
	cloned["messages"] = []any{map[string]any{"role": "user", "content": prompt}}
	return cloned
}

func repetitionsOption(options map[string]any, fallback int) int {
	value, ok := options["repetitions"].(float64)
	if !ok || value < 1 || value > 10 {
		return fallback
	}
	return int(value)
}

func numberOption(options map[string]any, key string, fallback float64) float64 {
	value, ok := options[key].(float64)
	if !ok {
		return fallback
	}
	return value
}

func stringOption(options map[string]any, key, fallback string) string {
	value, ok := options[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
