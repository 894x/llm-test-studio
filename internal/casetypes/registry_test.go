package casetypes

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestBuiltinRegistryPublishesAndValidatesVersionedCaseTypes(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 4 {
		t.Fatalf("descriptor count = %d, want 4", len(descriptors))
	}
	if descriptors[0].Type != TypeInputLatencyLadder || descriptors[1].Type != TypeLegacyAPIAudit ||
		descriptors[2].Type != TypeRequestSingle || descriptors[3].Type != TypeResponseProbe {
		t.Fatalf("descriptor order = %#v", descriptors)
	}
	if descriptors[0].TypeVersion != 2 {
		t.Fatalf("latency.input_ladder type version = %d, want 2", descriptors[0].TypeVersion)
	}

	requestSpec, err := json.Marshal(RequestSingleSpec{
		Request: domain.TestRequest{
			Method:  domain.RequestPOST,
			Path:    "/v1/chat/completions",
			Headers: map[string]string{},
			Body:    json.RawMessage(`{"messages":[{"role":"user","content":"hello"}]}`),
		},
		Expected: domain.TestExpected{
			AllowedHTTPStatuses: []int{200},
			StreamCompletion:    domain.StreamCompletionNotApplicable,
		},
		Assertions: []domain.TestAssertion{{Kind: domain.AssertionText, Config: json.RawMessage(`{"contains":"hello"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	definition := domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          TypeRequestSingle,
		TypeVersion:   1,
		Spec:          requestSpec,
	}
	if err := registry.Validate(domain.ProtocolOpenAIChat, definition); err != nil {
		t.Fatalf("validate request.single: %v", err)
	}

	definition.Type = "missing.type"
	if err := registry.Validate(domain.ProtocolOpenAIChat, definition); err == nil {
		t.Fatal("unknown case type was accepted")
	}
}

func TestLegacyAPIAuditDescriptorSupportsWanVideo(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range registry.Descriptors() {
		if descriptor.Type != TypeLegacyAPIAudit {
			continue
		}
		for _, protocol := range descriptor.SupportedProtocols {
			if protocol == domain.ProtocolWanVideo {
				return
			}
		}
		t.Fatalf("legacy.apiaudit protocols = %v, want %q", descriptor.SupportedProtocols, domain.ProtocolWanVideo)
	}
	t.Fatal("legacy.apiaudit descriptor not found")
}

func TestInputLatencyLadderAcceptsUniformDefaultsAndPerStageOverrides(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	valid := InputLatencyLadderSpec{
		Request: domain.TestRequest{
			Method:  domain.RequestPOST,
			Path:    "/v1/chat/completions",
			Headers: map[string]string{},
			Body:    json.RawMessage(`{"messages":[{"role":"user","content":"placeholder"}]}`),
		},
		Stages: []InputLatencyStage{
			{InputTokens: 128},
			{InputTokens: 512, Warmups: uint32Pointer(2), Samples: uint32Pointer(5)},
			{InputTokens: 2048, Samples: uint32Pointer(8)},
		},
		WarmupsPerStep: 1,
		SamplesPerStep: 3,
		OutputTokens:   16,
		TimeoutMS:      60_000,
		CacheMode:      CacheModeCold,
	}
	spec, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	definition := domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          TypeInputLatencyLadder,
		TypeVersion:   2,
		Spec:          spec,
	}
	if err := registry.Validate(domain.ProtocolOpenAIChat, definition); err != nil {
		t.Fatalf("validate ladder: %v", err)
	}

	valid.Stages = []InputLatencyStage{{InputTokens: 128}, {InputTokens: 128}}
	spec, err = json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	definition.Spec = spec
	if err := registry.Validate(domain.ProtocolOpenAIChat, definition); err == nil {
		t.Fatal("duplicate ladder steps were accepted")
	}

	valid.Stages = []InputLatencyStage{{InputTokens: 128, Samples: uint32Pointer(0)}}
	spec, err = json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	definition.Spec = spec
	if err := registry.Validate(domain.ProtocolOpenAIChat, definition); err == nil {
		t.Fatal("zero per-stage samples override was accepted")
	}
}

func TestResponseProbeDescriptorValidatesSafeSignatureRules(t *testing.T) {
	registry := MustBuiltinRegistry()
	descriptor, ok := registry.Descriptor(domain.CaseType("response.probe"), 1)
	if !ok {
		t.Fatal("response.probe@1 descriptor is missing")
	}
	if !descriptor.Creatable || descriptor.SchedulingOwner != SchedulingOwnerPlan ||
		!supportsProtocol(descriptor.SupportedProtocols, domain.ProtocolOpenAIChat) ||
		!supportsProtocol(descriptor.SupportedProtocols, domain.ProtocolKimiK3) {
		t.Fatalf("response probe descriptor = %#v", descriptor)
	}

	valid := domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          domain.CaseType("response.probe"),
		TypeVersion:   1,
		Spec: json.RawMessage(`{
			"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"仅输出 OK"}],"stream":false}},
			"signatures":[
				{"label":"provider-a","match":[{"pointer":"/provider","operator":"equals","value":"a"}]},
				{"label":"provider-b","match":[{"pointer":"/usage/prompt_tokens_details","operator":"type","value":"object"}]}
			]
		}`),
	}
	if err := registry.Validate(domain.ProtocolOpenAIChat, valid); err != nil {
		t.Fatalf("valid response probe rejected: %v", err)
	}

	invalidSpecs := []json.RawMessage{
		json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{}},"signatures":[{"label":"duplicate","match":[{"pointer":"/a","operator":"exists"}]},{"label":"duplicate","match":[{"pointer":"/b","operator":"exists"}]}]}`),
		json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{}},"signatures":[{"label":"bad-pointer","match":[{"pointer":"provider","operator":"exists"}]}]}`),
		json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{}},"signatures":[{"label":"bad-exists","match":[{"pointer":"/provider","operator":"exists","value":true}]}]}`),
	}
	for index, spec := range invalidSpecs {
		definition := valid
		definition.Spec = spec
		if err := registry.Validate(domain.ProtocolOpenAIChat, definition); err == nil {
			t.Fatalf("invalid response probe spec %d was accepted", index)
		}
	}
}

func uint32Pointer(value uint32) *uint32 { return &value }
