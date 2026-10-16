package runnerhttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestHTTPEventMaximumWaitExpiresWithAnEmptyPage(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission := fixture.submission(t, func(command *quicktest.PerformanceCommand) { command.RequestCount = 1 })
	admitHTTPJob(t, fixture, submission)
	before := waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	path := "/jobs/" + submission.Job.JobID + "/events?limit=1&wait_ms=20000&after_sequence=" +
		strconv.FormatUint(before.LatestSequence, 10)
	started := time.Now()
	response := fixture.request(
		t,
		http.MethodGet,
		path,
		nil,
	)
	elapsed := time.Since(started)
	var page jobhost.EventPage
	err := json.NewDecoder(response.Body).Decode(&page)
	_ = response.Body.Close()
	emptyPage := len(page.Events) == 0 && !page.HasMore && page.LastSequence == before.LatestSequence
	validResponse := response.StatusCode == http.StatusOK && emptyPage
	if err != nil || !validResponse {
		t.Fatal("maximum event wait did not return the original empty cursor page")
	}
	if elapsed < 20*time.Second || elapsed > 30*time.Second {
		t.Fatalf("maximum event wait actually waited %s, want expiry at 20 seconds", elapsed)
	}
	after, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	unchanged := err == nil && bytes.Equal(encode(t, before), encode(t, after))
	if !unchanged || fixture.requests.Load() != 1 {
		t.Fatal("empty maximum wait changed settled execution or repeated provider traffic")
	}
	t.Logf("wait_ms=20000 expired after %s", elapsed)
}

type emptyEventReadRepository struct {
	runnerhttp.ReadRepository
	empty chan struct{}
	once  sync.Once
}

func (repository *emptyEventReadRepository) JobEvents(
	ctx context.Context,
	id string,
	after uint64,
	limit uint32,
) (jobhost.EventPage, error) {
	page, err := repository.ReadRepository.JobEvents(ctx, id, after, limit)
	if err == nil && len(page.Events) == 0 {
		repository.once.Do(func() { close(repository.empty) })
	}
	return page, err
}

func TestHTTPActiveJobContinuesAfterLongPollObservationIsCancelled(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	submission := fixture.submission(t, func(command *quicktest.PerformanceCommand) { command.RequestCount = 1 })
	admitHTTPJob(t, fixture, submission)
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("active observation fixture did not enter its held request")
	}
	before, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	if err != nil || before.Receipt.Status != domain.RunRunning {
		t.Fatalf("observation fixture is not running: %v", err)
	}
	repository := &emptyEventReadRepository{ReadRepository: fixture.repository, empty: make(chan struct{})}
	handler := newHTTPReadBoundaryHandler(t, fixture, repository)
	done := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(done)
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(api.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := "/jobs/" + submission.Job.JobID + "/events?limit=1&wait_ms=20000&after_sequence=" +
		strconv.FormatUint(before.LatestSequence, 10)
	request := httpReadBoundaryRequest(
		t,
		ctx,
		api.URL+path,
		"",
	)
	observed := make(chan error, 1)
	go func() {
		response, err := api.Client().Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
		observed <- err
	}()
	select {
	case <-repository.empty:
	case err := <-observed:
		t.Fatalf("observation returned before reading an empty active cursor: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("active long poll never reached an empty durable event page")
	}
	cancel()
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled HTTP observation returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled HTTP observation did not release its client")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled HTTP observation retained its server handler")
	}
	after, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	sameExecution := err == nil && after.Receipt.AcceptedAt.Equal(before.Receipt.AcceptedAt) &&
		after.Receipt.Origin == before.Receipt.Origin
	stillRunning := sameExecution && after.Receipt.Status == domain.RunRunning
	if !stillRunning || fixture.requests.Load() != 1 {
		t.Fatal("cancelling observation changed or cancelled the held original execution")
	}
	fixture.release()
	final := waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	sealed, err := fixture.repository.ReadPerformanceResult(t.Context(), submission.Job.JobID)
	sameReport := err == nil && sealed.Result.ReportID == submission.Job.Spec.Performance.ReportID &&
		sealed.Result.Terminal.Receipt.JobID == final.Receipt.JobID
	if !sameReport || fixture.requests.Load() != 1 {
		t.Fatal("original job did not seal its original report with exactly one provider request")
	}
}

