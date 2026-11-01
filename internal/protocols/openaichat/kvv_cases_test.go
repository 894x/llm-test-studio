package openaichat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/openaichat"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type kvvTransport func(context.Context, runtime.Request) (runtime.Response, error)

func (send kvvTransport) Send(ctx context.Context, request runtime.Request) (runtime.Response, error) {
	return send(ctx, request)
}

func loadKVVSpec(t *testing.T, directory string) testspec.Spec {
	t.Helper()
	path := filepath.Join("..", "..", "..", "data", "cases", "openai-chat", directory, "case.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	spec, err := testspec.Decode(doc.Definitions["openai-chat"])
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func runKVVCase(t *testing.T, directory string, status int, contentType, response string) testspec.Verdict {
	t.Helper()
	spec := loadKVVSpec(t, directory)
	execution := runtime.Execution{
		Spec: spec,
		Inputs: testspec.ResolvedInputs{
			JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{},
		},
		Transport: kvvTransport(func(_ context.Context, request runtime.Request) (runtime.Response, error) {
			var body map[string]any
			if err := json.Unmarshal(request.Body.Body, &body); err != nil {
				t.Fatal(err)
			}
			if body["stream"] != strings.HasSuffix(directory, "-stream") {
				t.Fatalf("request mode not preserved: %s", request.Body.Body)
			}
			if strings.Contains(directory, "dynamic-system") {
				if !strings.Contains(string(request.Body.Body), `"tools"`) {
					t.Fatalf("dynamic tools not preserved: %s", request.Body.Body)
				}
			}
			return runtime.Response{
				Started: time.Now(), RequestBody: request.Body.Body,
				HTTP: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}},
					Body: io.NopCloser(strings.NewReader(response))},
			}, nil
		}),
	}
	observation := openaichat.New().Execute(context.Background(), execution)
	return testspec.Evaluate(spec.Assertions, observation)
}

func TestKVVToolSchemaChecksBothModesAndBrokenStreams(t *testing.T) {
	sync := `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"type":"function","function":{"name":"weather","arguments":"{\"city\":\"北京\"}"}}]}}]}`
	chunk := func(delta, finish string) string {
		return fmt.Sprintf("data: {\"id\":\"id\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, finish)
	}
	stream := chunk(`{"role":"assistant","tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"weather","arguments":"{\"ci"}}]}`, `null`) +
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"北京\"}"}}]}`, `null`) +
		chunk(`{}`, `"tool_calls"`) + "data: [DONE]\n\n"
	for _, tc := range []struct {
		name, directory, contentType, response string
		want                                   testspec.VerdictStatus
	}{
		{"sync passes", "KVV-tool-schema-flat-sync", "application/json", sync, testspec.VerdictPassed},
		{"sync wrong argument type", "KVV-tool-schema-flat-sync", "application/json", strings.Replace(sync, `\"北京\"`, `123`, 1), testspec.VerdictFailed},
		{"stream joins arguments", "KVV-dynamic-system-after-user-stream", "text/event-stream", stream, testspec.VerdictPassed},
		{"missing done fails", "KVV-dynamic-system-after-user-stream", "text/event-stream", strings.Replace(stream, "data: [DONE]\n\n", "", 1), testspec.VerdictFailed},
		{"wrong tool fails", "KVV-dynamic-system-after-user-stream", "text/event-stream", strings.Replace(stream, `weather`, `other`, 1), testspec.VerdictFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verdict := runKVVCase(t, tc.directory, 200, tc.contentType, tc.response)
			if verdict.Status != tc.want {
				t.Fatalf("verdict = %+v, want %s", verdict, tc.want)
			}
		})
	}
}

func TestKVVRejectionAndStrictFormatAssertions(t *testing.T) {
	for _, mode := range []string{"sync", "stream"} {
		t.Run("rejection "+mode, func(t *testing.T) {
			directory := "KVV-dynamic-role-user-" + mode
			for _, tc := range []struct {
				status int
				body   string
				want   testspec.VerdictStatus
			}{
				{400, `{"error":{"message":"invalid role"}}`, testspec.VerdictPassed},
				{400, `{"error":{"message":""}}`, testspec.VerdictFailed},
				{200, `{"error":{"message":"invalid role"}}`, testspec.VerdictFailed},
			} {
				if got := runKVVCase(t, directory, tc.status, "application/json", tc.body); got.Status != tc.want {
					t.Fatalf("verdict = %+v, want %s", got, tc.want)
				}
			}
		})
	}
	valid := `{"choices":[{"finish_reason":"stop","message":{"content":"{\"name\":\"A\",\"active\":true}"}}]}`
	invalid := `{"choices":[{"finish_reason":"stop","message":{"content":"{\"name\":\"A\",\"active\":true,\"extra\":1}"}}]}`
	for _, tc := range []struct {
		body string
		want testspec.VerdictStatus
	}{{valid, testspec.VerdictPassed}, {invalid, testspec.VerdictFailed}} {
		if got := runKVVCase(t, "KVV-format-schema-flat-sync", 200, "application/json", tc.body); got.Status != tc.want {
			t.Fatalf("verdict = %+v, want %s", got, tc.want)
		}
	}
}

func TestKVVPromptTokenBaselineSupportsBothUsageLocations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event string
		want  testspec.VerdictStatus
	}{
		{"top minimum", `{"id":"id","model":"model","usage":{"prompt_tokens":99},"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, testspec.VerdictPassed},
		{"choice maximum", `{"id":"id","model":"model","choices":[{"index":0,"delta":{},"finish_reason":"stop","usage":{"prompt_tokens":102}}]}`, testspec.VerdictPassed},
		{"choice usage followed by null", `{"id":"id","model":"model","choices":[{"index":0,"delta":{},"usage":{"prompt_tokens":100}}]}` + "\n\ndata: " + `{"id":"id","model":"model","choices":[{"index":0,"delta":{},"usage":null}]}`, testspec.VerdictPassed},
		{"top usage followed by null", `{"id":"id","model":"model","usage":{"prompt_tokens":100},"choices":[{"index":0,"delta":{}}]}` + "\n\ndata: " + `{"id":"id","model":"model","usage":null,"choices":[{"index":0,"delta":{}}]}`, testspec.VerdictPassed},
		{"below range", `{"id":"id","model":"model","usage":{"prompt_tokens":98},"choices":[{"index":0,"delta":{}}]}`, testspec.VerdictFailed},
		{"above range", `{"id":"id","model":"model","usage":{"prompt_tokens":103},"choices":[{"index":0,"delta":{}}]}`, testspec.VerdictFailed},
		{"non-integer", `{"id":"id","model":"model","usage":{"prompt_tokens":100.5},"choices":[{"index":0,"delta":{}}]}`, testspec.VerdictFailed},
		{"missing", `{"id":"id","model":"model","choices":[{"index":0,"delta":{}}]}`, testspec.VerdictFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := "data: " + tc.event + "\n\ndata: [DONE]\n\n"
			got := runKVVCase(t, "KVV-prompt-tokens-assistant-hello-stream", 200, "text/event-stream", stream)
			if got.Status != tc.want {
				t.Fatalf("verdict = %+v, want %s", got, tc.want)
			}
		})
	}
}
