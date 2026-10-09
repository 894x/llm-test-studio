package textapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type timingDecoder struct{}

func (timingDecoder) JSON(map[string]any, *testspec.Observation) {}
func (timingDecoder) Finish(*testspec.Observation)               {}
func (timingDecoder) Event(event map[string]any, _ *testspec.Observation) (bool, bool, bool) {
	semantic, _ := event["semantic"].(bool)
	terminal, _ := event["terminal"].(bool)
	return semantic, semantic, terminal
}

func TestReadStreamMeasuresSecondSemanticChunkAfterFirstFrame(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		for _, payload := range []string{
			`{"semantic":false}`, `{"semantic":true}`, `{"semantic":true}`,
			`{"semantic":true}`, `{"terminal":true}`,
		} {
			_, _ = io.Copy(writer, strings.NewReader("data: "+payload+"\n\n"))
			time.Sleep(5 * time.Millisecond)
		}
	}()
	observation := runtime.NewObservation("timing-test")
	readStream(runtime.Response{Started: time.Now(), HTTP: &http.Response{Body: reader}}, timingDecoder{}, &observation)
	<-done
	metrics := observation.Metrics
	if metrics["semantic_chunk_count"] != 3 ||
		!(metrics["first_frame_ms"] < metrics["ttft_ms"] &&
			metrics["ttft_ms"] < metrics["second_semantic_ms"] &&
			metrics["second_semantic_ms"] < metrics["last_semantic_ms"]) {
		t.Fatalf("stream timing = %+v", metrics)
	}
}
