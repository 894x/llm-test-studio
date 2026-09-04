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
	if len(descriptors) != 3 {
		t.Fatalf("descriptor count = %d, want 3", len(descriptors))
	}
	if descriptors[0].Type != TypeInputLatencyLadder || descriptors[1].Type != TypeLegacyAPIAudit || descriptors[2].Type != TypeRequestSingle {
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

func uint32Pointer(value uint32) *uint32 { return &value }
