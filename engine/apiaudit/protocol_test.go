package apiaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestCurrentCLIUsesExplicitVerdictsAndExportsSafeEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		assertions string
		want       string
	}{
		{"expected rejection", 400, `[{"id":"status","source":"http.status","operator":"equals","value":400}]`, StatusPass},
		{"unexpected success", 200, `[{"id":"status","source":"http.status","operator":"equals","value":400}]`, StatusFail},
		{"observation only", 200, `[]`, StatusObserved},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["temperature"] != "invalid" || body["model"] != "bound-model" {
					t.Errorf("body = %v", body)
				}
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer private-test-credential" {
					t.Error("incorrect protocol binding")
				}
				w.WriteHeader(test.status)
				io.WriteString(w, `{"error":{"message":"private-test-credential"},"url":"https://example.test/video?signature=private-signature"}`)
			}))
			defer server.Close()
			spec, err := testspec.Decode(json.RawMessage(`{"inputs":{},"request":{"body":{"temperature":"invalid"}},"assertions":` + test.assertions + `}`))
			if err != nil {
				t.Fatal(err)
			}
			config := RunConfig{Suite: "openai-chat", BaseURL: server.URL, APIKey: "private-test-credential", Model: "bound-model", Timeout: time.Second}
			planned := PlannedRun{Case: CaseDefinition{ID: "negative", Name: "Boundary", Protocol: config.Suite, Spec: spec}, Model: config.Model, ResultID: "negative"}
			result, err := RunCase(context.Background(), server.Client(), config, planned)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || result.HTTPStatus != test.status {
				t.Fatalf("result = %+v", result)
			}
			report := BuildReport(config, []CaseResult{result})
			if test.want == StatusObserved && (report.Overall != "review" || report.Summary.Pass != 0 || report.Summary.Observed != 1) {
				t.Fatalf("observed report = %+v", report.Summary)
			}
			directory := t.TempDir()
			if err := WriteReport(directory, report); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"report.json", "report.html", "raw/negative/exchange-01.json"} {
				raw, err := os.ReadFile(filepath.Join(directory, name))
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte(config.APIKey)) || bytes.Contains(raw, []byte("private-signature")) {
					t.Fatalf("secret in %s", name)
				}
			}
		})
	}
}

func TestCLILoadsAllCurrentProtocolsAndRejectsOldDataWithoutWriting(t *testing.T) {
	total := 0
	for _, protocol := range SupportedProtocols() {
		cases, err := LoadSuite(filepath.Join("..", "..", "data", "cases"), protocol)
		if err != nil {
			t.Fatal(err)
		}
		total += len(cases)
		selected, err := SelectCases(cases, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range selected {
			if item.Disabled || item.ExecutionMode != "automatic" {
				t.Fatalf("selected unavailable case %s", item.ID)
			}
			config := RunConfig{Suite: protocol, BaseURL: "https://example.test", DryRun: true, Seed: 42}
			planned := PlannedRun{Case: item, Model: "bound-model", ResultID: item.ID}
			result, err := RunCase(context.Background(), nil, config, planned)
			if err != nil {
				t.Fatalf("preview %s/%s: %v", protocol, item.ID, err)
			}
			if result.Status != StatusUnknown || len(result.Exchanges) != 1 || result.Observation != nil {
				t.Fatalf("preview %s/%s fabricated execution or omitted the initial request", protocol, item.ID)
			}
		}
	}
	if total != 727 {
		t.Fatalf("catalog count = %d", total)
	}
	root := t.TempDir()
	directory := filepath.Join(root, "openai-chat", "old")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "case.json")
	raw := []byte(`{"schema_version":2,"kind":"chat_sync","request":{"body":{}}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuite(root, "openai-chat"); err == nil || !strings.Contains(err.Error(), "schema_version 2 with id and definitions") {
		t.Fatalf("old format error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("rejected data was modified")
	}
}

func TestCLIPreviewUsesInputsAndSeedWithoutNetwork(t *testing.T) {
	spec, err := testspec.Decode(json.RawMessage(`{"inputs":{"prompt":{"type":"string","required":true}},"request":{"body":{"messages":[{"role":"user","content":{"$input":"prompt"}}],"user":{"$generate":"random_text","length":16,"alphabet":"abcXYZ012"}}},"assertions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	config := RunConfig{Suite: "openai-chat", BaseURL: "https://example.test", Model: "bound-model", DryRun: true, Seed: 9, Inputs: map[string]json.RawMessage{"prompt": json.RawMessage(`"private-test-credential"`)}, APIKey: "private-test-credential"}
	planned := PlannedRun{Case: CaseDefinition{ID: "preview", Protocol: config.Suite, Spec: spec}, Model: config.Model, ResultID: "preview"}
	first, err := RunCase(context.Background(), nil, config, planned)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunCase(context.Background(), nil, config, planned)
	if err != nil {
		t.Fatal(err)
	}
	if first.Observation != nil || first.Status != StatusUnknown || first.Verification.Status != testspec.VerdictIndeterminate {
		t.Fatal("preview fabricated evidence")
	}
	if first.Exchanges[0].RequestBody["user"] != second.Exchanges[0].RequestBody["user"] {
		t.Fatal("seed is not deterministic")
	}
	encoded, _ := json.Marshal(first)
	if bytes.Contains(encoded, []byte(config.APIKey)) {
		t.Fatal("input credential leaked")
	}
	config.Inputs["unknown"] = json.RawMessage(`true`)
	if _, err := RunCase(context.Background(), nil, config, planned); err == nil {
		t.Fatal("undeclared input accepted")
	}
}
