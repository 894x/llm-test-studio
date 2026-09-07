package suitebundle_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
)

func TestBundledQuickTasksCoverProtocolsAndAuthoredModelScopes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cases, err := casecatalog.New(casecatalog.Options{Builtin: casebundle.Bundle, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: suitebundle.Bundle, UserRoot: filepath.Join(root, "suites"), Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := suites.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]domain.TestCase)
	for _, entry := range caseEntries {
		byID[entry.TestCase.ID] = entry.TestCase
	}
	registry := casetypes.MustBuiltinRegistry()
	tasksByProtocol, tasksByScope := map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		suite := entry.Suite
		if suite.QuickTest == nil {
			continue
		}
		tasksByProtocol[string(suite.Protocol)] = true
		tasksByScope[string(suite.Protocol)+"/"+suite.ModelTarget] = true
		members := make([]domain.TestCase, 0, len(suite.Cases))
		for _, ref := range suite.Cases {
			members = append(members, byID[ref.CaseID])
		}
		before, _ := json.Marshal(members)
		for _, overrides := range []map[string]json.RawMessage{nil, {"prompt": json.RawMessage(`"A cat walking in a garden"`)}} {
			effective, _, err := suite.ApplyInputs(members, overrides)
			if err != nil {
				t.Fatalf("%s inputs: %v", suite.Key, err)
			}
			for _, testCase := range effective {
				if err := registry.Validate(testCase.Protocol, testCase.Definition); err != nil {
					t.Fatalf("%s effective %s: %v", suite.Key, testCase.Key, err)
				}
			}
		}
		after, _ := json.Marshal(members)
		if string(before) != string(after) {
			t.Fatalf("%s mutated source Cases", suite.Key)
		}
	}
	for _, info := range protocol.All() {
		if !tasksByProtocol[info.ID] {
			t.Errorf("no bundled quick task for %s", info.ID)
		}
	}
	for _, entry := range entries {
		if scope := string(entry.Suite.Protocol) + "/" + entry.Suite.ModelTarget; !tasksByScope[scope] {
			t.Errorf("no quick task for authored model scope %s", scope)
		}
	}
}
