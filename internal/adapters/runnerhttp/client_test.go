package runnerhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func httpManagementClient(t *testing.T, fixture *httpFixture) *runnerclient.Client {
	t.Helper()
	identity := fixture.host.Identity()
	client, err := runnerclient.New(runnerclient.Identity{
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID,
	}, func(ctx context.Context, input remotessh.Request) (*http.Response, error) {
		request, err := http.NewRequestWithContext(
			ctx,
			input.Method,
			fixture.api.URL+input.Route,
			input.Body,
		)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Accept-Encoding", "identity")
		if input.JSONGzip {
			request.Header.Set("Accept-Encoding", "gzip")
		}
		if input.Body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if input.RangeStart != nil {
			request.Header.Set("Range", "bytes="+strconv.FormatInt(*input.RangeStart, 10)+"-")
		}
		return fixture.api.Client().Do(request)
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestManagementClientRunsAllKindsAndDownloadsOriginalReportsWithResume(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison, jobcontract.KindPerformance} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newHTTPFixture(t, false, capabilities())
			client := httpManagementClient(t, fixture)
			if _, err := client.Probe(t.Context()); err != nil {
				t.Fatal(err)
			}
			submission := fixture.runSubmission(t, kind == jobcontract.KindComparison)
			if kind == jobcontract.KindPerformance {
				submission = fixture.submission(t, nil)
			}
			ctx, cancel := context.WithCancel(t.Context())
			receipt, err := client.Submit(ctx, &submission)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := client.Submit(t.Context(), &submission)
			if err != nil || replayed.JobID != receipt.JobID {
				t.Fatal("same-job retry did not return the original identity")
			}
			final := waitHTTPJob(
				t,
				fixture,
				receipt.JobID,
				domain.RunCompleted,
			)
			otherClient := httpManagementClient(t, fixture)
			observed, err := otherClient.GetJob(t.Context(), receipt.JobID)
			if err != nil || !reflect.DeepEqual(observed.Receipt, final.Receipt) {
				t.Fatal("another client did not observe the same accepted task")
			}
			page, err := client.ListJobs(t.Context(), 0, 1)
			if err != nil || len(page.Jobs) != 1 || page.Jobs[0].Receipt.JobID != receipt.JobID {
				t.Fatal("management list did not return the shared job")
			}
			after, _ := strconv.ParseUint(page.NextCursor, 10, 63)
			empty, err := client.ListJobs(t.Context(), after, 1)
			if err != nil || len(empty.Jobs) != 0 || empty.NextCursor != page.NextCursor {
				t.Fatal("empty job page did not preserve the keyset cursor")
			}
			var cursor uint64
			references := []jobcontract.ReportReference{}
			expectedReports := len(receipt.RunIDs)
			if kind == jobcontract.KindPerformance {
				expectedReports = 1
			}
			observeCtx, stop := context.WithTimeout(t.Context(), 10*time.Second)
			defer stop()
			for len(references) < expectedReports {
				page, err := client.ReadEvents(observeCtx, runnerclient.EventQuery{
					JobID: receipt.JobID, AfterSequence: cursor, Limit: 3, Wait: time.Second,
				})
				if err != nil {
					t.Fatal(err)
				}
				cursor = page.LastSequence
				for _, event := range page.Events {
					switch event.Kind {
					case jobcontract.EventReport:
						if kind == jobcontract.KindPerformance {
							continue
						}
						var reference jobcontract.ReportReference
						if err := json.Unmarshal(event.Payload, &reference); err != nil {
							t.Fatal(err)
						}
						references = append(references, reference)
					case jobcontract.EventPerformanceResult:
						var reference jobcontract.PerformanceResultReference
						if err := json.Unmarshal(event.Payload, &reference); err != nil {
							t.Fatal(err)
						}
						references = append(references, jobcontract.ReportReference{
							ID: reference.ReportID, Kind: jobcontract.KindPerformance,
							SHA256: reference.SHA256, Size: reference.Size,
						})
					}
				}
			}
			for _, reference := range references {
				var original []byte
				if kind == jobcontract.KindPerformance {
					stored, err := fixture.repository.ReadPerformanceResult(t.Context(), receipt.JobID)
					if err != nil {
						t.Fatal(err)
					}
					original = stored.Document
				} else {
					stored, err := fixture.repository.ReadRunReport(t.Context(), receipt.JobID, reference.ID)
					if err != nil {
						t.Fatal(err)
					}
					original = stored.Document
				}
				path := filepath.Join(t.TempDir(), "partial-report")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if _, err := file.Write(original[:23]); err != nil {
					t.Fatal(err)
				}
				if err := client.DownloadReport(
					t.Context(),
					receipt.JobID,
					reference,
					file,
				); err != nil {
					t.Fatal(err)
				}
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, original) {
					t.Fatal("resumed report differs from its canonical stored bytes")
				}
			}
		})
	}
}

func TestManagementClientCommandsKeepStableConfirmationsAcrossClients(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	client := httpManagementClient(t, fixture)
	active := fixture.submission(t, nil)
	if _, err := client.Submit(t.Context(), &active); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not start")
	}
	queued := fixture.submission(t, nil)
	if _, err := client.Submit(t.Context(), &queued); err != nil {
		t.Fatal(err)
	}
	command := jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: httpMeta(t).ID,
		JobID: queued.Job.JobID, Kind: jobcontract.CommandStopSending, ExpectedRevision: 1}
	confirmed, err := client.Command(t.Context(), command)
	if err != nil || confirmed.Snapshot.Receipt.Status != domain.RunCancelled {
		t.Fatal("queued stop did not persist cancellation")
	}
	replayed, err := httpManagementClient(t, fixture).Command(t.Context(), command)
	if err != nil || !reflect.DeepEqual(confirmed, replayed) {
		t.Fatal("command retry changed the durable confirmation")
	}
	state, err := client.GetJob(t.Context(), active.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Command(t.Context(), jobcontract.Command{Contract: jobcontract.CurrentContract,
		CommandID: httpMeta(t).ID, JobID: active.Job.JobID, Kind: jobcontract.CommandCancel,
		ExpectedRevision: state.Receipt.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.requests.Load() != 1 {
		t.Fatal("queued cancellation emitted provider requests")
	}
}
