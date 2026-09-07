package suitecatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
)

const quickSuiteProfile = `{"description":"Send one request and validate the reply","timeout_ms":30000,"inputs":[{"key":"prompt","label":"Message","type":"text","default":"hello","bindings":[{"case_key":"T001","pointer":"/request/body/messages/0/content"}]}]}`

func quickSuiteCatalog(t *testing.T, modelTarget, caseTarget, profile string) *Service {
	t.Helper()
	caseRaw := caseDocument("T001", "Connection", "gpt-5.2")
	if caseTarget == "" {
		caseRaw = strings.Replace(caseRaw, `"model_targets":["gpt-5.2"],`, "", 1)
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseRaw)}}, UserRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":1,"key":"connectivity","name":"Connection","protocol":"openai-chat","model_target":"` + modelTarget + `","case_keys":["T001"],"quick_test":` + profile + `}`
	service, err := New(Options{Builtin: fstest.MapFS{"openai-chat/connectivity/suite.json": &fstest.MapFile{Data: []byte(raw)}}, UserRoot: t.TempDir(), Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestQuickSuiteMetadataSurvivesCatalogEditingAndRevisionHistory(t *testing.T) {
	service := quickSuiteCatalog(t, "gpt-5.2", "gpt-5.2", quickSuiteProfile)
	ctx := context.Background()
	entries, err := service.Entries(ctx)
	if err != nil {
		t.Fatalf("load quick suite: %v", err)
	}
	original := entries[0].Suite
	changed := original
	changed.Name = "Renamed connection"
	if err := service.SaveSuite(ctx, "openai-chat", "connectivity", changed); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(service.userRoot, "openai-chat", "connectivity", "suite.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		QuickTest json.RawMessage `json:"quick_test"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.QuickTest) == 0 || !strings.Contains(string(saved.QuickTest), "/request/body/messages/0/content") {
		t.Fatalf("quick metadata lost on save: %s", raw)
	}
	historical, err := service.FindRevision(ctx, original.ID, original.Revision)
	if err != nil {
		t.Fatal(err)
	}
	historicalJSON, err := json.Marshal(historical.Suite)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(historicalJSON), `"quick_test"`) || historical.Suite.Name != "Connection" {
		t.Fatalf("quick metadata lost from history: %s", historicalJSON)
	}
}

func TestQuickSuiteCanUseAnyModelOnlyWhenCasesAreGeneric(t *testing.T) {
	if _, err := quickSuiteCatalog(t, "", "", quickSuiteProfile).Entries(context.Background()); err != nil {
		t.Fatalf("generic quick suite rejected: %v", err)
	}
	if _, err := quickSuiteCatalog(t, "", "gpt-5.2", quickSuiteProfile).Entries(context.Background()); err == nil {
		t.Fatal("generic suite accepted a model-scoped case")
	}
}

func TestQuickSuiteRejectsInvalidParameterBindings(t *testing.T) {
	for _, test := range []struct{ name, profile string }{
		{"missing timeout", strings.Replace(quickSuiteProfile, `"timeout_ms":30000,`, "", 1)},
		{"unknown case", strings.ReplaceAll(quickSuiteProfile, "T001", "T999")},
		{"credential binding", strings.ReplaceAll(quickSuiteProfile, "/request/body/messages/0/content", "/request/headers/Authorization")},
		{"target override", strings.ReplaceAll(quickSuiteProfile, "/request/body/messages/0/content", "/request/body/model")},
		{"array alias", strings.ReplaceAll(quickSuiteProfile, "/request/body/messages/0/content", "/request/body/messages/00/content")},
		{"invalid escape", strings.ReplaceAll(quickSuiteProfile, "/request/body/messages/0/content", "/request/body/~2")},
		{"missing request field", strings.ReplaceAll(quickSuiteProfile, "/request/body/messages/0/content", "/request/body/missing")},
		{"wrong value type", strings.Replace(quickSuiteProfile, `"default":"hello"`, `"default":17`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := quickSuiteCatalog(t, "gpt-5.2", "gpt-5.2", test.profile).Entries(context.Background()); err == nil {
				t.Fatal("invalid quick task profile accepted")
			}
		})
	}
}

func TestQuickSuiteWithoutEditableInputsRoundTripsUnchanged(t *testing.T) {
	profile := `{"description":"Fixed connection","timeout_ms":30000,"inputs":[]}`
	service := quickSuiteCatalog(t, "", "", profile)
	entries, err := service.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	original := entries[0].Suite
	if err := service.SaveSuite(context.Background(), "openai-chat", "connectivity", original); err != nil {
		t.Fatal(err)
	}
	entries, err = service.Entries(context.Background())
	if err != nil || entries[0].Suite.Revision != original.Revision || entries[0].Suite.QuickTest.Inputs == nil {
		t.Fatalf("fixed task changed on save: %+v, %v", entries, err)
	}
}
