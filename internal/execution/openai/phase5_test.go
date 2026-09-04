package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestStreamingExecutorMeasuresHeaderAndTextMilestones(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		writer.WriteHeader(http.StatusOK)
		flusher.Flush()
		time.Sleep(15 * time.Millisecond)
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"!\"}}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := mustClient(t, server)
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	observation := executor(context.Background(), load.Request{Index: 9})

	if !observation.Success || !observation.Streaming || !observation.StreamComplete {
		t.Fatalf("observation = %#v", observation)
	}
	if observation.TTFB <= 0 || observation.TTFTAny <= observation.TTFB || observation.TTFT != observation.TTFTAny {
		t.Fatalf("header/any milestones = %#v", observation)
	}
	if observation.TTFTVisible <= observation.TTFTAny || observation.TTST != observation.TTFTVisible {
		t.Fatalf("visible/second milestones = %#v", observation)
	}
	if observation.SemanticChunkCount != 3 || observation.ObservedICL <= 0 {
		t.Fatalf("stream cadence = %#v", observation)
	}
}

func TestExecutorRecordsTTFBOnlyFromHTTPTrace(t *testing.T) {
	for _, test := range []struct {
		name     string
		callHook bool
	}{
		{name: "hook", callHook: true},
		{name: "no hook"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := mustTransportClient(t, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if test.callHook {
					trace := httptrace.ContextClientTrace(request.Context())
					if trace == nil || trace.GotFirstResponseByte == nil {
						t.Fatal("GotFirstResponseByte hook is missing")
					}
					trace.GotFirstResponseByte()
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
				}, nil
			}))
			executor, err := client.Executor(chatRequest(false))
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{})
			if !observation.Success || observation.Streaming {
				t.Fatalf("observation = %#v", observation)
			}
			if test.callHook != (observation.TTFB > 0) {
				t.Fatalf("TTFB = %v, callHook = %t", observation.TTFB, test.callHook)
			}
		})
	}
}

func TestStreamingExecutorPanicPreservesStreamingAndTTFB(t *testing.T) {
	client := mustTransportClient(t, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		if trace == nil || trace.GotFirstResponseByte == nil {
			t.Fatal("GotFirstResponseByte hook is missing")
		}
		trace.GotFirstResponseByte()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: panicReadBody{}}, nil
	}))
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	observation := executor(context.Background(), load.Request{Index: 42})
	if observation.Success || observation.ErrorCode != load.ErrorExecutorPanic || observation.Index != 42 ||
		!observation.Streaming || observation.TTFB <= 0 || observation.E2E <= 0 {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestExecutorPreservesAny2xxTransportSemantics(t *testing.T) {
	for _, test := range []struct {
		status   int
		body     string
		wantOK   bool
		wantCode string
	}{
		{status: http.StatusOK, body: `{"choices":[{"message":{"content":"ok"}}]}`, wantOK: true},
		{status: http.StatusCreated, body: `{"choices":[{"message":{"content":"ok"}}]}`, wantOK: true},
		{status: http.StatusNoContent, wantCode: string(load.ErrorProtocol)},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, nil
			}))
			executor, err := client.Executor(chatRequest(false))
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{})
			if observation.HTTPStatus != test.status || observation.Success != test.wantOK || string(observation.ErrorCode) != test.wantCode {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}
}