func TestHTTPFullPerformanceResultsRejectUnreadyAndOtherActualJobOwners(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	first := fixture.submission(t, func(command *quicktest.PerformanceCommand) { command.RequestCount = 1 })
	second := fixture.submission(t, func(command *quicktest.PerformanceCommand) { command.RequestCount = 1 })
	for _, submission := range []jobcontract.Submission{first, second} {
		admitHTTPJob(t, fixture, submission)
	}
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("performance owner fixture did not hold its first request")
	}
	for _, submission := range []jobcontract.Submission{first, second} {
		path := "/jobs/" + submission.Job.JobID + "/reports/" + submission.Job.Spec.Performance.ReportID
		response := fixture.request(
			t,
			http.MethodGet,
			path,
			nil,
		)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound || fixture.requests.Load() != 1 {
			t.Fatal("unready full performance result was exposed or caused another request")
		}
	}
	fixture.release()
	for _, submission := range []jobcontract.Submission{first, second} {
		waitHTTPJob(
			t,
			fixture,
			submission.Job.JobID,
			domain.RunCompleted,
		)
	}
	for _, pair := range [][2]jobcontract.Submission{{first, second}, {second, first}} {
		owner, other := pair[0], pair[1]
		sealed, err := fixture.repository.ReadPerformanceResult(t.Context(), owner.Job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		otherSealed, err := fixture.repository.ReadPerformanceResult(t.Context(), other.Job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		path := "/jobs/" + owner.Job.JobID + "/reports/" + other.Job.Spec.Performance.ReportID
		wrong := fixture.request(
			t,
			http.MethodGet,
			path,
			nil,
		)
		body, err := io.ReadAll(wrong.Body)
		_ = wrong.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		noReports := !bytes.Contains(body, sealed.Document) && !bytes.Contains(body, otherSealed.Document)
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var rejection runnerhttp.ErrorResponse
		decodeErr := decoder.Decode(&rejection)
		validJSON := decodeErr == nil && errors.Is(decoder.Decode(&struct{}{}), io.EOF)
		validEnvelope := rejection.Contract == jobcontract.CurrentContract && rejection.Code == "not_found" &&
			rejection.Message != "" && rejection.Field == "" && !rejection.Retryable
		validRejection := wrong.StatusCode == http.StatusNotFound &&
			wrong.Header.Get("Content-Type") == "application/json" && validJSON && validEnvelope
		if !noReports || !validRejection {
			t.Fatal("full performance download disclosed a result through another actual job")
		}
		path = "/jobs/" + owner.Job.JobID + "/reports/" + owner.Job.Spec.Performance.ReportID
		response := fixture.request(
			t,
			http.MethodGet,
			path,
			nil,
		)
		body, err = io.ReadAll(response.Body)
		_ = response.Body.Close()
		matching := err == nil && bytes.Equal(body, sealed.Document) &&
			response.Header.Get("X-Content-SHA256") == sealed.Reference.SHA256
		if response.StatusCode != http.StatusOK || !matching {
			t.Fatal("original owner's full performance result changed after mismatched reads")
		}
	}
	if fixture.requests.Load() != 2 {
		t.Fatal("performance result readiness or owner reads repeated execution")
	}
}

type interruptedRunDownloadWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (writer interruptedRunDownloadWriter) Write(document []byte) (int, error) {
	written, err := writer.ResponseWriter.Write(document[:min(16, len(document))])
	if err != nil {
		return written, err
	}
	if err := http.NewResponseController(writer.ResponseWriter).Flush(); err != nil {
		return written, err
	}
	<-writer.ctx.Done()
	return written, writer.ctx.Err()
}

func TestHTTPRunReportRangesAndInterruptedDownloadResumeOriginalBytes(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission, stored := completedHTTPRunReport(t, fixture)
	path := "/jobs/" + submission.Job.JobID + "/reports/" + stored.Report.ID
	size := len(stored.Document)
	for _, test := range []struct {
		value string
		start int
		end   int
	}{
		{value: "bytes=0-15", start: 0, end: 15},
		{value: "bytes=-16", start: size - 16, end: size - 1},
		{value: "bytes=16-", start: 16, end: size - 1},
	} {
		request := httpReadBoundaryRequest(
			t,
			t.Context(),
			fixture.api.URL+path,
			test.value,
		)
		response, err := fixture.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		wantRange := "bytes " + strconv.Itoa(test.start) + "-" + strconv.Itoa(test.end) + "/" + strconv.Itoa(size)
		matching := err == nil && bytes.Equal(body, stored.Document[test.start:test.end+1]) &&
			response.Header.Get("Content-Range") == wantRange
		if response.StatusCode != http.StatusPartialContent || !matching {
			t.Fatalf("Run range %q did not retain the stored bytes and offset", test.value)
		}
	}
	for _, value := range []string{"bytes=-0", "bytes=2-1", "bytes=0-1,3-4", "bytes=" + strconv.Itoa(size) + "-"} {
		request := httpReadBoundaryRequest(
			t,
			t.Context(),
			fixture.api.URL+path,
			value,
		)
		response, err := fixture.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("invalid Run range %q returned %d", value, response.StatusCode)
		}
	}
	done := make(chan struct{})
	interrupted := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(done)
		interrupted := interruptedRunDownloadWriter{ResponseWriter: writer, ctx: request.Context()}
		fixture.api.Config.Handler.ServeHTTP(interrupted, request)
	}))
	t.Cleanup(interrupted.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httpReadBoundaryRequest(
		t,
		ctx,
		interrupted.URL+path,
		"",
	)
	response, err := interrupted.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 16)
	if _, err := io.ReadFull(response.Body, prefix); err != nil {
		t.Fatal(err)
	}
	cancel()
	_, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || readErr == nil {
		t.Fatal("Run download interruption did not fail the incomplete full response")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("interrupted Run download retained its response writer")
	}
	request = httpReadBoundaryRequest(
		t,
		t.Context(),
		fixture.api.URL+path,
		"bytes=16-",
	)
	request.Header.Set("If-Range", `"`+stored.Reference.SHA256+`"`)
	resumed, err := fixture.api.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := io.ReadAll(resumed.Body)
	_ = resumed.Body.Close()
	reconstructed := append(prefix, tail...)
	digest := sha256.Sum256(reconstructed)
	matching := err == nil && bytes.Equal(reconstructed, stored.Document) &&
		hex.EncodeToString(digest[:]) == stored.Reference.SHA256
	if resumed.StatusCode != http.StatusPartialContent || !matching {
		t.Fatal("Run range resume did not reconstruct the original report and digest")
	}
	if fixture.requests.Load() != 1 {
		t.Fatal("Run download interruption or resume repeated provider requests")
	}
}

