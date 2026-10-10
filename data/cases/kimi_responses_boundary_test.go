package casebundle_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols/openairesponses"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func kimiNativeCase(t *testing.T, directory string) domain.TestCase {
	t.Helper()
	path := "openai-responses/K3-" + directory + "/case.json"
	raw, err := fs.ReadFile(casebundle.Bundle, path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := casecodec.DecodeFilesystemCase(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func kimiNativeSpec(t *testing.T, directory string) testspec.Spec {
	t.Helper()
	spec, err := kimiNativeCase(t, directory).SpecFor(domain.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func kimiBoundaryObservation(spec testspec.Spec, root map[string]any, status int) testspec.Observation {
	return openairesponses.New().Execute(context.Background(), runtime.Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{},
		Transport: &kimiResponsesFixture{status: status, root: root},
	})
}

func TestKimiResponsesBoundaryRejectionsReachProviderUnchanged(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "openai-responses/K3-*/case.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("boundary discovery: %v", err)
	}
	for _, directory := range []string{
		"F012-tool-choice-allowed-tools", "F023-video-url-object", "F024-video-url-string",
		"F029-multimodal-video-url-required", "F030-image-url-object-url-required", "F031-video-url-object-url-required",
	} {
		paths = append(paths, "openai-chat/"+directory+"/case.json")
	}
	for _, path := range paths {
		raw, readErr := fs.ReadFile(casebundle.Bundle, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		item, decodeErr := casecodec.DecodeFilesystemCase(path, raw)
		if decodeErr != nil {
			t.Fatalf("%s: %v", path, decodeErr)
		}
		spec, specErr := item.SpecFor(domain.ProtocolOpenAIResponses)
		if specErr != nil {
			t.Fatal(specErr)
		}
		if string(spec.Assertions[0].Value) != "400" {
			continue
		}
		t.Run(item.Key, func(t *testing.T) {
			errorRoot := map[string]any{"error": map[string]any{
				"type": "invalid_request_error", "message": "invalid native request",
			}}
			observation := kimiBoundaryObservation(spec, errorRoot, 400)
			if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
				t.Fatalf("documented rejection failed: %+v", verdict)
			}
			if len(observation.Exchanges) != 1 || observation.Exchanges[0].Path != "/v1/responses" {
				t.Fatalf("native admission exchange = %+v", observation.Exchanges)
			}
			var expected, actual any
			if err := json.Unmarshal(spec.Request.Body, &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(observation.Exchanges[0].RequestBody, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, actual) {
				t.Fatal("invalid provider input was normalized or silently dropped")
			}
			accepted := kimiBoundaryObservation(spec, kimiResponse("READY"), 200)
			if verdict := testspec.Evaluate(spec.Assertions, accepted); verdict.Status == testspec.VerdictPassed {
				t.Fatal("unsupported request accepted with HTTP200 passed")
			}
			errorRoot["error"].(map[string]any)["type"] = "server_error"
			wrongError := kimiBoundaryObservation(spec, errorRoot, 400)
			if verdict := testspec.Evaluate(spec.Assertions, wrongError); verdict.Status == testspec.VerdictPassed {
				t.Fatal("unrelated HTTP400 error passed a parameter rejection")
			}
		})
	}
}

func TestKimiResponsesVideoDefinitionsReuseChatAssetsAndRejectNativeVideo(t *testing.T) {
	for _, directory := range []string{"F023-video-url-object", "F024-video-url-string"} {
		t.Run(directory, func(t *testing.T) {
			spec := kimiResponsesSpec(t, directory)
			var body map[string]any
			if err := json.Unmarshal(spec.Request.Body, &body); err != nil {
				t.Fatal(err)
			}
			message := body["input"].([]any)[0].(map[string]any)
			parts := message["content"].([]any)
			videoFound := false
			for _, rawPart := range parts {
				part := rawPart.(map[string]any)
				if part["type"] == "text" {
					t.Fatal("Chat text discriminator masks the intended video rejection")
				}
				if part["type"] != "video_url" {
					continue
				}
				url, isString := part["video_url"].(string)
				if !isString {
					url = part["video_url"].(map[string]any)["url"].(string)
				}
				videoFound = strings.HasPrefix(url, "data:video/mp4;base64,")
			}
			if !videoFound {
				t.Fatal("video rejection lost its repository-owned valid video fixture")
			}
		})
	}
}

func TestKimiResponsesBoundarySuccessRequiresSemanticResults(t *testing.T) {
	for _, directory := range []string{
		"input-string", "message-type-omitted", "message-role-assistant", "message-role-developer",
		"output-text-history", "instructions", "history-function-string", "history-function-parts",
		"history-custom-tool-string", "history-custom-tool-parts", "search-history", "include-without-search",
	} {
		t.Run(directory, func(t *testing.T) {
			spec := kimiNativeSpec(t, directory)
			for _, answer := range []string{"READY", "WRONG"} {
				observation := kimiBoundaryObservation(spec, kimiResponse(answer), 200)
				passed := testspec.Evaluate(spec.Assertions, observation).Status == testspec.VerdictPassed
				if passed != (answer == "READY") {
					t.Fatalf("answer %q passed=%v", answer, passed)
				}
			}
		})
	}
	for _, directory := range []string{"schema-strict-true", "schema-strict-false"} {
		t.Run(directory, func(t *testing.T) {
			spec := kimiNativeSpec(t, directory)
			for _, text := range []string{`{"code":"READY"}`, `{"code":7}`, `{"code":"WRONG"}`} {
				observation := kimiBoundaryObservation(spec, kimiResponse(text), 200)
				passed := testspec.Evaluate(spec.Assertions, observation).Status == testspec.VerdictPassed
				if passed != (text == `{"code":"READY"}`) {
					t.Fatalf("JSON %s passed=%v", text, passed)
				}
			}
		})
	}
}

func TestKimiResponsesNamespaceAndCustomRequireActualNativeCalls(t *testing.T) {
	for _, directory := range []string{"namespace-function", "namespace-custom", "custom-apply-patch"} {
		t.Run(directory, func(t *testing.T) {
			spec := kimiNativeSpec(t, directory)
			call := map[string]any{
				"type": "custom_tool_call", "name": "apply_patch", "call_id": "call_fixture",
				"input": "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch",
			}
			if directory == "namespace-function" {
				call = map[string]any{
					"type": "function_call", "name": "get_number", "call_id": "call_fixture", "arguments": "{}",
				}
			}
			if strings.HasPrefix(directory, "namespace") {
				call["namespace"] = "math_tools"
			}
			root := kimiResponse("")
			root["output"] = append(root["output"].([]any), call)
			observation := kimiBoundaryObservation(spec, root, 200)
			if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
				t.Fatalf("valid native call: %+v", verdict)
			}
			delete(call, "call_id")
			observation = kimiBoundaryObservation(spec, root, 200)
			if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status == testspec.VerdictPassed {
				t.Fatal("call without call_id passed")
			}
		})
	}
}

func TestKimiResponsesProviderReplayUsesOriginalOutputAndStaysDisabled(t *testing.T) {
	item := kimiNativeCase(t, "reasoning-provider-replay")
	if item.Enabled || item.Default {
		t.Fatal("expensive replay unexpectedly enabled")
	}
	spec := kimiNativeSpec(t, "reasoning-provider-replay")
	observation := kimiBoundaryObservation(spec, kimiResponse("READY"), 200)
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
		t.Fatalf("native replay fixture: %+v", verdict)
	}
	var first, second map[string]any
	if err := json.Unmarshal(observation.Exchanges[0].Response, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(observation.Exchanges[1].RequestBody, &second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first["output"], second["input"]) {
		t.Fatal("replay changed provider-returned output items")
	}
}

func TestKimiResponsesSSEEnvelopeRejectsMissingSequenceNumbers(t *testing.T) {
	spec := kimiNativeSpec(t, "stream-event-envelope")
	observation := kimiBoundaryObservation(spec, kimiResponse("READY"), 200)
	completed := true
	observation.StreamCompleted = &completed
	observation.Exchanges[0].Events = []json.RawMessage{
		json.RawMessage(`{"type":"response.created","sequence_number":0}`),
		json.RawMessage(`{"type":"response.completed","sequence_number":1}`),
	}
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
		t.Fatalf("typed stream envelope: %+v", verdict)
	}
	observation.Exchanges[0].Events[1] = json.RawMessage(`{"type":"response.completed"}`)
	if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status == testspec.VerdictPassed {
		t.Fatal("stream event missing documented sequence_number passed")
	}
}

func TestKimiResponsesSearchImageLimitsRequireResultsWithinRequestedCap(t *testing.T) {
	for _, directory := range []string{"search-image-max-1", "search-image-max-5", "search-image-max-10"} {
		t.Run(directory, func(t *testing.T) {
			item := kimiNativeCase(t, directory)
			if item.Enabled || item.Default {
				t.Fatal("paid search case unexpectedly enabled")
			}
			spec := kimiNativeSpec(t, directory)
			var body struct {
				Tools []struct {
					ImageSettings struct {
						MaxResults int `json:"max_results"`
					} `json:"image_settings"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(spec.Request.Body, &body); err != nil {
				t.Fatal(err)
			}
			maximum := body.Tools[0].ImageSettings.MaxResults
			for _, count := range []int{0, maximum, maximum + 1} {
				results := []any{}
				for range count {
					results = append(results, map[string]any{
						"type": "image_result", "image_url": "https://platform.kimi.com/fixture.png",
					})
				}
				root := kimiResponse("fixture search answer")
				root["output"] = append(root["output"].([]any), map[string]any{
					"type": "web_search_call", "status": "completed", "results": results,
				})
				observation := kimiBoundaryObservation(spec, root, 200)
				passed := testspec.Evaluate(spec.Assertions, observation).Status == testspec.VerdictPassed
				if passed != (count == maximum) {
					t.Fatalf("results=%d maximum=%d passed=%v", count, maximum, passed)
				}
			}
		})
	}
}
