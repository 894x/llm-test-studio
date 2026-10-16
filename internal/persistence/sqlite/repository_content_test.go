package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func contentStoreFixture() jobcontent.Store {
	return jobcontent.Store{
		ID: "88000000-0000-4000-8000-000000000001", Kind: jobcontent.StoreKindExecutor,
		InstallationID: "88000000-0000-4000-8000-000000000002", CreatedAt: repositoryEpoch,
	}
}

func publicationFixture(store jobcontent.Store) jobcontent.Publication {
	return jobcontent.Publication{
		Object: jobcontent.ObjectIdentity{
			StoreID: store.ID, Content: jobcontract.ContentIdentity{SHA256: strings.Repeat("a", 64), Size: 31},
			IncarnationID: "88000000-0000-4000-8000-000000000003",
			WitnessSHA256: strings.Repeat("b", 64), ChangeToken: strings.Repeat("c", 64),
		}, At: repositoryEpoch.Add(time.Second),
	}
}

func TestContentPublicationRegistersExactStoreAndReusesUnclaimedGeneration(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	store := contentStoreFixture()
	if err := repository.RegisterContentStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if err := repository.RegisterContentStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	changed := store
	changed.InstallationID = "88000000-0000-4000-8000-000000000004"
	if err := repository.RegisterContentStore(t.Context(), changed); !errors.Is(err, jobcontent.ErrConflict) {
		t.Fatalf("changed owner registered: %v", err)
	}
	publication := publicationFixture(store)
	first, created, err := repository.PublishContent(t.Context(), publication)
	if err != nil || !created || first.Number != 1 {
		t.Fatalf(
			"first generation=%+v created=%v err=%v",
			first,
			created,
			err,
		)
	}
	publication.At = publication.At.Add(time.Second)
	retry, created, err := repository.PublishContent(t.Context(), publication)
	if err != nil || created || !reflect.DeepEqual(first, retry) {
		t.Fatalf(
			"unclaimed retry changed generation=%+v created=%v err=%v",
			retry,
			created,
			err,
		)
	}
	stored, err := repository.ContentPublication(t.Context(), store.ID, publication.Object.Content.SHA256)
	if err != nil || !reflect.DeepEqual(first, stored) {
		t.Fatalf("stored publication=%+v err=%v", stored, err)
	}
}

func TestContentPublicationRejectsChangedPhysicalIdentityWithoutMutation(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	store := contentStoreFixture()
	if err := repository.RegisterContentStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	publication := publicationFixture(store)
	first, _, err := repository.PublishContent(t.Context(), publication)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*jobcontent.Publication){
		func(value *jobcontent.Publication) { value.Object.Content.Size++ },
		func(value *jobcontent.Publication) {
			value.Object.IncarnationID = "88000000-0000-4000-8000-000000000004"
		},
		func(value *jobcontent.Publication) { value.Object.WitnessSHA256 = strings.Repeat("d", 64) },
		func(value *jobcontent.Publication) { value.Object.ChangeToken = strings.Repeat("d", 64) },
	} {
		changed := publication
		change(&changed)
		if _, _, err := repository.PublishContent(t.Context(), changed); !errors.Is(err, jobcontent.ErrConflict) {
			t.Fatalf("changed object accepted: %v", err)
		}
		stored, err := repository.ContentPublication(t.Context(), store.ID, publication.Object.Content.SHA256)
		if err != nil || !reflect.DeepEqual(first, stored) {
			t.Fatalf("rejected publication changed facts: %+v %v", stored, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := repository.PublishContent(ctx, publication); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	if _, err := repository.ContentPublication(t.Context(), store.ID, publication.Object.Content.SHA256); err != nil {
		t.Fatalf("single connection leaked after cancellation: %v", err)
	}
}

func TestContentPublicationConcurrentRetriesCreateOneGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "content.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "content-test"}); err != nil {
		t.Fatal(err)
	}
	first := reopenHardeningRepository(t, path)
	defer first.Close()
	second := reopenHardeningRepository(t, path)
	defer second.Close()
	store := contentStoreFixture()
	if err := first.RegisterContentStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repositories := []*persistence.Repository{first, second}
	var group sync.WaitGroup
	var createdCount atomic.Int64
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			repository := repositories[index%len(repositories)]
			generation, created, err := repository.PublishContent(t.Context(), publicationFixture(store))
			if err != nil || generation.Number != 1 {
				t.Errorf("concurrent publication=%+v err=%v", generation, err)
			}
			if created {
				createdCount.Add(1)
			}
		}()
	}
	group.Wait()
	if createdCount.Load() != 1 {
		t.Fatalf("created %d generations for one unclaimed upload", createdCount.Load())
	}
}

