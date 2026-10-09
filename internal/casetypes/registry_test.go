package casetypes_test

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestRegistryExposesOnlyCurrentProtocolDefinitions(t *testing.T) {
	registry, err := casetypes.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 6 {
		t.Fatalf("protocol count = %d", len(descriptors))
	}
	for _, descriptor := range descriptors {
		if !descriptor.Creatable || descriptor.SchedulingOwner != casetypes.SchedulingOwnerPlan {
			t.Fatalf("invalid descriptor: %+v", descriptor)
		}
		definition := domain.ProtocolDefinitions{domain.Protocol(descriptor.Type): descriptor.DefaultSpec}
		if err := registry.ValidateDefinitions(definition); err != nil {
			t.Fatalf("default %s: %v", descriptor.Type, err)
		}
		if err := registry.Validate(domain.Protocol("different"), descriptor.DefaultSpec); err == nil {
			t.Fatal("cross-protocol definition accepted")
		}
	}
	for _, oldType := range []domain.CaseType{"legacy.apiaudit", "request.single", "response.probe", "latency.input_ladder"} {
		if _, exists := registry.Descriptor(oldType, 1); exists {
			t.Fatalf("obsolete type remains registered: %s", oldType)
		}
	}
}

func TestRegistryRejectsOldSpecWithoutReinterpretation(t *testing.T) {
	registry := casetypes.MustBuiltinRegistry()
	definition := domain.ProtocolDefinitions{domain.Protocol("openai-chat"): json.RawMessage(`{"kind":"chat_sync","request":{"method":"POST","path":"/chat/completions","headers":{},"body":{}},"options":{}}`)}
	before := string(definition[domain.ProtocolOpenAIChat])
	if err := registry.ValidateDefinitions(definition); err == nil {
		t.Fatal("old spec accepted")
	}
	if string(definition[domain.ProtocolOpenAIChat]) != before {
		t.Fatal("rejected data was mutated")
	}
}
