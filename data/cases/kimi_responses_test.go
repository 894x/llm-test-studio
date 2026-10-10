package casebundle_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols/openairesponses"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func kimiResponsesSpec(t *testing.T, directory string) testspec.Spec {
	t.Helper()
	path := "openai-chat/" + directory + "/case.json"
	raw, err := fs.ReadFile(casebundle.Bundle, path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := casecodec.DecodeFilesystemCase(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := item.SpecFor(domain.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

type kimiResponsesFixture struct {
	status int
	root   map[string]any
	calls  int
}

func (fixture *kimiResponsesFixture) Send(_ context.Context, request runtime.Request) (runtime.Response, error) {
	fixture.calls++
	if fixture.calls == 2 {
		usage := fixture.root["usage"].(map[string]any)
		details := usage["input_tokens_details"].(map[string]any)
		details["cached_tokens"] = 300
		details["cache_write_tokens"] = 212
	}
	raw, err := json.Marshal(fixture.root)
	if err != nil {
		return runtime.Response{}, err
	}
	return runtime.Response{
		Started: time.Now(), RequestBody: request.Body,
		HTTP: &http.Response{StatusCode: fixture.status, Body: io.NopCloser(strings.NewReader(string(raw)))},
	}, nil
}

func kimiResponse(text string) map[string]any {
	return map[string]any{
		"id": "resp_fixture", "object": "response", "status": "completed",
		"output": []any{
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{
				"type": "summary_text", "text": "fixture reasoning",
			}}},
			map[string]any{"type": "message", "content": []any{map[string]any{
				"type": "output_text", "text": text,
			}}},
		},
		"usage": map[string]any{
			"input_tokens": 512, "output_tokens": 3, "total_tokens": 515,
			"input_tokens_details":  map[string]any{"cached_tokens": 0, "cache_write_tokens": 512},
			"output_tokens_details": map[string]any{"reasoning_tokens": 2},
		},
	}
}

func TestKimiResponsesAssertionsUseNativeOutcomesAndOutputItems(t *testing.T) {
	for _, test := range []struct {
		name, directory, text string
		status                int
		passed                bool
	}{
		{name: "schema name defaults", directory: "F032-json-schema-missing-name", text: "{}", status: 200, passed: true},
		{
			name: "schema exact values", directory: "F022-json-schema",
			text: `{"name":"小林","age":28}`, status: 200, passed: true,
		},
		{name: "schema wrong type", directory: "F022-json-schema", text: `{"name":"小林","age":"28"}`, status: 200},
		{name: "specified choice rejection", directory: "F011-tool-choice-function", status: 400, passed: true},
		{name: "specified choice accepted", directory: "F011-tool-choice-function", status: 200},
		{name: "native image semantics", directory: "F005-image-url-string", text: "7", status: 200, passed: true},
		{name: "native image wrong digit", directory: "F005-image-url-string", text: "9", status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := kimiResponsesSpec(t, test.directory)
			fixture := &kimiResponsesFixture{status: test.status, root: kimiResponse(test.text)}
			if test.status == 400 {
				fixture.root = map[string]any{"error": map[string]any{
					"type": "invalid_request_error", "message": "unsupported value",
				}}
			}
			observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
				Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
			})
			verdict := testspec.Evaluate(spec.Assertions, observation)
			if (verdict.Status == testspec.VerdictPassed) != test.passed {
				t.Fatalf("native verdict = %+v", verdict)
			}
		})
	}
}

func TestKimiResponsesFunctionCallAssertionsSearchAllOutputItems(t *testing.T) {
	spec := kimiResponsesSpec(t, "F006-tool-call")
	inputs, err := testspec.ValidateInputs(spec.Inputs, map[string]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, callID, arguments string
		passed                  bool
	}{
		{name: "call after reasoning", callID: "call_fixture", arguments: `{"city":"北京"}`, passed: true},
		{name: "missing call id", arguments: `{"city":"北京"}`},
		{name: "invalid arguments", callID: "call_fixture", arguments: `{"city":`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := kimiResponse("")
			output := root["output"].([]any)
			root["output"] = append(output[:1], map[string]any{
				"id": "fc_fixture", "type": "function_call", "status": "completed",
				"name": "get_weather", "call_id": test.callID, "arguments": test.arguments,
			})
			fixture := &kimiResponsesFixture{status: 200, root: root}
			observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
				Spec: spec, Inputs: inputs, Transport: fixture,
			})
			var body map[string]any
			if err := json.Unmarshal(observation.Exchanges[0].RequestBody, &body); err != nil {
				t.Fatal(err)
			}
			if body["tool_choice"] != "auto" {
				t.Fatal("tool-call case did not use the native auto choice")
			}
			verdict := testspec.Evaluate(spec.Assertions, observation)
			if (verdict.Status == testspec.VerdictPassed) != test.passed {
				t.Fatalf("native tool verdict = %+v", verdict)
			}
		})
	}
}

