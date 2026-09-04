package caseimport

import (
	"bytes"
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
	kimiCoverage := map[string]int{}
	kimiTargets := map[string]struct{}{
		"kimi-k3": {}, "kimi-k2.7-code": {}, "kimi-k2.7-code-highspeed": {}, "kimi-k2.6": {},
	}
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
		if candidate.Protocol == domain.ProtocolKimiK3 && candidate.Enabled && candidate.ExecutionMode == domain.CaseExecutionAutomatic {
			if len(candidate.ModelTargets) == 0 {
				t.Errorf("%s runnable Kimi case has no explicit model targets", path)
			}
			for _, target := range candidate.ModelTargets {
				if _, known := kimiTargets[target]; !known {
					t.Errorf("%s has unsupported Kimi model target %q", path, target)
				}
				kimiCoverage[target]++
			}
		}
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
	if counts[domain.ProtocolOpenAIChat] != 44 || counts[domain.ProtocolKimiK3] != 87 || counts[domain.ProtocolSeedance] != 6 {
		t.Fatalf("protocol counts = %#v", counts)
	}
	if runnable != 117 || disabled != 14 || manual != 6 {
		t.Fatalf("policy counts = runnable:%d disabled:%d manual:%d", runnable, disabled, manual)
	}
	if typeCounts[casetypes.TypeLegacyAPIAudit] != 136 || typeCounts[casetypes.TypeInputLatencyLadder] != 1 {
		t.Fatalf("case type counts = %#v", typeCounts)
	}
	for target := range kimiTargets {
		if kimiCoverage[target] == 0 {
			t.Errorf("Kimi model %q has no runnable case in the multi-model suite", target)
		}
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
	decodedSpec, err := canonicalJSON(decoded.Definition.Spec)
	if err != nil {
		t.Fatal(err)
	}
	originalSpec, err := canonicalJSON(testCase.Definition.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Key != testCase.Key || decoded.Definition.Type != testCase.Definition.Type || !bytes.Equal(decodedSpec, originalSpec) {
		t.Fatalf("round trip mismatch: %#v != %#v", decoded, testCase)
	}
}

func TestFilesystemCaseV2RoundTripsModelTargets(t *testing.T) {
	raw := []byte(`{"schema_version":2,"key":"K001","name":"Kimi scoped","dimension":"compatibility","protocol":"kimi-k3","model_targets":["kimi-k3","kimi-k2.6"],"enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}}}`)
	testCase, err := DecodeFilesystemCase("cases/kimi-k3/K001/case.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeFilesystemCase(testCase)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFilesystemCase("cases/kimi-k3/K001/case.json", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.ModelTargets) != 2 || decoded.ModelTargets[0] != "kimi-k3" || decoded.ModelTargets[1] != "kimi-k2.6" {
		t.Fatalf("model targets after round trip = %#v", decoded.ModelTargets)
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
