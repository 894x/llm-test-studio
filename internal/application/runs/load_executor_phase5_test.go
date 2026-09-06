package runs

import (
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestDraftFromObservationMapsPartialFineStreamingTelemetry(t *testing.T) {
	draft := draftFromObservation("case-1", load.Observation{
		Index: 2, E2E: 10 * time.Millisecond, Streaming: true,
		TTFB: time.Millisecond, TTFT: 9 * time.Millisecond, TTFTAny: 2 * time.Millisecond,
		TTFTVisible: 3 * time.Millisecond, TTST: 4 * time.Millisecond, ObservedICL: 2 * time.Millisecond,
		SemanticChunkCount: 2, CompletionTokens: 3, ErrorCode: load.ErrorIncompleteStream,
	})
	for name, want := range map[string]float64{
		"ttfb_ms": 1, "ttft_any_ms": 2, "ttft_ms": 2, "ttft_visible_ms": 3,
		"ttst_ms": 4, "observed_icl_ms": 2, "semantic_chunk_count": 2, "tpot_ms": 4,
	} {
		if got := draft.Metrics[name]; got != want {
			t.Fatalf("metric %s = %v, want %v; draft = %#v", name, got, want, draft)
		}
	}
	if draft.Success.Overall() || draft.ErrorCode != load.ErrorIncompleteStream {
		t.Fatalf("failed partial draft = %#v", draft)
	}
}

func TestDraftFromObservationOmitsUnavailableTextTelemetry(t *testing.T) {
	draft := draftFromObservation("case-1", load.Observation{
		E2E: 5 * time.Millisecond, Streaming: true, TTFB: time.Millisecond, Success: true,
	})
	if draft.Metrics["semantic_chunk_count"] != 0 || draft.Metrics["ttfb_ms"] != 1 {
		t.Fatalf("available metrics = %#v", draft.Metrics)
	}
	for _, name := range []string{"ttft_ms", "ttft_any_ms", "ttft_visible_ms", "ttst_ms", "observed_icl_ms", "tpot_ms"} {
		if _, ok := draft.Metrics[name]; ok {
			t.Fatalf("unavailable metric %s was emitted: %#v", name, draft.Metrics)
		}
	}
}
