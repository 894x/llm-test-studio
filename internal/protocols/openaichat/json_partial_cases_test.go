package openaichat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/openaichat"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func jsonPartialFixtureResponse(t *testing.T, text, mode, finish string) string {
	t.Helper()
	message := map[string]any{"role": "assistant", "content": text}
	choice := map[string]any{"index": 0, "finish_reason": finish, "message": message}
	if mode == "stream" {
		choice["delta"] = message
		delete(choice, "message")
	}
	chunk := map[string]any{"id": "json-partial", "model": "fixture-model", "choices": []any{choice}}
	var leadingEvent string
	if mode == "stream" {
		characters := []rune(text)
		middle := len(characters) / 2
		message["content"] = string(characters[:middle])
		choice["finish_reason"] = nil
		first, err := json.Marshal(chunk)
		if err != nil {
			t.Fatal(err)
		}
		leadingEvent = "data: " + string(first) + "\n\n"
		message["content"] = string(characters[middle:])
		choice["finish_reason"] = finish
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "stream" {
		return leadingEvent + "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
	}
	return string(raw)
}

func TestJSONPartialCasePairsPreserveParametersAndCompareSemanticJSON(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		prefix string
		suffix string
	}{
		{name: "string", prefix: `{"answer":"PA`, suffix: `RIS"}`},
		{name: "field", prefix: `{"answer":`, suffix: "\n \"PARIS\" }\n"},
		{name: "object", prefix: "{", suffix: ` "answer" : "\u0050ARIS" }`},
		{name: "no-flag", prefix: `{"answer":`, suffix: ` { "answer" : "PARIS" } `},
		{name: "extraction", prefix: "{", suffix: `"colors":["black","silver"],
			"price_yuan":299.0,"size_mm":[120,80,30],"name":"Atlas Mini"}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requests := map[string][]byte{}
			for _, mode := range []string{"sync", "stream"} {
				directory := "JSON-partial-" + scenario.name + "-" + mode
				spec := loadKVVSpec(t, directory)
				transport := kvvTransport(func(_ context.Context, request runtime.Request) (runtime.Response, error) {
					body := map[string]any{}
					if err := json.Unmarshal(request.Body.Body, &body); err != nil {
						t.Fatal(err)
					}
					if body["stream"] != (mode == "stream") {
						t.Fatalf("wrong request mode: %s", request.Body.Body)
					}
					if body["max_tokens"] != float64(1024) {
						t.Fatalf("wrong output budget: %s", request.Body.Body)
					}
					if _, exists := body["response_format"]; exists {
						t.Fatal("Partial must not be mixed with JSON response_format")
					}
					messages := body["messages"].([]any)
					last := messages[len(messages)-1].(map[string]any)
					if last["content"] != scenario.prefix {
						t.Fatalf("prefix changed: %v", last)
					}
					if scenario.name == "no-flag" {
						if _, exists := last["partial"]; exists {
							t.Fatal("control unexpectedly sets partial")
						}
					} else if last["partial"] != true {
						t.Fatal("continuation does not enable partial")
					}
					delete(body, "stream")
					canonical, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					requests[mode] = canonical
					contentType := "application/json"
					if mode == "stream" {
						contentType = "text/event-stream"
					}
					wire := jsonPartialFixtureResponse(
						t, scenario.suffix, mode, "stop",
					)
					return runtime.Response{
						Started: time.Now(), RequestBody: request.Body.Body,
						HTTP: &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": []string{contentType}},
							Body:       io.NopCloser(strings.NewReader(wire)),
						},
					}, nil
				})
				observation := openaichat.New().Execute(context.Background(), runtime.Execution{
					Spec: spec, Transport: transport,
					Inputs: testspec.ResolvedInputs{
						JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{},
					},
				})
				if verdict := testspec.Evaluate(spec.Assertions, observation); verdict.Status != testspec.VerdictPassed {
					t.Fatalf("%s semantic JSON verdict = %+v", mode, verdict)
				}
			}
			if !bytes.Equal(requests["sync"], requests["stream"]) {
				t.Fatal("paired requests differ beyond stream")
			}
		})
	}
}

func TestJSONPartialCasesRejectObservedFailuresAndProtocolFaults(t *testing.T) {
	for _, scenario := range []struct {
		name string
		text string
	}{
		{name: "string", text: `IRS"}`},
		{name: "field", text: `{"answer":"PARIS"}`},
		{name: "object", text: `“answer”:“PARIS”}`},
		{name: "no-flag", text: `"PARIS"}`},
		{name: "extraction", text: `"name":"Atlas Mini","size_mm":[120,80,30],
			"price_yuan":"299","colors":["black","silver"]}`},
	} {
		for _, mode := range []string{"sync", "stream"} {
			t.Run(scenario.name+"/"+mode, func(t *testing.T) {
				directory := "JSON-partial-" + scenario.name + "-" + mode
				contentType := "application/json"
				if mode == "stream" {
					contentType = "text/event-stream"
				}
				for _, tc := range []struct {
					name   string
					text   string
					finish string
				}{
					{name: "observed bad output", text: scenario.text, finish: "stop"},
					{name: "empty output", finish: "stop"},
					{name: "truncated generation", text: scenario.text, finish: "length"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						wire := jsonPartialFixtureResponse(
							t, tc.text, mode, tc.finish,
						)
						verdict := runKVVCase(
							t, directory, http.StatusOK, contentType, wire,
						)
						if verdict.Status == testspec.VerdictPassed {
							t.Fatal("invalid JSON continuation passed")
						}
					})
				}
			})
		}
	}
	for _, mode := range []string{"sync", "stream"} {
		for _, finish := range []string{"length", ""} {
			t.Run(mode+"/correct JSON with wrong finish/"+finish, func(t *testing.T) {
				wire := jsonPartialFixtureResponse(
					t, ` "answer":"PARIS"}`, mode, finish,
				)
				contentType := "application/json"
				if mode == "stream" {
					contentType = "text/event-stream"
				}
				verdict := runKVVCase(
					t, "JSON-partial-object-"+mode, http.StatusOK, contentType, wire,
				)
				if verdict.Status == testspec.VerdictPassed {
					t.Fatal("wrong finish passed with otherwise-correct JSON")
				}
			})
		}
	}
	valid := jsonPartialFixtureResponse(
		t, ` "answer":"PARIS"}`, "stream", "stop",
	)
	for _, tc := range []struct {
		name string
		wire string
	}{
		{name: "missing done", wire: strings.ReplaceAll(valid, "data: [DONE]\n\n", "")},
		{name: "malformed event", wire: "data: not-json\n\n" + valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verdict := runKVVCase(
				t, "JSON-partial-object-stream", http.StatusOK, "text/event-stream", tc.wire,
			)
			if verdict.Status == testspec.VerdictPassed {
				t.Fatal("broken SSE passed with correct JSON content")
			}
		})
	}
	t.Run("wrong HTTP status with correct JSON", func(t *testing.T) {
		verdict := runKVVCase(
			t, "JSON-partial-object-stream", http.StatusBadRequest, "text/event-stream", valid,
		)
		if verdict.Status == testspec.VerdictPassed {
			t.Fatal("HTTP rejection passed with otherwise-correct JSON")
		}
	})
}