type sizedRunReportRepository struct {
	runnerhttp.ReadRepository
	document jobhost.RunReportDocument
}

func (repository sizedRunReportRepository) ReadRunReport(
	ctx context.Context,
	jobID string,
	reportID string,
) (jobhost.RunReportDocument, error) {
	stored, err := repository.ReadRepository.ReadRunReport(ctx, jobID, reportID)
	if err != nil || stored.Report.ID != repository.document.Report.ID {
		return stored, err
	}
	return repository.document, nil
}

func TestHTTPRunReportCompleteEntityAtByteLimitAndAbove(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission, stored := completedHTTPRunReport(t, fixture)
	for _, size := range []int{jobcontract.MaxRunReportBytes, jobcontract.MaxRunReportBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			report := stored.Report
			report.Baseline = json.RawMessage(`{"padding":""}`)
			minimum := encode(t, report)
			report.Baseline = json.RawMessage(`{"padding":"` + strings.Repeat("x", size-len(minimum)) + `"}`)
			if err := report.Validate(); err != nil {
				t.Fatal("sized complete Run report violates the current report contract")
			}
			document := encode(t, report)
			if len(document) != size {
				t.Fatalf("complete Run report bytes=%d, want %d", len(document), size)
			}
			canonical, err := jobcontract.EncodeRunReport(report)
			if size == jobcontract.MaxRunReportBytes {
				if err != nil || len(canonical) != size {
					t.Fatal("maximum complete Run report was rejected by the canonical encoder")
				}
				document = canonical
			} else if !errors.Is(err, jobcontract.ErrInvalid) {
				t.Fatal("oversized complete Run report was not rejected by the canonical encoder")
			}
			digest := sha256.Sum256(document)
			reference := stored.Reference
			reference.Size, reference.SHA256 = int64(size), hex.EncodeToString(digest[:])
			// This isolates the HTTP/encoder byte boundary. SQLite admission and
			// its stored report remain the real, unchanged fixture below.
			repository := sizedRunReportRepository{ReadRepository: fixture.repository,
				document: jobhost.RunReportDocument{Report: report, Reference: reference, Document: document}}
			api := httptest.NewServer(newHTTPReadBoundaryHandler(t, fixture, repository))
			defer api.Close()
			path := "/jobs/" + submission.Job.JobID + "/reports/" + stored.Report.ID
			request := httpReadBoundaryRequest(
				t,
				t.Context(),
				api.URL+path,
				"",
			)
			response, err := api.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if size > jobcontract.MaxRunReportBytes {
				if response.StatusCode != http.StatusServiceUnavailable {
					t.Fatal("HTTP disclosed the oversized complete Run report")
				}
				return
			}
			received := sha256.New()
			count, err := io.Copy(received, io.LimitReader(response.Body, int64(size)+1))
			matching := err == nil && count == int64(size) &&
				hex.EncodeToString(received.Sum(nil)) == reference.SHA256
			validHeaders := response.ContentLength == int64(size) &&
				response.Header.Get("X-Content-SHA256") == reference.SHA256
			validResponse := response.StatusCode == http.StatusOK && matching && validHeaders
			if !validResponse {
				t.Fatal("maximum complete Run entity did not retain exact bytes, size and digest over HTTP")
			}
		})
	}
	after, err := fixture.repository.ReadRunReport(t.Context(), submission.Job.JobID, stored.Report.ID)
	unchanged := err == nil && bytes.Equal(after.Document, stored.Document)
	if !unchanged || fixture.requests.Load() != 1 {
		t.Fatal("sized HTTP reads changed the real stored report or repeated execution")
	}
}

