package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

type documentScope uint8

const (
	scopeUnknown documentScope = iota
	scopeRoot
	scopeJob
	scopePayload
	scopeComparison
	scopeAssets
	scopeSecrets
	scopeRuns
	scopeAssetItem
	scopeSecretItem
	scopeTarget
	scopePerformance
	scopeRun
	scopeCommand
	scopeMaintenanceCommand
	scopeTaskConcurrencyCommand
	scopeMaintenanceRecord
	scopeBoolean
)

// Scan collection counts before decoding typed slices. A bounded byte body can
// otherwise expand into millions of large zero-value structs before rejection.
func checkCollectionBounds(raw []byte, scope documentScope) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanValue(decoder, scope); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid("body")
	}
	return nil
}

func scanValue(decoder *json.Decoder, scope documentScope) error {
	token, err := decoder.Token()
	if err != nil {
		return invalid("body")
	}
	if scope == scopeBoolean {
		if _, ok := token.(bool); !ok {
			return invalid("functional_progress")
		}
		return nil
	}
	switch token {
	case json.Delim('{'):
		allowed := envelopeFields(scope)
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return invalid("body")
			}
			name, ok := key.(string)
			if !ok {
				return invalid("body")
			}
			if allowed != nil {
				if !slices.Contains(allowed, name) {
					return invalid("body")
				}
				if _, duplicate := seen[name]; duplicate {
					return invalid("body")
				}
				seen[name] = struct{}{}
			}
			if err := scanValue(decoder, childScope(scope, name)); err != nil {
				return err
			}
		}
	case json.Delim('['):
		limit, field := collectionLimit(scope)
		count := 0
		for decoder.More() {
			if limit > 0 && count >= limit {
				return invalid(field)
			}
			if err := scanValue(decoder, elementScope(scope)); err != nil {
				return err
			}
			count++
		}
	default:
		return nil
	}
	if _, err := decoder.Token(); err != nil {
		return invalid("body")
	}
	return nil
}

func childScope(parent documentScope, name string) documentScope {
	switch parent {
	case scopeRoot, scopeJob:
		switch name {
		case "functional_progress":
			return scopeBoolean
		case "job":
			if parent == scopeRoot {
				return scopeJob
			}
		case "spec":
			return scopePayload
		case "target":
			return scopeTarget
		case "assets":
			return scopeAssets
		case "secrets":
			if parent == scopeRoot {
				return scopeSecrets
			}
		}
	case scopePayload:
		switch name {
		case "comparison":
			return scopeComparison
		case "performance":
			return scopePerformance
		case "run":
			return scopeRun
		}
	case scopeComparison:
		if name == "runs" {
			return scopeRuns
		}
	}
	return scopeUnknown
}

func elementScope(parent documentScope) documentScope {
	switch parent {
	case scopeAssets:
		return scopeAssetItem
	case scopeSecrets:
		return scopeSecretItem
	case scopeRuns:
		return scopeRun
	default:
		return scopeUnknown
	}
}

func envelopeFields(scope documentScope) []string {
	switch scope {
	case scopeCommand:
		return []string{"contract", "command_id", "job_id", "kind", "expected_revision"}
	case scopeMaintenanceCommand:
		return []string{"contract", "operation_id", "action"}
	case scopeTaskConcurrencyCommand:
		return []string{"contract", "node_id", "node_installation_id", "expected_max_active_jobs", "max_active_jobs"}
	case scopeMaintenanceRecord:
		return []string{"contract", "node_installation_id", "operation_id", "paused", "changed_at"}
	case scopeRoot:
		// Both a complete Submission and a standalone JobSpec use this scanner.
		// Secret's string-only decoder also uses the same strict decode helper.
		return []string{
			"job", "secrets", "contract", "job_id", "node_installation_id", "engine_build_id",
			"target", "kind", "spec", "assets", "functional_progress", "slot", "value",
		}
	case scopeJob:
		return []string{
			"contract", "job_id", "node_installation_id", "engine_build_id", "target",
			"kind", "spec", "assets", "functional_progress",
		}
	case scopePayload:
		return []string{"run", "performance", "comparison"}
	case scopeComparison:
		return []string{"meta", "plan", "model", "runs"}
	case scopeAssetItem:
		return []string{"ref", "sha256", "size", "media_type"}
	case scopeSecretItem:
		return []string{"slot", "value"}
	case scopeTarget:
		return []string{"kind", "node_id"}
	case scopePerformance:
		return []string{"report_id", "protocol", "address_mode", "base_url", "endpoint", "model_id", "profile"}
	case scopeRun:
		return []string{"meta", "plan_id", "snapshot"}
	default:
		return nil
	}
}

func collectionLimit(scope documentScope) (int, string) {
	switch scope {
	case scopeAssets:
		return MaxAssets, "assets"
	case scopeSecrets:
		return MaxSecrets, "secrets"
	case scopeRuns:
		return MaxSecrets, "spec.comparison.runs"
	default:
		return 0, ""
	}
}
