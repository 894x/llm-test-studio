package remotesync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

type documentPassRepository struct {
	remoteresults.Repository
	mirror             remoteresults.Mirror
	documents          []remoteresults.DocumentReference
	runImports         []remoteresults.RunReportImport
	performanceImports []remoteresults.PerformanceDocumentImport
	importRun          func(context.Context, remoteresults.RunReportImport) error
	importPerformance  func(context.Context, remoteresults.PerformanceDocumentImport) error
}

func (repository *documentPassRepository) GetRemoteMirror(context.Context, string) (remoteresults.Mirror, error) {
	return repository.mirror, nil
}

func (repository *documentPassRepository) ApplyRemoteBatch(
	_ context.Context,
	_ remoteresults.Batch,
) (remoteresults.Mirror, error) {
	return repository.mirror, nil
}

func (repository *documentPassRepository) ListRemoteDocuments(
	context.Context,
	string,
) ([]remoteresults.DocumentReference, error) {
	return slices.Clone(repository.documents), nil
}

func (repository *documentPassRepository) ImportRemoteRunReport(
	ctx context.Context,
	input remoteresults.RunReportImport,
) error {
	repository.runImports = append(repository.runImports, input)
	if repository.importRun != nil {
		if err := repository.importRun(ctx, input); err != nil {
			return err
		}
	}
	repository.markImported(jobcontract.EventReport, input.Document)
	return nil
}

func (repository *documentPassRepository) ImportRemotePerformanceDocument(
	ctx context.Context,
	input remoteresults.PerformanceDocumentImport,
) error {
	repository.performanceImports = append(repository.performanceImports, input)
	if repository.importPerformance != nil {
		if err := repository.importPerformance(ctx, input); err != nil {
			return err
		}
	}
	repository.markImported(input.Kind, input.Document)
	return nil
}

func (repository *documentPassRepository) markImported(kind jobcontract.EventKind, document []byte) {
	for index, reference := range repository.documents {
		if reference.Kind == kind && reference.Reference.SHA256 == string(document) {
			repository.documents[index].Imported = true
		}
	}
}

type documentPassCache struct {
	reads      []jobcontract.ReportReference
	beforeRead func(context.Context, jobcontract.ReportReference) error
}

func (cache *documentPassCache) Read(
	ctx context.Context,
	reference jobcontract.ReportReference,
	_ func(context.Context, io.ReadWriteSeeker) error,
) ([]byte, error) {
	cache.reads = append(cache.reads, reference)
	if cache.beforeRead != nil {
		if err := cache.beforeRead(ctx, reference); err != nil {
			return nil, err
		}
	}
	return []byte(reference.SHA256), nil
}

type documentPassRemote struct {
	Remote
	origin remoteresults.Origin
}

func (remote documentPassRemote) Probe(context.Context) (jobcontract.RunnerInfo, error) {
	return jobcontract.RunnerInfo{
		NodeID: remote.origin.NodeID, NodeInstallationID: remote.origin.InstallationID,
	}, nil
}

func (documentPassRemote) ReadEvents(
	_ context.Context,
	query runnerclient.EventQuery,
) (jobhost.EventPage, error) {
	return jobhost.EventPage{Events: []jobcontract.Event{}, LastSequence: query.AfterSequence}, nil
}

type documentPassFixture struct {
	repository   *documentPassRepository
	cache        *documentPassCache
	synchronizer *Synchronizer
	request      Request
}

