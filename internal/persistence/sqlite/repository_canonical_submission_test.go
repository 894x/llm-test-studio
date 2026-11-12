package sqlite_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestSubmissionStateTransitionsKeepCanonicalPreparedBytes(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	record := submissionFixture(t, spec)
	record, err := repository.CreateSubmission(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(record.Prepared)
	if err != nil {
		t.Fatal(err)
	}
	pending := pendingSubmission(record)
	if err := repository.UpdateSubmission(t.Context(), pending, record.Revision); err != nil {
		t.Fatal(err)
	}
	read, err := repository.GetSubmission(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(read.Prepared)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("prepared input changed: %v", err)
	}
	// Returned snapshots are owned, despite cache reuse on the next read.
	entry := &read.Prepared.Spec.Spec.Run.Snapshot.Entries[0]
	entry.CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{"api_key":"poison"}`)
	again, err := repository.GetSubmission(t.Context(), spec.JobID)
	if err != nil || again.State != jobs.SubmissionPending {
		t.Fatalf("cached read poisoned: %v", err)
	}
	if err := again.Validate(); err != nil {
		t.Fatalf("cached preparation was mutated: %v", err)
	}
}
