package runnerhttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type corruptedArchiveRepository struct {
	runnerhttp.ReadRepository
	change func(*jobhost.PerformanceArchiveDocument)
}

func (repository corruptedArchiveRepository) ReadPerformanceArchive(
	ctx context.Context,
	jobID string,
	reportID string,
) (jobhost.PerformanceArchiveDocument, error) {
	archive, err := repository.ReadRepository.ReadPerformanceArchive(ctx, jobID, reportID)
	if err == nil {
		repository.change(&archive)
	}
	return archive, err
}

func TestHTTPPerformanceArchiveRejectsWrongOwnerQueriesAndCorruption(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	client := httpManagementClient(t, fixture)
	first, second := fixture.submission(t, nil), fixture.submission(t, nil)
	for _, submission := range []*jobcontract.Submission{&first, &second} {
		if _, err := client.Submit(t.Context(), submission); err != nil {
			t.Fatal(err)
		}
		waitHTTPJob(
			t,
			fixture,
			submission.Job.JobID,
			domain.RunCompleted,
		)
	}
	waitHTTPSettlement(t, client)
	jobID, reportID := first.Job.JobID, first.Job.Spec.Performance.ReportID
	path := "/jobs/" + jobID + "/archives/" + reportID
	for _, test := range []struct {
		path   string
		status int
	}{
		{path: path + "?limit=1", status: http.StatusBadRequest},
		{path: "/jobs/" + jobID + "/archives/invalid", status: http.StatusBadRequest},
		{path: "/jobs/" + jobID + "/archives/" + second.Job.Spec.Performance.ReportID, status: http.StatusNotFound},
		{path: "/jobs/" + second.Job.JobID + "/archives/" + reportID, status: http.StatusNotFound},
		{path: "/jobs/" + httpMeta(t).ID + "/archives/" + reportID, status: http.StatusNotFound},
	} {
		response := fixture.request(
			t,
			http.MethodGet,
			test.path,
			nil,
		)
		_ = response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("archive boundary response=%d, want=%d", response.StatusCode, test.status)
		}
	}
	original, err := fixture.repository.ReadPerformanceArchive(t.Context(), jobID, reportID)
	if err != nil {
		t.Fatal(err)
	}
	requests := fixture.requests.Load()
	changes := []func(*jobhost.PerformanceArchiveDocument){
		func(value *jobhost.PerformanceArchiveDocument) { value.Document = []byte("http-channel-test-secret") },
		func(value *jobhost.PerformanceArchiveDocument) { value.Reference.SHA256 = strings.Repeat("f", 64) },
		func(value *jobhost.PerformanceArchiveDocument) { value.Reference.Kind = jobcontract.KindRun },
		func(value *jobhost.PerformanceArchiveDocument) { value.Report.ModelID = "another-model" },
	}
	for _, change := range changes {
		handler, err := runnerhttp.New(runnerhttp.Dependencies{
			Host: fixture.host, Maintenance: fixture.maintenance, TokenSHA256: sha256.Sum256([]byte(nodeToken)),
			Repository: corruptedArchiveRepository{ReadRepository: fixture.repository, change: change},
			Version:    "corrupt-archive-test", Capabilities: capabilities(),
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "test-secret") {
			t.Fatal("archive corruption was disclosed instead of rejected")
		}
	}
	after, err := fixture.repository.ReadPerformanceArchive(t.Context(), jobID, reportID)
	if err != nil || !bytes.Equal(original.Document, after.Document) || fixture.requests.Load() != requests {
		t.Fatal("rejected archive read changed original bytes or repeated provider traffic")
	}
}
