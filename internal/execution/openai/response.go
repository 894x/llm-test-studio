package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/execution/load"
)

const streamReadBufferBytes = 4 << 10

var errSSELineTooLarge = errors.New("SSE line exceeds parser budget")

func readSynchronous(state *executionState, body io.Reader, endpoint endpointKind, observation load.Observation) load.Observation {
	encoded, tooLarge, err := readBounded(body, state.maxResponseBytes)
	if tooLarge {
		observation.ErrorCode = load.ErrorResponseTooLarge
		return observation
	}
	if err != nil {
		observation.ErrorCode, observation.TimedOut = state.classify(err)
		return observation
	}
	if code, timedOut, classified := state.contextClassification(); classified {
		observation.ErrorCode, observation.TimedOut = code, timedOut
		return observation
	}
	object, err := decodeObject(encoded)
	if err != nil {
		observation.ErrorCode = load.ErrorProtocol
		return observation
	}
	usage, err := parseUsage(object)
	if err != nil {
		observation.ErrorCode = load.ErrorProtocol
		return observation
	}
	semantic, err := validateResponseSchema(object, endpoint, false)
	if err != nil {
		observation.ErrorCode = load.ErrorProtocol
		return observation
	}
	usage.apply(&observation)
	if endpoint == endpointChatCompletions && !semantic {
		observation.ErrorCode = load.ErrorSemanticEmpty
		return observation
	}
	observation.StreamComplete = true
	observation.Success = true
	return observation
}

func readStream(state *executionState, body io.Reader, endpoint endpointKind, started time.Time, observation load.Observation) load.Observation {
	reader := bufio.NewReaderSize(body, streamReadBufferBytes)
	var event bytes.Buffer
	var totalBytes int64
	var lineCount int64
	seenEnvelope := false
	seenSemantic := false
	var firstText time.Duration
	var lastText time.Duration

	flush := func() (done bool, valid bool) {
		if event.Len() == 0 {
			return false, true
		}
		eventAt := time.Since(started)
		if eventAt <= 0 {
			eventAt = time.Nanosecond
		}
		payload := bytes.TrimSpace(event.Bytes())
		if bytes.Equal(payload, []byte("[DONE]")) {
			event.Reset()
			return true, true
		}
		object, err := decodeObject(payload)
		if err != nil {
			observation.ErrorCode = load.ErrorProtocol
			return false, false
		}
		usage, err := parseUsage(object)
		if err != nil {
			observation.ErrorCode = load.ErrorProtocol
			return false, false
		}
		semantic, err := validateResponseSchema(object, endpoint, true)
		if err != nil {
			observation.ErrorCode = load.ErrorProtocol
			return false, false
		}
		usage.apply(&observation)
		seenEnvelope = true
		seenSemantic = seenSemantic || semantic
		text, visible := chatStreamTextEvent(object, endpoint)
		if text {
			observation.SemanticChunkCount++
			if observation.SemanticChunkCount == 1 {
				firstText = eventAt
				observation.TTFTAny = eventAt
				observation.TTFT = eventAt
			}
			if visible && observation.TTFTVisible == 0 {
				observation.TTFTVisible = eventAt
			}
			if observation.SemanticChunkCount == 2 {
				observation.TTST = eventAt
			}
			lastText = eventAt
			if observation.SemanticChunkCount >= 2 {
				observation.ObservedICL = (lastText - firstText) / time.Duration(observation.SemanticChunkCount-1)
			}
		}
		event.Reset()
		return false, true
	}

	finishDone := func() load.Observation {
		if !seenEnvelope {
			observation.ErrorCode = load.ErrorProtocol
			return observation
		}
		observation.StreamComplete = true
		if endpoint == endpointChatCompletions && !seenSemantic {
			observation.ErrorCode = load.ErrorSemanticEmpty
			return observation
		}
		observation.Success = true
		return observation
	}

	for {
		line, atEOF, err := readPhysicalLine(reader, state.maxSSELineBytes)
		if errors.Is(err, errSSELineTooLarge) {
			observation.ErrorCode = load.ErrorResponseTooLarge
			return observation
		}
		if err != nil {
			observation.ErrorCode, observation.TimedOut = state.classify(err)
			return observation
		}
		if len(line) > 0 {
			if int64(len(line)) > state.maxResponseBytes-totalBytes {
				observation.ErrorCode = load.ErrorResponseTooLarge
				return observation
			}
			totalBytes += int64(len(line))
			lineCount++
			if lineCount > state.maxSSELines {
				observation.ErrorCode = load.ErrorResponseTooLarge
				return observation
			}

			line = bytes.TrimSuffix(line, []byte("\n"))
			line = bytes.TrimSuffix(line, []byte("\r"))
			switch {
			case len(line) == 0:
				done, valid := flush()
				if !valid {
					return observation
				}
				if done {
					return finishDone()
				}
			case line[0] == ':':
				// SSE comments are deliberately ignored.
			case bytes.Equal(line, []byte("data")):
				if !appendEventData(&event, nil, state.maxSSEEventBytes) {
					observation.ErrorCode = load.ErrorResponseTooLarge
					return observation
				}
			case bytes.HasPrefix(line, []byte("data:")):
				payload := line[len("data:"):]
				if len(payload) > 0 && payload[0] == ' ' {
					payload = payload[1:]
				}
				if !appendEventData(&event, payload, state.maxSSEEventBytes) {
					observation.ErrorCode = load.ErrorResponseTooLarge
					return observation
				}
			}
		}
		if atEOF {
			if code, timedOut, classified := state.contextClassification(); classified {
				observation.ErrorCode, observation.TimedOut = code, timedOut
				return observation
			}
			done, valid := flush()
			if !valid {
				return observation
			}
			if done {
				return finishDone()
			}
			observation.ErrorCode = load.ErrorIncompleteStream
			return observation
		}
	}
}