func TestJobAdmissionCreatesEmptyContentInventoryAndRejectsUnpinnedClaims(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	input := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	input.ContentClaims = nil
	if _, _, err := repository.AcceptJob(t.Context(), input); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatalf("missing claims accepted: %v", err)
	}
	input.ContentClaims = []jobcontent.Claim{{}}
	if _, _, err := repository.AcceptJob(t.Context(), input); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatalf("metadata-only claims accepted: %v", err)
	}
	input.ContentClaims = []jobcontent.Claim{}
	accepted, created, err := repository.AcceptJob(t.Context(), input)
	if err != nil || !created {
		t.Fatalf("empty admission created=%v err=%v", created, err)
	}
	inventory, err := repository.JobContent(t.Context(), input.Spec.JobID)
	validHeader := inventory.Revision == 1 && inventory.SealedManifestSHA256 == nil
	emptyEntries := inventory.Entries != nil && len(inventory.Entries) == 0
	if err != nil || !validHeader || !emptyEntries {
		t.Fatalf("empty inventory=%+v err=%v", inventory, err)
	}
	input.ContentClaims = nil
	if _, _, err := repository.AcceptJob(t.Context(), input); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatalf("existing job accepted missing claims: %v", err)
	}
	input.ContentClaims = []jobcontent.Claim{{}}
	if _, _, err := repository.AcceptJob(t.Context(), input); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatalf("existing job accepted unpinned claims: %v", err)
	}
	input.ContentClaims = []jobcontent.Claim{}
	for _, invalid := range invalidAdmissionMetadata() {
		t.Run("replay "+invalid.name, func(t *testing.T) {
			retry := input
			invalid.change(&retry)
			replayed, created, err := repository.AcceptJob(t.Context(), retry)
			if err != nil || created || !reflect.DeepEqual(accepted, replayed) {
				t.Fatalf("existing retry reran admission: created=%v err=%v", created, err)
			}
		})
	}
	page, err := repository.JobEvents(t.Context(), input.Spec.JobID, 0, 10)
	if err != nil || len(page.Events) != 1 || page.LastSequence != 1 {
		t.Fatalf("invalid retry rewrote admission journal: %+v %v", page, err)
	}
}

