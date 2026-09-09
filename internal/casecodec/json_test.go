package casecodec

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestEveryBuiltinCaseUsesCurrentProtocolContract(t *testing.T) {
	root := os.DirFS(testRepositoryRoot(t))
	counts := map[domain.Protocol]int{}
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
			t.Errorf("%s: %v", path, err)
			return nil
		}
		if string(candidate.Definition.Type) != string(candidate.Protocol) || candidate.Definition.TypeVersion != 1 {
			t.Errorf("%s: mismatched type", path)
		}
		if err := casetypes.MustBuiltinRegistry().Validate(candidate.Protocol, candidate.Definition); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		counts[candidate.Protocol]++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolSeedance, domain.ProtocolWanVideo, domain.ProtocolMiniMaxVideo} {
		if counts[protocol] == 0 {
			t.Errorf("missing %s authored cases", protocol)
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

func TestFilesystemCaseRejectsRemovedModelTargets(t *testing.T) {
	raw := []byte(`{"schema_version":3,"key":"K001","name":"Scoped","dimension":"compatibility","protocol":"openai-chat","model_targets":["model"],"enabled":true,"default":false,"severity":"normal","execution_mode":"automatic","definition":{"schema_version":2,"type":"openai-chat","type_version":1,"spec":{"inputs":{},"request":{"body":{}},"assertions":[]}}}`)
	if _, err := DecodeFilesystemCase("cases/openai-chat/K001/case.json", raw); err == nil {
		t.Fatal("accepted removed model_targets")
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
