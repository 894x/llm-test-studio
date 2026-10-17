package textapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type fileTransport struct{ calls int }

func (transport *fileTransport) Send(context.Context, runtime.Request) (runtime.Response, error) {
	transport.calls++
	return runtime.Response{}, errors.New("unexpected transport call")
}

func TestModuleKeepsDeclaredAndResolvedFilesUnsupported(t *testing.T) {
	module := &Module{ID: "text", Path: "/messages"}
	for _, declared := range []bool{false, true} {
		inputs := testspec.ResolvedInputs{JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{}}
		spec := testspec.Spec{Inputs: map[string]testspec.Input{}, Request: testspec.Request{Body: json.RawMessage(`{}`)}}
		if declared {
			spec.Inputs["image"] = testspec.Input{Type: "file", Required: true, File: &testspec.FileInput{
				Encoding: "image_data_url", MediaTypes: []string{"image/png"}, MaxBytes: testspec.MaxFileInputBytes,
			}}
			if !errors.Is(module.ValidateProtocol(spec), testspec.ErrFileInputsUnsupported) {
				t.Fatal("text protocol accepted a file declaration")
			}
		} else {
			inputs.Files["image"] = testspec.FileBinding{AssetRef: "image-1"}
		}
		transport := &fileTransport{}
		observation := module.Execute(context.Background(), runtime.Execution{Spec: spec, Inputs: inputs, Transport: transport})
		if transport.calls != 0 || len(observation.Exchanges) != 0 || len(observation.Issues) != 1 ||
			observation.Issues[0].Code != "file_inputs_unsupported" {
			t.Fatalf("text file execution: calls=%d observation=%+v", transport.calls, observation)
		}
	}
}

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
