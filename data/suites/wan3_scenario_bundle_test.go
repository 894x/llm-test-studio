package suitebundle_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
)

func TestWan3ScenarioSuitesRespectExecutionPolicy(t *testing.T) {
	t.Parallel()

	type caseDocument struct {
		Key           string   `json:"key"`
		ModelTargets  []string `json:"model_targets"`
		Enabled       bool     `json:"enabled"`
		ExecutionMode string   `json:"execution_mode"`
		Definition    struct {
			Spec struct {
				Kind string `json:"kind"`
			} `json:"spec"`
		} `json:"definition"`
	}
	casesByKey := make(map[string]caseDocument)
	casePaths, err := fs.Glob(casebundle.Bundle, "wan-video/*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range casePaths {
		raw, readErr := fs.ReadFile(casebundle.Bundle, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var document caseDocument
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		casesByKey[document.Key] = document
	}
	allKeysByTarget := map[string][]string{"wan3.0-video": {}, "wan3.0-video-prime": {}}
	automaticKeysByTarget := map[string][]string{"wan3.0-video": {}, "wan3.0-video-prime": {}}
	negativeKeysByTarget := map[string][]string{"wan3.0-video": {}, "wan3.0-video-prime": {}}
	for key, definition := range casesByKey {
		for target := range allKeysByTarget {
			if !containsTarget(definition.ModelTargets, target) {
				continue
			}
			allKeysByTarget[target] = append(allKeysByTarget[target], key)
			if definition.Enabled && definition.ExecutionMode == "automatic" {
				automaticKeysByTarget[target] = append(automaticKeysByTarget[target], key)
				if definition.Definition.Spec.Kind == "wan_task_rejected" {
					negativeKeysByTarget[target] = append(negativeKeysByTarget[target], key)
				}
			}
		}
	}

	type suiteDocument struct {
		Key         string   `json:"key"`
		ModelTarget string   `json:"model_target"`
		CaseKeys    []string `json:"case_keys"`
	}
	suitePaths, err := fs.Glob(suitebundle.Bundle, "wan-video/*/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range suitePaths {
		raw, readErr := fs.ReadFile(suitebundle.Bundle, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var suite suiteDocument
		if err := json.Unmarshal(raw, &suite); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if suite.ModelTarget != "wan3.0-video" && suite.ModelTarget != "wan3.0-video-prime" {
			continue
		}
		for _, key := range suite.CaseKeys {
			definition, found := casesByKey[key]
			if !found || !containsTarget(definition.ModelTargets, suite.ModelTarget) {
				t.Fatalf("suite %s references missing or inapplicable case %s", suite.Key, key)
			}
			if strings.HasSuffix(suite.Key, ".connectivity") || strings.HasSuffix(suite.Key, ".basic") || strings.HasSuffix(suite.Key, ".automatic") || strings.HasSuffix(suite.Key, ".negative") {
				if !definition.Enabled || definition.ExecutionMode != "automatic" {
					t.Fatalf("runnable scenario suite %s contains non-runnable case %s", suite.Key, key)
				}
			}
			if strings.HasSuffix(suite.Key, ".negative") && definition.Definition.Spec.Kind != "wan_task_rejected" {
				t.Fatalf("negative suite %s contains non-rejection case %s", suite.Key, key)
			}
		}
		switch {
		case strings.HasSuffix(suite.Key, ".automatic"):
			assertSameKeySet(t, suite.Key, suite.CaseKeys, automaticKeysByTarget[suite.ModelTarget])
		case strings.HasSuffix(suite.Key, ".negative"):
			assertSameKeySet(t, suite.Key, suite.CaseKeys, negativeKeysByTarget[suite.ModelTarget])
		case suite.Key == "wan-video."+suite.ModelTarget:
			assertSameKeySet(t, suite.Key, suite.CaseKeys, allKeysByTarget[suite.ModelTarget])
		}
	}
}

func assertSameKeySet(t *testing.T, suite string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("suite %s has %d cases, want %d", suite, len(got), len(want))
	}
	wantSet := make(map[string]bool, len(want))
	for _, key := range want {
		wantSet[key] = true
	}
	seen := make(map[string]bool, len(got))
	for _, key := range got {
		if !wantSet[key] || seen[key] {
			t.Fatalf("suite %s has unexpected or duplicate case %s", suite, key)
		}
		seen[key] = true
	}
}

func containsTarget(targets []string, target string) bool {
	for _, candidate := range targets {
		if candidate == target {
			return true
		}
	}
	return false
}
