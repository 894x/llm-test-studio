package casebundle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestKimiCacheUsageCasesThroughHTTP(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "kimi-cache-usage/*/case.json")
	if err != nil || len(paths) != 10 {
		t.Fatalf("cache cases = %v, %v", paths, err)
	}
	for _, path := range paths {
		raw, err := fs.ReadFile(casebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		item, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			t.Fatal(err)
		}
		if item.Default || len(item.Definitions) != 3 {
			t.Fatalf("cache probe must be non-default and support all three protocols: %s", item.Key)
		}
		for _, proto := range item.Protocols() {
			spec, err := item.SpecFor(proto)
			if err != nil {
				t.Fatal(err)
			}
			read := strings.Contains(item.Key, ".read_")
			stream := strings.HasSuffix(item.Key, "_stream")
			faults := []string{"", "missing_write", "negative_write", "fractional_write"}
			if read {
				faults = append(faults, "no_hit", "rewrite")
			} else {
				faults = append(faults, "zero_write")
			}
			if proto == domain.ProtocolOpenAIChat {
				faults = append(faults, "missing_header", "header_mismatch", "wrong_ttl")
			}
			if proto == domain.ProtocolAnthropicMessages {
				faults = append(faults, "wrong_ttl")
			}
			if stream {
				faults = append(faults, "missing_usage", "interrupted")
				if proto == domain.ProtocolOpenAIChat {
					faults = append(faults, "usage_only_initial")
				}
			}
			for _, fault := range faults {
				t.Run(item.Key+"/"+string(proto)+"/"+fault, func(t *testing.T) {
					fixture := &kimiCacheHTTP{t: t, protocol: proto, key: item.Key, fault: fault, read: read, stream: stream}
					server := httptest.NewServer(fixture)
					defer server.Close()
					lease, err := credentials.NewTemporaryLease([]byte("local-cache-fixture"))
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Close()
					client, err := protocols.NewClient(
						protocols.NewRegistry(),
						lease,
						domain.ChannelSnapshot{
							EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
							Name:              "local cache fixture", Protocol: proto, BaseURL: server.URL,
							UpstreamModelName: "kimi-k3.1-pro",
						},
						server.Client().Transport,
					)
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					result, err := client.Execute(context.Background(), protocols.Execution{
						Spec: spec, Random: testspec.RandomContext{Seed: 31, ExecutionItemID: t.Name()},
					})
					if err != nil {
						t.Fatal(err)
					}
					wantPassed := fault == ""
					if (result.Verdict.Status == testspec.VerdictPassed) != wantPassed {
						t.Fatalf("verdict = %+v; issues = %+v", result.Verdict, result.Observation.Issues)
					}
					wantCalls := 1
					if read {
						wantCalls = 2
					}
					if fault == "" && fixture.calls != wantCalls {
						t.Fatalf("HTTP calls = %d, want %d", fixture.calls, wantCalls)
					}
					if fault == "" {
						encoded, err := json.Marshal(result)
						if err != nil || !json.Valid(encoded) {
							t.Fatalf("evidence cannot be persisted: %v", err)
						}
						var restored testspec.Result
						if err := json.Unmarshal(encoded, &restored); err != nil {
							t.Fatal(err)
						}
						if testspec.Evaluate(spec.Assertions, restored.Observation).Status != testspec.VerdictPassed {
							t.Fatal("recorded evidence lost cache accounting")
						}
					}
				})
			}
		}
	}
}

type kimiCacheHTTP struct {
	t        *testing.T
	protocol domain.Protocol
	key      string
	fault    string
	read     bool
	stream   bool
	calls    int
	first    []byte
}

