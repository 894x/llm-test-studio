package casetypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	TypeInputLatencyLadder domain.CaseType = "latency.input_ladder"
	TypeLegacyAPIAudit     domain.CaseType = "legacy.apiaudit"
	TypeRequestSingle      domain.CaseType = "request.single"
	TypeResponseProbe      domain.CaseType = "response.probe"
)

type SchedulingOwner string

const (
	SchedulingOwnerCase SchedulingOwner = "case"
	SchedulingOwnerPlan SchedulingOwner = "plan"
)

type Descriptor struct {
	Type               domain.CaseType   `json:"type"`
	TypeVersion        uint32            `json:"type_version"`
	Label              string            `json:"label"`
	Category           string            `json:"category"`
	SchedulingOwner    SchedulingOwner   `json:"scheduling_owner"`
	SupportedProtocols []domain.Protocol `json:"supported_protocols"`
	Creatable          bool              `json:"creatable"`
	DefaultSpec        json.RawMessage   `json:"default_spec"`
}

type RequestSingleSpec struct {
	Request    domain.TestRequest     `json:"request"`
	Expected   domain.TestExpected    `json:"expected"`
	Assertions []domain.TestAssertion `json:"assertions"`
}

type LegacyAPIAuditSpec struct {
	Kind    string             `json:"kind"`
	Request domain.TestRequest `json:"request"`
	Options map[string]any     `json:"options"`
}

type ResponseProbeSpec struct {
	Request    domain.TestRequest       `json:"request"`
	Signatures []ResponseProbeSignature `json:"signatures"`
}

type ResponseProbeSignature struct {
	Label string                 `json:"label"`
	Match []ResponseProbeMatcher `json:"match"`
}

