package sqlite_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func storePrioritySubmission(t *testing.T, repository *persistence.Repository, record jobs.SubmissionRecord, state string) {
	t.Helper()
	if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if state == "prepared" {
		return
	}
	record = pendingSubmission(record)
	if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
		t.Fatal(err)
	}
	if state == "accepted" {
		record = acceptedSubmission(t, record)
		if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteSubmissionPriorityPagesUseExactScopeAndBothDeliveryStates(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	wanted := []string{}
	for index, variant := range []string{"pending", "accepted", "prepared", "configuration", "node", "installation", "local"} {
		spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
		spec.JobID = submittedPageJobID(index + 1)
		if variant != "local" {
			spec = remoteSpec(spec)
		}
		record := submissionFixture(t, spec)
		switch variant {
		case "configuration":
			record.Prepared.ConfigurationID = submittedPageJobID(101)
		case "node":
			record.Prepared.Spec.Target.NodeID, record.Prepared.Runner.NodeID = submittedPageJobID(102), submittedPageJobID(102)
		case "installation":
			record.Prepared.Spec.NodeInstallationID = submittedPageJobID(103)
			record.Prepared.Runner.NodeInstallationID = submittedPageJobID(103)
		}
		var err error
		record, err = jobs.NewSubmission(record.Prepared, repositoryEpoch)
		if err != nil {
			t.Fatal(err)
		}
		state := "pending"
		if variant == "accepted" || variant == "prepared" {
			state = variant
		}
		storePrioritySubmission(t, repository, record, state)
		if variant == "pending" || variant == "accepted" {
			wanted = append(wanted, spec.JobID)
		}
	}
	page, err := repository.ListRemoteSubmissionIDs(t.Context(), remoteOrigin, "", 1)
	if err != nil || !slices.Equal(page.JobIDs, wanted[:1]) || !page.HasMore || page.NextCursor != wanted[0] {
		t.Fatalf("priority first page: %+v, %v", page, err)
	}
	page, err = repository.ListRemoteSubmissionIDs(t.Context(), remoteOrigin, page.NextCursor, 1)
	if err != nil || !slices.Equal(page.JobIDs, wanted[1:]) || page.HasMore || page.NextCursor != wanted[1] {
		t.Fatalf("priority tail: %+v, %v", page, err)
	}
	page, err = repository.ListRemoteSubmissionIDs(t.Context(), remoteOrigin, page.NextCursor, 1)
	if err != nil || page.JobIDs == nil || len(page.JobIDs) != 0 || page.HasMore || page.NextCursor != wanted[1] {
		t.Fatal("empty priority tail changed its keyset cursor")
	}
}

func TestRemoteSubmissionPriorityRejectsColumnDocumentMismatchWithoutMutation(t *testing.T) {
	for _, column := range []string{"configuration", "node", "installation", "job", "contract", "state", "digest",
		"revision", "oversized", "unsupported_root", "unsupported_prepared"} {
		t.Run(column, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			record := submissionFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
			storePrioritySubmission(t, repository, record, "pending")
			db := openDatabase(t, path)
			defer db.Close()
			jsonPath, value := "$.prepared.configuration_id", any(submittedPageJobID(200))
			switch column {
			case "node":
				jsonPath = "$.prepared.runner.node_id"
			case "installation":
				jsonPath = "$.prepared.spec.node_installation_id"
			case "job":
				jsonPath = "$.prepared.spec.job_id"
			case "contract":
				jsonPath, value = "$.prepared.spec.contract", 0
			case "state":
				jsonPath, value = "$.state", "prepared"
			case "digest":
				jsonPath, value = "$.spec_sha256", strings.Repeat("c", 64)
			case "revision":
				jsonPath, value = "$.revision", 100
			case "oversized":
				jsonPath, value = "$.unexpected", strings.Repeat("x", 9<<20)
			case "unsupported_root":
				jsonPath, value = "$.schema_version", 0
			case "unsupported_prepared":
				jsonPath, value = "$.prepared.schema_version", 0
			}
			if _, err := db.ExecContext(t.Context(), `UPDATE job_submissions SET document_json = json_set(document_json, ?, ?)`,
				jsonPath, value); err != nil {
				t.Fatal(err)
			}
			var before, after string
			if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.ListRemoteSubmissionIDs(t.Context(), remoteOrigin, "", 2); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt priority owner or format accepted: %v", err)
			}
			if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&after); err != nil || before != after {
				t.Fatal("rejected priority hint mutated persisted input")
			}
		})
	}
}

func TestRemoteSubmissionPriorityValidatesPageAndCancellation(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	for _, invalid := range []struct {
		origin remoteresults.Origin
		after  string
		limit  uint32
	}{
		{origin: remoteresults.Origin{}, limit: 2},
		{origin: remoteOrigin, after: "not-an-id", limit: 2},
		{origin: remoteOrigin, limit: 0},
		{origin: remoteOrigin, limit: 26},
	} {
		if _, err := repository.ListRemoteSubmissionIDs(t.Context(), invalid.origin, invalid.after, invalid.limit); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal("invalid priority page accepted")
		}
	}
	if _, err := repository.ListRemoteSubmissionIDs(nil, remoteOrigin, "", 2); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListRemoteSubmissionIDs(ctx, remoteOrigin, "", 2); !errors.Is(err, context.Canceled) {
		t.Fatal("priority query did not propagate cancellation")
	}
}

func TestRemoteSubmissionPriorityColumnCorruptionCannotPromoteAnotherScope(t *testing.T) {
	for _, field := range []string{"configuration_id", "node_id", "installation_id"} {
		t.Run(field, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			record := submissionFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
			storePrioritySubmission(t, repository, record, "pending")
			db := openDatabase(t, path)
			defer db.Close()
			origin, replacement := remoteOrigin, submittedPageJobID(201)
			// The column is from this fixed test enum, never external input.
			if _, err := db.ExecContext(t.Context(), "UPDATE job_submissions SET "+field+" = ?", replacement); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "configuration_id":
				origin.ConfigurationID = replacement
			case "node_id":
				origin.NodeID = replacement
			case "installation_id":
				origin.InstallationID = replacement
			}
			if _, err := repository.ListRemoteSubmissionIDs(t.Context(), origin, "", 2); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatal("corrupted owner columns promoted a foreign submission to priority")
			}
		})
	}
}
