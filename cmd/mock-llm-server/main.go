// Command mock-llm-server is a deterministic OpenAI-compatible SSE fixture.
// Repository verification therefore needs only the Go toolchain plus the
// shell utilities used by the benchmark itself.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
	"unicode"
)

type config struct {
	apiKey        string
	baseLatency   time.Duration
	prefillTPS    float64
	decodeTPS     float64
	ttftOverride  time.Duration
	ttftSet       bool
	tpotOverride  time.Duration
	tpotSet       bool
	tokenCap      int
	requestNumber atomic.Uint64
}

func main() {
	host := flag.String("host", "127.0.0.1", "listen host")
	port := flag.Int("port", 18080, "listen port; zero selects a free port")
	portFile := flag.String("port-file", "", "optional file receiving the selected port")
	apiKey := flag.String("api-key", "mock-key", "required bearer key")
	baseLatencyMS := flag.Float64("base-latency-ms", 200, "latency before first token")
	prefillTPS := flag.Float64("prefill-tps", 2000, "simulated input throughput")
	decodeTPS := flag.Float64("decode-tps", 20, "simulated output throughput")
	ttftMS := optionalFloat{}
	tpotMS := optionalFloat{}
	flag.Var(&ttftMS, "ttft-ms", "fixed TTFT override")
	flag.Var(&tpotMS, "tpot-ms", "fixed TPOT override")
	tokens := flag.Int("tokens", 0, "optional generated-token cap")
	flag.Parse()
	if *port < 0 || *port > 65535 || *baseLatencyMS < 0 || *prefillTPS <= 0 || *decodeTPS <= 0 || (ttftMS.set && ttftMS.value < 0) || (tpotMS.set && tpotMS.value < 0) || *tokens < 0 {
		log.Fatal("invalid mock server configuration")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		log.Fatal(err)
	}
	selectedPort := listener.Addr().(*net.TCPAddr).Port
	if *portFile != "" {
		if err := os.WriteFile(*portFile, []byte(fmt.Sprintf("%d\n", selectedPort)), 0o600); err != nil {
			_ = listener.Close()
			log.Fatal(err)
		}
	}
	cfg := &config{
		apiKey: *apiKey, baseLatency: time.Duration(*baseLatencyMS * float64(time.Millisecond)),
		prefillTPS: *prefillTPS, decodeTPS: *decodeTPS,
		ttftOverride: time.Duration(ttftMS.value * float64(time.Millisecond)), ttftSet: ttftMS.set,
		tpotOverride: time.Duration(tpotMS.value * float64(time.Millisecond)), tpotSet: tpotMS.set,
		tokenCap: *tokens,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/v1/chat/completions", cfg.chat)
	fmt.Printf("Mock LLM listening on http://%s:%d/v1\n", *host, selectedPort)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type optionalFloat struct {
	value float64
	set   bool
}

func (value *optionalFloat) String() string { return fmt.Sprintf("%g", value.value) }
func (value *optionalFloat) Set(raw string) error {
	if _, err := fmt.Sscan(raw, &value.value); err != nil {
		return err
	}
	value.set = true
	return nil
}

func (cfg *config) chat(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeJSON(writer, http.StatusMethodNotAllowed, map[string]any{"error": map[string]string{"message": "method not allowed"}})
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+cfg.apiKey {
		writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "invalid API key"}})
		return
	}
	var payload struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<20))
	if err := decoder.Decode(&payload); err != nil || !payload.Stream || len(payload.Messages) == 0 || payload.MaxTokens < 1 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]string{"message": "invalid streaming request"}})
		return
	}
	promptTokens := estimatePromptTokens(payload.Messages)
	completionTokens := payload.MaxTokens
	if cfg.tokenCap > 0 && completionTokens > cfg.tokenCap {
		completionTokens = cfg.tokenCap
	}
	ttft := cfg.ttftOverride
	if !cfg.ttftSet {
		ttft = cfg.baseLatency + time.Duration(float64(time.Second)*float64(promptTokens)/cfg.prefillTPS) + time.Duration(float64(time.Second)/cfg.decodeTPS)
	}
	tpot := cfg.tpotOverride
	if !cfg.tpotSet {
		tpot = time.Duration(float64(time.Second) / cfg.decodeTPS)
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "stream unsupported"})
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)
	responseID := fmt.Sprintf("chatcmpl-mock-%d", cfg.requestNumber.Add(1))
	model := payload.Model
	if model == "" {
		model = "mock-model"
	}
	writeSSE(writer, flusher, map[string]any{"id": responseID, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant"}, "finish_reason": nil}}})
	time.Sleep(ttft)
	for index := 0; index < completionTokens; index++ {
		writeSSE(writer, flusher, map[string]any{"id": responseID, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": fmt.Sprintf("token-%d ", index)}, "finish_reason": nil}}})
		if index+1 < completionTokens {
			time.Sleep(tpot)
		}
	}
	writeSSE(writer, flusher, map[string]any{"id": responseID, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	writeSSE(writer, flusher, map[string]any{"id": responseID, "model": model, "choices": []any{}, "usage": map[string]int{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens}})
	_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
	flusher.Flush()
}

func estimatePromptTokens(messages []struct {
	Content any `json:"content"`
}) int {
	count := len(messages)*4 + 2
	for _, message := range messages {
		switch content := message.Content.(type) {
		case string:
			count += tokenCount(content)
		case []any:
			for _, raw := range content {
				if part, ok := raw.(map[string]any); ok {
					if text, ok := part["text"].(string); ok {
						count += tokenCount(text)
					}
				}
			}
		}
	}
	if count < 1 {
		return 1
	}
	return count
}

func tokenCount(value string) int {
	count, inWord := 0, false
	for _, character := range value {
		word := unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_'
		if word && !inWord {
			count++
		}
		if !word && !unicode.IsSpace(character) {
			count++
		}
		inWord = word
	}
	return count
}

func writeSSE(writer http.ResponseWriter, flusher http.Flusher, value any) {
	encoded, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	flusher.Flush()
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
