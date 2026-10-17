package remotesync

import (
	"context"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/domain"
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

type documentFixtureRepository struct {
	remoteresults.Repository
	mu                 sync.Mutex
	mirror             remoteresults.Mirror
	documents          []remoteresults.DocumentReference
	runImports         []remoteresults.RunReportImport
	performanceImports []remoteresults.PerformanceDocumentImport
	importRun          func(context.Context, remoteresults.RunReportImport) error
	importPerformance  func(context.Context, remoteresults.PerformanceDocumentImport) error
	fullResult         jobcontract.PerformanceResult
	resultError        error
}

func (repository *documentFixtureRepository) GetRemoteMirror(context.Context, string) (remoteresults.Mirror, error) {
	return repository.mirror, nil
}

func (repository *documentFixtureRepository) ApplyRemoteBatch(
	_ context.Context,
	_ remoteresults.Batch,
) (remoteresults.Mirror, error) {
	return repository.mirror, nil
}

func (repository *documentFixtureRepository) ListRemoteDocuments(
	context.Context,
	string,
) ([]remoteresults.DocumentReference, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return slices.Clone(repository.documents), nil
}

func (repository *documentFixtureRepository) ImportRemoteRunReport(
	ctx context.Context,
	input remoteresults.RunReportImport,
) error {
	repository.mu.Lock()
	repository.runImports = append(repository.runImports, input)
	repository.mu.Unlock()
	if repository.importRun != nil {
		if err := repository.importRun(ctx, input); err != nil {
			return err
		}
	}
	repository.markImported(jobcontract.EventReport, input.Document)
	return nil
}

func (repository *documentFixtureRepository) ImportRemotePerformanceDocument(
	ctx context.Context,
	input remoteresults.PerformanceDocumentImport,
) error {
	repository.mu.Lock()
	repository.performanceImports = append(repository.performanceImports, input)
	repository.mu.Unlock()
	if repository.importPerformance != nil {
		if err := repository.importPerformance(ctx, input); err != nil {
			return err
		}
	}
	repository.markImported(input.Kind, input.Document)
	return nil
}

func (repository *documentFixtureRepository) ReadRemotePerformanceResult(
	context.Context,
	string,
) (jobcontract.PerformanceResult, error) {
	return repository.fullResult, repository.resultError
}

func (repository *documentFixtureRepository) markImported(kind jobcontract.EventKind, document []byte) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for index, reference := range repository.documents {
		if reference.Kind == kind && reference.Reference.SHA256 == string(document) {
			repository.documents[index].Imported = true
		}
	}
}

type documentFixtureCache struct {
	mu         sync.Mutex
	reads      []jobcontract.ReportReference
	beforeRead func(context.Context, jobcontract.ReportReference) error
}

func (cache *documentFixtureCache) Read(
	ctx context.Context,
	reference jobcontract.ReportReference,
	_ func(context.Context, io.ReadWriteSeeker) error,
) ([]byte, error) {
	cache.mu.Lock()
	cache.reads = append(cache.reads, reference)
	cache.mu.Unlock()
	if cache.beforeRead != nil {
		if err := cache.beforeRead(ctx, reference); err != nil {
			return nil, err
		}
	}
	return []byte(reference.SHA256), nil
}

type documentFixtureRemote struct {
	Remote
	origin remoteresults.Origin
}

func (remote documentFixtureRemote) Probe(context.Context) (jobcontract.RunnerInfo, error) {
	return jobcontract.RunnerInfo{
		NodeID: remote.origin.NodeID, NodeInstallationID: remote.origin.InstallationID,
	}, nil
}

func (documentFixtureRemote) ReadEvents(
	_ context.Context,
	query runnerclient.EventQuery,
) (jobhost.EventPage, error) {
	return jobhost.EventPage{Events: []jobcontract.Event{}, LastSequence: query.AfterSequence}, nil
}

type documentFixture struct {
	repository   *documentFixtureRepository
	cache        *documentFixtureCache
	synchronizer *Synchronizer
	request      Request
}

