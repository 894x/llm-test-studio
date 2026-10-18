package load

import (
	"testing"
	"time"
)

func TestObservedZeroLatencyRemainsInMetricsWithoutReinterpretingLegacy(t *testing.T) {
	zero := Observation{Success: true, Streaming: true, TTFTObserved: true,
		SemanticChunkCount: 2, CompletionTokens: 2}
	normalized := normalizeObservationTimings(zero)
	if !normalized.TTFTObserved || normalized.SemanticChunkCount != 2 {
		t.Fatal("normalization discarded a real zero observation")
	}
	positive := Observation{Success: true, Streaming: true, E2E: 20 * time.Millisecond,
		TTFT: 10 * time.Millisecond, TTFTAny: 10 * time.Millisecond, CompletionTokens: 2}
	metrics := ComputeMetrics([]Observation{zero, positive}, time.Second)
	if metrics.TTFTSamples != 2 || metrics.TTFTAverage != 5 || metrics.TPOTAverage != 5 || metrics.TTSTSamples != 1 {
		t.Fatalf("zero samples were dropped: %+v", metrics)
	}
	zero.TTFTObserved = false
	legacy := ComputeMetrics([]Observation{zero, positive}, time.Second)
	if legacy.TTFTSamples != 1 || legacy.TTFTAverage != 10 || legacy.TPOTAverage != 10 {
		t.Fatal("historical unmarked observations changed interpretation")
	}
}
