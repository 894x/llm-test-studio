package runnerhttp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	documentcache "github.com/894x/llm-test-studio/internal/remote/cache"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func syncHTTPClient(
	t *testing.T,
	fixture *httpFixture,
	observe func(remotessh.Request, *http.Response),
) *runnerclient.Client {
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
		if input.RangeStart != nil {
			request.Header.Set("Range", "bytes="+strconv.FormatInt(*input.RangeStart, 10)+"-")
		}
		response, err := fixture.api.Client().Do(request)
		if err == nil {
			observe(input, response)
		}
		return response, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func syncDocuments(t *testing.T) (*documentcache.Cache, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cache, err := documentcache.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return cache, root
}

func TestSynchronizationObservesAndImportsAllKindsWithoutRedownloading(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison, jobcontract.KindPerformance} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newHTTPFixture(t, false, capabilities())
			management := httpManagementClient(t, fixture)
			submission := fixture.runSubmission(t, kind == jobcontract.KindComparison)
			if kind == jobcontract.KindPerformance {
				submission = fixture.submission(t, nil)
			}
			if _, err := management.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			waitHTTPJob(
				t,
				fixture,
				submission.Job.JobID,
				domain.RunCompleted,
			)
			waitHTTPSettlement(t, management)
			requests := fixture.requests.Load()
			desktop, _ := performanceMirrorStore(t)
			cache, _ := syncDocuments(t)
			synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: desktop, Documents: cache})
			if err != nil {
				t.Fatal(err)
			}
			downloads := 0
			client := syncHTTPClient(t, fixture, func(input remotessh.Request, _ *http.Response) {
				if strings.Contains(input.Route, "/reports/") || strings.Contains(input.Route, "/archives/") {
					downloads++
				}
			})
			identity := fixture.host.Identity()
			request := remotesync.Request{JobID: submission.Job.JobID, Remote: client,
				Origin: remoteresults.Origin{ConfigurationID: httpMeta(t).ID,
					NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}}
			outcome, err := synchronizer.Observe(t.Context(), request)
			if err != nil || outcome.MoreEvents || outcome.Mirror.Snapshot.Receipt.Status != domain.RunCompleted {
				t.Fatalf("synchronization did not reach node terminal: %v", err)
			}
			documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
			if err != nil || len(documents) == 0 || downloads != 0 {
				t.Fatalf("document download count=%d: %v", downloads, err)
			}
			for _, kind := range []jobcontract.EventKind{jobcontract.EventPerformanceResult, jobcontract.EventReport} {
				for _, document := range documents {
					if document.Kind != kind {
						continue
					}
					imported, err := synchronizer.ImportDocument(t.Context(), remotesync.DocumentRequest{
						Request: request, Document: document,
					})
					if err != nil || !imported.Imported {
						t.Fatalf("original document import failed: %v", err)
					}
				}
			}
			documents, err = desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
			if err != nil || downloads != len(documents) {
				t.Fatal("document imports did not match their original downloads")
			}
			for _, document := range documents {
				if !document.Imported {
					t.Fatal("sync returned before document import")
				}
				if _, err := cache.Read(t.Context(), document.Reference, nil); err != nil {
					t.Fatal("imported document has no verified offline cache")
				}
			}
			sequence := outcome.Mirror.Snapshot.LatestSequence
			outcome, err = synchronizer.Observe(t.Context(), request)
			if err != nil || outcome.Mirror.Snapshot.LatestSequence != sequence || downloads != len(documents) {
				t.Fatalf("replay redownloaded or changed the mirror: %v", err)
			}
			for _, document := range documents {
				if _, err := synchronizer.ImportDocument(t.Context(), remotesync.DocumentRequest{
					Request: request, Document: document,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if downloads != len(documents) {
				t.Fatal("an imported durable reference downloaded again")
			}
			if fixture.requests.Load() != requests {
				t.Fatal("synchronization repeated provider traffic")
			}
		})
	}
}

type truncatedBody struct {
	io.Reader
	io.Closer
}

