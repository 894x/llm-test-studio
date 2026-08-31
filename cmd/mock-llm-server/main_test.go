package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockServerProducesCompleteCredentialProtectedSSE(t *testing.T) {
	cfg := &config{apiKey: "mock-key", prefillTPS: 1_000_000, decodeTPS: 1_000_000, ttftSet: true, tpotSet: true}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"mock-model","stream":true,"max_tokens":2,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	request.Header.Set("Authorization", "Bearer mock-key")
	recorder := httptest.NewRecorder()
	cfg.chat(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	body := recorder.Body.String()
	if strings.Count(body, `"content":"token-`) != 2 || !strings.Contains(body, `"completion_tokens":2`) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("SSE body = %q", body)
	}
}

func TestMockServerRejectsWrongCredential(t *testing.T) {
	cfg := &config{apiKey: "mock-key"}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	cfg.chat(recorder, request)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "invalid API key") {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
}