func TestFreshJobAdmissionPreparationFailureDoesNotWriteAnyJobObjects(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison} {
		for _, invalid := range invalidAdmissionMetadata() {
			t.Run(string(kind)+" "+invalid.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "invalid-admission.db")
				options := persistence.MigrateOptions{AppVersion: "content-test"}
				if err := persistence.Migrate(t.Context(), path, options); err != nil {
					t.Fatal(err)
				}
				repository := reopenHardeningRepository(t, path)
				defer repository.Close()
				db := openDatabase(t, path)
				defer db.Close()
				spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
				if kind == jobcontract.KindComparison {
					spec = comparisonJobSpec(t)
				}
				input := jobAcceptance(spec)
				invalid.change(&input)
				if _, created, err := repository.AcceptJob(t.Context(), input); err == nil || created {
					t.Fatalf("invalid fresh admission created=%v err=%v", created, err)
				}
				for _, table := range []string{
					"execution_jobs", "execution_runs", "execution_run_revisions", "execution_job_runs",
					"comparisons", "comparison_revisions", "comparison_runs", "execution_job_events",
					"execution_job_content",
				} {
					if count := queryInt(t, db, `SELECT COUNT(*) FROM `+table); count != 0 {
						t.Fatalf("failed preparation left %s=%d", table, count)
					}
				}
				if count := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_sequence WHERE name = 'execution_jobs'`); count != 0 {
					t.Fatal("failed preparation allocated a job sequence")
				}
			})
		}
	}
}

func invalidAdmissionMetadata() []struct {
	name   string
	change func(*jobhost.Acceptance)
} {
	return []struct {
		name   string
		change func(*jobhost.Acceptance)
	}{
		{name: "invalid origin", change: func(input *jobhost.Acceptance) { input.Origin.RunnerVersion = "" }},
		{name: "zero acceptance time", change: func(input *jobhost.Acceptance) { input.AcceptedAt = time.Time{} }},
		{name: "invalid origin and time", change: func(input *jobhost.Acceptance) {
			input.Origin.RunnerVersion = ""
			input.AcceptedAt = time.Time{}
		}},
	}
}

func TestContentPublicationAndInventoryRollbackWithTheirRelations(t *testing.T) {
	repository, db, snapshot := openJournalFixture(t)
	store := contentStoreFixture()
	if err := repository.RegisterContentStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_content_head BEFORE INSERT ON content_heads
		BEGIN SELECT RAISE(ABORT, 'injected content head failure'); END`); err != nil {
		t.Fatal(err)
	}
	publication := publicationFixture(store)
	if _, _, err := repository.PublishContent(t.Context(), publication); err == nil {
		t.Fatal("publication with missing head committed")
	}
	for _, table := range []string{"content_objects", "content_generations", "content_heads"} {
		if count := queryInt(t, db, `SELECT COUNT(*) FROM `+table); count != 0 {
			t.Fatalf("failed publication left %s=%d", table, count)
		}
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER reject_content_head`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishContent(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_content_inventory
		BEFORE INSERT ON execution_job_content
		BEGIN SELECT RAISE(ABORT, 'injected content inventory failure'); END`); err != nil {
		t.Fatal(err)
	}
	second := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	second.Spec.JobID = "88000000-0000-4000-8000-000000000005"
	second.Spec.Spec.Run.Meta.ID = "88000000-0000-4000-8000-000000000006"
	if _, _, err := repository.AcceptJob(t.Context(), second); err == nil {
		t.Fatal("job with missing inventory committed")
	}
	for _, table := range []string{
		"execution_jobs", "execution_job_content", "execution_job_events", "execution_job_runs",
	} {
		if count := queryInt(t, db, `SELECT COUNT(*) FROM `+table); count != 1 {
			t.Fatalf("failed admission changed %s=%d", table, count)
		}
	}
	if _, err := repository.JobContent(t.Context(), snapshot.Receipt.JobID); err != nil {
		t.Fatalf("rollback damaged original inventory: %v", err)
	}
}

func TestContentCurrentRelationsRejectTamperingInGettersAndBackupWithoutMutation(t *testing.T) {
	for _, test := range []struct{ name, query string }{
		{name: "missing inventory", query: `DELETE FROM execution_job_content`},
		{name: "advanced unsealed inventory", query: `UPDATE execution_job_content SET inventory_revision = 2`},
		{name: "store owner", query: `UPDATE content_stores SET owner_installation_id = 'not-an-installation'`},
		{name: "missing head", query: `DELETE FROM content_heads`},
		{name: "wrong witness", query: `UPDATE content_generations SET witness_sha256 = '` + strings.Repeat("e", 64) + `'`},
		{name: "unsupported future manifest", query: `INSERT INTO job_content_manifests(
			job_id, sha256, size, inventory_revision, terminal_revision, sealed_sequence, created_at, document_json)
			SELECT job_id, '` + strings.Repeat("e", 64) + `', 2, 1, 1, 1, '2026-01-01T00:00:00Z', '{}'
			FROM execution_job_content`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "content.db")
			options := persistence.MigrateOptions{AppVersion: "content-test"}
			if err := persistence.Migrate(t.Context(), path, options); err != nil {
				t.Fatal(err)
			}
			repository := reopenHardeningRepository(t, path)
			defer repository.Close()
			db := openDatabase(t, path)
			defer db.Close()
			acceptance := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
			if _, _, err := repository.AcceptJob(t.Context(), acceptance); err != nil {
				t.Fatal(err)
			}
			store := contentStoreFixture()
			if err := repository.RegisterContentStore(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if _, _, err := repository.PublishContent(t.Context(), publicationFixture(store)); err != nil {
				t.Fatal(err)
			}
			if _, err := persistence.VerifyRunnerDatabase(t.Context(), path); err != nil {
				t.Fatalf("valid current publication failed verification: %v", err)
			}
			if _, err := db.ExecContext(t.Context(), test.query); err != nil {
				t.Fatal(err)
			}
			before := directoryHashes(t, filepath.Dir(path))
			if test.name == "unsupported future manifest" {
				if err := repository.RegisterContentStore(t.Context(), store); !errors.Is(err, jobcontent.ErrCorrupt) {
					t.Fatalf("registration accepted inactive lifecycle facts: %v", err)
				}
				publication := publicationFixture(store)
				_, _, publishErr := repository.PublishContent(t.Context(), publication)
				if !errors.Is(publishErr, jobcontent.ErrCorrupt) {
					t.Fatalf("publication accepted inactive lifecycle facts: %v", publishErr)
				}
				_, readErr := repository.ContentPublication(t.Context(), store.ID, publication.Object.Content.SHA256)
				if !errors.Is(readErr, jobcontent.ErrCorrupt) {
					t.Fatalf("publication getter accepted inactive lifecycle facts: %v", readErr)
				}
			}
			if test.name == "unsupported future manifest" {
				if err := repository.CheckWritable(t.Context()); !errors.Is(err, persistence.ErrCorrupt) {
					t.Fatalf("health accepted inactive lifecycle facts: %v", err)
				}
			} else if err := repository.CheckWritable(t.Context()); err != nil {
				t.Fatalf("light writer probe scanned unrelated history: %v", err)
			}
			var readErr error
			switch test.name {
			case "missing inventory", "advanced unsealed inventory", "unsupported future manifest":
				_, readErr = repository.JobContent(t.Context(), "72000000-0000-4000-8000-000000000001")
			default:
				_, readErr = repository.ContentPublication(t.Context(), store.ID, publicationFixture(store).Object.Content.SHA256)
			}
			if !errors.Is(readErr, persistence.ErrCorrupt) {
				t.Fatalf("getter accepted corrupt relation: %v", readErr)
			}
			if _, err := persistence.VerifyRunnerDatabase(t.Context(), path); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("backup validator accepted bad relation: %v", err)
			}
			if !reflect.DeepEqual(before, directoryHashes(t, filepath.Dir(path))) {
				t.Fatal("rejecting corrupt facts mutated database files")
			}
			if reopened, err := persistence.OpenRepository(t.Context(), path, persistence.RepositoryOptions{}); err == nil {
				_ = reopened.Close()
				t.Fatal("open accepted corrupt content relations")
			} else if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("open lost corrupt relation: %v", err)
			}
		})
	}
}