func (fixture *kimiCacheHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fixture.calls++
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fixture.t.Error(err)
		return
	}
	if fixture.calls == 1 {
		fixture.first = bytes.Clone(body)
	} else if !bytes.Equal(fixture.first, body) {
		fixture.t.Error("read probe changed the repeated request, invalidating the cache claim")
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		fixture.t.Error(err)
		return
	}
	ttl := "5m"
	if strings.Contains(fixture.key, "1h") {
		ttl = "1h"
	}
	option := "prompt_cache_options"
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		option = "cache_control"
	}
	options, present := request[option].(map[string]any)
	if strings.Contains(fixture.key, "default") {
		if present {
			fixture.t.Error("default-TTL probe must omit cache options")
		}
	} else if !present || options["ttl"] != ttl {
		fixture.t.Errorf("TTL request = %+v, want %s", options, ttl)
	}
	if fixture.stream && fixture.protocol == domain.ProtocolOpenAIChat {
		options, _ := request["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			fixture.t.Error("Chat streaming probe did not request final usage")
		}
	}
	write, read := 512, 0
	if fixture.read && fixture.calls > 1 {
		write, read = 0, 512
	}
	if fixture.fault == "zero_write" {
		write = 0
	}
	if fixture.fault == "no_hit" {
		read = 0
	}
	if fixture.fault == "rewrite" && fixture.calls > 1 {
		write = 1
	}
	short, long := write, 0
	if ttl == "1h" {
		short, long = 0, write
	}
	if fixture.fault == "wrong_ttl" {
		short, long = long, short
		if short == long {
			short = 1
		}
	}
	if fixture.fault != "missing_header" || fixture.calls == 1 && fixture.read {
		w.Header().Set(protocol.MSHCacheWriteHeader5m, strconv.Itoa(short))
		w.Header().Set(protocol.MSHCacheWriteHeader1h, strconv.Itoa(long))
	}
	if fixture.fault == "header_mismatch" {
		w.Header().Set(protocol.MSHCacheWriteHeader5m, "7")
	}
	usage := map[string]any{}
	var details map[string]any
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		usage = map[string]any{
			"input_tokens": 512, "output_tokens": 1, "cache_read_input_tokens": read,
			"cache_creation_input_tokens": write,
			"cache_creation":              map[string]any{"ephemeral_5m_input_tokens": short, "ephemeral_1h_input_tokens": long},
		}
		details = usage
	} else {
		input, output := "prompt_tokens", "completion_tokens"
		if fixture.protocol == domain.ProtocolOpenAIResponses {
			input, output = "input_tokens", "output_tokens"
		}
		details = map[string]any{"cached_tokens": read, "cache_write_tokens": write}
		usage = map[string]any{input: 1024, output: 1, "total_tokens": 1025, input + "_details": details}
	}
	field := "cache_write_tokens"
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		field = "cache_creation_input_tokens"
	}
	switch fixture.fault {
	case "missing_write":
		delete(details, field)
	case "negative_write":
		details[field] = -1
	case "fractional_write":
		details[field] = 1.5
	}
	if fixture.fault == "missing_usage" {
		usage = nil
	}
	fixture.respond(w, usage)
}

func (fixture *kimiCacheHTTP) respond(w http.ResponseWriter, usage map[string]any) {
	message := map[string]any{"role": "assistant", "content": "OK"}
	root := map[string]any{"id": "cache_fixture", "model": "kimi-k3.1-pro", "usage": usage}
	switch fixture.protocol {
	case domain.ProtocolOpenAIChat:
		root["object"] = "chat.completion"
		root["choices"] = []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}}
	case domain.ProtocolOpenAIResponses:
		root["object"], root["status"] = "response", "completed"
		root["output"] = []any{map[string]any{
			"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "OK"}},
		}}
	case domain.ProtocolAnthropicMessages:
		root["type"], root["role"], root["stop_reason"] = "message", "assistant", "end_turn"
		root["content"] = []any{map[string]any{"type": "text", "text": "OK"}}
	}
	if !fixture.stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(root)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(value any) {
		raw, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	switch fixture.protocol {
	case domain.ProtocolOpenAIChat:
		root["object"] = "chat.completion.chunk"
		root["choices"] = []any{map[string]any{"index": 0, "delta": message, "finish_reason": "stop"}}
		if fixture.fault != "usage_only_initial" {
			delete(root, "usage")
		}
		emit(root)
		if fixture.fault == "interrupted" {
			return
		}
		root["choices"], root["usage"] = []any{}, usage
		if fixture.fault == "usage_only_initial" {
			delete(root, "usage")
		}
		emit(root)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	case domain.ProtocolOpenAIResponses:
		emit(map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{
			"id": "cache_fixture", "object": "response", "status": "in_progress", "output": []any{},
		}})
		if fixture.fault != "interrupted" {
			emit(map[string]any{"type": "response.completed", "sequence_number": 1, "response": root})
		}
	case domain.ProtocolAnthropicMessages:
		root["content"], root["stop_reason"] = []any{}, nil
		emit(map[string]any{"type": "message_start", "message": root})
		if fixture.fault != "interrupted" {
			emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"},
				"usage": map[string]any{"output_tokens": 1}})
			emit(map[string]any{"type": "message_stop"})
		}
	}
}

func TestKimiCacheWritePrefixChangesBetweenExecutions(t *testing.T) {
	raw, err := fs.ReadFile(casebundle.Bundle, "kimi-cache-usage/Kimi-cache-write-5m/case.json")
	if err != nil {
		t.Fatal(err)
	}
	item, err := casecodec.DecodeFilesystemCase("cache-write", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, proto := range item.Protocols() {
		spec, err := item.SpecFor(proto)
		if err != nil {
			t.Fatal(err)
		}
		first, err := testspec.Generate(spec, map[string]json.RawMessage{}, testspec.RandomContext{ExecutionItemID: "run-1"})
		if err != nil {
			t.Fatal(err)
		}
		second, err := testspec.Generate(spec, map[string]json.RawMessage{}, testspec.RandomContext{ExecutionItemID: "run-2"})
		if err != nil || bytes.Equal(first.Body, second.Body) {
			t.Fatalf("%s reused a cold-write prefix: %v", proto, err)
		}
	}
}
