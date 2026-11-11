package openaichat_test

import (
	"bytes"
	"context"
	"encoding/json"
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

type kvvPartialFixture struct {
	ID        string          `json:"id"`
	Request   json.RawMessage `json:"request"`
	Directory string          `json:"-"`
}

func loadKVVPartialFixtures(t *testing.T) []kvvPartialFixture {
	t.Helper()
	path := filepath.Join(
		"..", "..", "..", "data", "cases", "kimi-k3", "kvv-partial.jsonl",
	)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	directories := map[string]string{
		"k3_partial_partial_assistant":                  "KVV-partial-assistant-stream",
		"k3_partial_partial_think":                      "KVV-partial-reasoning-content-stream",
		"k3_partial_partial_think_empty_content":        "KVV-partial-reasoning-no-content-stream",
		"k3_partial_partial_think_empty_reasoning":      "KVV-partial-empty-reasoning-stream",
		"k3_partial_partial_think_no_response":          "KVV-partial-reasoning-only-stream",
		"k3_partial_tools_with_thinking_effort_partial": "KVV-partial-tools-max-stream",
		"k3_think_partial_think_no_response":            "KVV-partial-empty-content-stream",
		"k3_think_tools_with_thinking_effort_partial":   "KVV-partial-tools-high-stream",
	}
	fixtures := []kvvPartialFixture{}
	seen := map[string]bool{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var fixture kvvPartialFixture
		err := decoder.Decode(&fixture)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		fixture.Directory = directories[fixture.ID]
		if fixture.Directory == "" || seen[fixture.ID] {
			t.Fatalf("unknown or duplicate source fixture %q", fixture.ID)
		}
		seen[fixture.ID] = true
		fixtures = append(fixtures, fixture)
	}
	if len(fixtures) != len(directories) {
		t.Fatalf("Partial fixtures = %d, want %d", len(fixtures), len(directories))
	}
	return fixtures
}

type kvvPartialResponse struct {
	Text          string
	Finish        string
	ToolArguments string
	ToolName      string
	IncludeTool   bool
	Usage         map[string]any
	UsageLocation string
}

func (fixture kvvPartialFixture) response() kvvPartialResponse {
	response := kvvPartialResponse{Text: " a new response", Finish: "stop"}
	if strings.Contains(fixture.Directory, "tools-") {
		response.Text = ""
		response.Finish = "tool_calls"
		response.ToolName = "CodeRunner"
		response.ToolArguments = `{"language":"python","code":"print(1)"}`
		response.IncludeTool = true
	}
	return response
}

func kvvPartialStream(t *testing.T, response kvvPartialResponse) string {
	t.Helper()
	text := []rune(response.Text)
	middle := len(text) / 2
	deltas := []map[string]any{
		{"role": "assistant", "content": string(text[:middle])},
		{"content": string(text[middle:])},
	}
	if response.IncludeTool {
		arguments := response.ToolArguments
		middle := len(arguments) / 2
		deltas[0]["tool_calls"] = []any{map[string]any{
			"index": 0, "id": "partial-call", "type": "function",
			"function": map[string]any{"name": response.ToolName, "arguments": arguments[:middle]},
		}}
		deltas[1]["tool_calls"] = []any{map[string]any{
			"index": 0, "function": map[string]any{"arguments": arguments[middle:]},
		}}
	}
	var stream strings.Builder
	for index, delta := range deltas {
		choice := map[string]any{"index": 0, "delta": delta, "finish_reason": nil}
		event := map[string]any{
			"id": "partial-fixture", "model": "fixture-model", "choices": []any{choice},
		}
		if index == len(deltas)-1 {
			choice["finish_reason"] = response.Finish
			if response.Usage != nil {
				if response.UsageLocation == "choice" {
					choice["usage"] = response.Usage
				} else {
					event["usage"] = response.Usage
				}
			}
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		stream.WriteString("data: " + string(raw) + "\n\n")
	}
	stream.WriteString("data: [DONE]\n\n")
	return stream.String()
}

func TestKVVPartialRequestsPreserveGoldenFields(t *testing.T) {
	for _, fixture := range loadKVVPartialFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			spec := loadKVVSpec(t, fixture.Directory)
			want := map[string]any{}
			if err := json.Unmarshal(fixture.Request, &want); err != nil {
				t.Fatal(err)
			}
			want["stream"] = true
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			transport := kvvTransport(func(_ context.Context, request runtime.Request) (runtime.Response, error) {
				calls++
				got := map[string]any{}
				if err := json.Unmarshal(request.Body.Body, &got); err != nil {
					t.Fatal(err)
				}
				gotJSON, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(gotJSON, wantJSON) {
					t.Fatalf("golden request changed:\ngot %s\nwant %s", gotJSON, wantJSON)
				}
				stream := kvvPartialStream(t, fixture.response())
				return runtime.Response{
					Started: time.Now(), RequestBody: request.Body.Body,
					HTTP: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(stream)),
					},
				}, nil
			})
			observation := openaichat.New().Execute(context.Background(), runtime.Execution{
				Spec: spec, Transport: transport,
				Inputs: testspec.ResolvedInputs{
					JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{},
				},
			})
			verdict := testspec.Evaluate(spec.Assertions, observation)
			if calls != 1 || verdict.Status != testspec.VerdictPassed {
				t.Fatalf("calls = %d, golden verdict = %+v", calls, verdict)
			}
		})
	}
}

