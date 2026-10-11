package protocols_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestRegistryValidationPreservesInputErrorOrder(t *testing.T) {
	registry := protocols.NewRegistry()
	if err := registry.Validate("unknown", testspec.Spec{}); err == nil || err.Error() != "protocol is not registered" {
		t.Fatalf("parsed spec error = %v", err)
	}
	for _, test := range []struct {
		raw, want string
	}{
		{raw: `{`, want: "case spec must use inputs, request.body, assertions and optional workflow"},
		{raw: `{"inputs":null,"request":{"body":{}},"assertions":[]}`,
			want: "case inputs must be an object and assertions must be an array"},
		{raw: `{"inputs":{},"request":{"body":{}},"assertions":[]}`, want: "protocol is not registered"},
	} {
		raw := json.RawMessage(test.raw)
		if _, err := registry.DecodeSpec("unknown", raw); err == nil || err.Error() != test.want {
			t.Errorf("DecodeSpec(%s) error = %v, want %q", raw, err, test.want)
		}
		if string(raw) != test.raw {
			t.Fatal("rejected authoring input changed")
		}
	}
}

func TestRegistryChecksBaseAndNativeProtocolRules(t *testing.T) {
	registry := protocols.NewRegistry()
	for _, test := range []struct {
		protocol, raw string
	}{
		{protocol: "openai-chat", raw: `{"operation":"unknown","inputs":{},"request":{"body":{}},"assertions":[]}`},
		{protocol: "openai-responses", raw: `{"inputs":{},"request":{"body":{}},"workflow":{"mode":"wait"},"assertions":[]}`},
		{protocol: "seedance", raw: `{
			"inputs":{},"request":{"body":{}},"assertions":[],
			"workflow":{"mode":"sequence","steps":[{"id":"next","request":{"body":{}}}]}
		}`},
	} {
		spec := decodeSpec(t, test.raw)
		if err := registry.Validate(test.protocol, spec); err == nil {
			t.Errorf("Validate(%s) accepted native protocol violation", test.protocol)
		}
		if _, err := registry.DecodeSpec(test.protocol, json.RawMessage(test.raw)); err == nil {
			t.Errorf("DecodeSpec(%s) accepted native protocol violation", test.protocol)
		}
	}
	if err := registry.Validate("openai-chat", testspec.Spec{}); err == nil {
		t.Fatal("direct parsed spec bypassed shared validation")
	}
}

func TestRegisterRejectsInvalidDefaultSpecBeforeInstallingModule(t *testing.T) {
	registry := protocols.NewRegistry()
	module := &validationTestModule{spec: testspec.Spec{}}
	if err := registry.Register(module); err == nil || module.nativeCalls != 0 {
		t.Fatalf("invalid default spec: error = %v, native calls = %d", err, module.nativeCalls)
	}
	module.spec = decodeSpec(t, `{"inputs":{},"request":{"body":{}},"assertions":[]}`)
	module.nativeError = errors.New("native validation rejected")
	if err := registry.Register(module); !errors.Is(err, module.nativeError) {
		t.Fatalf("native error = %v", err)
	}
	module.nativeError = nil
	if err := registry.Register(module); err != nil {
		t.Fatalf("rejected registration installed a module: %v", err)
	}
}

type validationTestModule struct {
	spec        testspec.Spec
	nativeError error
	nativeCalls int
}

func (module *validationTestModule) Descriptor() runtime.Descriptor {
	return runtime.Descriptor{ID: "validation-test", DefaultSpec: module.spec}
}

func (module *validationTestModule) ValidateProtocol(testspec.Spec) error {
	module.nativeCalls++
	return module.nativeError
}

func (*validationTestModule) Execute(context.Context, runtime.Execution) testspec.Observation {
	return testspec.Observation{}
}
