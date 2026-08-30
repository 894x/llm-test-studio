package caseimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/domain"
)

const legacyDriverVersion = 1

type legacyRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type legacyCase struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Dimension string         `json:"dimension"`
	Protocol  string         `json:"protocol"`
	Kind      string         `json:"kind"`
	Default   bool           `json:"default"`
	Disabled  bool           `json:"disabled,omitempty"`
	Severity  string         `json:"severity,omitempty"`
	Request   legacyRequest  `json:"request"`
	Options   map[string]any `json:"options,omitempty"`
}

type convertedCase struct {
	SourcePath         string
	SourceBytesSHA256  string
	SemanticSHA256     string
	MaterializedSHA256 string
	Key                string
	Name               string
	Dimension          string
	Protocol           domain.Protocol
	Enabled            bool
	Default            bool
	Severity           domain.CaseSeverity
	ExecutionMode      domain.CaseExecutionMode
	Definition         domain.TestCaseDefinition
}

type legacyDriverConfig struct {
	Driver        string         `json:"driver"`
	DriverVersion int            `json:"driver_version"`
	LegacyKind    string         `json:"legacy_kind"`
	BodyPresent   bool           `json:"body_present"`
	Options       map[string]any `json:"options"`
}

var supportedLegacyKinds = map[domain.Protocol]map[string]struct{}{
	domain.ProtocolOpenAIChat: makeKindSet(
		"manual_unknown", "id_consistency", "stream_usage", "chat_stream", "error_schema", "models_contains",
		"response_id", "stop_parameter", "structured_json", "tool_call", "chat_sync", "usage_baseline",
		"stream_parity", "sse_integrity", "reasoning_visibility", "logprobs_contract", "deterministic_stability",
		"multi_turn_usage", "structured_stability", "concurrency_probe", "stream_ttft", "long_stream_stability",
		"cache_visibility", "stream_throughput", "sampling_effect", "parameter_boundaries", "usage_growth",
		"needle_retrieval", "error_no_usage", "padding_ratio",
	),
	domain.ProtocolKimiK3: makeKindSet(
		"manual_unknown", "id_consistency", "stream_usage", "chat_stream", "error_schema", "models_contains",
		"response_id", "stop_parameter", "structured_json", "tool_call", "chat_sync", "usage_baseline",
		"stream_parity", "sse_integrity", "reasoning_visibility", "logprobs_contract", "deterministic_stability",
		"multi_turn_usage", "structured_stability", "concurrency_probe", "stream_ttft", "long_stream_stability",
		"cache_visibility", "stream_throughput", "sampling_effect", "parameter_boundaries", "usage_growth",
		"needle_retrieval", "error_no_usage", "padding_ratio", "kimi_success", "kimi_tool_call",
		"kimi_reasoning_visible", "kimi_reasoning_hidden", "kimi_error_400",
	),
	domain.ProtocolSeedance: makeKindSet("seedance_task"),
}

func makeKindSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func convertLegacyCase(sourcePath string, raw []byte) (convertedCase, error) {
	if sourcePath == "" || strings.TrimSpace(sourcePath) != sourcePath {
		return convertedCase{}, errors.New("legacy case source path is required")
	}
	var legacy legacyCase
	if err := decodeStrictJSON(raw, &legacy); err != nil {
		return convertedCase{}, fmt.Errorf("decode legacy case %s: %w", sourcePath, err)
	}
	legacy.ID = strings.TrimSpace(legacy.ID)
	legacy.Name = strings.TrimSpace(legacy.Name)
	legacy.Dimension = strings.TrimSpace(legacy.Dimension)
	legacy.Protocol = strings.TrimSpace(legacy.Protocol)
	legacy.Kind = strings.TrimSpace(legacy.Kind)
	legacy.Severity = strings.TrimSpace(legacy.Severity)
	legacy.Request.Method = strings.ToUpper(strings.TrimSpace(legacy.Request.Method))
	legacy.Request.Path = strings.TrimSpace(legacy.Request.Path)
	if legacy.Severity == "" {
		legacy.Severity = string(domain.CaseSeverityNormal)
	}
	protocol := domain.Protocol(legacy.Protocol)
	if err := protocol.Validate(); err != nil {
		return convertedCase{}, fmt.Errorf("legacy case %s protocol: %w", sourcePath, err)
	}
	if _, ok := supportedLegacyKinds[protocol][legacy.Kind]; !ok {
		return convertedCase{}, fmt.Errorf("legacy case %s has unsupported evaluator kind", sourcePath)
	}
	if legacy.Request.Method == "" {
		legacy.Request.Method = "POST"
	}
	if legacy.Options == nil {
		legacy.Options = map[string]any{}
	}
	semantic, err := json.Marshal(legacy)
	if err != nil {
		return convertedCase{}, fmt.Errorf("encode legacy semantics %s: %w", sourcePath, err)
	}
	bodyPresent := len(bytes.TrimSpace(legacy.Request.Body)) > 0 && !bytes.Equal(bytes.TrimSpace(legacy.Request.Body), []byte("null"))
	var body json.RawMessage
	if bodyPresent {
		body = append(json.RawMessage(nil), legacy.Request.Body...)
	}
	driverConfig, err := json.Marshal(legacyDriverConfig{
		Driver: "legacy.apiaudit", DriverVersion: legacyDriverVersion,
		LegacyKind: legacy.Kind, BodyPresent: bodyPresent, Options: legacy.Options,
	})
	if err != nil {
		return convertedCase{}, fmt.Errorf("encode legacy evaluator %s: %w", sourcePath, err)
	}
	mode := domain.CaseExecutionAutomatic
	if legacy.Kind == "manual_unknown" {
		mode = domain.CaseExecutionManual
	}
	candidate := convertedCase{
		SourcePath: sourcePath, SourceBytesSHA256: sha256Hex(raw), SemanticSHA256: sha256Hex(semantic),
		Key: legacy.ID, Name: legacy.Name, Dimension: legacy.Dimension,
		Protocol: protocol, Enabled: !legacy.Disabled, Default: legacy.Default,
		Severity: domain.CaseSeverity(legacy.Severity), ExecutionMode: mode,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Request: domain.TestRequest{
				Method: domain.RequestMethod(legacy.Request.Method), Path: legacy.Request.Path,
				Headers: map[string]string{}, Body: body,
			},
			Expected: domain.TestExpected{
				AllowedHTTPStatuses: expectedStatuses(legacy.Kind),
				StreamCompletion:    streamExpectation(legacy.Kind),
			},
			Assertions: []domain.TestAssertion{{Kind: domain.AssertionCustom, Config: driverConfig}},
		},
	}
	if err := candidate.validate(); err != nil {
		return convertedCase{}, fmt.Errorf("validate legacy case %s: %w", sourcePath, err)
	}
	candidate.MaterializedSHA256, err = materializedHash(candidate.materialize(domain.EntityMeta{}))
	if err != nil {
		return convertedCase{}, fmt.Errorf("hash converted case %s: %w", sourcePath, err)
	}
	return candidate, nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}

