package sqlite_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
)

// These fixtures exercise DDL relationships only. The inactive lifecycle rows
// deliberately remain unavailable to every current runtime writer and reader.
func TestContentDDLDeletionLinksRequireSamePhysicalIdentityAcrossGenerations(t *testing.T) {
	for _, kind := range []string{
		"different hash", "different incarnation", "different witness", "same physical next generation",
	} {
		t.Run(kind, func(t *testing.T) {
			repository, db, first := openJournalFixture(t)
			second := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
			second.Spec.JobID = contentDDLID(2)
			second.Spec.Spec.Run.Meta.ID = contentDDLID(3)
			if _, _, err := repository.AcceptJob(t.Context(), second); err != nil {
				t.Fatal(err)
			}
			store := contentStoreFixture()
			if err := repository.RegisterContentStore(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			firstPublication := publicationFixture(store)
			if _, _, err := repository.PublishContent(t.Context(), firstPublication); err != nil {
				t.Fatal(err)
			}
			other := firstPublication.Object
			generation := 2
			switch kind {
			case "different hash":
				other.Content.SHA256 = strings.Repeat("d", 64)
				other.IncarnationID = contentDDLID(4)
				generation = 1
				if _, _, err := repository.PublishContent(t.Context(), jobcontent.Publication{
					Object: other, At: firstPublication.At,
				}); err != nil {
					t.Fatal(err)
				}
			case "different incarnation":
				other.IncarnationID = contentDDLID(4)
			case "different witness":
				other.WitnessSHA256 = strings.Repeat("d", 64)
			}
			connection, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if _, err := connection.ExecContext(t.Context(), `PRAGMA foreign_keys = ON`); err != nil {
				t.Fatal(err)
			}
			tx, err := connection.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if generation == 2 {
				contentDDLExec(
					t,
					tx,
					`INSERT INTO content_generations(store_id, sha256, generation, size,
					physical_incarnation_id, witness_sha256, created_at) VALUES(?, ?, 2, ?, ?, ?, ?)`,
					other.StoreID,
					other.Content.SHA256,
					other.Content.Size,
					other.IncarnationID,
					other.WitnessSHA256,
					firstPublication.At,
				)
			}
			contentDDLRelease(
				t,
				tx,
				first.Receipt.JobID,
				10,
				firstPublication.Object,
				1,
			)
			contentDDLRelease(
				t,
				tx,
				second.Spec.JobID,
				20,
				other,
				generation,
			)
			contentDDLExec(
				t,
				tx,
				`INSERT INTO content_deletion_intents(deletion_id, release_command_id,
				grant_item_id, store_id, sha256, size, expected_generation, expected_incarnation, witness_sha256,
				pre_change_token, step, revision) VALUES(?, ?, ?, ?, ?, ?, 1, ?, ?, ?, 'prepared', 1)`,
				contentDDLID(30),
				contentDDLID(12),
				contentDDLID(10),
				store.ID,
				firstPublication.Object.Content.SHA256,
				firstPublication.Object.Content.Size,
				firstPublication.Object.IncarnationID,
				firstPublication.Object.WitnessSHA256,
				firstPublication.Object.ChangeToken,
			)
			_, err = tx.ExecContext(
				t.Context(),
				`UPDATE job_content_release_items SET deletion_id = ?
				WHERE command_id = ? AND item_id = ?`,
				contentDDLID(30),
				contentDDLID(22),
				contentDDLID(20),
			)
			if kind == "same physical next generation" {
				if err != nil {
					t.Fatalf("same physical generation cleanup was rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("release item linked another physical content deletion")
			}
		})
	}
}

func contentDDLRelease(
	t *testing.T,
	tx *sql.Tx,
	jobID string,
	base int,
	object jobcontent.ObjectIdentity,
	generation int,
) {
	t.Helper()
	manifestSHA := strings.Repeat("e", 64)
	contentDDLExec(
		t,
		tx,
		`INSERT INTO job_content_entries(id, job_id, role, ref, store_id, sha256,
		size, media_type, generation, state, linked_at) VALUES(?, ?, 'input', 'fixture', ?, ?, ?, 'image/png', ?, 'live', ?)`,
		contentDDLID(base),
		jobID,
		object.StoreID,
		object.Content.SHA256,
		object.Content.Size,
		generation,
		repositoryEpoch,
	)
	contentDDLExec(
		t,
		tx,
		`INSERT INTO job_content_manifests(job_id, sha256, size, inventory_revision,
		terminal_revision, sealed_sequence, created_at, document_json) VALUES(?, ?, 2, 1, 1, 1, ?, '{}')`,
		jobID,
		manifestSHA,
		repositoryEpoch,
	)
	contentDDLExec(
		t,
		tx,
		`INSERT INTO job_content_manifest_items(job_id, item_id, position, store_id, sha256, size, generation)
		VALUES(?, ?, 0, ?, ?, ?, ?)`,
		jobID,
		contentDDLID(base),
		object.StoreID,
		object.Content.SHA256,
		object.Content.Size,
		generation,
	)
	contentDDLExec(
		t,
		tx,
		`INSERT INTO job_content_hold_attestations(receipt_id, job_id, manifest_sha256,
		holder_id, received_at, request_sha256, document_json, auth_scope) VALUES(?, ?, ?, ?, ?, ?, '{}', 'node')`,
		contentDDLID(base+1),
		jobID,
		manifestSHA,
		contentDDLID(50),
		repositoryEpoch,
		strings.Repeat("f", 64),
	)
	contentDDLExec(
		t,
		tx,
		`INSERT INTO job_content_releases(command_id, job_id, manifest_sha256, hold_receipt_id,
		request_sha256, expected_revision, revision, state, started_at) VALUES(?, ?, ?, ?, ?, 1, 1, 'intent', ?)`,
		contentDDLID(base+2),
		jobID,
		manifestSHA,
		contentDDLID(base+1),
		strings.Repeat("f", 64),
		repositoryEpoch,
	)
	contentDDLExec(
		t,
		tx,
		`INSERT INTO job_content_release_items(command_id, item_id, job_id, store_id,
		sha256, size, expected_generation, expected_incarnation, witness_sha256, disposition, revision)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`,
		contentDDLID(base+2),
		contentDDLID(base),
		jobID,
		object.StoreID,
		object.Content.SHA256,
		object.Content.Size,
		generation,
		object.IncarnationID,
		object.WitnessSHA256,
	)
}

func contentDDLID(value int) string { return fmt.Sprintf("89000000-0000-4000-8000-%012d", value) }

func contentDDLExec(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