func admitHTTPJob(t *testing.T, fixture *httpFixture, submission jobcontract.Submission) {
	t.Helper()
	response := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("HTTP fixture admission=%d", response.StatusCode)
	}
}

func completedHTTPRunReport(t *testing.T, fixture *httpFixture) (jobcontract.Submission, jobhost.RunReportDocument) {
	t.Helper()
	submission := fixture.runSubmission(t, false)
	admitHTTPJob(t, fixture, submission)
	final := waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	reports, err := fixture.repository.ListReportsForRuns(t.Context(), final.Receipt.RunIDs)
	if err != nil || len(reports) != 1 {
		t.Fatalf("completed Run fixture reports=%d: %v", len(reports), err)
	}
	stored, err := fixture.repository.ReadRunReport(t.Context(), submission.Job.JobID, reports[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return submission, stored
}

func newHTTPReadBoundaryHandler(
	t *testing.T,
	fixture *httpFixture,
	repository runnerhttp.ReadRepository,
) *runnerhttp.Handler {
	t.Helper()
	handler, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: fixture.host, Maintenance: fixture.maintenance, TokenSHA256: sha256.Sum256([]byte(nodeToken)),
		Repository: repository, Capabilities: capabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func httpReadBoundaryRequest(
	t *testing.T,
	ctx context.Context,
	url string,
	byteRange string,
) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	return request
}