func materializedHash(testCase domain.TestCase) (string, error) {
	payload := struct {
		Key           string                    `json:"key"`
		Name          string                    `json:"name"`
		Dimension     string                    `json:"dimension"`
		Protocol      domain.Protocol           `json:"protocol"`
		Enabled       bool                      `json:"enabled"`
		Default       bool                      `json:"default"`
		Severity      domain.CaseSeverity       `json:"severity"`
		ExecutionMode domain.CaseExecutionMode  `json:"execution_mode"`
		Definition    domain.TestCaseDefinition `json:"definition"`
	}{
		Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension, Protocol: testCase.Protocol,
		Enabled: testCase.Enabled, Default: testCase.Default, Severity: testCase.Severity,
		ExecutionMode: testCase.ExecutionMode, Definition: testCase.Definition,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalJSON(encoded)
	if err != nil {
		return "", err
	}
	return sha256Hex(canonical), nil
}

// MaterializedSHA256 returns the stable content hash used by import source
// records. Versioning metadata is intentionally excluded so the same imported
// case content has one identity across databases and revisions.
func MaterializedSHA256(testCase domain.TestCase) (string, error) {
	if err := testCase.Validate(); err != nil {
		return "", err
	}
	return materializedHash(testCase)
}

func (candidate convertedCase) validate() error {
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	return candidate.materialize(domain.EntityMeta{
		ID: "00000000-0000-5000-8000-000000000001", SchemaVersion: domain.CurrentEntitySchemaVersion,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}).Validate()
}

func (candidate convertedCase) materialize(meta domain.EntityMeta) domain.TestCase {
	return domain.TestCase{
		EntityMeta: meta, Key: candidate.Key, Name: candidate.Name, Dimension: candidate.Dimension,
		Protocol: candidate.Protocol, Enabled: candidate.Enabled, Default: candidate.Default,
		Severity: candidate.Severity, ExecutionMode: candidate.ExecutionMode, Definition: candidate.Definition,
	}
}

func expectedStatuses(kind string) []int {
	switch kind {
	case "manual_unknown":
		return []int{200}
	case "kimi_error_400":
		return []int{400}
	case "error_schema":
		return integerRange(400, 599)
	case "error_no_usage":
		return integerRange(400, 499)
	case "concurrency_probe":
		return append(integerRange(200, 299), 429)
	case "logprobs_contract", "parameter_boundaries":
		return append(integerRange(200, 299), integerRange(400, 499)...)
	default:
		return integerRange(200, 299)
	}
}

func integerRange(first, last int) []int {
	values := make([]int, last-first+1)
	for index := range values {
		values[index] = first + index
	}
	return values
}

func streamExpectation(kind string) domain.StreamCompletionExpectation {
	switch kind {
	case "stream_parity", "sse_integrity", "stream_usage", "stream_ttft", "long_stream_stability", "stream_throughput":
		return domain.StreamCompletionRequired
	default:
		return domain.StreamCompletionNotApplicable
	}
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("JSON must contain exactly one value")
		}
		return err
	}
	return nil
}

func canonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("JSON must contain exactly one value")
		}
		return nil, err
	}
	return json.Marshal(normalized)
}
