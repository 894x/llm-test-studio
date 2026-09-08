package casecodec

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	wanCoverage := map[string]int{}
	wanTargets := map[string]struct{}{
		"wan3.0-video": {}, "wan3.0-video-prime": {}, "wan2.7-t2v": {}, "wan2.7-t2v-2026-06-12": {},
		"wan2.6-t2v": {}, "wan2.5-t2v-preview": {}, "wan2.2-t2v-plus": {}, "wanx2.1-t2v-turbo": {}, "wanx2.1-t2v-plus": {},
	}
	miniMaxCoverage := map[string]int{}
	miniMaxTargets := map[string]struct{}{"MiniMax-H3": {}}
	err := fs.WalkDir(root, "data/cases", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "case.json" {
			return walkErr
		}
		raw, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		candidate, err := DecodeFilesystemCase(path, raw)
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
		if len(candidate.ModelTargets) > 0 && strings.HasPrefix(candidate.ModelTargets[0], "kimi-") && candidate.Enabled && candidate.ExecutionMode == domain.CaseExecutionAutomatic {
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
		if candidate.Protocol == domain.ProtocolWanVideo && candidate.Enabled && candidate.ExecutionMode == domain.CaseExecutionAutomatic {
			if candidate.Default {
				t.Errorf("%s must not be in the default paid-capable plan", path)
			}
			if len(candidate.ModelTargets) == 0 {
				t.Errorf("%s runnable Wan case has no explicit model targets", path)
			}
			for _, target := range candidate.ModelTargets {
				if _, known := wanTargets[target]; !known {
					t.Errorf("%s has unsupported Wan model target %q", path, target)
				}
				wanCoverage[target]++
			}
		}
		if candidate.Protocol == domain.ProtocolMiniMaxVideo && candidate.Enabled && candidate.ExecutionMode == domain.CaseExecutionAutomatic {
			if candidate.Default {
				t.Errorf("%s must not be in the default paid-capable plan", path)
			}
			if len(candidate.ModelTargets) == 0 {
				t.Errorf("%s runnable MiniMax case has no explicit model targets", path)
			}
			for _, target := range candidate.ModelTargets {
				if _, known := miniMaxTargets[target]; !known {
					t.Errorf("%s has unsupported MiniMax model target %q", path, target)
				}
				miniMaxCoverage[target]++
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
	if counts[domain.ProtocolOpenAIChat] != 343 || counts[domain.ProtocolSeedance] != 6 || counts[domain.ProtocolWanVideo] != 213 || counts[domain.ProtocolMiniMaxVideo] != 149 {
		t.Fatalf("protocol counts = %#v", counts)
	}
	if runnable != 426 || disabled != 178 || manual != 107 {
		t.Fatalf("policy counts = runnable:%d disabled:%d manual:%d", runnable, disabled, manual)
	}
	if typeCounts[casetypes.TypeLegacyAPIAudit] != 710 || typeCounts[casetypes.TypeInputLatencyLadder] != 1 {
		t.Fatalf("case type counts = %#v", typeCounts)
	}
	for target := range kimiTargets {
		if kimiCoverage[target] == 0 {
			t.Errorf("Kimi model %q has no runnable case in the multi-model suite", target)
		}
	}
	for target := range wanTargets {
		if wanCoverage[target] == 0 {
			t.Errorf("Wan model %q has no runnable version-scoped case", target)
		}
	}
	for target := range miniMaxTargets {
		if miniMaxCoverage[target] == 0 {
			t.Errorf("MiniMax model %q has no runnable version-scoped case", target)
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
	raw, err := os.ReadFile(filepath.Join(testRepositoryRoot(t), "data", "cases", "openai-chat", "T001-sync-response", "case.json"))
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
	originalSpec, err := canonicalJSON(testCase.Definition.Spec)
	if err != nil {
		t.Fatal(err)
	}
	roundTripSpec, err := canonicalJSON(decoded.Definition.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Key != testCase.Key || decoded.Definition.Type != testCase.Definition.Type || !bytes.Equal(roundTripSpec, originalSpec) {
		t.Fatalf("round trip mismatch: %#v != %#v", decoded, testCase)
	}
}

func TestFilesystemCaseV2RoundTripsModelTargets(t *testing.T) {
	raw := []byte(`{"schema_version":2,"key":"K001","name":"Kimi scoped","dimension":"compatibility","protocol":"openai-chat","model_targets":["kimi-k3","kimi-k2.6"],"enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"legacy.apiaudit","type_version":1,"spec":{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}}}`)
	testCase, err := DecodeFilesystemCase("cases/openai-chat/K001/case.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	// Protocol is part of file identity; pin the migrated OpenAI identity and revision.
	if testCase.ID != "108f1df0-2cf5-5827-8307-4b98cfbe9edb" || testCase.Revision != 233259920761913 {
		t.Fatalf("stable file identity changed: %s revision %d", testCase.ID, testCase.Revision)
	}
	digest, err := MaterializedSHA256(testCase)
	if err != nil || digest != "5f20d4260eb7a4392a3b222f405f0fdba9278ed27ca42fd6b5886fc8891f0a4f" {
		t.Fatalf("materialized hash changed: %q, %v", digest, err)
	}
	encoded, err := EncodeFilesystemCase(testCase)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFilesystemCase("cases/openai-chat/K001/case.json", encoded)
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
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
