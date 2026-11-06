package benchmarkruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/benchmarks"
	"github.com/894x/llm-test-studio/internal/domain"
)

// Opt-in because preparing the pinned environment and downloading ARC requires
// network access. Model inference stays on the local deterministic HTTP server.
func TestInspectProcessRunsRegisteredDataset(t *testing.T) {
	if os.Getenv("LLM_TEST_INSPECT_INTEGRATION") != "1" {
		t.Skip("set LLM_TEST_INSPECT_INTEGRATION=1 for the real Inspect process")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	adapter, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer local-test-key" {
			t.Errorf("invalid upstream request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "local", "object": "chat.completion", "created": 1, "model": "local-test",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "ANSWER: A"}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
		})
	}))
	defer server.Close()
	events := []benchmarks.Event{}
	err = adapter.Execute(ctx, benchmarks.WorkerRequest{SchemaVersion: 1, RunID: "run", ReportID: "report", DatasetID: "arc_easy",
		Protocol: domain.ProtocolOpenAIChat, Model: "local-test", BaseURL: server.URL + "/v1", APIKey: "local-test-key", LogDir: t.TempDir(),
		Options: benchmarks.Options{SampleLimit: 1, Epochs: 1, Concurrency: 1, MaxTokens: 32, TimeoutSeconds: 10, Seed: 1}},
		func(event benchmarks.Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatalf("Inspect process: %v; events=%+v", err, events)
	}
	if len(events) == 0 {
		t.Fatal("no Inspect events")
	}
	result := events[len(events)-1]
	if result.Kind != "result" || result.Status != "completed" || result.Completed != 1 || len(result.Metrics) == 0 || result.Provenance == nil {
		t.Fatalf("missing dataset report: %+v", result)
	}
}
