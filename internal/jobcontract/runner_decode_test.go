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

func TestRunnerInfoDecoderRejectsWrongCurrentFieldTypesWithoutMutation(t *testing.T) {
	document, err := json.Marshal(runnerInfo())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		scope string
		field string
		value string
	}{
		{field: "contract", value: "true"},
		{field: "node_id", value: "1"},
		{field: "node_installation_id", value: "1"},
		{field: "engine_build_id", value: "1"},
		{field: "runner_version", value: "1"},
		{field: "os", value: "1"},
		{field: "arch", value: "1"},
		{field: "maintenance_operation_id", value: "1"},
		{field: "capabilities", value: "[]"},
		{field: "budget", value: "false"},
		{field: "ready", value: "0"},
		{field: "healthy", value: "0"},
		{field: "active", value: `"0"`},
		{field: "queued", value: `"0"`},
		{field: "pending", value: `"0"`},
		{scope: "capabilities", field: "job_kinds", value: `"run"`},
		{scope: "capabilities", field: "protocols", value: "{}"},
		{scope: "capabilities", field: "features", value: "[false]"},
		{scope: "budget", field: "max_active_jobs", value: `"1"`},
		{scope: "budget", field: "max_queued_jobs", value: `"1"`},
		{scope: "budget", field: "max_concurrency", value: `"1"`},
		{scope: "budget", field: "queue_timeout_ns", value: `"1"`},
		{scope: "budget", field: "execution_timeout_ns", value: `"1"`},
		{field: "active", value: "0.5"},
		{scope: "budget", field: "queue_timeout_ns", value: "0.5"},
	} {
		t.Run(test.scope+"."+test.field+"="+test.value, func(t *testing.T) {
			root := map[string]json.RawMessage{}
			if err := json.Unmarshal(document, &root); err != nil {
				t.Fatal(err)
			}
			fields := root
			if test.scope != "" {
				fields = map[string]json.RawMessage{}
				if err := json.Unmarshal(root[test.scope], &fields); err != nil {
					t.Fatal(err)
				}
			}
			fields[test.field] = json.RawMessage(test.value)
			if test.scope != "" {
				root[test.scope], err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
			}
			invalid, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			before := bytes.Clone(invalid)
			if _, err := jobcontract.DecodeRunnerInfo(bytes.NewReader(invalid)); !errors.Is(err, jobcontract.ErrInvalid) {
				t.Fatalf("wrong field type error = %v; want ErrInvalid", err)
			}
			if !bytes.Equal(invalid, before) {
				t.Fatal("rejecting runner metadata changed the input bytes")
			}
		})
	}
}
