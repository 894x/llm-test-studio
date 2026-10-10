package suitebundle_test

import (
	"context"
	"encoding/json"
	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledSuiteReferencesAndInputMappings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cases, err := casecatalog.New(casecatalog.Options{Builtin: casebundle.Bundle, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.TestCase{}
	for _, entry := range caseEntries {
		byID[entry.TestCase.ID] = entry.TestCase
	}
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: suitebundle.Bundle, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := suites.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 53 || len(byID) != 925 {
		t.Fatalf("catalog size = %d Suites / %d Cases", len(entries), len(byID))
	}
	protocols := map[domain.Protocol]bool{}
	for _, entry := range entries {
		suite := entry.Suite
		protocols[suite.Protocol] = true
		before, _ := json.Marshal(suite)
		if len(suite.Cases) == 0 {
			t.Fatalf("empty bundled Suite %s", suite.Key)
		}
		_, inputs, err := suite.ResolveInputs(map[string]json.RawMessage{})
		if err != nil {
			t.Fatalf("%s defaults: %v", suite.Key, err)
		}
		for _, member := range suite.Cases {
			candidate, exists := byID[member.CaseID]
			if !exists || !candidate.SupportsProtocol(suite.Protocol) {
				t.Fatalf("%s has missing or cross-protocol member %s", suite.Key, member.CaseID)
			}
			spec, err := candidate.SpecFor(suite.Protocol)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := testspec.ValidateInputs(spec.Inputs, inputs[candidate.ID]); err != nil {
				t.Fatalf("%s / %s input binding: %v", suite.Key, candidate.Key, err)
			}
			for _, input := range suite.Inputs {
				for _, binding := range input.Bindings {
					if binding.CaseID == candidate.ID {
						if _, declared := spec.Inputs[binding.Input]; !declared {
							t.Fatalf("%s maps undeclared input %s", suite.Key, binding.Input)
						}
					}
				}
			}
		}
		for _, input := range suite.Inputs {
			if input.Key != "prompt" {
				continue
			}
			_, mapped, err := suite.ResolveInputs(map[string]json.RawMessage{"prompt": json.RawMessage(`"A cat walking in a garden"`)})
			if err != nil {
				t.Fatalf("%s prompt override: %v", suite.Key, err)
			}
			for _, binding := range input.Bindings {
				if string(mapped[binding.CaseID][binding.Input]) != `"A cat walking in a garden"` {
					t.Fatalf("%s lost explicit prompt mapping", suite.Key)
				}
			}
		}
		after, _ := json.Marshal(suite)
		if string(before) != string(after) {
			t.Fatalf("%s mutated by input resolution", suite.Key)
		}
	}
	if len(protocols) != 6 {
		t.Fatalf("bundled protocols = %v", protocols)
	}
	// Scenario membership stays authored; model/channel binding happens once at Run start.
	for _, profile := range []struct{ prefix, automatic, complete string }{
		{"openai-chat.glm-5.3", ".automatic-regression", ".complete"},
		{"minimax-video.MiniMax-H3", ".automatic-regression", ""},
		{"wan-video.wan3.0-video", ".automatic", ""},
		{"wan-video.wan3.0-video-prime", ".automatic", ""},
	} {
		groups := map[string]map[string]bool{}
		for _, entry := range entries {
			suite := entry.Suite
			if suite.Key != profile.prefix && !strings.HasPrefix(suite.Key, profile.prefix+".") {
				continue
			}
			members := map[string]bool{}
			for _, ref := range suite.Cases {
				candidate := byID[ref.CaseID]
				members[candidate.Key] = true
				isRunnable := strings.HasSuffix(suite.Key, ".connectivity") || strings.HasSuffix(suite.Key, profile.automatic)
				if isRunnable && (!candidate.Enabled || candidate.ExecutionMode != domain.CaseExecutionAutomatic) {
					t.Fatalf("%s includes non-runnable %s", suite.Key, candidate.Key)
				}
			}
			groups[strings.TrimPrefix(suite.Key, profile.prefix)] = members
		}
		for _, suffix := range []string{".connectivity", ".basic", profile.automatic, profile.complete} {
			if len(groups[suffix]) == 0 {
				t.Fatalf("missing %s%s", profile.prefix, suffix)
			}
		}
		for _, pair := range [][2]string{{".connectivity", ".basic"}, {profile.automatic, profile.complete}, {".basic", profile.complete}} {
			for key := range groups[pair[0]] {
				if !groups[pair[1]][key] {
					t.Fatalf("%s%s is missing %s from %s", profile.prefix, pair[1], key, pair[0])
				}
			}
		}
	}
}
