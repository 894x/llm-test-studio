package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/domain"
)

func convertRunSnapshotDocument(raw []byte) ([]byte, error) {
	rewritten, err := rewriteContractVersions(raw)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(rewritten))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if _, ok := document["entries"]; !ok {
		converted, err := convertLegacySnapshot(document)
		if err != nil {
			return nil, err
		}
		document = converted
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, err
	}
	return marshalCanonical(snapshot)
}

func extractRunSnapshotDocument(raw []byte) ([]byte, error) {
	var document struct {
		PlanSnapshot json.RawMessage `json:"plan_snapshot"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if len(document.PlanSnapshot) == 0 || !json.Valid(document.PlanSnapshot) {
		return nil, fmt.Errorf("run document is missing a valid plan snapshot")
	}
	return append([]byte(nil), document.PlanSnapshot...), nil
}

func convertLegacySnapshot(document map[string]any) (map[string]any, error) {
	model, _ := document["model"].(map[string]any)
	protocol, _ := asString(model["protocol"])
	if protocol == "" {
		return nil, fmt.Errorf("legacy snapshot is missing model protocol")
	}
	planMeta, _ := document["plan"].(map[string]any)
	planID, _ := asString(planMeta["id"])
	cases, _ := document["cases"].([]any)
	definitions, _ := document["case_definitions"].([]any)
	if len(cases) == 0 || len(cases) != len(definitions) {
		return nil, fmt.Errorf("legacy snapshot cases and definitions are incomplete")
	}
	planDocument, _ := document["plan_document"].(map[string]any)
	if planDocument == nil {
		return nil, fmt.Errorf("legacy snapshot is missing plan_document")
	}
	load := document["load"]
	if load == nil {
		load = planDocument["load"]
	}
	sla := document["sla"]
	if sla == nil {
		sla = planDocument["sla"]
	}
	entries := make([]any, 0, len(cases))
	planEntries := make([]any, 0, len(cases))
	for index, rawCase := range cases {
		caseRef, _ := rawCase.(map[string]any)
		caseID, _ := asString(caseRef["case_id"])
		definition, _ := definitions[index].(map[string]any)
		if caseID == "" || definition == nil {
			return nil, fmt.Errorf("legacy snapshot case %d is incomplete", index)
		}
		entryID := snapshotEntryID(planID, caseID)
		key, _ := asString(definition["key"])
		name, _ := asString(definition["name"])
		if name == "" {
			name = key
		}
		convertedDefinition := convertLegacyCaseDefinition(protocol, definition)
		parameters := map[string]any{}
		settings := map[string]any{}
		entry := map[string]any{
			"warmup_count":     0,
			"settings":         settings,
			"entry_id":         entryID,
			"target_kind":      "case",
			"target_id":        caseID,
			"name":             name,
			"key":              key,
			"cases":            []any{caseRef},
			"case_definitions": []any{convertedDefinition},
			"parameters":       parameters,
			"case_inputs":      map[string]any{caseID: map[string]any{}},
			"load":             load,
			"sla":              sla,
		}
		entries = append(entries, entry)
		planEntries = append(planEntries, map[string]any{
			"entry_id":     entryID,
			"target_kind":  "case",
			"target_id":    caseID,
			"parameters":   parameters,
			"warmup_count": 0,
			"settings":     settings,
			"load":         load,
			"sla":          sla,
		})
	}
	convertedPlan := map[string]any{
		"id":             planDocument["id"],
		"schema_version": json.Number("1"),
		"revision":       planDocument["revision"],
		"created_at":     planDocument["created_at"],
		"updated_at":     planDocument["updated_at"],
		"name":           planDocument["name"],
		"protocol":       protocol,
		"seed":           json.Number("0"),
		"entries":        planEntries,
	}
	return map[string]any{
		"schema_version": json.Number("1"),
		"plan":           document["plan"],
		"model":          document["model"],
		"channel":        document["channel"],
		"environment":    document["environment"],
		"plan_document":  convertedPlan,
		"mapping":        document["mapping"],
		"entries":        entries,
	}, nil
}

func convertLegacyCaseDefinition(protocol string, definition map[string]any) map[string]any {
	converted := map[string]any{}
	for key, value := range definition {
		if key == "model_targets" || key == "definition" {
			continue
		}
		converted[key] = value
	}
	converted["protocol"] = protocol
	converted["schema_version"] = json.Number("1")
	body := json.RawMessage(`{}`)
	if nested, ok := definition["definition"].(map[string]any); ok {
		if spec, ok := nested["spec"].(map[string]any); ok {
			if request, ok := spec["request"].(map[string]any); ok {
				if encoded, err := json.Marshal(request["body"]); err == nil && json.Valid(encoded) {
					body = encoded
				}
			}
		}
	}
	var bodyValue any
	_ = json.Unmarshal(body, &bodyValue)
	converted["definition"] = map[string]any{
		"schema_version": json.Number("1"),
		"type":           protocol,
		"type_version":   json.Number("1"),
		"spec": map[string]any{
			"inputs":     map[string]any{},
			"request":    map[string]any{"body": bodyValue},
			"assertions": []any{},
		},
	}
	return converted
}

func snapshotEntryID(planID, caseID string) string {
	sum := sha1.Sum([]byte("llm-test-studio/schema-reset/entry/" + planID + "/" + caseID))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func asString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}
