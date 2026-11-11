package openaichat_test

import (
	"bytes"
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

type kvvPartialFixture struct {
	ID                   string          `json:"id"`
	Request              json.RawMessage `json:"request"`
	ExpectedPromptTokens int             `json:"expected_prompt_tokens"`
	Directory            string          `json:"-"`
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

func kvvPartialUsageEvent(t *testing.T, tokens any, location string) string {
	t.Helper()
	choice := map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}
	event := map[string]any{
		"id": "partial-fixture", "model": "fixture-model", "choices": []any{choice},
	}
	usage := map[string]any{"prompt_tokens": tokens}
	switch location {
	case "top":
		event["usage"] = usage
	case "choice":
		choice["usage"] = usage
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(raw) + "\n\n"
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
				stream := kvvPartialUsageEvent(t, fixture.ExpectedPromptTokens, "top") + "data: [DONE]\n\n"
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

func TestKVVPartialTokenRangesAndUsageLocations(t *testing.T) {
	for _, fixture := range loadKVVPartialFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			for _, location := range []string{"top", "choice"} {
				t.Run(location, func(t *testing.T) {
					for _, tc := range []struct {
						name   string
						tokens any
						want   testspec.VerdictStatus
					}{
						{name: "minimum", tokens: fixture.ExpectedPromptTokens, want: testspec.VerdictPassed},
						{name: "maximum", tokens: fixture.ExpectedPromptTokens + 3, want: testspec.VerdictPassed},
						{name: "below", tokens: fixture.ExpectedPromptTokens - 1, want: testspec.VerdictFailed},
						{name: "above", tokens: fixture.ExpectedPromptTokens + 4, want: testspec.VerdictFailed},
						{name: "fraction", tokens: float64(fixture.ExpectedPromptTokens) + 0.5,
							want: testspec.VerdictFailed},
						{name: "string", tokens: fmt.Sprint(fixture.ExpectedPromptTokens), want: testspec.VerdictFailed},
						{name: "boolean", tokens: true, want: testspec.VerdictFailed},
						{name: "null", tokens: nil, want: testspec.VerdictFailed},
					} {
						t.Run(tc.name, func(t *testing.T) {
							stream := kvvPartialUsageEvent(t, tc.tokens, location) + "data: [DONE]\n\n"
							verdict := runKVVCase(
								t, fixture.Directory, http.StatusOK, "text/event-stream", stream,
							)
							if verdict.Status != tc.want {
								t.Fatalf("verdict = %+v, want %s", verdict, tc.want)
							}
						})
					}
				})
			}
		})
	}
}

func TestKVVPartialRejectsMissingUsageAndBrokenStreams(t *testing.T) {
	for _, fixture := range loadKVVPartialFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			valid := kvvPartialUsageEvent(t, fixture.ExpectedPromptTokens, "top")
			for _, tc := range []struct {
				name   string
				status int
				stream string
			}{
				{name: "missing usage", status: http.StatusOK,
					stream: kvvPartialUsageEvent(t, nil, "missing") + "data: [DONE]\n\n"},
				{name: "missing done", status: http.StatusOK, stream: valid},
				{name: "malformed event", status: http.StatusOK,
					stream: "data: not-json\n\n" + valid + "data: [DONE]\n\n"},
				{name: "wrong HTTP status", status: http.StatusBadRequest, stream: valid + "data: [DONE]\n\n"},
			} {
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
