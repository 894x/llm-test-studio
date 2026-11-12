package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jsonbudget"
)

func TestSixteenChannelFullPlanRoundTrip(t *testing.T) {
	spec := comparisonSpec(t, 16)
	template := spec.Spec.Comparison.Runs[0].Snapshot
	original := template.Entries[0]
	plan := *template.PlanDocument
	plan.Entries = []domain.PlanEntry{}
	entries := []domain.RunEntrySnapshot{}
	// Five plan items and 121 cases, including large multimodal-sized definitions.
	for item, count := range []int{30, 30, 23, 18, 20} {
		entry := original
		entry.EntryID = testMeta(t).ID
		suite := *original.Suite
		suite.EntityMeta = testMeta(t)
		suite.Key = fmt.Sprintf("full-plan-%d", item)
		suite.Cases = []domain.CaseRef{}
		entry.TargetID = suite.ID
		entry.Suite = &suite
		entry.Cases = []domain.CaseRevisionRef{}
		entry.CaseDefinitions = []domain.TestCase{}
		entry.CaseInputs = map[string]map[string]json.RawMessage{}
		for index := range count {
			testCase := original.CaseDefinitions[0]
			testCase.EntityMeta = testMeta(t)
			testCase.Key = fmt.Sprintf("case-%d-%d", item, index)
			body, err := json.Marshal(map[string]any{"request": map[string]any{"body": map[string]any{
				"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("x", 8192)}},
			}}, "inputs": map[string]any{}, "assertions": []any{}})
			if err != nil {
				t.Fatal(err)
			}
			testCase.Definitions = domain.ProtocolDefinitions{domain.ProtocolOpenAIChat: body}
			suite.Cases = append(suite.Cases, domain.CaseRef{CaseID: testCase.ID})
			entry.Cases = append(entry.Cases, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: 1})
			entry.CaseDefinitions = append(entry.CaseDefinitions, testCase)
			entry.CaseInputs[testCase.ID] = map[string]json.RawMessage{}
		}
		planEntry := planEntryForLargeComparison(original)
		planEntry.EntryID, planEntry.TargetID = entry.EntryID, entry.TargetID
		plan.Entries = append(plan.Entries, planEntry)
		entries = append(entries, entry)
	}
	for index := range spec.Spec.Comparison.Runs {
		snapshot := &spec.Spec.Comparison.Runs[index].Snapshot
		snapshot.PlanDocument = &plan
		snapshot.Entries = entries
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 8<<20 {
		t.Fatalf("fixture must exceed old byte budget: %d", len(raw))
	}
	if err := jsonbudget.Check(raw, 32_768); !errors.Is(err, jsonbudget.ErrExceeded) {
		t.Fatalf("fixture must exceed old collection budget: %v", err)
	}
	t.Logf("16-channel full plan: %d bytes", len(raw))
	frozen, err := spec.Freeze()
	if err != nil {
		t.Fatalf("freeze full plan: %v", err)
	}
	submission := submissionWithFixtureSecrets(frozen)
	document, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSubmission(bytes.NewReader(document))
	if err != nil {
		t.Fatalf("decode full submission: %v", err)
	}
	defer decoded.Clear()
	if len(decoded.Job.Spec.Comparison.Runs) != 16 {
		t.Fatal("lost comparison channels")
	}
	for _, run := range decoded.Job.Spec.Comparison.Runs {
		count := 0
		for _, entry := range run.Snapshot.Entries {
			count += len(entry.CaseDefinitions)
		}
		if len(run.Snapshot.Entries) != 5 || count != 121 {
			t.Fatal("lost plan items or cases")
		}
	}
	first, err := frozen.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := decoded.Job.Digest()
	if err != nil || first != second {
		t.Fatalf("digest changed after transport: %v", err)
	}
}

func planEntryForLargeComparison(entry domain.RunEntrySnapshot) domain.PlanEntry {
	return domain.PlanEntry{EntryID: entry.EntryID, TargetKind: entry.TargetKind, TargetID: entry.TargetID,
		Parameters: entry.Parameters, Load: entry.Load, SLA: entry.SLA}
}