type ResponseProbeMatcher struct {
	Pointer  string          `json:"pointer"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
}

type CacheMode string

const (
	CacheModeCold CacheMode = "cold"
	CacheModeWarm CacheMode = "warm"
)

type InputLatencyLadderSpec struct {
	Request        domain.TestRequest  `json:"request"`
	Stages         []InputLatencyStage `json:"stages"`
	WarmupsPerStep uint32              `json:"warmups_per_step"`
	SamplesPerStep uint32              `json:"samples_per_step"`
	OutputTokens   uint32              `json:"output_tokens"`
	TimeoutMS      uint64              `json:"timeout_ms"`
	CacheMode      CacheMode           `json:"cache_mode"`
}

type InputLatencyStage struct {
	InputTokens uint32  `json:"input_tokens"`
	Warmups     *uint32 `json:"warmups,omitempty"`
	Samples     *uint32 `json:"samples,omitempty"`
}

func (spec InputLatencyLadderSpec) Schedule(stage InputLatencyStage) (warmups uint32, samples uint32) {
	warmups, samples = spec.WarmupsPerStep, spec.SamplesPerStep
	if stage.Warmups != nil {
		warmups = *stage.Warmups
	}
	if stage.Samples != nil {
		samples = *stage.Samples
	}
	return warmups, samples
}

type caseType struct {
	descriptor Descriptor
	validate   func(domain.Protocol, json.RawMessage) error
}

type Registry struct {
	types map[string]caseType
}

func NewBuiltinRegistry() (*Registry, error) {
	registry := &Registry{types: make(map[string]caseType)}
	definitions := []caseType{
		{descriptor: descriptorInputLatencyLadder(), validate: validateInputLatencyLadder},
		{descriptor: descriptorLegacyAPIAudit(), validate: validateLegacyAPIAudit},
		{descriptor: descriptorResponseProbe(), validate: validateResponseProbe},
		{descriptor: descriptorRequestSingle(), validate: validateRequestSingle},
	}
	for _, definition := range definitions {
		if err := registry.register(definition); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func MustBuiltinRegistry() *Registry {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry *Registry) Descriptors() []Descriptor {
	if registry == nil {
		return []Descriptor{}
	}
	result := make([]Descriptor, 0, len(registry.types))
	for _, definition := range registry.types {
		descriptor := definition.descriptor
		descriptor.SupportedProtocols = append([]domain.Protocol(nil), descriptor.SupportedProtocols...)
		descriptor.DefaultSpec = append(json.RawMessage(nil), descriptor.DefaultSpec...)
		result = append(result, descriptor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Type < result[j].Type })
	return result
}

func (registry *Registry) Descriptor(caseType domain.CaseType, version uint32) (Descriptor, bool) {
	if registry == nil {
		return Descriptor{}, false
	}
	definition, found := registry.types[registryKey(caseType, version)]
	if !found {
		return Descriptor{}, false
	}
	descriptor := definition.descriptor
	descriptor.SupportedProtocols = append([]domain.Protocol(nil), descriptor.SupportedProtocols...)
	descriptor.DefaultSpec = append(json.RawMessage(nil), descriptor.DefaultSpec...)
	return descriptor, true
}

func (registry *Registry) Validate(protocol domain.Protocol, definition domain.TestCaseDefinition) error {
	if registry == nil {
		return errors.New("case type registry is unavailable")
	}
	if err := definition.Validate(); err != nil {
		return err
	}
	caseType, found := registry.types[registryKey(definition.Type, definition.TypeVersion)]
	if !found {
		return fmt.Errorf("case type %s@%d is not registered", definition.Type, definition.TypeVersion)
	}
	if !supportsProtocol(caseType.descriptor.SupportedProtocols, protocol) {
		return fmt.Errorf("case type %s@%d does not support protocol %s", definition.Type, definition.TypeVersion, protocol)
	}
	return caseType.validate(protocol, definition.Spec)
}

func (registry *Registry) register(definition caseType) error {
	descriptor := definition.descriptor
	if err := descriptor.validate(); err != nil {
		return err
	}
	if definition.validate == nil {
		return errors.New("case type validator is required")
	}
	key := registryKey(descriptor.Type, descriptor.TypeVersion)
	if _, duplicate := registry.types[key]; duplicate {
		return fmt.Errorf("case type %s is already registered", key)
	}
	registry.types[key] = definition
	return nil
}

func (descriptor Descriptor) validate() error {
	if descriptor.Type == "" || descriptor.TypeVersion == 0 || strings.TrimSpace(descriptor.Label) == "" || strings.TrimSpace(descriptor.Category) == "" {
		return errors.New("invalid case type descriptor identity")
	}
	if descriptor.SchedulingOwner != SchedulingOwnerCase && descriptor.SchedulingOwner != SchedulingOwnerPlan {
		return errors.New("invalid case type scheduling owner")
	}
	if len(descriptor.SupportedProtocols) == 0 || len(descriptor.DefaultSpec) == 0 {
		return errors.New("case type descriptor requires protocols and a default spec")
	}
	return nil
}

func registryKey(caseType domain.CaseType, version uint32) string {
	return fmt.Sprintf("%s@%d", caseType, version)
}

func supportsProtocol(values []domain.Protocol, protocol domain.Protocol) bool {
	for _, value := range values {
		if value == protocol {
			return true
		}
	}
	return false
}

func descriptorRequestSingle() Descriptor {
	return Descriptor{
		Type: TypeRequestSingle, TypeVersion: 1, Label: "单请求验证", Category: "compatibility",
		SchedulingOwner: SchedulingOwnerPlan, SupportedProtocols: []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolKimiK3},
		Creatable: true,
		DefaultSpec: mustJSON(RequestSingleSpec{
			Request:    domain.TestRequest{Method: domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"user","content":"仅输出 OK"}],"max_tokens":16}`)},
			Expected:   domain.TestExpected{AllowedHTTPStatuses: []int{200}, StreamCompletion: domain.StreamCompletionNotApplicable},
			Assertions: []domain.TestAssertion{{Kind: domain.AssertionText, Config: json.RawMessage(`{"contains":"OK"}`)}},
		}),
	}
}

func descriptorResponseProbe() Descriptor {
	return Descriptor{
		Type: TypeResponseProbe, TypeVersion: 1, Label: "响应指纹探测", Category: "routing",
		SchedulingOwner: SchedulingOwnerPlan, SupportedProtocols: []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolKimiK3},
		Creatable: true,
		DefaultSpec: mustJSON(ResponseProbeSpec{
			Request: domain.TestRequest{
				Method: domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{},
				Body: json.RawMessage(`{"messages":[{"role":"user","content":"仅输出 OK"}],"max_tokens":16,"stream":false}`),
			},
			Signatures: []ResponseProbeSignature{},
		}),
	}
}