func chatStreamTextEvent(object map[string]any, endpoint endpointKind) (text bool, visible bool) {
	if endpoint != endpointChatCompletions {
		return false, false
	}
	choices, ok := object["choices"].([]any)
	if !ok {
		return false, false
	}
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			text = true
			visible = true
		}
		for _, field := range []string{"reasoning_content", "reasoning"} {
			if reasoning, ok := delta[field].(string); ok && reasoning != "" {
				text = true
			}
		}
	}
	return text, visible
}

func readPhysicalLine(reader *bufio.Reader, limit int64) ([]byte, bool, error) {
	capacity := streamReadBufferBytes
	if limit < int64(capacity) {
		capacity = int(limit)
	}
	line := make([]byte, 0, capacity)
	for {
		fragment, err := reader.ReadSlice('\n')
		if int64(len(fragment)) > limit-int64(len(line)) {
			return nil, false, errSSELineTooLarge
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, true, nil
		default:
			return nil, false, err
		}
	}
}

func appendEventData(event *bytes.Buffer, payload []byte, limit int64) bool {
	additional := int64(len(payload))
	if event.Len() > 0 {
		additional++
	}
	if additional > limit-int64(event.Len()) {
		return false
	}
	if event.Len() > 0 {
		event.WriteByte('\n')
	}
	event.Write(payload)
	return true
}

func decodeObject(encoded []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("JSON value is not an object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("JSON contains trailing values")
	}
	return object, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	encoded, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(encoded)) > limit {
		return nil, true, nil
	}
	return encoded, false, nil
}

type usageUpdate struct {
	prompt     *uint64
	completion *uint64
	cached     *uint64
}

func parseUsage(object map[string]any) (usageUpdate, error) {
	var update usageUpdate
	rawUsage, exists := object["usage"]
	if !exists || rawUsage == nil {
		return update, nil
	}
	usage, ok := rawUsage.(map[string]any)
	if !ok {
		return update, errors.New("usage must be an object")
	}
	var err error
	if update.prompt, err = optionalTokenCount(usage, "prompt_tokens"); err != nil {
		return usageUpdate{}, err
	}
	if update.completion, err = optionalTokenCount(usage, "completion_tokens"); err != nil {
		return usageUpdate{}, err
	}
	if _, err = optionalTokenCount(usage, "total_tokens"); err != nil {
		return usageUpdate{}, err
	}
	if rawDetails, exists := usage["prompt_tokens_details"]; exists && rawDetails != nil {
		details, ok := rawDetails.(map[string]any)
		if !ok {
			return usageUpdate{}, errors.New("prompt token details must be an object")
		}
		if update.cached, err = optionalTokenCount(details, "cached_tokens"); err != nil {
			return usageUpdate{}, err
		}
	}
	return update, nil
}

func optionalTokenCount(object map[string]any, field string) (*uint64, error) {
	value, exists := object[field]
	if !exists {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, errors.New("token count must be an integer")
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 {
		return nil, errors.New("token count must be a non-negative integer")
	}
	converted := uint64(parsed)
	return &converted, nil
}

func (update usageUpdate) apply(observation *load.Observation) {
	if update.prompt != nil {
		observation.PromptTokens = *update.prompt
	}
	if update.completion != nil {
		observation.CompletionTokens = *update.completion
	}
	if update.cached != nil {
		observation.CachedTokens = *update.cached
	}
}

func validateResponseSchema(object map[string]any, endpoint endpointKind, stream bool) (bool, error) {
	switch endpoint {
	case endpointChatCompletions:
		return validateChatResponse(object, stream)
	case endpointModels:
		if stream {
			return false, errors.New("models response cannot be streamed")
		}
		return false, validateModelsResponse(object)
	case endpointEmbeddings:
		if stream {
			return false, errors.New("embeddings response cannot be streamed")
		}
		return false, validateEmbeddingsResponse(object)
	default:
		return false, errors.New("unsupported response endpoint")
	}
}

func validateChatResponse(object map[string]any, stream bool) (bool, error) {
	rawChoices, exists := object["choices"]
	if !exists {
		return false, errors.New("choices is required")
	}
	choices, ok := rawChoices.([]any)
	if !ok {
		return false, errors.New("choices must be an array")
	}
	semantic := false
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			return false, errors.New("choice must be an object")
		}
		field := "message"
		if stream {
			field = "delta"
		}
		rawContainer, exists := choice[field]
		if !exists {
			return false, errors.New("choice payload is required")
		}
		container, ok := rawContainer.(map[string]any)
		if !ok {
			return false, errors.New("choice payload must be an object")
		}
		current, err := validateSemanticContainer(container)
		if err != nil {
			return false, err
		}
		semantic = semantic || current
	}
	return semantic, nil
}