func TestStreamingExecutorClassifiesTextEventsOncePerSSEEvent(t *testing.T) {
	for _, test := range []struct {
		name         string
		events       string
		wantSuccess  bool
		wantChunks   uint64
		wantAny      bool
		wantVisible  bool
		wantSecond   bool
		wantInterval bool
	}{
		{
			name: "content first",
			events: "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n",
			wantSuccess: true, wantChunks: 2, wantAny: true, wantVisible: true, wantSecond: true, wantInterval: true,
		},
		{
			name: "multiple choices and text fields count once",
			events: "data: {\"choices\":[" +
				"{\"delta\":{\"content\":\"answer\",\"reasoning_content\":\"thought\"}}," +
				"{\"delta\":{\"content\":\"alternative\",\"reasoning\":\"other\"}}]}\n\n",
			wantSuccess: true, wantChunks: 1, wantAny: true, wantVisible: true,
		},
		{
			name:        "tool only",
			events:      "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]}}]}\n\n",
			wantSuccess: true,
		},
		{
			name:        "reasoning only",
			events:      "data: {\"choices\":[{\"delta\":{\"reasoning\":\"thought\"}}]}\n\n",
			wantSuccess: true, wantChunks: 1, wantAny: true,
		},
		{
			name: "metadata and empty events ignored",
			events: ": keepalive\n\n" +
				"data:\n\n" +
				"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1}}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"\",\"reasoning_content\":\"\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n",
			wantSuccess: true, wantChunks: 1, wantAny: true, wantVisible: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := executeStreamFixture(t, test.events+"data: [DONE]\n\n")
			if observation.Success != test.wantSuccess || !observation.StreamComplete || observation.SemanticChunkCount != test.wantChunks {
				t.Fatalf("observation = %#v", observation)
			}
			if (observation.TTFTAny > 0) != test.wantAny || observation.TTFT != observation.TTFTAny ||
				(observation.TTFTVisible > 0) != test.wantVisible || (observation.TTST > 0) != test.wantSecond {
				t.Fatalf("milestones = %#v", observation)
			}
			if test.wantChunks == 1 && observation.ObservedICL != 0 {
				t.Fatalf("single-event ICL = %v", observation.ObservedICL)
			}
			if test.wantInterval && observation.ObservedICL < 0 {
				t.Fatalf("observed ICL = %v", observation.ObservedICL)
			}
			if test.wantInterval && test.wantChunks == 2 && observation.ObservedICL != observation.TTST-observation.TTFTAny {
				t.Fatalf("two-event ICL = %v, want TTST - TTFTAny = %v", observation.ObservedICL, observation.TTST-observation.TTFTAny)
			}
			if test.name == "content first" && observation.TTFTVisible != observation.TTFTAny {
				t.Fatalf("content-first milestones = %#v", observation)
			}
		})
	}
}

func TestStreamingExecutorDoesNotCommitTextTimingFromMalformedEvent(t *testing.T) {
	observation := executeStreamFixture(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"apparent text\"}},7]}\n\n"+
			"data: [DONE]\n\n")

	if observation.Success || observation.StreamComplete || observation.ErrorCode != load.ErrorProtocol {
		t.Fatalf("observation = %#v", observation)
	}
	if observation.TTFT != 0 || observation.TTFTAny != 0 || observation.TTFTVisible != 0 ||
		observation.TTST != 0 || observation.ObservedICL != 0 || observation.SemanticChunkCount != 0 {
		t.Fatalf("malformed event committed text telemetry: %#v", observation)
	}
}

func TestStreamingExecutorAcceptsHTTP201WithCompleteSemanticStream(t *testing.T) {
	observation := executeStreamStatusFixture(t, http.StatusCreated,
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n"+
			"data: [DONE]\n\n")

	if !observation.Success || !observation.Streaming || !observation.StreamComplete ||
		observation.HTTPStatus != http.StatusCreated || observation.ErrorCode != "" {
		t.Fatalf("observation = %#v", observation)
	}
	if observation.TTFTAny <= 0 || observation.TTFT != observation.TTFTAny ||
		observation.TTFTVisible < observation.TTFTAny || observation.TTST != observation.TTFTVisible ||
		observation.SemanticChunkCount != 2 || observation.ObservedICL != observation.TTST-observation.TTFTAny {
		t.Fatalf("fine streaming telemetry = %#v", observation)
	}
}

func executeStreamFixture(t *testing.T, stream string) load.Observation {
	t.Helper()
	return executeStreamStatusFixture(t, http.StatusOK, stream)
}

func executeStreamStatusFixture(t *testing.T, status int, stream string) load.Observation {
	t.Helper()
	client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
		}, nil
	}))
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	return executor(context.Background(), load.Request{})
}
