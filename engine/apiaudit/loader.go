package apiaudit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
)

var safeResultIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)

func LoadSuite(root, suite string) ([]CaseDefinition, error) {
	registry := protocols.NewRegistry()
	if !containsProtocol(suite) {
		return nil, fmt.Errorf("unsupported protocol %q", suite)
	}
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("case catalog must not contain symbolic links")
		}
		if !entry.IsDir() && entry.Name() == "case.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	cases := []CaseDefinition{}
	seen := map[string]bool{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		candidate, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		if !candidate.SupportsProtocol(domain.Protocol(suite)) {
			continue
		}
		spec, err := candidate.SpecFor(domain.Protocol(suite))
		if err != nil {
			return nil, err
		}
		if err = registry.Validate(suite, spec); err != nil {
			return nil, err
		}
		if seen[candidate.Key] {
			return nil, fmt.Errorf("duplicate case %s", candidate.Key)
		}
		seen[candidate.Key] = true
		cases = append(cases, CaseDefinition{
			ID: candidate.Key, Name: candidate.Name, Dimension: candidate.Dimension,
			Protocol: suite, Type: suite,
			Default: candidate.Default, Disabled: !candidate.Enabled,
			ExecutionMode: string(candidate.ExecutionMode), Severity: string(candidate.Severity), Spec: spec,
		})
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("suite %s has no case directories", suite)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func SelectCases(cases []CaseDefinition, ids []string, all bool) ([]CaseDefinition, error) {
	if all && len(ids) > 0 {
		return nil, fmt.Errorf("explicit case ids cannot be combined with all cases")
	}
	if all {
		selected := []CaseDefinition{}
		for _, item := range cases {
			if !item.Disabled && item.ExecutionMode == "automatic" {
				selected = append(selected, item)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("suite has no runnable cases")
		}
		return selected, nil
	}
	if len(ids) == 0 {
		selected := make([]CaseDefinition, 0, len(cases))
		for _, definition := range cases {
			if definition.Default && !definition.Disabled && definition.ExecutionMode == "automatic" {
				selected = append(selected, definition)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("suite has no default cases")
		}
		return selected, nil
	}

	byID := make(map[string]CaseDefinition, len(cases))
	for _, definition := range cases {
		byID[definition.ID] = definition
	}
	selected := make([]CaseDefinition, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		definition, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("unknown case %s", id)
		}
		if definition.Disabled || definition.ExecutionMode != "automatic" {
			return nil, fmt.Errorf("case %s is disabled or requires manual execution", id)
		}
		if !seen[id] {
			selected = append(selected, definition)
			seen[id] = true
		}
	}
	return selected, nil
}
