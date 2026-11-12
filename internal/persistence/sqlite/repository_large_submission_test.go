package sqlite_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestLargePendingSubmissionAdvancesWithoutHidingFollowingJobs(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	entry := &spec.Spec.Run.Snapshot.Entries[0]
	template := entry.CaseDefinitions[0]
	entry.Cases = []domain.CaseRevisionRef{}
	entry.Suite.Cases = []domain.CaseRef{}
	entry.CaseDefinitions = []domain.TestCase{}
	entry.CaseInputs = map[string]map[string]json.RawMessage{}
	definition, err := json.Marshal(map[string]any{
		"request": map[string]any{"body": map[string]any{"messages": []map[string]any{
			{"role": "user", "content": strings.Repeat("p", 3<<20)},
		}}}, "inputs": map[string]any{}, "assertions": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 6 {
		testCase := template
		testCase.ID = fmt.Sprintf("72000000-0000-4000-8000-%012d", index+1)
		testCase.Key = fmt.Sprintf("large-case-%d", index)
		testCase.Definitions = domain.ProtocolDefinitions{domain.ProtocolOpenAIChat: definition}
		entry.Cases = append(entry.Cases, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision})
		entry.Suite.Cases = append(entry.Suite.Cases, domain.CaseRef{CaseID: testCase.ID})
		entry.CaseDefinitions = append(entry.CaseDefinitions, testCase)
		entry.CaseInputs[testCase.ID] = map[string]json.RawMessage{}
	}
	spec.JobID = "72000000-0000-4000-8000-000000000001"
	if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
		t.Fatal(err)
	}
	next := jobSpecForRun(t, runWithCompleteSnapshot(t))
	next.JobID = "72000000-0000-4000-8000-000000000002"
	if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, next)); err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListPendingSubmissions(t.Context(), "", 25)
	if err != nil || len(page.Records) != 1 || page.NextCursor != spec.JobID || !page.HasMore {
		t.Fatalf("large first record did not advance alone: %v", err)
	}
	page, err = repository.ListPendingSubmissions(t.Context(), page.NextCursor, 25)
	if err != nil || len(page.Records) != 1 || page.NextCursor != next.JobID || page.HasMore {
		t.Fatalf("following record disappeared: %v", err)
	}
}