func descriptorLegacyAPIAudit() Descriptor {
	return Descriptor{
		Type: TypeLegacyAPIAudit, TypeVersion: 1, Label: "内置兼容性审计", Category: "compatibility",
		SchedulingOwner: SchedulingOwnerCase, SupportedProtocols: []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolKimiK3, domain.ProtocolSeedance, domain.ProtocolWanVideo, domain.ProtocolMiniMaxVideo},
		Creatable: false,
		DefaultSpec: mustJSON(LegacyAPIAuditSpec{
			Kind:    "chat_sync",
			Request: domain.TestRequest{Method: domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"user","content":"仅输出 OK"}]}`)},
			Options: map[string]any{},
		}),
	}
}

func descriptorInputLatencyLadder() Descriptor {
	return Descriptor{
		Type: TypeInputLatencyLadder, TypeVersion: 2, Label: "输入阶梯延迟", Category: "performance",
		SchedulingOwner: SchedulingOwnerCase, SupportedProtocols: []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolKimiK3},
		Creatable: true,
		DefaultSpec: mustJSON(InputLatencyLadderSpec{
			Request: domain.TestRequest{Method: domain.RequestPOST, Path: "/v1/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"user","content":"placeholder"}],"stream":true}`)},
			Stages: []InputLatencyStage{
				{InputTokens: 128}, {InputTokens: 512}, {InputTokens: 2048},
				{InputTokens: 8192}, {InputTokens: 32768}, {InputTokens: 131072},
			},
			WarmupsPerStep: 1, SamplesPerStep: 3,
			OutputTokens: 16, TimeoutMS: 600_000, CacheMode: CacheModeCold,
		}),
	}
}

func validateRequestSingle(_ domain.Protocol, raw json.RawMessage) error {
	var spec RequestSingleSpec
	if err := decodeStrict(raw, &spec); err != nil {
		return fmt.Errorf("decode request.single spec: %w", err)
	}
	if err := spec.Request.Validate(); err != nil {
		return err
	}
	if err := spec.Expected.Validate(); err != nil {
		return err
	}
	if len(spec.Assertions) == 0 {
		return errors.New("request.single requires at least one assertion")
	}
	for index, assertion := range spec.Assertions {
		if err := assertion.Validate(); err != nil {
			return fmt.Errorf("invalid request.single assertion %d: %w", index, err)
		}
	}
	return nil
}

func validateResponseProbe(_ domain.Protocol, raw json.RawMessage) error {
	var spec ResponseProbeSpec
	if err := decodeStrict(raw, &spec); err != nil {
		return fmt.Errorf("decode response.probe spec: %w", err)
	}
	if err := spec.Request.Validate(); err != nil {
		return err
	}
	if spec.Request.Method != domain.RequestPOST {
		return errors.New("response.probe requires POST")
	}
	var body map[string]any
	if err := json.Unmarshal(spec.Request.Body, &body); err != nil || body == nil {
		return errors.New("response.probe request body must be a JSON object")
	}
	if stream, exists := body["stream"]; exists && stream != false {
		return errors.New("response.probe requires a non-streaming request")
	}
	if len(spec.Signatures) > 32 {
		return errors.New("response.probe supports at most 32 signatures")
	}
	labels := make(map[string]struct{}, len(spec.Signatures))
	for signatureIndex, signature := range spec.Signatures {
		label := strings.TrimSpace(signature.Label)
		if label == "" || label != signature.Label || len(label) > 64 {
			return fmt.Errorf("response.probe signature %d has an invalid label", signatureIndex)
		}
		labelKey := strings.ToLower(label)
		if _, duplicate := labels[labelKey]; duplicate {
			return fmt.Errorf("response.probe signature label %q is duplicated", label)
		}
		labels[labelKey] = struct{}{}
		if len(signature.Match) == 0 || len(signature.Match) > 16 {
			return fmt.Errorf("response.probe signature %q requires 1 to 16 matchers", label)
		}
		for matcherIndex, matcher := range signature.Match {
			if err := validateResponseProbeMatcher(matcher); err != nil {
				return fmt.Errorf("response.probe signature %q matcher %d: %w", label, matcherIndex, err)
			}
		}
	}
	return nil
}