func TestKimiResponsesToolChoiceRejectsUnsupportedNativeValues(t *testing.T) {
	for _, test := range []struct {
		name, directory, choice, text string
		status                        int
		withTool, passed              bool
	}{
		{
			name: "required calls a tool", directory: "F009-tool-choice-required", choice: "required",
			status: 200, withTool: true,
		},
		{
			name: "required only replies with text", directory: "F009-tool-choice-required", choice: "required",
			text: "你好", status: 200,
		},
		{
			name: "required is rejected", directory: "F009-tool-choice-required", choice: "required",
			status: 400, passed: true,
		},
		{
			name: "none replies with text", directory: "F010-tool-choice-none", choice: "none",
			text: "你好", status: 200,
		},
		{
			name: "none calls a tool", directory: "F010-tool-choice-none", choice: "none",
			text: "你好", status: 200, withTool: true,
		},
		{
			name: "none has no text", directory: "F010-tool-choice-none", choice: "none", status: 200,
		},
		{
			name: "none is rejected", directory: "F010-tool-choice-none", choice: "none",
			status: 400, passed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := kimiResponsesSpec(t, test.directory)
			root := kimiResponse(test.text)
			if test.withTool {
				root["output"] = append(root["output"].([]any), map[string]any{
					"type": "function_call", "name": "get_weather", "call_id": "call_fixture",
					"arguments": `{"city":"北京"}`,
				})
			}
			if test.status == 400 {
				root = map[string]any{"error": map[string]any{
					"type": "invalid_request_error", "message": "unsupported tool_choice value",
				}}
			}
			fixture := &kimiResponsesFixture{status: test.status, root: root}
			observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
				Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
			})
			var body map[string]any
			if err := json.Unmarshal(observation.Exchanges[0].RequestBody, &body); err != nil {
				t.Fatal(err)
			}
			if body["tool_choice"] != test.choice {
				t.Fatalf("native choice = %v, want %s", body["tool_choice"], test.choice)
			}
			verdict := testspec.Evaluate(spec.Assertions, observation)
			if (verdict.Status == testspec.VerdictPassed) != test.passed {
				t.Fatalf("native choice verdict = %+v", verdict)
			}
		})
	}
}

func TestKimiResponsesCacheWorkflowUsesTwoNativeRequests(t *testing.T) {
	spec := kimiResponsesSpec(t, "K3-cache-prefix-hit")
	fixture := &kimiResponsesFixture{status: 200, root: kimiResponse("CACHE_READY")}
	observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
	})
	verdict := testspec.Evaluate(spec.Assertions, observation)
	if fixture.calls != 2 || verdict.Status != testspec.VerdictPassed {
		t.Fatalf("cache workflow calls=%d, verdict=%+v", fixture.calls, verdict)
	}
	for _, exchange := range observation.Exchanges {
		var body map[string]any
		if err := json.Unmarshal(exchange.RequestBody, &body); err != nil {
			t.Fatal(err)
		}
		nativeRequest := exchange.Path == "/v1/responses" && body["input"] != nil
		if !nativeRequest || body["messages"] != nil {
			t.Fatal("cache workflow did not use the native Responses request")
		}
	}
}

func TestKimiResponsesMinimalOutputAssertsNativeIncompleteReason(t *testing.T) {
	spec := kimiResponsesSpec(t, "P050-max-completion-minimal")
	root := kimiResponse("")
	root["status"] = "incomplete"
	root["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	usage := root["usage"].(map[string]any)
	usage["output_tokens"] = 1
	usage["total_tokens"] = 513
	usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] = 1
	fixture := &kimiResponsesFixture{status: 200, root: root}
	observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
	})
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
		t.Fatalf("expected output limit = %+v", verdict)
	}
	if len(observation.Issues) != 0 {
		t.Fatalf("documented output truncation created a protocol issue: %+v", observation.Issues)
	}
	usage["output_tokens"] = 4
	usage["total_tokens"] = 516
	observation = openairesponses.New().Execute(context.Background(), runtime.Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
	})
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status == testspec.VerdictPassed {
		t.Fatal("usage above the documented output budget passed")
	}
	usage["output_tokens"] = 1
	usage["total_tokens"] = 513
	root["incomplete_details"] = map[string]any{"reason": "content_filter"}
	observation = openairesponses.New().Execute(context.Background(), runtime.Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
	})
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status == testspec.VerdictPassed {
		t.Fatal("content filtering passed an output-budget assertion")
	}
}

func TestKimiResponsesMaxReasoningAcceptsOnlyDocumentedTerminalOutcomes(t *testing.T) {
	spec := kimiResponsesSpec(t, "R003-reasoning-effort-max")
	for _, test := range []struct {
		name, status, reason string
		outputTokens         int
		passed               bool
	}{
		{name: "completed", status: "completed", outputTokens: 3, passed: true},
		{name: "budget reached", status: "incomplete", reason: "max_output_tokens", outputTokens: 2048, passed: true},
		{name: "budget exceeded", status: "incomplete", reason: "max_output_tokens", outputTokens: 2049},
		{name: "filtered", status: "incomplete", reason: "content_filter", outputTokens: 3},
		{name: "missing reason", status: "incomplete", outputTokens: 3},
		{name: "provider failed", status: "failed", outputTokens: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := kimiResponse("2")
			root["status"] = test.status
			if test.reason != "" {
				root["incomplete_details"] = map[string]any{"reason": test.reason}
			}
			usage := root["usage"].(map[string]any)
			usage["output_tokens"] = test.outputTokens
			usage["total_tokens"] = 512 + test.outputTokens
			fixture := &kimiResponsesFixture{status: 200, root: root}
			observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
				Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
			})
			verdict := testspec.Evaluate(spec.Assertions, observation)
			if (verdict.Status == testspec.VerdictPassed) != test.passed {
				t.Fatalf("max reasoning verdict = %+v", verdict)
			}
		})
	}
}

func TestKimiResponsesOutputMaximumDoesNotAssertCombinedContextOverflow(t *testing.T) {
	spec := kimiResponsesSpec(t, "P051-max-completion-context-overflow")
	fixture := &kimiResponsesFixture{status: 200, root: kimiResponse("你好")}
	observation := openairesponses.New().Execute(context.Background(), runtime.Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{}, Transport: fixture,
	})
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
		t.Fatalf("documented output maximum was rejected: %+v", verdict)
	}
}