func TestKVVPartialContinuationDoesNotDependOnTokenCounts(t *testing.T) {
	for _, fixture := range loadKVVPartialFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			for _, location := range []string{"missing", "top", "choice"} {
				t.Run(location, func(t *testing.T) {
					for _, tokens := range []int{0, 1, 999999} {
						response := fixture.response()
						if location != "missing" {
							response.Usage = map[string]any{"prompt_tokens": tokens}
							response.UsageLocation = location
						}
						stream := kvvPartialStream(t, response)
						verdict := runKVVCase(
							t, fixture.Directory, http.StatusOK, "text/event-stream", stream,
						)
						if verdict.Status != testspec.VerdictPassed {
							t.Fatalf("tokens %d changed continuation verdict: %+v", tokens, verdict)
						}
					}
				})
			}
		})
	}
}

func TestPartialCatalogExcludesTokenAndFixedAnswerAssertions(t *testing.T) {
	directories := []string{}
	for _, fixture := range loadKVVPartialFixtures(t) {
		directories = append(directories, fixture.Directory)
	}
	for _, scenario := range []string{"string", "field", "object", "no-flag", "extraction"} {
		for _, mode := range []string{"sync", "stream"} {
			directories = append(directories, "JSON-partial-"+scenario+"-"+mode)
		}
	}
	var check func(*testing.T, []testspec.Assertion)
	check = func(t *testing.T, assertions []testspec.Assertion) {
		t.Helper()
		for _, assertion := range assertions {
			checksUsage := assertion.Source == "usage" || strings.Contains(assertion.Pointer, "/usage")
			checksTokens := strings.Contains(assertion.Pointer, "token")
			if checksUsage || checksTokens || assertion.Operator == "json_prefix_equals" {
				t.Errorf("unexpected token or fixed-answer assertion: %+v", assertion)
			}
			check(t, assertion.All)
			check(t, assertion.Any)
			check(t, assertion.Each)
		}
	}
	for _, directory := range directories {
		t.Run(directory, func(t *testing.T) {
			check(t, loadKVVSpec(t, directory).Assertions)
		})
	}
}

func TestKVVPartialRejectsEmptyRepeatedAndBrokenResponses(t *testing.T) {
	for _, fixture := range loadKVVPartialFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			valid := kvvPartialStream(t, fixture.response())
			cases := []struct {
				name   string
				status int
				stream string
			}{
				{name: "missing done", status: http.StatusOK,
					stream: strings.TrimSuffix(valid, "data: [DONE]\n\n")},
				{name: "malformed event", status: http.StatusOK,
					stream: "data: not-json\n\n" + valid},
				{name: "wrong HTTP status", status: http.StatusBadRequest, stream: valid},
			}
			invalid := map[string]kvvPartialResponse{}
			for _, finish := range []string{"length", ""} {
				response := fixture.response()
				response.Finish = finish
				invalid["wrong finish "+finish] = response
			}
			if strings.Contains(fixture.Directory, "tools-") {
				for _, arguments := range []string{"", "not-json", "[]", "null"} {
					response := fixture.response()
					response.ToolArguments = arguments
					invalid["bad tool arguments "+arguments] = response
				}
				response := fixture.response()
				response.IncludeTool = false
				invalid["missing tool call"] = response
				response = fixture.response()
				response.ToolName = "UnknownFunction"
				invalid["unknown tool name"] = response
			} else {
				for _, text := range []string{"", " \n\t"} {
					response := fixture.response()
					response.Text = text
					invalid["blank content "+text] = response
				}
			}
			request := map[string]any{}
			if err := json.Unmarshal(fixture.Request, &request); err != nil {
				t.Fatal(err)
			}
			messages := request["messages"].([]any)
			last := messages[len(messages)-1].(map[string]any)
			if prefix, ok := last["content"].(string); ok && prefix != "" {
				response := fixture.response()
				response.Text = prefix + " repeated prefix"
				invalid["repeated prefix"] = response
			}
			for name, response := range invalid {
				cases = append(cases, struct {
					name   string
					status int
					stream string
				}{name: name, status: http.StatusOK, stream: kvvPartialStream(t, response)})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					verdict := runKVVCase(
						t, fixture.Directory, tc.status, "text/event-stream", tc.stream,
					)
					if verdict.Status != testspec.VerdictFailed {
						t.Fatalf("invalid response passed: %+v", verdict)
					}
				})
			}
		})
	}
}
