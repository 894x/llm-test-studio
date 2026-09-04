package caseimport

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestEveryBuiltinCaseIsAV2TypedDocument(t *testing.T) {
	t.Parallel()

	root := os.DirFS(testRepositoryRoot(t))
	counts := map[domain.Protocol]int{}
	typeCounts := map[domain.CaseType]int{}
	registry := casetypes.MustBuiltinRegistry()
	runnable, disabled, manual := 0, 0, 0
	err := fs.WalkDir(root, "cases", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "case.json" {
			return walkErr
		}
		raw, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		candidate, err := convertFilesystemCase(path, raw)
		if err != nil {
			t.Errorf("convert %s: %v", path, err)
			return nil
		}
		wantTypeVersion := uint32(1)
		if candidate.Definition.Type == casetypes.TypeInputLatencyLadder {
			wantTypeVersion = 2
		}
		if candidate.Definition.SchemaVersion != 2 || candidate.Definition.TypeVersion != wantTypeVersion {
			t.Errorf("%s definition = %#v", path, candidate.Definition)
		}
		if err := registry.Validate(candidate.Protocol, candidate.Definition); err != nil {
			t.Errorf("%s registry validation = %v", path, err)
		}
		if candidate.Definition.Type == casetypes.TypeLegacyAPIAudit {
			var spec casetypes.LegacyAPIAuditSpec
			if err := json.Unmarshal(candidate.Definition.Spec, &spec); err != nil || spec.Kind == "" || spec.Request.Headers == nil {
				t.Errorf("%s spec = %#v, error = %v", path, spec, err)
			}
		}
		counts[candidate.Protocol]++
		typeCounts[candidate.Definition.Type]++
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
	if counts[domain.ProtocolOpenAIChat] != 44 || counts[domain.ProtocolKimiK3] != 40 || counts[domain.ProtocolSeedance] != 6 {
		t.Fatalf("protocol counts = %#v", counts)
	}
	if runnable != 58 || disabled != 26 || manual != 6 {
		t.Fatalf("policy counts = runnable:%d disabled:%d manual:%d", runnable, disabled, manual)
	}
	if typeCounts[casetypes.TypeLegacyAPIAudit] != 89 || typeCounts[casetypes.TypeInputLatencyLadder] != 1 {
		t.Fatalf("case type counts = %#v", typeCounts)
	}
}

func TestDecodeFilesystemCaseRejectsV1UnknownAndCredentialMaterial(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"id":"T900","name":"v1","dimension":"boundary","protocol":"openai-chat","kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions"}}`,
		`{"schema_version":2,"key":"T900","name":"unknown","dimension":"boundary","protocol":"openai-chat","enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{}},"options":{}}},"surprise":true}`,
		`{"schema_version":2,"key":"T901","name":"secret","dimension":"boundary","protocol":"openai-chat","enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"api_key":"plaintext"}},"options":{}}}}`,
	} {
		if _, err := DecodeFilesystemCase("cases/openai-chat/example/case.json", []byte(raw)); err == nil {
			t.Fatalf("DecodeFilesystemCase() accepted %s", raw)
		}
	}
}

func TestFilesystemCaseV2RoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testRepositoryRoot(t), "cases", "openai-chat", "T001-sync-response", "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	testCase, err := DecodeFilesystemCase("cases/openai-chat/T001-sync-response/case.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeFilesystemCase(testCase)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFilesystemCase("cases/openai-chat/T001-sync-response/case.json", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Key != testCase.Key || decoded.Definition.Type != testCase.Definition.Type || string(decoded.Definition.Spec) != string(testCase.Definition.Spec) {
		t.Fatalf("round trip mismatch: %#v != %#v", decoded, testCase)
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