func validateResponseProbeMatcher(matcher ResponseProbeMatcher) error {
	if !validJSONPointer(matcher.Pointer) {
		return errors.New("pointer must be a valid non-root JSON Pointer")
	}
	switch matcher.Operator {
	case "exists", "not_exists":
		if len(matcher.Value) != 0 {
			return fmt.Errorf("operator %s does not accept a value", matcher.Operator)
		}
	case "equals":
		if len(matcher.Value) == 0 || !json.Valid(matcher.Value) {
			return errors.New("equals requires a JSON value")
		}
	case "type":
		var valueType string
		if err := json.Unmarshal(matcher.Value, &valueType); err != nil {
			return errors.New("type requires a string value")
		}
		switch valueType {
		case "null", "boolean", "number", "string", "array", "object":
		default:
			return fmt.Errorf("unsupported JSON type %q", valueType)
		}
	case "contains":
		var fragment string
		if err := json.Unmarshal(matcher.Value, &fragment); err != nil || fragment == "" || len(fragment) > 256 {
			return errors.New("contains requires a non-empty string value of at most 256 bytes")
		}
	default:
		return fmt.Errorf("unsupported operator %q", matcher.Operator)
	}
	return nil
}

func validJSONPointer(pointer string) bool {
	if pointer == "" || !strings.HasPrefix(pointer, "/") || len(pointer) > 512 {
		return false
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1') {
			return false
		}
		index++
	}
	return true
}

func validateLegacyAPIAudit(protocol domain.Protocol, raw json.RawMessage) error {
	var spec LegacyAPIAuditSpec
	if err := decodeStrict(raw, &spec); err != nil {
		return fmt.Errorf("decode legacy.apiaudit spec: %w", err)
	}
	if strings.TrimSpace(spec.Kind) == "" || spec.Kind != strings.TrimSpace(spec.Kind) || spec.Options == nil {
		return errors.New("legacy.apiaudit kind and options are required")
	}
	if err := spec.Request.Validate(); err != nil {
		return err
	}
	if _, supported := legacyKinds[protocol][spec.Kind]; !supported {
		return fmt.Errorf("legacy.apiaudit kind %q is not supported for %s", spec.Kind, protocol)
	}
	return nil
}

func validateInputLatencyLadder(_ domain.Protocol, raw json.RawMessage) error {
	var spec InputLatencyLadderSpec
	if err := decodeStrict(raw, &spec); err != nil {
		return fmt.Errorf("decode latency.input_ladder spec: %w", err)
	}
	if err := spec.Request.Validate(); err != nil {
		return err
	}
	if spec.Request.Method != domain.RequestPOST {
		return errors.New("latency.input_ladder requires POST")
	}
	if len(spec.Stages) == 0 || len(spec.Stages) > 32 {
		return errors.New("latency.input_ladder requires 1 to 32 token steps")
	}
	previous := uint32(0)
	for _, stage := range spec.Stages {
		if stage.InputTokens == 0 || stage.InputTokens > 1_000_000 || stage.InputTokens <= previous {
			return errors.New("latency.input_ladder token steps must be strictly increasing and at most 1000000")
		}
		if stage.Warmups != nil && *stage.Warmups > 10 {
			return errors.New("latency.input_ladder per-stage warmups are out of range")
		}
		if stage.Samples != nil && (*stage.Samples == 0 || *stage.Samples > 100) {
			return errors.New("latency.input_ladder per-stage samples are out of range")
		}
		previous = stage.InputTokens
	}
	if spec.WarmupsPerStep > 10 || spec.SamplesPerStep == 0 || spec.SamplesPerStep > 100 {
		return errors.New("latency.input_ladder warmups or samples are out of range")
	}
	if spec.OutputTokens == 0 || spec.OutputTokens > 65_536 || spec.TimeoutMS == 0 || spec.TimeoutMS > 600_000 {
		return errors.New("latency.input_ladder output or timeout is out of range")
	}
	if spec.CacheMode != CacheModeCold && spec.CacheMode != CacheModeWarm {
		return errors.New("latency.input_ladder cache mode is invalid")
	}
	return nil
}

func decodeStrict(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("spec must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func makeKindSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

var legacyKinds = map[domain.Protocol]map[string]struct{}{
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
	domain.ProtocolSeedance:     makeKindSet("seedance_task"),
	domain.ProtocolWanVideo:     makeKindSet("wan_task_success", "wan_task_rejected"),
	domain.ProtocolMiniMaxVideo: makeKindSet("minimax_video_task_success", "minimax_video_task_rejected", "minimax_video_auth_rejected"),
}
