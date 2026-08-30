package caseimport

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/894x/llm-studio/internal/domain"
)

func TestConvertLegacyCasePreservesRequestPolicyAndVersionedDriver(t *testing.T) {
	t.Parallel()

	repositoryRoot := testRepositoryRoot(t)
	tests := []struct {
		path             string
		key              string
		enabled          bool
		defaultCase      bool
		severity         domain.CaseSeverity
		execution        domain.CaseExecutionMode
		bodyPresent      bool
		streamCompletion domain.StreamCompletionExpectation
		statusCount      int
		lastStatus       int
	}{
		{path: "cases/openai-chat/T002-model-list/case.json", key: "T002", enabled: true, severity: domain.CaseSeverityNormal, execution: domain.CaseExecutionAutomatic, statusCount: 100, lastStatus: 299},
		{path: "cases/openai-chat/T010-fingerprint/case.json", key: "T010", enabled: true, severity: domain.CaseSeverityCritical, execution: domain.CaseExecutionManual, statusCount: 1, lastStatus: 200},
		{path: "cases/openai-chat/T023-速率限制-并发与-429/case.json", key: "T023", enabled: true, severity: domain.CaseSeverityNormal, execution: domain.CaseExecutionAutomatic, bodyPresent: true, statusCount: 101, lastStatus: 429},
		{path: "cases/openai-chat/T024-TTFT-首-Token-延迟/case.json", key: "T024", enabled: true, severity: domain.CaseSeverityNormal, execution: domain.CaseExecutionAutomatic, bodyPresent: true, streamCompletion: domain.StreamCompletionRequired, statusCount: 100, lastStatus: 299},
		{path: "cases/kimi-k3/F025-multimodal-type-required/case.json", key: "must.multimodal_type_required", enabled: false, severity: domain.CaseSeverityCritical, execution: domain.CaseExecutionAutomatic, bodyPresent: true, statusCount: 1, lastStatus: 400},
		{path: "cases/seedance/V001-text/case.json", key: "V001", enabled: true, defaultCase: true, severity: domain.CaseSeverityNormal, execution: domain.CaseExecutionAutomatic, bodyPresent: true, statusCount: 100, lastStatus: 299},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(test.path)))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			candidate, err := convertLegacyCase(test.path, raw)
			if err != nil {
				t.Fatalf("convertLegacyCase() error = %v", err)
			}
			if candidate.Key != test.key || candidate.Enabled != test.enabled || candidate.Default != test.defaultCase || candidate.Severity != test.severity || candidate.ExecutionMode != test.execution {
				t.Fatalf("converted policy = %#v", candidate)
			}
			if got := len(candidate.Definition.Request.Body) != 0; got != test.bodyPresent {
				t.Fatalf("body present = %t, want %t", got, test.bodyPresent)
			}
			completion := test.streamCompletion
			if completion == "" {
				completion = domain.StreamCompletionNotApplicable
			}
			if candidate.Definition.Expected.StreamCompletion != completion {
				t.Fatalf("stream completion = %q, want %q", candidate.Definition.Expected.StreamCompletion, completion)
			}
			statuses := candidate.Definition.Expected.AllowedHTTPStatuses
			if len(statuses) != test.statusCount || statuses[len(statuses)-1] != test.lastStatus {
				t.Fatalf("statuses = %v, want count=%d last=%d", statuses, test.statusCount, test.lastStatus)
			}
			if len(candidate.Definition.Assertions) != 1 || candidate.Definition.Assertions[0].Kind != domain.AssertionCustom {
				t.Fatalf("assertions = %#v, want one custom assertion", candidate.Definition.Assertions)
			}
			var driver struct {
				Driver        string          `json:"driver"`
				DriverVersion int             `json:"driver_version"`
				LegacyKind    string          `json:"legacy_kind"`
				BodyPresent   bool            `json:"body_present"`
				Options       json.RawMessage `json:"options"`
			}
			if err := json.Unmarshal(candidate.Definition.Assertions[0].Config, &driver); err != nil {
				t.Fatalf("decode driver config: %v", err)
			}
			if driver.Driver != "legacy.apiaudit" || driver.DriverVersion != 1 || driver.BodyPresent != test.bodyPresent || driver.LegacyKind == "" || len(driver.Options) == 0 {
				t.Fatalf("driver config = %#v", driver)
			}
		})
	}
}

func TestConvertLegacyCaseRejectsUnknownOrCredentialBearingInput(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"id":"T900","name":"unknown","dimension":"boundary","protocol":"openai-chat","kind":"chat_sync","default":false,"severity":"normal","request":{"method":"POST","path":"/v1/chat/completions","body":{}},"surprise":true}`,
		`{"id":"T901","name":"secret","dimension":"boundary","protocol":"openai-chat","kind":"chat_sync","default":false,"severity":"normal","request":{"method":"POST","path":"/v1/chat/completions","body":{"api_key":"plaintext"}}}`,
	} {
		if _, err := convertLegacyCase("cases/openai-chat/example/case.json", []byte(raw)); err == nil {
			t.Fatalf("convertLegacyCase() accepted %s", raw)
		}
	}
}

func TestEveryLegacyCaseHasAValidLosslessStorageConversion(t *testing.T) {
	t.Parallel()

	root := os.DirFS(testRepositoryRoot(t))
	counts := map[domain.Protocol]int{}
	runnable, disabled, manual := 0, 0, 0
	err := fs.WalkDir(root, "cases", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "case.json" {
			return nil
		}
		raw, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		candidate, err := convertLegacyCase(path, raw)
		if err != nil {
			t.Errorf("convert %s: %v", path, err)
			return nil
		}
		counts[candidate.Protocol]++
		switch {
		case !candidate.Enabled:
			disabled++
		case candidate.ExecutionMode == domain.CaseExecutionManual:
			manual++
		default:
			runnable++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
	if counts[domain.ProtocolOpenAIChat] != 43 || counts[domain.ProtocolKimiK3] != 40 || counts[domain.ProtocolSeedance] != 6 {
		t.Fatalf("protocol counts = %#v", counts)
	}
	if runnable != 57 || disabled != 26 || manual != 6 {
		t.Fatalf("policy counts = runnable:%d disabled:%d manual:%d", runnable, disabled, manual)
	}
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
