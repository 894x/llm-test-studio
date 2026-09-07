package runs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/execution/openai"
	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

const maxProbeShapeDepth = 64

type ResponseProbeExecutor struct {
	transport http.RoundTripper
}

func NewResponseProbeExecutor(transport http.RoundTripper) *ResponseProbeExecutor {
	return &ResponseProbeExecutor{transport: transport}
}

func (executor *ResponseProbeExecutor) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	if executor == nil || ctx == nil || request.Credential == nil || emit == nil || len(request.Cases) == 0 {
		return ErrInvalid
	}
	snapshot := request.Run.Snapshot()
	if snapshot.Channel.Protocol != domain.ProtocolOpenAIChat && snapshot.Channel.Protocol != domain.ProtocolKimiK3 {
		return ErrUnsupportedExecutionProtocol
	}
	client, err := openai.NewClient(snapshot.Channel, request.Credential, executor.transport)
	if err != nil {
		return fmt.Errorf("create response probe client: %w", err)
	}
	defer client.Close()

	caseExecutors := make([]load.Executor, len(request.Cases))
	for index, testCase := range request.Cases {
		if !testCase.Enabled || testCase.Protocol != snapshot.Channel.Protocol ||
			testCase.Definition.Type != casetypes.TypeResponseProbe || testCase.Definition.TypeVersion != 1 {
			return ErrNotRunnable
		}
		var spec casetypes.ResponseProbeSpec
		if decodeErr := json.Unmarshal(testCase.Definition.Spec, &spec); decodeErr != nil {
			return fmt.Errorf("decode response probe case %s: %w", testCase.ID, decodeErr)
		}
		signatures := append([]casetypes.ResponseProbeSignature(nil), spec.Signatures...)
		caseExecutor, buildErr := client.ProbeExecutor(spec.Request, func(encoded []byte) (map[string]string, error) {
			return classifyProbeResponse(encoded, signatures)
		})
		if buildErr != nil {
			return fmt.Errorf("prepare response probe case %s: %w", testCase.ID, buildErr)
		}
		caseExecutors[index] = caseExecutor
	}
	composite := func(executionContext context.Context, scheduled load.Request) load.Observation {
		return caseExecutors[int(scheduled.Index%uint64(len(caseExecutors)))](executionContext, scheduled)
	}
	outcome, runErr := load.Run(ctx, snapshot.Load, composite, load.Options{StopSending: request.StopSending})
	for _, observation := range outcome.Results {
		caseIndex := int(observation.Index % uint64(len(request.Cases)))
		if err := emit(draftFromProbeObservation(request.Cases[caseIndex].ID, observation)); err != nil {
			return err
		}
	}
	return runErr
}

func draftFromProbeObservation(caseID string, observation load.Observation) ResultDraft {
	draft := draftFromObservation(caseID, observation)
	if draft.Dimensions == nil {
		draft.Dimensions = make(map[string]string, 3)
	}
	draft.Dimensions["probe_case_id"] = caseID
	if !observation.Success {
		draft.Dimensions["probe_bucket"] = "failed"
		draft.Dimensions["probe_classification"] = "failed"
	}
	return draft
}

func classifyProbeResponse(encoded []byte, signatures []casetypes.ResponseProbeSignature) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var response any
	if err := decoder.Decode(&response); err != nil || response == nil {
		return nil, errors.New("response probe requires a JSON response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("response probe JSON contains trailing data")
	}
	root, ok := response.(map[string]any)
	if !ok {
		return nil, errors.New("response probe requires a JSON object response")
	}

	shape, err := probeShape(root, 0)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(shape))
	dimensions := map[string]string{
		"probe_format":         "json",
		"probe_shape":          "sha256:" + hex.EncodeToString(digest[:12]),
		"probe_bucket":         "unknown",
		"probe_classification": "unknown",
	}
	matched := make([]string, 0, len(signatures))
	for _, signature := range signatures {
		if probeSignatureMatches(root, signature) {
			matched = append(matched, signature.Label)
		}
	}
	switch len(matched) {
	case 0:
	case 1:
		dimensions["probe_bucket"] = matched[0]
		dimensions["probe_classification"] = "matched"
	default:
		dimensions["probe_bucket"] = "ambiguous"
		dimensions["probe_classification"] = "ambiguous"
	}
	return dimensions, nil
}

func probeSignatureMatches(root map[string]any, signature casetypes.ResponseProbeSignature) bool {
	for _, matcher := range signature.Match {
		value, exists := jsonpointer.Lookup(root, matcher.Pointer)
		switch matcher.Operator {
		case "exists":
			if !exists {
				return false
			}
		case "not_exists":
			if exists {
				return false
			}
		case "equals":
			expected, ok := decodeProbeMatcherValue(matcher.Value)
			if !exists || !ok || !reflect.DeepEqual(value, expected) {
				return false
			}
		case "type":
			var expected string
			if !exists || json.Unmarshal(matcher.Value, &expected) != nil || probeJSONType(value) != expected {
				return false
			}
		case "contains":
			var fragment string
			text, stringValue := value.(string)
			if !exists || !stringValue || json.Unmarshal(matcher.Value, &fragment) != nil || !strings.Contains(text, fragment) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func decodeProbeMatcherValue(raw json.RawMessage) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return value, true
}

func probeShape(value any, depth int) (string, error) {
	if depth > maxProbeShapeDepth {
		return "", errors.New("response probe JSON nesting is too deep")
	}
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return "boolean", nil
	case json.Number:
		return "number", nil
	case string:
		return "string", nil
	case []any:
		shapes := make(map[string]struct{}, len(typed))
		for _, item := range typed {
			shape, err := probeShape(item, depth+1)
			if err != nil {
				return "", err
			}
			shapes[shape] = struct{}{}
		}
		items := make([]string, 0, len(shapes))
		for shape := range shapes {
			items = append(items, shape)
		}
		sort.Strings(items)
		return "array[" + strings.Join(items, "|") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			shape, err := probeShape(typed[key], depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, strconv.Quote(key)+":"+shape)
		}
		return "object{" + strings.Join(parts, ",") + "}", nil
	default:
		return "", fmt.Errorf("unsupported response probe JSON value %T", value)
	}
}

func probeJSONType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}
