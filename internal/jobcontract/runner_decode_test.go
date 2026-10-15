package jobcontract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestRunnerInfoDecodeRequiresEveryCurrentFieldWithoutDefaults(t *testing.T) {
	info := runnerInfo()
	document, _ := json.Marshal(info)
	var root map[string]json.RawMessage
	_ = json.Unmarshal(document, &root)
	reordered, _ := json.Marshal(root)
	if _, err := jobcontract.DecodeRunnerInfo(bytes.NewReader(reordered)); err != nil {
		t.Fatal("current JSON field order became a build compatibility restriction")
	}
	for _, scope := range []string{"", "capabilities", "budget"} {
		object := root
		if scope != "" {
			object = map[string]json.RawMessage{}
			_ = json.Unmarshal(root[scope], &object)
		}
		for name := range object {
			for _, mutation := range []string{"missing", "null", "case"} {
				changed := map[string]json.RawMessage{}
				for key, value := range object {
					changed[key] = value
				}
				value := changed[name]
				delete(changed, name)
				switch mutation {
				case "null":
					changed[name] = json.RawMessage("null")
				case "case":
					changed[strings.ToUpper(name)] = value
				}
				invalid, _ := json.Marshal(changed)
				if scope != "" {
					container := map[string]json.RawMessage{}
					_ = json.Unmarshal(document, &container)
					container[scope] = invalid
					invalid, _ = json.Marshal(container)
				}
				if _, err := jobcontract.DecodeRunnerInfo(bytes.NewReader(invalid)); err == nil {
					t.Fatalf(
						"accepted %s %s.%s",
						mutation,
						scope,
						name,
					)
				}
			}
		}
	}
	for _, invalid := range []string{
		strings.Replace(
			string(document),
			`"ready":false`,
			`"ready":false,"ready":false`,
			1,
		),
		strings.Replace(
			string(document),
			`"features":[]`,
			`"features":[],"extra":false`,
			1,
		),
		strings.Replace(
			string(document),
			`"max_active_jobs":1`,
			`"max_active_jobs":1,"max_active_jobs":1`,
			1,
		),
		string(document) + " {}", `null`, `[]`,
	} {
		if _, err := jobcontract.DecodeRunnerInfo(strings.NewReader(invalid)); err == nil {
			t.Fatal("duplicate, unknown or extra metadata was accepted")
		}
	}
	info.Contract++
	unsupported, _ := json.Marshal(info)
	if _, err := jobcontract.DecodeRunnerInfo(bytes.NewReader(unsupported)); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported contract did not require an explicit upgrade")
	}
	exact := string(document) + strings.Repeat(" ", jobcontract.MaxRunnerInfoBytes-len(document))
	if _, err := jobcontract.DecodeRunnerInfo(strings.NewReader(exact)); err != nil {
		t.Fatal("metadata at exact byte limit was rejected")
	}
	if _, err := jobcontract.DecodeRunnerInfo(strings.NewReader(exact + " ")); err == nil {
		t.Fatal("oversized metadata was accepted")
	}
}
