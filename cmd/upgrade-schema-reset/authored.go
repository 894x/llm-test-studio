package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func upgradeAuthoredFiles(root string) (string, error) {
	casesRoot := filepath.Join(root, "data", "cases")
	suitesRoot := filepath.Join(root, "data", "suites")
	plansRoot := filepath.Join(root, "data", "plans")
	caseCount, err := rewriteTree(casesRoot, "case.json", upgradeCaseDocument)
	if err != nil {
		return "", err
	}
	suiteCount, err := rewriteTree(suitesRoot, "suite.json", upgradeSuiteDocument)
	if err != nil {
		return "", err
	}
	profileCount, err := rewriteTree(suitesRoot, "*.suite-profiles.json", upgradeSuiteProfileDocument)
	if err != nil {
		return "", err
	}
	planCount, err := rewriteTree(plansRoot, "*.json", upgradePlanDocument)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("updated %d cases, %d suites, %d suite profiles, %d plans", caseCount, suiteCount, profileCount, planCount), nil
}

func rewriteTree(root, match string, upgrade func([]byte) ([]byte, bool, error)) (int, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var count int
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		matched, err := filepath.Match(match, filepath.Base(path))
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated, changed, err := upgrade(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !changed {
			return nil
		}
		if err := os.WriteFile(path, updated, 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

func upgradeCaseDocument(raw []byte) ([]byte, bool, error) {
	updated := bytes.Replace(raw, []byte(`"schema_version": 3,`), []byte(`"schema_version": 1,`), 1)
	updated = bytes.Replace(updated, []byte(`"schema_version": 2,`), []byte(`"schema_version": 1,`), 1)
	if bytes.Equal(updated, raw) {
		return raw, false, nil
	}
	return updated, true, nil
}

func upgradeSuiteDocument(raw []byte) ([]byte, bool, error) {
	updated := bytes.Replace(raw, []byte(`"schema_version": 2,`), []byte(`"schema_version": 1,`), 1)
	if bytes.Equal(updated, raw) {
		return raw, false, nil
	}
	return updated, true, nil
}

func upgradePlanDocument(raw []byte) ([]byte, bool, error) {
	updated := bytes.Replace(raw, []byte(`"file_schema_version": 4,`), []byte(`"file_schema_version": 1,`), 1)
	updated = bytes.Replace(updated, []byte(`"file_schema_version": 3,`), []byte(`"file_schema_version": 1,`), 1)
	updated = bytes.Replace(updated, []byte(`"file_schema_version": 2,`), []byte(`"file_schema_version": 1,`), 1)
	if bytes.Equal(updated, raw) {
		return raw, false, nil
	}
	return updated, true, nil
}

func upgradeSuiteProfileDocument(raw []byte) ([]byte, bool, error) {
	updated := []byte(strings.ReplaceAll(string(raw), `"schema_version": 2,`, `"schema_version": 1,`))
	if bytes.Equal(updated, raw) {
		return raw, false, nil
	}
	var document map[string]any
	if err := json.Unmarshal(updated, &document); err != nil {
		return nil, false, err
	}
	if version, _ := document["schema_version"].(float64); version != 1 {
		return nil, false, fmt.Errorf("suite profile manifest schema_version must remain 1")
	}
	return updated, true, nil
}