// These ports isolate document scheduling; runnerhttp integration tests cover
// canonical document validation and the actual cache/download/import boundary.
func newDocumentPassFixture(t *testing.T, documents []remoteresults.DocumentReference) documentPassFixture {
	t.Helper()
	node := watcherNode(1)
	repository := &documentPassRepository{
		mirror: remoteresults.Mirror{Origin: node.Origin}, documents: slices.Clone(documents),
	}
	cache := &documentPassCache{}
	synchronizer, err := New(Dependencies{Repository: repository, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	return documentPassFixture{
		repository: repository, cache: cache, synchronizer: synchronizer,
		request: Request{Origin: node.Origin, JobID: queuedJob(node, 1).Receipt.JobID,
			Remote: documentPassRemote{origin: node.Origin}},
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

func TestPassImportedDocumentCannotConsumePendingDocumentBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		imported, pending := runDocument(1), runDocument(2)
		imported.Imported = true
		fixture := newDocumentPassFixture(t, []remoteresults.DocumentReference{imported, pending})
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
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		started := time.Now()
		if _, err := fixture.synchronizer.Pass(ctx, fixture.request); err != nil {
			t.Fatalf("pending document did not complete within its budget: %v", err)
		}
		if elapsed := time.Since(started); elapsed != 70*time.Second {
			t.Fatalf("already imported document consumed observation time: %v", elapsed)
		}
		if !slices.Equal(fixture.cache.reads, []jobcontract.ReportReference{pending.Reference}) {
			t.Fatalf("unexpected cache reads: %v", fixture.cache.reads)
		}
		if len(fixture.repository.runImports) != 1 || !fixture.repository.documents[1].Imported {
			t.Fatal("pending document was not durably imported")
		}
		if _, err := fixture.synchronizer.Pass(ctx, fixture.request); err != nil {
			t.Fatal(err)
		}
		if len(fixture.cache.reads) != 1 || len(fixture.repository.runImports) != 1 {
			t.Fatal("repeated pass reread or reimported a completed document")
		}
	})
}

func TestPassImportsIndependentReportsAfterDocumentFailures(t *testing.T) {
	for _, stage := range []string{"cache", "import"} {
		t.Run(stage, func(t *testing.T) {
			documents := []remoteresults.DocumentReference{runDocument(1), runDocument(2), runDocument(3)}
			fixture := newDocumentPassFixture(t, documents)
			firstError, laterError := errors.New("first document failed"), errors.New("second document failed")
			failure := func(document string) error {
				switch document {
				case documents[0].Reference.SHA256:
					return firstError
				case documents[1].Reference.SHA256:
					return laterError
				default:
					return nil
				}
			}
			if stage == "cache" {
				fixture.cache.beforeRead = func(_ context.Context, reference jobcontract.ReportReference) error {
					return failure(reference.SHA256)
				}
			}
			if stage == "import" {
				fixture.repository.importRun = func(_ context.Context, input remoteresults.RunReportImport) error {
					return failure(string(input.Document))
				}
			}
			if _, err := fixture.synchronizer.Pass(t.Context(), fixture.request); err != firstError {
				t.Fatalf("pass did not preserve the first document failure: %v", err)
			}
			if len(fixture.cache.reads) != 3 || !fixture.repository.documents[2].Imported {
				t.Fatal("failed documents blocked an independent report")
			}
			if fixture.repository.documents[0].Imported || fixture.repository.documents[1].Imported {
				t.Fatal("a failed document was marked imported")
			}
			for _, input := range fixture.repository.runImports {
				if input.Origin != fixture.request.Origin || input.JobID != fixture.request.JobID {
					t.Fatal("report import changed its fixed source")
				}
			}
		})
	}
}

func TestPassPerformanceArchiveWaitsForImportedSealedResult(t *testing.T) {
	tests := []struct {
		name           string
		resultPresent  bool
		resultImported bool
		failureStage   string
		wantImports    []jobcontract.EventKind
	}{
		{name: "result arrives after archive", resultPresent: true,
			wantImports: []jobcontract.EventKind{jobcontract.EventPerformanceResult, jobcontract.EventReport}},
		{name: "result already imported", resultPresent: true, resultImported: true,
			wantImports: []jobcontract.EventKind{jobcontract.EventReport}},
		{name: "result download failed", resultPresent: true, failureStage: "cache"},
		{name: "result import failed", resultPresent: true, failureStage: "import",
			wantImports: []jobcontract.EventKind{jobcontract.EventPerformanceResult}},
		{name: "result not published"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, archive := runDocument(1), runDocument(2)
			result.Kind, result.Reference.Kind = jobcontract.EventPerformanceResult, jobcontract.KindPerformance
			result.Reference.RunID, result.Imported = "", test.resultImported
			archive.Reference.Kind, archive.Reference.RunID = jobcontract.KindPerformance, ""
			archive.Reference.ID = result.Reference.ID
			documents := []remoteresults.DocumentReference{archive}
			if test.resultPresent {
				documents = append(documents, result)
			}
			fixture := newDocumentPassFixture(t, documents)
			failure := errors.New("sealed result failed")
			fixture.cache.beforeRead = func(_ context.Context, reference jobcontract.ReportReference) error {
				if test.failureStage == "cache" && reference == result.Reference {
					return failure
				}
				return nil
			}
			fixture.repository.importPerformance = func(
				_ context.Context,
				input remoteresults.PerformanceDocumentImport,
			) error {
				if test.failureStage == "import" && input.Kind == jobcontract.EventPerformanceResult {
					return failure
				}
				return nil
			}
			_, err := fixture.synchronizer.Pass(t.Context(), fixture.request)
			if test.failureStage != "" && err != failure {
				t.Fatalf("result failure was not returned: %v", err)
			}
			if test.failureStage == "" && err != nil {
				t.Fatal(err)
			}
			var importedKinds []jobcontract.EventKind
			for _, input := range fixture.repository.performanceImports {
				importedKinds = append(importedKinds, input.Kind)
			}
			if !slices.Equal(importedKinds, test.wantImports) {
				t.Fatalf("wrong import order: %v", importedKinds)
			}
			wantArchive := slices.Contains(test.wantImports, jobcontract.EventReport)
			readArchive := slices.Contains(fixture.cache.reads, archive.Reference)
			if readArchive != wantArchive || fixture.repository.documents[0].Imported != wantArchive {
				t.Fatal("archive was read or imported before its sealed result")
			}
			if test.resultImported && slices.Contains(fixture.cache.reads, result.Reference) {
				t.Fatal("already imported sealed result was downloaded again")
			}
		})
	}
}

func TestPassCancellationStopsLaterDocumentsAndPreservesFirstFailure(t *testing.T) {
	for _, stage := range []string{"cache", "import", "after committed import"} {
		t.Run(stage, func(t *testing.T) {
			documents := []remoteresults.DocumentReference{runDocument(1), runDocument(2), runDocument(3)}
			fixture := newDocumentPassFixture(t, documents)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			firstError := errors.New("first document failed")
			fixture.cache.beforeRead = func(_ context.Context, reference jobcontract.ReportReference) error {
				if reference == documents[0].Reference {
					return firstError
				}
				if stage == "cache" {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			fixture.repository.importRun = func(context.Context, remoteresults.RunReportImport) error {
				cancel()
				if stage == "import" {
					return ctx.Err()
				}
				return nil
			}
			_, err := fixture.synchronizer.Pass(ctx, fixture.request)
			if !errors.Is(err, firstError) || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost the original document failure: %v", err)
			}
			if len(fixture.cache.reads) != 2 || fixture.repository.documents[2].Imported {
				t.Fatal("canceled pass touched a later document")
			}
		})
	}
}
