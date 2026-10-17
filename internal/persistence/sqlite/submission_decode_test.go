package sqlite_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestSubmissionDecoderRejectsDuplicateAliasesAndHugeDocumentsWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "outbox-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	db := openDatabase(t, path)
	defer db.Close()
	record, err := repository.CreateSubmission(t.Context(), submissionFixture(t,
		remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))))
	if err != nil {
		t.Fatal(err)
	}
	var original []byte
	id := record.Prepared.Spec.JobID
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions WHERE job_id = ?`, id).
		Scan(&original); err != nil {
		t.Fatal(err)
	}
	unchecked := `{"spec":{"comparison":{"runs":[` + strings.Repeat(`null,`, 32768) + `null]}}}`
	for name, document := range map[string][]byte{
		"duplicate prepared": bytes.Replace(
			original,
			[]byte(`"prepared":`),
			[]byte(`"prepared":{"spec":`+unchecked+`},"prepared":`),
			1,
		),
		"duplicate spec": bytes.Replace(
			original,
			[]byte(`"spec":`),
			[]byte(`"spec":`+unchecked+`,"spec":`),
			1,
		),
		"prepared case alias": bytes.Replace(
			original,
			[]byte(`"prepared":`),
			[]byte(`"Prepared":`),
			1,
		),
		"duplicate receipt": bytes.Replace(
			original,
			[]byte(`"receipt":`),
			[]byte(`"receipt":{},"receipt":`),
			1,
		),
		"null required observation": bytes.Replace(
			original,
			[]byte(`"observation":""`),
			[]byte(`"observation":null`),
			1,
		),
		"oversized document": []byte(`{"padding":"` + strings.Repeat("p", 9<<20) + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(original, document) {
				t.Fatal("corruption fixture did not change the document")
			}
			if _, err := db.ExecContext(
				t.Context(),
				`UPDATE job_submissions SET document_json = ? WHERE job_id = ?`,
				document,
				id,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.GetSubmission(t.Context(), id); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatal("corrupt outbox was accepted")
			}
			if _, err := repository.ListPendingSubmissions(t.Context(), "", 25); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatal("pending listing accepted a corrupt outbox")
			}
			var retained []byte
			if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions WHERE job_id = ?`, id).
				Scan(&retained); err != nil || !bytes.Equal(document, retained) {
				t.Fatal("rejected outbox document was changed")
			}
		})
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE job_submissions SET document_json = ?, node_id = ? WHERE job_id = ?`,
		original,
		remoteOrigin.ConfigurationID,
		id,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetSubmission(t.Context(), id); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatal("stored owner columns were not checked against the frozen executor")
	}
}
