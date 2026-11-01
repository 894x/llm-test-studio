package suitebundle_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestKimiDailyProfilesAndRetainedCases(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := casecatalog.New(casecatalog.Options{
		Builtin:  casebundle.Bundle,
		UserRoot: filepath.Join(root, "cases"),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseEntries, err := catalog.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]domain.TestCase{}
	for _, entry := range caseEntries {
		cases[entry.TestCase.Key] = entry.TestCase
	}

	suiteCatalog, err := suitecatalog.New(suitecatalog.Options{
		Builtin:  suitebundle.Bundle,
		UserRoot: filepath.Join(root, "suites"),
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := suiteCatalog.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	suites := map[string]domain.Suite{}
	for _, entry := range entries {
		suites[entry.Suite.Key] = entry.Suite
	}

	for key, expectedID := range map[string]string{
		"kimi-k3.official-foundation":                  "25ce387f-0db7-5d41-bd2d-1e357d756bda",
		"kimi-k3.1-pro.basic":                          "6b54ac67-1ff6-5238-a228-b8ef139285c2",
		"kimi-k3.1-pro.complete":                       "4a17d5ec-431b-51bf-b470-34aa94e64f07",
		"kimi-k3.1-pro-highspeed.basic":                "b08fc3dd-5f56-5ed8-a133-30f957268237",
		"kimi-k3.1-pro-highspeed.complete":             "ceb8bc51-5b79-52e5-86d1-c449c48d97f2",
		"openai-responses.kimi-k3.official-foundation": "8d518e0a-a5bf-52fa-8c8c-c9937eb6c47c",
		"openai-responses.kimi-k3.expected-success":    "2cd0d5db-7c08-5d4e-a606-34b7ceb8fb98",
	} {
		if suite, exists := suites[key]; !exists || suite.ID != expectedID {
			t.Errorf("existing suite %s no longer has ID %s", key, expectedID)
		}
	}

	for _, check := range []struct {
		daily     string
		parents   []string
		caseCount int
	}{
		{daily: "kimi-k3.daily", parents: []string{"kimi-k3.official-foundation"}, caseCount: 7},
		{daily: "kimi-k3.1.daily", parents: []string{
			"kimi-k3.1-pro.basic", "kimi-k3.1-pro-highspeed.basic",
			"kimi-k3.1-pro.complete", "kimi-k3.1-pro-highspeed.complete",
		}, caseCount: 7},
		{daily: "openai-responses.kimi-k3.daily", parents: []string{
			"openai-responses.kimi-k3.official-foundation", "openai-responses.kimi-k3.expected-success",
		}, caseCount: 6},
	} {
		daily, ok := suites[check.daily]
		if !ok || len(daily.Cases) != check.caseCount {
			t.Fatalf("daily suite %s missing or has unexpected size", check.daily)
		}
		for _, parentKey := range check.parents {
			parent, exists := suites[parentKey]
			if !exists {
				t.Fatalf("missing existing suite %s", parentKey)
			}
			members := map[string]bool{}
			for _, ref := range parent.Cases {
				members[ref.CaseID] = true
			}
			for _, ref := range daily.Cases {
				if !members[ref.CaseID] {
					t.Errorf("%s missing daily case %s", parentKey, ref.CaseID)
				}
			}
		}
	}

	for _, check := range []struct {
		old, kept string
		protocol  domain.Protocol
		parents   []string
	}{
		{old: "must.tool_call", kept: "must.tool_choice_required", protocol: domain.Protocol("openai-chat"),
			parents: []string{"kimi-k3.official-foundation"}},
		{old: "k31.must.tool_call", kept: "k31.must.tool_choice_required", protocol: domain.Protocol("openai-chat"),
			parents: []string{"kimi-k3.1-pro.basic", "kimi-k3.1-pro.complete", "kimi-k3.1-pro-highspeed.basic", "kimi-k3.1-pro-highspeed.complete"}},
		{old: "k3.responses.cache_options_omitted", kept: "k3.responses.input_string", protocol: domain.Protocol("openai-responses"),
			parents: []string{"openai-responses.kimi-k3.official-foundation", "openai-responses.kimi-k3.expected-success"}},
	} {
		old, oldExists := cases[check.old]
		kept, keptExists := cases[check.kept]
		if !oldExists || !keptExists {
			t.Fatalf("existing cases must remain available: %s, %s", check.old, check.kept)
		}
		oldSpec, oldErr := old.SpecFor(check.protocol)
		keptSpec, keptErr := kept.SpecFor(check.protocol)
		if oldErr != nil || keptErr != nil || !reflect.DeepEqual(oldSpec, keptSpec) {
			t.Fatalf("replaced case %s no longer matches %s", check.old, check.kept)
		}
		for _, parentKey := range check.parents {
			parent, exists := suites[parentKey]
			if !exists {
				t.Fatalf("missing existing suite %s", parentKey)
			}
			foundKept := false
			for _, ref := range parent.Cases {
				if ref.CaseID == old.ID {
					t.Errorf("%s still repeats %s", parentKey, check.old)
				}
				foundKept = foundKept || ref.CaseID == kept.ID
			}
			if !foundKept {
				t.Errorf("%s missing equivalent case %s", parentKey, check.kept)
			}
		}
	}

	for _, key := range []string{"k31.dynamic_tool", "k31.dynamic_and_global_tools", "k31.dynamic_tool_with_content"} {
		candidate, ok := cases[key]
		if !ok {
			t.Fatalf("missing dynamic tool case %s", key)
		}
		for _, check := range []struct {
			suite string
			want  bool
		}{
			{suite: "kimi-k3.1-pro.complete", want: true},
			{suite: "kimi-k3.1-pro-highspeed.complete", want: false},
			{suite: "kimi-k3.1.daily", want: false},
		} {
			found := false
			for _, ref := range suites[check.suite].Cases {
				found = found || ref.CaseID == candidate.ID
			}
			if found != check.want {
				t.Errorf("%s membership of %s = %t, want %t", check.suite, key, found, check.want)
			}
		}
	}
}
