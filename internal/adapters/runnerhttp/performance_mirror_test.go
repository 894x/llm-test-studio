package runnerhttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

func performanceMirrorStore(t *testing.T) (*sqlite.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desktop-mirror.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "performance-mirror"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository, path
}

func waitHTTPSettlement(t *testing.T, client *runnerclient.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		info, err := client.Probe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Pending == 0 {
			return
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("performance settlement did not finish")
		case <-timer.C:
		}
	}
}

func syncPerformanceMetadata(
	t *testing.T,
	client *runnerclient.Client,
	repository *sqlite.Repository,
	origin remoteresults.Origin,
	jobID string,
) []remoteresults.DocumentReference {
	t.Helper()
	var cursor uint64
	for {
		page, err := client.ReadEvents(t.Context(), runnerclient.EventQuery{JobID: jobID, AfterSequence: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
			Origin: origin, JobID: jobID, AfterSequence: cursor, Events: page.Events,
		}); err != nil {
			t.Fatal(err)
		}
		cursor = page.LastSequence
		if !page.HasMore {
			break
		}
	}
	documents, err := repository.ListRemoteDocuments(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	return documents
}

func TestRemotePerformanceDocumentsDownloadImportAndReadOffline(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*quicktest.PerformanceCommand)
	}{
		{name: "measured"},
		{name: "warmup and ramp", configure: func(command *quicktest.PerformanceCommand) {
			command.WarmupRequests, command.RampDurationMS, command.RampRequestCap = 1, 10, 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHTTPFixture(t, false, capabilities())
			client := httpManagementClient(t, fixture)
			submission := fixture.submission(t, test.configure)
			if _, err := client.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			waitHTTPJob(
				t,
				fixture,
				submission.Job.JobID,
				domain.RunCompleted,
			)
			waitHTTPSettlement(t, client)
			desktop, path := performanceMirrorStore(t)
			identity := fixture.host.Identity()
			origin := remoteresults.Origin{ConfigurationID: httpMeta(t).ID, NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
			documents := syncPerformanceMetadata(
				t,
				client,
				desktop,
				origin,
				submission.Job.JobID,
			)
			if len(documents) != 2 {
				t.Fatalf("performance documents=%d", len(documents))
			}
			payloads := map[jobcontract.EventKind][]byte{}
			for _, document := range documents {
				if document.Imported {
					t.Fatal("metadata marked a download complete")
				}
				var original []byte
				if document.Kind == jobcontract.EventPerformanceResult {
					sealed, err := fixture.repository.ReadPerformanceResult(t.Context(), submission.Job.JobID)
					if err != nil {
						t.Fatal(err)
					}
					original = sealed.Document
				} else {
					archive, err := fixture.repository.ReadPerformanceArchive(t.Context(), submission.Job.JobID, document.Reference.ID)
					if err != nil {
						t.Fatal(err)
					}
					original = archive.Document
				}
				filePath := filepath.Join(t.TempDir(), string(document.Kind)+".partial")
				file, err := os.Create(filePath)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if _, err := file.Write(original[:17]); err != nil {
					t.Fatal(err)
				}
				download := client.DownloadReport
				if document.Kind == jobcontract.EventReport {
					download = client.DownloadPerformanceArchive
				}
				if err := download(
					t.Context(),
					submission.Job.JobID,
					document.Reference,
					file,
				); err != nil {
					t.Fatal(err)
				}
				actual, err := os.ReadFile(filePath)
				if err != nil || !bytes.Equal(actual, original) {
					t.Fatal("download did not preserve original stored bytes")
				}
				payloads[document.Kind] = actual
			}
			if bytes.Equal(payloads[jobcontract.EventReport], payloads[jobcontract.EventPerformanceResult]) {
				t.Fatal("two document types collapsed to one")
			}
			input := remoteresults.PerformanceDocumentImport{Origin: origin, JobID: submission.Job.JobID,
				Kind: jobcontract.EventReport, Document: payloads[jobcontract.EventReport]}
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); !errors.Is(err, remoteresults.ErrNotFound) {
				t.Fatalf("archive imported without its sealed result: %v", err)
			}
			input.Kind, input.Document = jobcontract.EventPerformanceResult, payloads[jobcontract.EventPerformanceResult]
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			// A failed acknowledgement must roll back the report and list projection.
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			assertMirrorRejectsContradictoryResult(
				t,
				db,
				desktop,
				input,
				func(result *jobcontract.PerformanceResult) {
					result.SealedAt = result.SealedAt.Add(time.Minute)
				},
			)
			assertMirrorRejectsContradictoryResult(
				t,
				db,
				desktop,
				input,
				func(result *jobcontract.PerformanceResult) {
					result.Observation.Revision++
				},
			)
			if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_archive_import BEFORE UPDATE OF imported ON remote_job_report_refs
				WHEN NEW.reference_type = 'report' BEGIN SELECT RAISE(ABORT, 'test import failure'); END`); err != nil {
				t.Fatal(err)
			}
			input.Kind, input.Document = jobcontract.EventReport, payloads[jobcontract.EventReport]
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); err == nil {
				t.Fatal("archive committed without import marker")
			}
			if _, err := desktop.GetQuickPerformanceReport(t.Context(), submission.Job.Spec.Performance.ReportID); !errors.Is(err, quicktest.ErrPerformanceArchiveNotFound) {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_archive_import`); err != nil {
				t.Fatal(err)
			}
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []jobcontract.EventKind{jobcontract.EventPerformanceResult, jobcontract.EventReport} {
				input.Kind, input.Document = kind, append(bytes.Clone(payloads[kind]), ' ')
				if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); !errors.Is(err, remoteresults.ErrConflict) {
					t.Fatalf("changed bytes accepted: %v", err)
				}
			}
			requests := fixture.requests.Load()
			fixture.api.Close()
			fixture.upstream.Close()
			if err := desktop.Close(); err != nil {
				t.Fatal(err)
			}
			offline, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer offline.Close()
			result, err := offline.ReadRemotePerformanceResult(t.Context(), submission.Job.JobID)
			if err != nil {
				t.Fatal(err)
			}
			archive, err := offline.GetQuickPerformanceReport(t.Context(), result.ReportID)
			if err != nil {
				t.Fatal(err)
			}
			expected, eligible, err := result.StatisticalArchive()
			if err != nil || !eligible || !reflect.DeepEqual(expected, archive) {
				t.Fatal("offline metrics differ from sealed node measurements")
			}
			summaries, err := offline.ListQuickPerformanceReportSummaries(t.Context())
			if err != nil || len(summaries) != 1 || summaries[0].ReportID != archive.ReportID {
				t.Fatalf("offline list missing imported archive: %v", err)
			}
			documents, err = offline.ListRemoteDocuments(t.Context(), submission.Job.JobID)
			if err != nil || len(documents) != 2 || !documents[0].Imported || !documents[1].Imported {
				t.Fatalf("import state not durable: %v", err)
			}
			if fixture.requests.Load() != requests {
				t.Fatal("download/import/offline read issued more provider traffic")
			}
			for _, mutation := range []string{
				`json_set(document_json, '$.sha256', 'invalid')`,
				`json_set(document_json, '$.kind', 'comparison')`,
				`json_set(document_json, '$.run_id', 'invalid')`,
				`json_set(document_json, '$.size', 999999999)`,
			} {
				var original []byte
				if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_job_report_refs
					WHERE job_id = ? AND reference_type = 'report'`, submission.Job.JobID).Scan(&original); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_report_refs SET document_json = `+mutation+`
					WHERE job_id = ? AND reference_type = 'report'`, submission.Job.JobID); err != nil {
					t.Fatal(err)
				}
				if _, err := offline.ListRemoteDocuments(t.Context(), submission.Job.JobID); !errors.Is(err, sqlite.ErrCorrupt) {
					t.Fatalf("invalid download plan returned: %v", err)
				}
				if _, err := db.ExecContext(
					t.Context(),
					`UPDATE remote_job_report_refs SET document_json = ?
					WHERE job_id = ? AND reference_type = 'report'`,
					original,
					submission.Job.JobID,
				); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(
				t.Context(),
				`UPDATE remote_performance_results SET report_id = ? WHERE job_id = ?`,
				httpMeta(t).ID,
				submission.Job.JobID,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := offline.ReadRemotePerformanceResult(t.Context(), submission.Job.JobID); !errors.Is(err, sqlite.ErrCorrupt) {
				t.Fatalf("invalid result scalar accepted: %v", err)
			}
			if _, err := db.ExecContext(
				t.Context(),
				`UPDATE remote_performance_results SET report_id = ? WHERE job_id = ?`,
				archive.ReportID,
				submission.Job.JobID,
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueuedPerformanceResultHasNoStatisticalArchive(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	client := httpManagementClient(t, fixture)
	active, queued := fixture.submission(t, nil), fixture.submission(t, nil)
	if _, err := client.Submit(t.Context(), &active); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("active fixture did not enter provider")
	}
	if _, err := client.Submit(t.Context(), &queued); err != nil {
		t.Fatal(err)
	}
	_, err := client.Command(t.Context(), jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: httpMeta(t).ID,
		JobID: queued.Job.JobID, Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	fixture.release()
	waitHTTPSettlement(t, client)
	desktop, path := performanceMirrorStore(t)
	identity := fixture.host.Identity()
	origin := remoteresults.Origin{ConfigurationID: httpMeta(t).ID, NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
	documents := syncPerformanceMetadata(
		t,
		client,
		desktop,
		origin,
		queued.Job.JobID,
	)
	if len(documents) != 1 || documents[0].Kind != jobcontract.EventPerformanceResult {
		t.Fatal("unstarted task advertised a statistical archive")
	}
	response := fixture.request(
		t,
		http.MethodGet,
		"/jobs/"+queued.Job.JobID+"/archives/"+queued.Job.Spec.Performance.ReportID,
		nil,
	)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unstarted archive response=%d", response.StatusCode)
	}
	sealed, err := fixture.repository.ReadPerformanceResult(t.Context(), queued.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
		Origin: origin, JobID: queued.Job.JobID, Kind: jobcontract.EventPerformanceResult, Document: sealed.Document,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := desktop.ReadRemotePerformanceResult(t.Context(), queued.Job.JobID)
	if err != nil || result.ExecutionStarted || result.Observation != nil {
		t.Fatalf("unstarted result fabricated measurements: %v", err)
	}
	if _, eligible, err := result.StatisticalArchive(); err != nil || eligible {
		t.Fatalf("unstarted result became statistical report: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertMirrorRejectsContradictoryResult(
		t,
		db,
		desktop,
		remoteresults.PerformanceDocumentImport{
			Origin: origin, JobID: queued.Job.JobID, Kind: jobcontract.EventPerformanceResult, Document: sealed.Document,
		},
		func(result *jobcontract.PerformanceResult) {
			result.ExecutionStarted = true
			result.ObservationState = jobcontract.PerformanceInterrupted
		},
	)
}

func canonicalMirrorJSON(t *testing.T, value any) []byte {
	t.Helper()
	var normalized any
	if err := json.Unmarshal(encode(t, value), &normalized); err != nil {
		t.Fatal(err)
	}
	return encode(t, normalized)
}

func assertMirrorRejectsContradictoryResult(
	t *testing.T,
	db *sql.DB,
	desktop *sqlite.Repository,
	input remoteresults.PerformanceDocumentImport,
	mutate func(*jobcontract.PerformanceResult),
) {
	t.Helper()
	originalDocument := bytes.Clone(input.Document)
	var result jobcontract.PerformanceResult
	if err := json.Unmarshal(input.Document, &result); err != nil {
		t.Fatal(err)
	}
	mutate(&result)
	changed, err := jobcontract.EncodePerformanceResult(result)
	if err != nil {
		t.Fatalf("relation test must use a structurally valid result: %v", err)
	}
	digest := sha256.Sum256(changed)
	reference := jobcontract.PerformanceResultReference{JobID: result.JobID, ReportID: result.ReportID,
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(changed))}
	var originalReference, originalEvent []byte
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_job_report_refs WHERE job_id = ?
		AND reference_type = 'performance_result'`, input.JobID).Scan(&originalReference); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_job_events WHERE job_id = ?
		AND kind = 'performance_result'`, input.JobID).Scan(&originalEvent); err != nil {
		t.Fatal(err)
	}
	var event jobcontract.Event
	if err := json.Unmarshal(originalEvent, &event); err != nil {
		t.Fatal(err)
	}
	event.Payload = canonicalMirrorJSON(t, reference)
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE remote_job_report_refs SET document_json = ? WHERE job_id = ?
		AND reference_type = 'performance_result'`,
		canonicalMirrorJSON(t, reference),
		input.JobID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE remote_job_events SET document_json = ? WHERE job_id = ?
		AND kind = 'performance_result'`,
		canonicalMirrorJSON(t, event),
		input.JobID,
	); err != nil {
		t.Fatal(err)
	}
	input.Document = changed
	if err := desktop.ImportRemotePerformanceDocument(t.Context(), input); err == nil {
		t.Fatal("valid digest concealed contradictory execution facts")
	}
	var retained []byte
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_performance_results WHERE job_id = ?`, input.JobID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained, originalDocument) {
		t.Fatal("rejected forged result overwrote original bytes")
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE remote_job_report_refs SET document_json = ? WHERE job_id = ?
		AND reference_type = 'performance_result'`,
		originalReference,
		input.JobID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE remote_job_events SET document_json = ? WHERE job_id = ?
		AND kind = 'performance_result'`,
		originalEvent,
		input.JobID,
	); err != nil {
		t.Fatal(err)
	}
}