func TestContentCurrentSchemaHasAllRelationsAndRejectsMissingForeignOwners(t *testing.T) {
	repository, db, _ := openJournalFixture(t)
	connection, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(t.Context(), `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if err := repository.CheckWritable(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"content_stores", "content_objects", "content_generations", "content_heads", "execution_job_content",
		"job_content_entries", "job_content_manifests", "job_content_manifest_items", "job_content_hold_attestations",
		"job_content_releases", "job_content_release_items", "content_deletion_intents", "remote_job_content_manifests",
		"client_job_content_receipts", "client_job_content_receipt_items", "client_job_content_release_outbox",
	} {
		if !tableExists(t, db, table) {
			t.Fatalf("missing current table %s", table)
		}
		rows, err := db.QueryContext(t.Context(), `PRAGMA foreign_key_list("`+table+`")`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, sequence int
			var parent, from, to, onUpdate, onDelete, match string
			if err := rows.Scan(
				&id,
				&sequence,
				&parent,
				&from,
				&to,
				&onUpdate,
				&onDelete,
				&match,
			); err != nil {
				t.Fatal(err)
			}
			if onDelete != "RESTRICT" {
				t.Fatalf(
					"%s foreign owner %s allows %s",
					table,
					parent,
					onDelete,
				)
			}
		}
		iterationErr, closeErr := rows.Err(), rows.Close()
		if iterationErr != nil || closeErr != nil {
			t.Fatalf("FK rows: %v %v", iterationErr, closeErr)
		}
	}
	missing := "88000000-0000-4000-8000-000000000099"
	for _, query := range []string{
		`INSERT INTO execution_job_content(job_id, inventory_revision) VALUES('` + missing + `', 1)`,
		`INSERT INTO content_heads(store_id, sha256, generation, revision)
			VALUES('` + missing + `', '` + strings.Repeat("a", 64) + `', 1, 1)`,
		fmt.Sprintf(
			`INSERT INTO client_job_content_release_outbox(command_id, receipt_id, target_json,
			request_sha256, request_json, revision, delivery_state) VALUES('%s', '%s', '{}', '%s', '{}', 1, 'pending')`,
			missing,
			missing,
			strings.Repeat("a", 64),
		),
	} {
		if _, err := connection.ExecContext(t.Context(), query); err == nil {
			t.Fatal("missing foreign owner was accepted")
		}
	}
}