func TestSynchronizationResumesTruncatedArchiveAfterDatabaseAndCacheReopen(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	management := httpManagementClient(t, fixture)
	submission := fixture.submission(t, nil)
	if _, err := management.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, management)
	requests := fixture.requests.Load()
	desktop, databasePath := performanceMirrorStore(t)
	cache, root := syncDocuments(t)
	synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: desktop, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	truncated, archiveResumed, fullDownloads := false, false, 0
	client := syncHTTPClient(t, fixture, func(input remotessh.Request, response *http.Response) {
		if strings.Contains(input.Route, "/reports/") {
			fullDownloads++
		}
		if !strings.Contains(input.Route, "/archives/") {
			return
		}
		if !truncated {
			truncated = true
			response.Body = truncatedBody{Reader: io.LimitReader(response.Body, 17), Closer: response.Body}
			return
		}
		archiveResumed = input.RangeStart != nil && *input.RangeStart == 17
	})
	identity := fixture.host.Identity()
	request := remotesync.Request{JobID: submission.Job.JobID, Remote: client,
		Origin: remoteresults.Origin{ConfigurationID: httpMeta(t).ID,
			NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}}
	outcome, err := synchronizer.Observe(t.Context(), request)
	if err != nil || outcome.Mirror.Snapshot.Receipt.Status != domain.RunCompleted {
		t.Fatalf("observation did not retain the execution terminal: %v", err)
	}
	documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
	if err != nil || len(documents) != 2 {
		t.Fatal(err)
	}
	var archive remoteresults.DocumentReference
	for _, document := range documents {
		if document.Kind == jobcontract.EventReport {
			archive = document
			continue
		}
		if _, err := synchronizer.ImportDocument(t.Context(), remotesync.DocumentRequest{
			Request: request, Document: document,
		}); err != nil {
			t.Fatal(err)
		}
	}
	imported, err := synchronizer.ImportDocument(t.Context(), remotesync.DocumentRequest{
		Request: request, Document: archive,
	})
	if !errors.Is(err, runnerclient.ErrConnection) || imported.Imported {
		t.Fatalf("truncated archive was marked imported: %v", err)
	}
	documents, err = desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
	if err != nil || len(documents) != 2 {
		t.Fatal(err)
	}
	for _, document := range documents {
		if document.Imported != (document.Kind == jobcontract.EventPerformanceResult) {
			t.Fatal("archive transfer changed the durable full-result dependency state")
		}
	}
	info, err := os.Stat(filepath.Join(root, archive.Reference.SHA256+".partial"))
	if err != nil || info.Size() != 17 {
		t.Fatal("truncated document prefix was not retained")
	}
	if err := desktop.Close(); err != nil {
		t.Fatal(err)
	}
	desktop, err = sqlite.OpenRepository(t.Context(), databasePath, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.Close()
	cache, err = documentcache.New(root)
	if err != nil {
		t.Fatal(err)
	}
	synchronizer, err = remotesync.New(remotesync.Dependencies{Repository: desktop, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	sequence := outcome.Mirror.Snapshot.LatestSequence
	outcome, err = synchronizer.Observe(t.Context(), request)
	if err != nil || outcome.Mirror.Snapshot.LatestSequence != sequence {
		t.Fatalf("reopen changed the durable observation cursor: %v", err)
	}
	imported, err = synchronizer.ImportDocument(t.Context(), remotesync.DocumentRequest{
		Request: request, Document: archive,
	})
	if err != nil || !imported.Imported || !archiveResumed || fullDownloads != 1 {
		t.Fatalf("reopen did not resume the original document: %v", err)
	}
	original, err := fixture.repository.ReadPerformanceArchive(t.Context(), submission.Job.JobID, archive.Reference.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.api.Close()
	actual, err := cache.Read(t.Context(), archive.Reference, nil)
	if err != nil || !bytes.Equal(actual, original.Document) {
		t.Fatal("offline cache differs from the node document")
	}
	if _, err := desktop.GetQuickPerformanceReport(t.Context(), archive.Reference.ID); err != nil {
		t.Fatal("offline report was not imported")
	}
	outcome, err = synchronizer.Observe(t.Context(), request)
	if err == nil || outcome.Mirror.Snapshot.Receipt.Status != domain.RunCompleted {
		t.Fatal("node outage replaced a known execution terminal")
	}
	if fixture.requests.Load() != requests {
		t.Fatal("resume repeated provider traffic")
	}
}
