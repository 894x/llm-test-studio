package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func convertResultDocument(raw []byte, entryID, fallbackCaseID string) ([]byte, error) {
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
	if err := synthesizeCurrentResult(document, entryID, fallbackCaseID); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var result domain.Result
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return marshalCanonical(result)
}

func synthesizeCurrentResult(document map[string]any, entryID, fallbackCaseID string) error {
	if entryID != "" {
		document["entry_id"] = entryID
	}
	if _, ok := document["case_id"]; !ok && fallbackCaseID != "" {
		document["case_id"] = fallbackCaseID
	}
	if _, ok := document["execution_status"]; !ok {
		document["execution_status"] = "completed"
		if outcome, ok := document["success"].(map[string]any); ok {
			if transport, _ := outcome["transport"].(bool); !transport {
				document["execution_status"] = "failed"
			}
		}
	}
	if _, ok := document["verification"]; !ok {
		status := testspec.VerdictPassed
		if outcome, ok := document["success"].(map[string]any); ok {
			protocolOK, _ := outcome["protocol"].(bool)
			semanticOK, _ := outcome["semantic"].(bool)
			if !protocolOK || !semanticOK {
				status = testspec.VerdictFailed
			}
		} else if passed, ok := document["success"].(bool); ok && !passed {
			status = testspec.VerdictFailed
		}
		document["verification"] = map[string]any{
			"status":     status,
			"assertions": []any{},
		}
	}
	delete(document, "success")
	if entryID == "" {
		return fmt.Errorf("result %v is missing a plan entry id", document["id"])
	}
	return nil
}

func convertReportDocument(raw []byte, snapshotJSON []byte) ([]byte, error) {
	if len(snapshotJSON) == 0 {
		return nil, fmt.Errorf("report is missing a converted plan snapshot")
	}
	overlaid, err := overlayPlanSnapshot(raw, snapshotJSON)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(overlaid))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return nil, err
	}
	if len(snapshot.Entries) != 1 {
		return nil, fmt.Errorf("converted snapshot must contain one entry")
	}
	entry := snapshot.Entries[0]
	rawResults, _ := document["case_results"].([]any)
	convertedResults := make([]any, 0, len(rawResults))
	typedResults := make([]domain.Result, 0, len(rawResults))
	for index, item := range rawResults {
		result, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("report case result %d is not an object", index)
		}
		if err := synthesizeCurrentResult(result, entry.EntryID, entry.TargetID); err != nil {
			return nil, err
		}
		encodedResult, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		var typed domain.Result
		if err := json.Unmarshal(encodedResult, &typed); err != nil {
			return nil, fmt.Errorf("report case result %d: %w", index, err)
		}
		if err := typed.Validate(); err != nil {
			return nil, fmt.Errorf("report case result %d: %w", index, err)
		}
		canonical, err := marshalCanonical(typed)
		if err != nil {
			return nil, err
		}
		var canonicalValue any
		if err := json.Unmarshal(canonical, &canonicalValue); err != nil {
			return nil, err
		}
		convertedResults = append(convertedResults, canonicalValue)
		typedResults = append(typedResults, typed)
	}
	summary := domain.SummarizeVerification(typedResults)
	document["case_results"] = convertedResults
	document["schema_version"] = json.Number("1")
	document["protocol"] = snapshot.Model.Protocol
	document["verification"] = summary
	document["plan_snapshot"] = json.RawMessage(snapshotJSON)
	status := domain.EntryReportCompleted
	switch runStatus, _ := asString(document["run_status"]); runStatus {
	case string(domain.RunFailed):
		status = domain.EntryReportFailed
	case string(domain.RunCancelled):
		status = domain.EntryReportCancelled
	}
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var entryDocument map[string]any
	if err := json.Unmarshal(entryJSON, &entryDocument); err != nil {
		return nil, err
	}
	document["entry_reports"] = []any{
		map[string]any{
			"warmup_count":  entryDocument["warmup_count"],
			"settings":      entryDocument["settings"],
			"entry_id":      entry.EntryID,
			"target_kind":   entryDocument["target_kind"],
			"target_id":     entry.TargetID,
			"name":          entry.Name,
			"key":           entry.Key,
			"protocol":      snapshot.Model.Protocol,
			"parameters":    entryDocument["parameters"],
			"load":          entryDocument["load"],
			"seed":          snapshot.PlanDocument.Seed,
			"status":        status,
			"conclusion":    document["conclusion"],
			"verification":  summary,
			"sla":           document["sla"],
			"metrics":       document["metrics"],
			"timeline":      document["timeline"],
			"distributions": document["distributions"],
			"case_results":  convertedResults,
		},
	}
	if document["error_clusters"] == nil {
		document["error_clusters"] = []any{}
	}
	if document["evidence"] == nil {
		document["evidence"] = []any{}
	}
	if document["attachments"] == nil {
		document["attachments"] = []any{}
	}
	if document["timeline"] == nil {
		document["timeline"] = []any{}
	}
	if document["distributions"] == nil {
		document["distributions"] = []any{}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var report domain.Report
	if err := json.Unmarshal(encoded, &report); err != nil {
		return nil, err
	}
	report.PlanSnapshot = snapshot
	report.CaseResults = typedResults
	report.EntryReports[0].CaseResults = typedResults
	report.Verification = summary
	report.EntryReports[0].Verification = summary
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return marshalCanonical(report)
}