func validateSemanticContainer(container map[string]any) (bool, error) {
	semantic := false
	if role, exists := container["role"]; exists {
		if _, ok := role.(string); !ok {
			return false, errors.New("role must be a string")
		}
	}
	for _, field := range []string{"content", "reasoning_content", "reasoning"} {
		value, exists := container[field]
		if !exists || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return false, errors.New("semantic text must be a string")
		}
		semantic = semantic || text != ""
	}
	rawToolCalls, exists := container["tool_calls"]
	if !exists || rawToolCalls == nil {
		return semantic, nil
	}
	toolCalls, ok := rawToolCalls.([]any)
	if !ok {
		return false, errors.New("tool calls must be an array")
	}
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]any)
		if !ok {
			return false, errors.New("tool call must be an object")
		}
		current, err := validateToolCall(toolCall)
		if err != nil {
			return false, err
		}
		semantic = semantic || current
	}
	return semantic, nil
}

func validateToolCall(toolCall map[string]any) (bool, error) {
	semantic := false
	for _, field := range []string{"id", "type"} {
		if value, exists := toolCall[field]; exists {
			text, ok := value.(string)
			if !ok {
				return false, errors.New("tool call identity must be a string")
			}
			semantic = semantic || text != ""
		}
	}
	if value, exists := toolCall["index"]; exists {
		if _, err := nonNegativeJSONInteger(value); err != nil {
			return false, err
		}
	}
	rawFunction, exists := toolCall["function"]
	if !exists || rawFunction == nil {
		return semantic, nil
	}
	function, ok := rawFunction.(map[string]any)
	if !ok {
		return false, errors.New("tool call function must be an object")
	}
	for _, field := range []string{"name", "arguments"} {
		if value, exists := function[field]; exists {
			text, ok := value.(string)
			if !ok {
				return false, errors.New("tool call function fields must be strings")
			}
			semantic = semantic || text != ""
		}
	}
	return semantic, nil
}

func nonNegativeJSONInteger(value any) (uint64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("value must be an integer")
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 {
		return 0, errors.New("value must be a non-negative integer")
	}
	return uint64(parsed), nil
}

func validateModelsResponse(object map[string]any) error {
	if kind, ok := object["object"].(string); !ok || kind != "list" {
		return errors.New("models response must be a list")
	}
	rawData, exists := object["data"]
	if !exists {
		return errors.New("models response data is required")
	}
	items, ok := rawData.([]any)
	if !ok {
		return errors.New("models response data must be an array")
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return errors.New("model item must be an object")
		}
		identifier, ok := item["id"].(string)
		if !ok || identifier == "" {
			return errors.New("model item id is required")
		}
		if kind, ok := item["object"].(string); !ok || kind != "model" {
			return errors.New("model item object is invalid")
		}
		if created, exists := item["created"]; exists {
			if _, err := nonNegativeJSONInteger(created); err != nil {
				return err
			}
		}
		if owner, exists := item["owned_by"]; exists {
			if _, ok := owner.(string); !ok {
				return errors.New("model owner must be a string")
			}
		}
	}
	return nil
}

func validateEmbeddingsResponse(object map[string]any) error {
	if kind, ok := object["object"].(string); !ok || kind != "list" {
		return errors.New("embeddings response must be a list")
	}
	if model, ok := object["model"].(string); !ok || model == "" {
		return errors.New("embeddings response model is required")
	}
	rawData, exists := object["data"]
	if !exists {
		return errors.New("embeddings response data is required")
	}
	items, ok := rawData.([]any)
	if !ok || len(items) == 0 {
		return errors.New("embeddings response data must be a non-empty array")
	}
	seenIndexes := make(map[uint64]struct{}, len(items))
	vectorLength := -1
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return errors.New("embedding item must be an object")
		}
		if kind, ok := item["object"].(string); !ok || kind != "embedding" {
			return errors.New("embedding item object is invalid")
		}
		index, err := nonNegativeJSONInteger(item["index"])
		if err != nil {
			return err
		}
		if _, duplicate := seenIndexes[index]; duplicate {
			return errors.New("embedding item index is duplicated")
		}
		seenIndexes[index] = struct{}{}

		vector, ok := item["embedding"].([]any)
		if !ok || len(vector) == 0 {
			return errors.New("embedding vector must be a non-empty number array")
		}
		if vectorLength < 0 {
			vectorLength = len(vector)
		} else if vectorLength != len(vector) {
			return errors.New("embedding vectors must have a consistent length")
		}
		for _, rawValue := range vector {
			number, ok := rawValue.(json.Number)
			if !ok {
				return errors.New("embedding vector value must be a number")
			}
			value, err := number.Float64()
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return errors.New("embedding vector value must be finite")
			}
		}
	}
	return nil
}
