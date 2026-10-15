package runnerhttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type disconnectedWriter struct {
	header http.Header
	status int
}

func (writer *disconnectedWriter) Header() http.Header       { return writer.header }
func (writer *disconnectedWriter) WriteHeader(status int)    { writer.status = status }
func (writer *disconnectedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestHTTPFailedReceiptWriteDoesNotAbandonOrDuplicateAcceptedExecution(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	submission := fixture.submission(t, nil)
	request := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader(encode(t, submission)))
	request.Header.Set("Authorization", "bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	request.Header.Set("Content-Type", "application/json")
	writer := &disconnectedWriter{header: http.Header{}}
	fixture.api.Config.Handler.ServeHTTP(writer, request)
	if writer.status != http.StatusAccepted {
		t.Fatal("submission was not committed before the lost receipt")
	}
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("lost receipt abandoned execution")
	}
	retry := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	if retry.StatusCode != http.StatusOK {
		t.Fatal("lost receipt could not be confirmed by the same submission")
	}
	fixture.release()
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	if fixture.requests.Load() != 2 {
		t.Fatal("receipt loss repeated or truncated provider traffic")
	}
}

func TestHTTPCommandPathAndByteRejectionsLeaveJobAndJournalUnchanged(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	submission := fixture.submission(t, nil)
	fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	before, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	command := jobcontract.Command{
		Contract: jobcontract.CurrentContract, CommandID: "44444444-4444-4444-8444-444444444444",
		JobID: submission.Job.JobID, Kind: jobcontract.CommandCancel, ExpectedRevision: before.Receipt.Revision,
	}
	path := "/jobs/" + submission.Job.JobID + "/cancel"
	wrongJob := command
	wrongJob.JobID = "33333333-3333-4333-8333-333333333333"
	wrongKind := command
	wrongKind.Kind = jobcontract.CommandStopSending
	document := encode(t, command)
	overLimit := append(bytes.Clone(document), bytes.Repeat([]byte(" "), jobcontract.MaxCommandBytes-len(document)+1)...)
	for _, body := range [][]byte{encode(t, wrongJob), encode(t, wrongKind), overLimit} {
		response := fixture.request(
			t,
			http.MethodPost,
			path,
			body,
		)
		want := http.StatusBadRequest
		if len(body) > jobcontract.MaxCommandBytes {
			want = http.StatusRequestEntityTooLarge
		}
		if response.StatusCode != want {
			t.Fatalf("command rejection status=%d, want %d", response.StatusCode, want)
		}
		after, err := fixture.repository.GetJob(t.Context(), command.JobID)
		if err != nil || after.Receipt.Revision != before.Receipt.Revision || fixture.requests.Load() != 1 {
			t.Fatal("rejected command changed durable job state or traffic")
		}
		if _, err := fixture.repository.GetJobCommand(t.Context(), command.CommandID); !errors.Is(err, jobhost.ErrNotFound) {
			t.Fatal("rejected command was persisted")
		}
	}
	exact := append(bytes.Clone(document), bytes.Repeat([]byte(" "), jobcontract.MaxCommandBytes-len(document))...)
	response := fixture.request(
		t,
		http.MethodPost,
		path,
		exact,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatal("exact command byte limit was rejected")
	}
	waitHTTPJob(
		t,
		fixture,
		command.JobID,
		domain.RunCancelled,
	)
}

func TestHTTPEventObservationCancellationDoesNotChangeSettledJob(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission := fixture.submission(t, nil)
	fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	before := waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	ctx, cancel := context.WithCancel(t.Context())
	path := "/jobs/" + submission.Job.JobID + "/events?limit=1&wait_ms=20000&after_sequence=" +
		strconv.FormatUint(before.LatestSequence, 10)
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.api.Config.Handler.ServeHTTP(httptest.NewRecorder(), request)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled observation retained a long-poll handler")
	}
	after, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	if err != nil || after.LatestSequence != before.LatestSequence || fixture.requests.Load() != 2 {
		t.Fatal("observation cancellation changed settled execution")
	}
}

type corruptedResultRepository struct {
	runnerhttp.ReadRepository
	change func(*jobhost.PerformanceResultDocument)
}

func (repository corruptedResultRepository) ReadPerformanceResult(
	ctx context.Context,
	id string,
) (jobhost.PerformanceResultDocument, error) {
	result, err := repository.ReadRepository.ReadPerformanceResult(ctx, id)
	if err == nil {
		repository.change(&result)
	}
	return result, err
}

func TestHTTPRejectsCorruptReportBytesWithoutRepairOrProviderTraffic(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission := fixture.submission(t, nil)
	fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	original, err := fixture.repository.ReadPerformanceResult(t.Context(), submission.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	changes := []func(*jobhost.PerformanceResultDocument){
		func(value *jobhost.PerformanceResultDocument) { value.Document = []byte("http-channel-test-secret") },
		func(value *jobhost.PerformanceResultDocument) { value.Reference.SHA256 = strings.Repeat("f", 64) },
	}
	for _, change := range changes {
		handler, err := runnerhttp.New(runnerhttp.Dependencies{
			Host: fixture.host, Maintenance: fixture.maintenance, TokenSHA256: sha256.Sum256([]byte(nodeToken)),
			Repository: corruptedResultRepository{ReadRepository: fixture.repository, change: change},
			Version:    "test-corrupt-report", Capabilities: capabilities(),
		})
		if err != nil {
			t.Fatal(err)
		}
		path := "/jobs/" + submission.Job.JobID + "/reports/" + submission.Job.Spec.Performance.ReportID
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "test-secret") {
			t.Fatal("corrupt report was disclosed instead of rejected")
		}
	}
	after, err := fixture.repository.ReadPerformanceResult(t.Context(), submission.Job.JobID)
	if err != nil || !bytes.Equal(original.Document, after.Document) || fixture.requests.Load() != 2 {
		t.Fatal("corrupt read changed the authoritative report or repeated traffic")
	}
}
