package suitebundle_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestGLM53ScenarioSuitesMatchModelAndExecutionPolicy(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "openai-chat/GLM53-*/case.json")
	if err != nil || len(paths) != 212 {
		t.Fatalf("GLM case count = %d: %v", len(paths), err)
	}
	all, automatic, rejected := []string{}, []string{}, []string{}
	for _, path := range paths {
		raw, err := fs.ReadFile(casebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		if len(candidate.ModelTargets) != 1 || candidate.ModelTargets[0] != "glm-5.3" || candidate.Default {
			t.Fatalf("case escaped model/default scope: %s", path)
		}
		var spec casetypes.LegacyAPIAuditSpec
		if err := json.Unmarshal(candidate.Definition.Spec, &spec); err != nil {
			t.Fatal(err)
		}
		if spec.Request.Path != "/chat/completions" || spec.Options["contract_row"] == nil {
			t.Fatalf("case endpoint or provenance missing: %s", path)
		}
		all = append(all, candidate.Key)
		if !candidate.Enabled {
			continue
		}
		if candidate.ExecutionMode != domain.CaseExecutionAutomatic || spec.Options["execution_tier"] == "T3" {
			t.Fatalf("T3/manual case became enabled: %s", path)
		}
		automatic = append(automatic, candidate.Key)
		if spec.Kind == "glm53_rejected" {
			rejected = append(rejected, candidate.Key)
		}
	}
	if len(automatic) != 165 || len(rejected) != 101 {
		t.Fatalf("automatic=%d rejected=%d", len(automatic), len(rejected))
	}
	profiles := map[string][]string{}
	for _, name := range []string{"connectivity", "basic", "parameter-rejection", "automatic-regression", "complete"} {
		path := "openai-chat/glm-5.3-" + name + "/suite.json"
		raw, err := fs.ReadFile(suitebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		var suite struct {
			Key         string   `json:"key"`
			Protocol    string   `json:"protocol"`
			ModelTarget string   `json:"model_target"`
			CaseKeys    []string `json:"case_keys"`
		}
		if err := json.Unmarshal(raw, &suite); err != nil {
			t.Fatal(err)
		}
		if suite.Protocol != "openai-chat" || suite.ModelTarget != "glm-5.3" || suite.Key != "openai-chat.glm-5.3."+name {
			t.Fatalf("invalid suite %s", path)
		}
		assertUniqueKeys(t, path, suite.CaseKeys)
		for _, key := range suite.CaseKeys {
			if !strings.HasPrefix(key, "glm53.") {
				t.Fatalf("generic chat cases leaked into GLM profile: %s", key)
			}
		}
		profiles[name] = suite.CaseKeys
	}
	assertExactKeys(t, "GLM connectivity", profiles["connectivity"], []string{
		"glm53.connectivity", "glm53.auth.missing", "glm53.auth.invalid",
	})
	assertExactKeys(t, "GLM basic", profiles["basic"], []string{
		"glm53.connectivity", "glm53.auth.missing", "glm53.auth.invalid", "glm53.thinking.enabled",
		"glm53.reasoning_effort.valid_0", "glm53.stream.true", "glm53.format.json", "glm53.tools.roundtrip",
		"glm53.tools.stream_true", "glm53.thinking.replay_false", "glm53.cache.counter", "glm53.thinking.disabled",
	})
	assertExactKeys(t, "GLM parameter rejection", profiles["parameter-rejection"], rejected)
	assertExactKeys(t, "GLM automatic", profiles["automatic-regression"], automatic)
	assertExactKeys(t, "GLM complete", profiles["complete"], all)
	assertSubset(t, "connectivity", profiles["connectivity"], "basic", profiles["basic"])
	assertSubset(t, "basic", profiles["basic"], "automatic", automatic)
	assertSubset(t, "rejected", rejected, "automatic", automatic)
	assertSubset(t, "automatic", automatic, "complete", all)
}