// These ports isolate document scheduling; runnerhttp integration tests cover
// canonical document validation and the actual cache/download/import boundary.
func newDocumentFixture(t *testing.T, documents []remoteresults.DocumentReference) documentFixture {
	t.Helper()
	node := watcherNode(1)
	repository := &documentFixtureRepository{
		mirror: remoteresults.Mirror{Origin: node.Origin}, documents: slices.Clone(documents),
	}
	cache := &documentFixtureCache{}
	synchronizer, err := New(Dependencies{Repository: repository, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	return documentFixture{
		repository: repository, cache: cache, synchronizer: synchronizer,
		request: Request{Origin: node.Origin, JobID: queuedJob(node, 1).Receipt.JobID,
			Remote: documentFixtureRemote{origin: node.Origin}},
	}
}

func runDocument(index int) remoteresults.DocumentReference {
	return remoteresults.DocumentReference{
		Kind: jobcontract.EventReport,
		Reference: jobcontract.ReportReference{
			ID: fmt.Sprintf("55555555-5555-4555-8555-%012d", index), Kind: jobcontract.KindRun,
			RunID:  fmt.Sprintf("66666666-6666-4666-8666-%012d", index),
			SHA256: fmt.Sprintf("%064x", index), Size: 64,
		},
	}
}

func TestObserveDoesNotReadOrImportDocuments(t *testing.T) {
	fixture := newDocumentFixture(t, []remoteresults.DocumentReference{runDocument(1)})
	if _, err := fixture.synchronizer.Observe(t.Context(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if len(fixture.cache.reads) != 0 || fixture.repository.documents[0].Imported {
		t.Fatal("facts observation touched a report")
	}
}

func TestImportDocumentSkipsImportedAndUsesOneIndependentBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		imported, pending := runDocument(1), runDocument(2)
		imported.Imported = true
		fixture := newDocumentFixture(t, []remoteresults.DocumentReference{imported, pending})
		fixture.cache.beforeRead = func(ctx context.Context, _ jobcontract.ReportReference) error {
			timer := time.NewTimer(70 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
		start := time.Now()
		for _, document := range []remoteresults.DocumentReference{imported, pending, pending} {
			outcome, err := fixture.synchronizer.ImportDocument(t.Context(), DocumentRequest{
				Request: fixture.request, Document: document,
			})
			if err != nil || !outcome.Imported {
				t.Fatalf("single import: %v", err)
			}
		}
		singleImport := len(fixture.cache.reads) == 1 && len(fixture.repository.runImports) == 1
		if time.Since(start) != 70*time.Second || !singleImport {
			t.Fatal("imported reference consumed a turn or a different reference was downloaded")
		}
	})
}

func TestImportDocumentFailureDoesNotCommitOrTouchIndependentReferences(t *testing.T) {
	for _, stage := range []string{"cache", "import"} {
		t.Run(stage, func(t *testing.T) {
			documents := []remoteresults.DocumentReference{runDocument(1), runDocument(2)}
			fixture := newDocumentFixture(t, documents)
			failure := errors.New("document failed")
			if stage == "cache" {
				fixture.cache.beforeRead = func(_ context.Context, reference jobcontract.ReportReference) error {
					if reference == documents[0].Reference {
						return failure
					}
					return nil
				}
			} else {
				fixture.repository.importRun = func(_ context.Context, input remoteresults.RunReportImport) error {
					if string(input.Document) == documents[0].Reference.SHA256 {
						return failure
					}
					return nil
				}
			}
			input := DocumentRequest{Request: fixture.request, Document: documents[0]}
			if _, err := fixture.synchronizer.ImportDocument(t.Context(), input); err != failure {
				t.Fatal("single-document failure was lost")
			}
			changedOther := fixture.repository.documents[0].Imported || fixture.repository.documents[1].Imported
			if len(fixture.cache.reads) != 1 || changedOther {
				t.Fatal("failed turn changed another durable reference")
			}
			input.Document = documents[1]
			if _, err := fixture.synchronizer.ImportDocument(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			if fixture.repository.documents[0].Imported || !fixture.repository.documents[1].Imported {
				t.Fatal("independent next turn did not progress")
			}
		})
	}
}

func TestImportDocumentAuthenticatesSourceAndExactDurableReference(t *testing.T) {
	for _, mutation := range []string{"configuration", "reference", "missing", "remote installation"} {
		t.Run(mutation, func(t *testing.T) {
			document := runDocument(1)
			fixture := newDocumentFixture(t, []remoteresults.DocumentReference{document})
			input := DocumentRequest{Request: fixture.request, Document: document}
			switch mutation {
			case "configuration":
				input.Request.Origin.ConfigurationID = watcherNode(2).Origin.ConfigurationID
			case "reference":
				input.Document.Reference.SHA256 = fmt.Sprintf("%064x", 99)
			case "missing":
				fixture.repository.documents = []remoteresults.DocumentReference{}
			case "remote installation":
				origin := fixture.request.Origin
				origin.InstallationID = watcherNode(2).Origin.InstallationID
				input.Request.Remote = documentFixtureRemote{origin: origin}
			}
			if _, err := fixture.synchronizer.ImportDocument(t.Context(), input); !errors.Is(err, remoteresults.ErrConflict) {
				t.Fatalf("wrong binding accepted: %v", err)
			}
			if len(fixture.cache.reads) != 0 {
				t.Fatal("untrusted hint opened a document")
			}
		})
	}
}

func perfDocuments(t *testing.T) (documentFixture, remoteresults.DocumentReference, remoteresults.DocumentReference) {
	t.Helper()
	fixture := newDocumentFixture(t, []remoteresults.DocumentReference{})
	repository, _ := reconnectFixture(t, watcherNode(1))
	terminal := repository.mirror.Snapshot
	terminal.Receipt.Status, terminal.Receipt.Revision = domain.RunCancelled, 2
	terminal.LatestSequence = 2
	terminal.UpdatedAt = terminal.UpdatedAt.Add(time.Second)
	fixture.repository.mirror = repository.mirror
	fixture.repository.mirror.Snapshot = terminal
	fixture.repository.fullResult = jobcontract.PerformanceResult{Contract: jobcontract.CurrentContract,
		JobID: terminal.Receipt.JobID, ReportID: terminal.Receipt.ReportIDs[0], Terminal: terminal,
		SealedAt: terminal.UpdatedAt, ObservationState: jobcontract.PerformanceNotStarted}
	if err := fixture.repository.fullResult.Validate(); err != nil {
		t.Fatal(err)
	}
	result, archive := runDocument(1), runDocument(2)
	result.Kind, result.Reference.Kind = jobcontract.EventPerformanceResult, jobcontract.KindPerformance
	result.Reference.ID, result.Reference.RunID = terminal.Receipt.ReportIDs[0], ""
	archive.Reference.ID = result.Reference.ID
	archive.Reference.Kind, archive.Reference.RunID = jobcontract.KindPerformance, ""
	fixture.repository.documents = []remoteresults.DocumentReference{archive, result}
	return fixture, result, archive
}

func TestImportArchiveRequiresActualMatchingDurableSealedResult(t *testing.T) {
	states := []string{"pending", "missing durable result", "wrong sealed source", "reader canceled", "valid"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			fixture, result, archive := perfDocuments(t)
			fixture.repository.documents[1].Imported = state != "pending"
			switch state {
			case "missing durable result":
				fixture.repository.resultError = remoteresults.ErrNotFound
			case "reader canceled":
				fixture.repository.resultError = context.Canceled
			case "wrong sealed source":
				fixture.repository.fullResult.Terminal.Receipt.Origin.NodeID = watcherNode(2).Origin.NodeID
			}
			input := DocumentRequest{Request: fixture.request, Document: archive}
			outcome, err := fixture.synchronizer.ImportDocument(t.Context(), input)
			if state == "valid" {
				imported := outcome.Imported && fixture.repository.documents[0].Imported
				if err != nil || !imported {
					t.Fatalf("ready archive: %v", err)
				}
			} else {
				untouched := len(fixture.cache.reads) == 0 && !fixture.repository.documents[0].Imported
				if err == nil || !untouched {
					t.Fatal("archive bypassed its actual imported full-result dependency")
				}
			}
			if state == "reader canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("durable sealed-result read lost context cancellation")
			}
			if slices.Contains(fixture.cache.reads, result.Reference) {
				t.Fatal("archive turn silently downloaded another document")
			}
		})
	}
}

func TestImportDocumentCancellationAndTurnLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		document := runDocument(1)
		fixture := newDocumentFixture(t, []remoteresults.DocumentReference{document})
		fixture.cache.beforeRead = func(ctx context.Context, _ jobcontract.ReportReference) error {
			<-ctx.Done()
			return ctx.Err()
		}
		start := time.Now()
		outcome, err := fixture.synchronizer.ImportDocument(t.Context(), DocumentRequest{
			Request: fixture.request, Document: document,
		})
		expired := errors.Is(err, context.DeadlineExceeded) && time.Since(start) == 2*time.Minute
		if !expired || outcome.Imported {
			t.Fatalf("document turn bound: %v", err)
		}
		if fixture.repository.documents[0].Imported {
			t.Fatal("expired partial was falsely imported")
		}
	})
}
