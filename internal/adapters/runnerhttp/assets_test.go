package runnerhttp_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type contentStoreFunc func(context.Context, jobcontract.ContentIdentity, io.Reader) (bool, error)

func (store contentStoreFunc) PutContent(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	source io.Reader,
) (bool, error) {
	return store(ctx, identity, source)
}

func contentHandler(t *testing.T, fixture *httpFixture, store runnerhttp.ContentStore) *runnerhttp.Handler {
	t.Helper()
	handler, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: fixture.host, Maintenance: fixture.maintenance, Repository: fixture.repository,
		TokenSHA256: sha256.Sum256([]byte(nodeToken)), Capabilities: capabilities(), Content: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assetRequest(t *testing.T, document []byte) *http.Request {
	t.Helper()
	digest := sha256.Sum256(document)
	request := httptest.NewRequest(http.MethodPut, "/assets/"+hex.EncodeToString(digest[:]), bytes.NewReader(document))
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	request.Header.Set("Content-Type", "application/octet-stream")
	return request
}

type observedBody struct {
	io.ReadCloser
	reads, closes atomic.Int32
}

func (body *observedBody) Read(buffer []byte) (int, error) {
	body.reads.Add(1)
	return body.ReadCloser.Read(buffer)
}

func (body *observedBody) Close() error {
	body.closes.Add(1)
	return body.ReadCloser.Close()
}

func TestAssetUploadValidatesAllMetadataBeforeReadingOrCallingStorage(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	var calls atomic.Int32
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		context.Context, jobcontract.ContentIdentity, io.Reader,
	) (bool, error) {
		calls.Add(1)
		return false, errors.New("must not enter storage")
	}))
	tests := []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{name: "auth_missing", change: func(r *http.Request) { r.Header.Del("Authorization") }, status: 401},
		{
			name:   "auth_duplicate",
			change: func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+nodeToken) },
			status: 401,
		},
		{name: "contract_missing", change: func(r *http.Request) { r.Header.Del(jobcontract.ContractHeader) }, status: 426},
		{name: "contract_old", change: func(r *http.Request) { r.Header.Set(jobcontract.ContractHeader, "0") }, status: 426},
		{
			name:   "contract_duplicate",
			change: func(r *http.Request) { r.Header.Add(jobcontract.ContractHeader, "1") },
			status: 426,
		},
		{name: "hash_uppercase", change: func(r *http.Request) {
			r.URL.Path = "/assets/" + strings.ToUpper(strings.TrimPrefix(r.URL.Path, "/assets/"))
		}, status: 400},
		{name: "hash_short", change: func(r *http.Request) { r.URL.Path = "/assets/aa" }, status: 400},
		{name: "path_bare", change: func(r *http.Request) { r.URL.Path = "/assets" }, status: 400},
		{name: "path_unknown", change: func(r *http.Request) { r.URL.Path = "/unknown" }, status: 400},
		{
			name: "path_leading_double_slash", status: 400,
			change: func(r *http.Request) { r.URL.Path = "//assets/" + strings.Repeat("a", 64) },
		},
		{
			name: "path_prefix_dot_segment", status: 400,
			change: func(r *http.Request) { r.URL.Path = "/x/../assets/" + strings.Repeat("a", 64) },
		},
		{
			name:   "path_double_slash",
			change: func(r *http.Request) { r.URL.Path = "/assets//" + strings.Repeat("a", 64) },
			status: 400,
		},
		{
			name:   "path_dot",
			change: func(r *http.Request) { r.URL.Path = "/assets/../" + strings.Repeat("a", 64) },
			status: 400,
		},
		{
			name:   "path_encoded_alias",
			change: func(r *http.Request) { r.URL.RawPath = "/%61ssets/" + strings.TrimPrefix(r.URL.Path, "/assets/") },
			status: 400,
		},
		{name: "query", change: func(r *http.Request) { r.URL.RawQuery = "x=1" }, status: 400},
		{name: "query_empty", change: func(r *http.Request) { r.URL.ForceQuery = true }, status: 400},
		{name: "query_large", change: func(r *http.Request) { r.URL.RawQuery = strings.Repeat("a", 4097) }, status: 400},
		{name: "type_missing", change: func(r *http.Request) { r.Header.Del("Content-Type") }, status: 415},
		{name: "type_json", change: func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, status: 415},
		{
			name:   "type_parameters",
			change: func(r *http.Request) { r.Header.Set("Content-Type", "application/octet-stream; charset=utf-8") },
			status: 415,
		},
		{
			name:   "type_uppercase",
			change: func(r *http.Request) { r.Header.Set("Content-Type", "Application/Octet-Stream") },
			status: 415,
		},
		{
			name:   "type_duplicate",
			change: func(r *http.Request) { r.Header.Add("Content-Type", "application/octet-stream") },
			status: 415,
		},
		{name: "encoding_gzip", change: func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, status: 415},
		{name: "encoding_empty", change: func(r *http.Request) { r.Header["Content-Encoding"] = []string{""} }, status: 415},
		{
			name:   "encoding_duplicate",
			change: func(r *http.Request) { r.Header["Content-Encoding"] = []string{"identity", "identity"} },
			status: 415,
		},
		{name: "length_unknown", change: func(r *http.Request) { r.ContentLength = -1 }, status: 400},
		{name: "length_zero", change: func(r *http.Request) { r.ContentLength = 0 }, status: 400},
		{
			name:   "length_over_limit",
			change: func(r *http.Request) { r.ContentLength = jobcontract.MaxAssetBytes + 1 },
			status: 413,
		},
		{name: "chunked", change: func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, status: 400},
		{
			name:   "raw_transfer_encoding",
			change: func(r *http.Request) { r.Header.Set("Transfer-Encoding", "chunked") },
			status: 400,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := assetRequest(t, []byte("test"))
			body := &observedBody{ReadCloser: request.Body}
			request.Body = body
			test.change(request)
			writer := httptest.NewRecorder()
			handler.ServeHTTP(writer, request)
			if writer.Code != test.status || body.reads.Load() != 0 || calls.Load() != 0 {
				t.Fatalf("code=%d reads=%d storage=%d", writer.Code, body.reads.Load(), calls.Load())
			}
			if writer.Header().Get("Connection") != "close" {
				t.Fatal("rejected upload kept an unread connection reusable")
			}
			_ = body.Close()
		})
	}
}

func TestAssetUploadReturnsTypedDurableReceiptsAndVerifiesDuplicateBodies(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	content := []byte("immutable test content")
	digest := sha256.Sum256(content)
	identity := jobcontract.ContentIdentity{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}
	var published []byte
	var calls int
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		ctx context.Context, actual jobcontract.ContentIdentity, reader io.Reader,
	) (bool, error) {
		calls++
		if actual != identity {
			t.Fatal("HTTP changed content identity")
		}
		body, err := io.ReadAll(io.LimitReader(reader, actual.Size+1))
		if err != nil {
			return false, errors.Join(jobcontract.ErrAssetRead, err)
		}
		hash := sha256.Sum256(body)
		if int64(len(body)) != actual.Size || hex.EncodeToString(hash[:]) != actual.SHA256 {
			return false, jobcontract.ErrAssetIntegrity
		}
		created := published == nil
		published = bytes.Clone(body)
		return created, ctx.Err()
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, expected := range []int{http.StatusCreated, http.StatusOK} {
		request := assetRequest(t, content)
		request.URL.Scheme, request.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
		request.RequestURI = ""
		request.Header.Set("Accept-Encoding", "gzip")
		request.Header.Set("Content-Encoding", "identity")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := jobcontract.DecodeAssetReceipt(response.Body)
		_ = response.Body.Close()
		want := fixture.host.Identity()
		validReceipt := receipt.NodeID == want.Target.NodeID && receipt.NodeInstallationID == want.InstallationID &&
			receipt.SHA256 == identity.SHA256 && receipt.Size == identity.Size
		if response.StatusCode != expected || err != nil || !validReceipt {
			t.Fatalf("receipt not tied to durable bytes/install: status=%d err=%v", response.StatusCode, err)
		}
		if response.Header.Get("Content-Encoding") != "" || response.Header.Get("Content-Type") != "application/json" {
			t.Fatal("bulk receipt encoding changed")
		}
	}
	if calls != 2 || !bytes.Equal(published, content) || fixture.requests.Load() != 0 {
		t.Fatal("duplicate skipped verification or executed a provider")
	}
	// Exercise an incoming mismatch through the HTTP-to-store boundary. The real
	// private-file hashing and fsync/close guarantees have separate Linux tests.
	for _, invalid := range [][]byte{
		[]byte(strings.Repeat("x", len(content))), content[:len(content)-1], append(bytes.Clone(content), 'x'),
	} {
		request := assetRequest(t, invalid)
		request.URL.Path = "/assets/" + identity.SHA256
		request.ContentLength = identity.Size
		writer := newAssetWriter()
		handler.ServeHTTP(writer, request)
		if writer.Code != http.StatusUnprocessableEntity || !bytes.Equal(published, content) {
			t.Fatal("duplicate with wrong bytes was acknowledged or changed existing content")
		}
	}
}

type assetWriter struct {
	*httptest.ResponseRecorder
	mu                            sync.Mutex
	readDeadlines, writeDeadlines []time.Time
	deadlineError                 error
}

func newAssetWriter() *assetWriter {
	return &assetWriter{
		ResponseRecorder: httptest.NewRecorder(), readDeadlines: []time.Time{}, writeDeadlines: []time.Time{},
	}
}

func (writer *assetWriter) SetReadDeadline(deadline time.Time) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.readDeadlines = append(writer.readDeadlines, deadline)
	return writer.deadlineError
}

func (writer *assetWriter) SetWriteDeadline(deadline time.Time) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.writeDeadlines = append(writer.writeDeadlines, deadline)
	return writer.deadlineError
}

func TestAssetUploadClassifiesInputCancellationCapacityAndStorageFailures(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "integrity", err: jobcontract.ErrAssetIntegrity, status: 422, code: "asset_integrity_mismatch"},
		{name: "source", err: jobcontract.ErrAssetRead, status: 408, code: "asset_read_failed"},
		{name: "canceled", err: context.Canceled, status: 408, code: "asset_read_failed"},
		{name: "deadline", err: context.DeadlineExceeded, status: 408, code: "asset_read_failed"},
		{name: "capacity", err: jobhost.ErrCapacity, status: 429, code: "capacity_exceeded"},
		{name: "maintenance", err: jobhost.ErrMaintenance, status: 423, code: "runner_maintenance"},
		{name: "storage", err: jobcontract.ErrAssetStorage, status: 503, code: "storage_unavailable"},
		{
			name:   "storage_and_bad_body",
			err:    errors.Join(jobcontract.ErrAssetIntegrity, jobcontract.ErrAssetStorage),
			status: 503, code: "storage_unavailable",
		},
		{
			name:   "storage_and_cancel",
			err:    errors.Join(context.Canceled, jobcontract.ErrAssetStorage),
			status: 503, code: "storage_unavailable",
		},
		{name: "unknown", err: errors.New("private/path/source-secret"), status: 503, code: "storage_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := contentHandler(t, fixture, contentStoreFunc(func(
				context.Context, jobcontract.ContentIdentity, io.Reader,
			) (bool, error) {
				return true, test.err // A published name is not a durable acknowledgement.
			}))
			request := assetRequest(t, []byte("test"))
			body := &observedBody{ReadCloser: request.Body}
			request.Body = body
			writer := newAssetWriter()
			handler.ServeHTTP(writer, request)
			var response runnerhttp.ErrorResponse
			decodeErr := json.Unmarshal(writer.Body.Bytes(), &response)
			validError := decodeErr == nil && writer.Code == test.status && response.Code == test.code
			if !validError {
				t.Fatalf("wrong error class: status=%d body=%s", writer.Code, writer.Body.String())
			}
			if strings.Contains(writer.Body.String(), "private/") || body.closes.Load() != 1 {
				t.Fatal("raw storage error leaked or body owner did not close exactly once")
			}
		})
	}
	if !fixture.host.Status().Healthy || fixture.requests.Load() != 0 {
		t.Fatal("HTTP classification mutated host health or made provider requests")
	}
}

func TestAssetUploadRequiresDeadlineControlAndDoesNotChangeJSONDeadline(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	var calls int
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		context.Context, jobcontract.ContentIdentity, io.Reader,
	) (bool, error) {
		calls++
		return true, nil
	}))
	request := assetRequest(t, []byte("test"))
	unsupported := httptest.NewRecorder()
	handler.ServeHTTP(unsupported, request)
	if unsupported.Code != 503 || calls != 0 {
		t.Fatal("upload proceeded without controllable socket deadlines")
	}
	writer := newAssetWriter()
	before := time.Now()
	handler.ServeHTTP(writer, assetRequest(t, []byte("test")))
	if writer.Code != 201 || len(writer.readDeadlines) != 1 || len(writer.writeDeadlines) != 1 {
		t.Fatal("route deadlines did not pass through JSON writer Unwrap")
	}
	deadline := writer.readDeadlines[0]
	tooEarly := deadline.Before(before.Add(jobcontract.AssetUploadTimeout))
	tooLate := deadline.After(time.Now().Add(jobcontract.AssetUploadTimeout))
	if tooEarly || tooLate {
		t.Fatal("bulk deadline differs from current contract")
	}
	if !deadline.Equal(writer.writeDeadlines[0]) {
		t.Fatal("upload read and write budgets differ")
	}
	jsonWriter := newAssetWriter()
	probe := httptest.NewRequest(http.MethodGet, "/runner", nil)
	probe.Header.Set("Authorization", "Bearer "+nodeToken)
	probe.Header.Set(jobcontract.ContractHeader, "1")
	handler.ServeHTTP(jsonWriter, probe)
	if jsonWriter.Code != 200 || len(jsonWriter.readDeadlines) != 0 || len(jsonWriter.writeDeadlines) != 0 {
		t.Fatal("bulk deadline changed ordinary JSON management")
	}
}

type blockingAssetBody struct {
	entered, closed      chan struct{}
	enterOnce, closeOnce sync.Once
	closeCount           atomic.Int32
}

func (body *blockingAssetBody) Read([]byte) (int, error) {
	body.enterOnce.Do(func() { close(body.entered) })
	<-body.closed
	return 0, io.ErrClosedPipe
}

func (body *blockingAssetBody) Close() error {
	body.closeCount.Add(1)
	body.closeOnce.Do(func() { close(body.closed) })
	return nil
}

func TestAssetUploadCancellationClosesBlockedBodyAndJoinsItsOwner(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		ctx context.Context, _ jobcontract.ContentIdentity, source io.Reader,
	) (bool, error) {
		_, err := io.Copy(io.Discard, source)
		return false, errors.Join(jobcontract.ErrAssetRead, err, ctx.Err())
	}))
	body := &blockingAssetBody{entered: make(chan struct{}), closed: make(chan struct{})}
	request := assetRequest(t, []byte("test"))
	request.Body = body
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := newAssetWriter()
	finished := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request.WithContext(ctx))
		close(finished)
	}()
	select {
	case <-body.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upload never entered body reader")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not join the blocked upload")
	}
	if writer.Code != 408 || body.closeCount.Load() != 1 || len(writer.readDeadlines) < 2 {
		t.Fatal("cancellation did not close exactly once and expire the read deadline")
	}
}

func TestAssetUploadCapabilityDoesNotEnableJobAssets(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	for _, enabled := range []bool{false, true} {
		var store runnerhttp.ContentStore
		if enabled {
			store = contentStoreFunc(func(context.Context, jobcontract.ContentIdentity, io.Reader) (bool, error) {
				return true, nil
			})
		}
		handler := contentHandler(t, fixture, store)
		writer := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/runner", nil)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		handler.ServeHTTP(writer, request)
		info, err := jobcontract.DecodeRunnerInfo(writer.Body)
		if err != nil || slices.Contains(info.Capabilities.Features, jobcontract.FeatureContentUpload) != enabled {
			t.Fatal("content upload capability does not match its actual dependency")
		}
		if slices.Contains(info.Capabilities.Features, "assets") {
			t.Fatal("content upload advertised asset execution")
		}
		if !enabled {
			missing := httptest.NewRecorder()
			handler.ServeHTTP(missing, assetRequest(t, []byte("test")))
			if missing.Code != 404 {
				t.Fatal("missing content dependency accepted an upload")
			}
		}
		submission := fixture.submission(t, nil)
		submission.Job.Assets = []jobcontract.Asset{{
			Ref: "input", SHA256: strings.Repeat("a", 64), Size: 1, MediaType: "image/png",
		}}
		request = httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader(encode(t, submission)))
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Content-Type", "application/json")
		writer = httptest.NewRecorder()
		handler.ServeHTTP(writer, request)
		if writer.Code != 422 || fixture.requests.Load() != 0 {
			t.Fatal("uploaded content capability enabled asset execution")
		}
		if _, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID); !errors.Is(err, jobhost.ErrNotFound) {
			t.Fatal("asset rejection created durable admission state")
		}
	}
	caps := capabilities()
	caps.Features = append(caps.Features, "assets")
	if _, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: fixture.host, Maintenance: fixture.maintenance, Repository: fixture.repository,
		TokenSHA256: sha256.Sum256([]byte(nodeToken)), Capabilities: caps,
	}); !errors.Is(err, runnerhttp.ErrInvalid) {
		t.Fatal("HTTP configuration accepted unsupported assets capability")
	}
}

func TestAssetUploadRealTCPRejectsUnreadLargeRequestsWithoutWaitingForBody(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	var businessReads atomic.Int32
	var storageCalls atomic.Int32
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		context.Context, jobcontract.ContentIdentity, io.Reader,
	) (bool, error) {
		storageCalls.Add(1)
		return false, jobhost.ErrCapacity
	}))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = &countBusinessReads{ReadCloser: request.Body, reads: &businessReads}
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	for _, test := range []struct {
		name, auth, contract, media, query string
		length                             int64
		status                             int
	}{
		{name: "auth", contract: "1", media: "application/octet-stream", length: jobcontract.MaxAssetBytes, status: 401},
		{
			name:     "contract",
			auth:     nodeToken,
			contract: "0",
			media:    "application/octet-stream",
			length:   jobcontract.MaxAssetBytes,
			status:   426,
		},
		{
			name:     "query",
			auth:     nodeToken,
			contract: "1",
			media:    "application/octet-stream",
			query:    "?",
			length:   jobcontract.MaxAssetBytes,
			status:   400,
		},
		{
			name:     "type",
			auth:     nodeToken,
			contract: "1",
			media:    "application/json",
			length:   jobcontract.MaxAssetBytes,
			status:   415,
		},
		{
			name:     "size",
			auth:     nodeToken,
			contract: "1",
			media:    "application/octet-stream",
			length:   jobcontract.MaxAssetBytes + 1,
			status:   413,
		},
		{
			name:     "capacity",
			auth:     nodeToken,
			contract: "1",
			media:    "application/octet-stream",
			length:   jobcontract.MaxAssetBytes,
			status:   429,
		},
		{
			name:     "capacity_small_unread",
			auth:     nodeToken,
			contract: "1",
			media:    "application/octet-stream",
			length:   1024,
			status:   429,
		},
		{name: "invalid_small_unread", auth: nodeToken, contract: "1", media: "application/json", length: 1024, status: 415},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			// Send only headers for a large body, with no Expect handshake. A
			// rejection must not wait for the missing bytes or drain them first.
			_, err = fmt.Fprintf(connection,
				"PUT /assets/%s%s HTTP/1.1\r\nHost: node\r\nAuthorization: Bearer %s\r\n"+
					"X-LLM-Test-Contract: %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n",
				strings.Repeat("a", 64),
				test.query,
				test.auth,
				test.contract,
				test.media,
				test.length,
			)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPut})
			if err != nil {
				t.Fatalf("rejection waited for unread body: %v", err)
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode != test.status || !response.Close || businessReads.Load() != 0 {
				t.Fatalf("rejected body consumed or reused: code=%d close=%t reads=%d err=%v",
					response.StatusCode, response.Close, businessReads.Load(), readErr)
			}
		})
	}
	if storageCalls.Load() != 2 || fixture.requests.Load() != 0 {
		t.Fatal("precondition failures reached storage or provider")
	}
}

type countBusinessReads struct {
	io.ReadCloser
	reads *atomic.Int32
}

func (body *countBusinessReads) Read(buffer []byte) (int, error) {
	body.reads.Add(1)
	return body.ReadCloser.Read(buffer)
}

type delayedCloseAssetBody struct {
	readStarted, readClosed, closeStarted, allowClose chan struct{}
	readOnce                                          sync.Once
	closeCount                                        atomic.Int32
}

func (body *delayedCloseAssetBody) Read([]byte) (int, error) {
	body.readOnce.Do(func() { close(body.readStarted) })
	<-body.readClosed
	return 0, io.ErrClosedPipe
}

func (body *delayedCloseAssetBody) Close() error {
	body.closeCount.Add(1)
	close(body.readClosed)
	close(body.closeStarted)
	<-body.allowClose
	return nil
}

func assetMaintenanceRequest(
	t *testing.T,
	handler http.Handler,
	command jobcontract.MaintenanceCommand,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/runner/maintenance", bytes.NewReader(encode(t, command)))
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	request.Header.Set("Content-Type", "application/json")
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	return writer
}

func TestAssetUploadAndMaintenanceArbitrateUntilAllBodyClosingHasJoined(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	maintenanceStore := &memoryMaintenanceStore{}
	var err error
	fixture.maintenance, err = jobhost.NewMaintenance(t.Context(), fixture.host, maintenanceStore)
	if err != nil {
		t.Fatal(err)
	}
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		ctx context.Context, _ jobcontract.ContentIdentity, source io.Reader,
	) (bool, error) {
		_, err := io.Copy(io.Discard, source)
		return false, errors.Join(jobcontract.ErrAssetRead, err, ctx.Err())
	}))
	body := &delayedCloseAssetBody{
		readStarted: make(chan struct{}), readClosed: make(chan struct{}),
		closeStarted: make(chan struct{}), allowClose: make(chan struct{}),
	}
	allowClose := sync.OnceFunc(func() { close(body.allowClose) })
	defer allowClose()
	request := assetRequest(t, []byte("test"))
	request.Body = body
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan struct{})
	writer := newAssetWriter()
	go func() {
		handler.ServeHTTP(writer, request.WithContext(ctx))
		close(finished)
	}()
	select {
	case <-body.readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("body did not start")
	}
	command := jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		Action: jobcontract.MaintenancePause,
	}
	assertBusy := func() {
		t.Helper()
		response := assetMaintenanceRequest(t, handler, command)
		var failure runnerhttp.ErrorResponse
		err := json.Unmarshal(response.Body.Bytes(), &failure)
		if response.Code != 409 || err != nil || failure.Code != "upload_in_progress" || !failure.Retryable {
			t.Fatal("maintenance did not return the retryable active-upload decision")
		}
		stored, err := maintenanceStore.Read(t.Context())
		if err != nil || stored != nil || !fixture.host.Status().Ready {
			t.Fatal("active-upload refusal mutated maintenance state")
		}
	}
	assertBusy()
	cancel()
	select {
	case <-body.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not begin body close")
	}
	assertBusy() // Read has stopped; Close still owns the upload.
	allowClose()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("body close was not joined")
	}
	if writer.Code != 408 || body.closeCount.Load() != 1 {
		t.Fatal("canceled upload retained its body owner")
	}
	response := assetMaintenanceRequest(t, handler, command)
	stored, err := maintenanceStore.Read(t.Context())
	if response.Code != 200 || err != nil || stored == nil || !stored.Paused || fixture.host.Status().Ready {
		t.Fatal("finished upload did not release maintenance arbitration")
	}
	command.Action = jobcontract.MaintenanceResume
	response = assetMaintenanceRequest(t, handler, command)
	if response.Code != 200 || !fixture.host.Status().Ready {
		t.Fatal("maintenance resume did not reopen upload admission")
	}
}

func TestAssetUploadIsRejectedBeforeStorageWhenMaintenanceWins(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	var calls int
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		context.Context, jobcontract.ContentIdentity, io.Reader,
	) (bool, error) {
		calls++
		return true, nil
	}))
	command := jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		Action: jobcontract.MaintenancePause,
	}
	if response := assetMaintenanceRequest(t, handler, command); response.Code != 200 {
		t.Fatal("pause did not acquire maintenance arbitration")
	}
	request := assetRequest(t, []byte("test"))
	body := &observedBody{ReadCloser: request.Body}
	request.Body = body
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	if writer.Code != 423 || calls != 0 || body.reads.Load() != 0 {
		t.Fatal("upload entered after pause won")
	}
	_ = body.Close()
	command.Action = jobcontract.MaintenanceResume
	if response := assetMaintenanceRequest(t, handler, command); response.Code != 200 {
		t.Fatal("resume failed")
	}
	success := newAssetWriter()
	handler.ServeHTTP(success, assetRequest(t, []byte("test")))
	if success.Code != 201 || calls != 1 {
		t.Fatal("resume did not restore content admission")
	}
	command.OperationID = "66666666-6666-4666-8666-666666666666"
	command.Action = jobcontract.MaintenancePause
	if response := assetMaintenanceRequest(t, handler, command); response.Code != 200 {
		t.Fatal("successful upload leaked active upload ownership")
	}
}

type lostAssetReceiptWriter struct{ *assetWriter }

func (*lostAssetReceiptWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAssetUploadLostReceiptCanRetryOnlyTheSameContentIdentity(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	var saved []byte
	var calls int
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		_ context.Context, identity jobcontract.ContentIdentity, source io.Reader,
	) (bool, error) {
		calls++
		body, err := io.ReadAll(source)
		if err != nil {
			return false, jobcontract.ErrAssetRead
		}
		digest := sha256.Sum256(body)
		if identity.Size != int64(len(body)) || identity.SHA256 != hex.EncodeToString(digest[:]) {
			return false, jobcontract.ErrAssetIntegrity
		}
		created := saved == nil
		saved = bytes.Clone(body)
		return created, nil
	}))
	content := []byte("one immutable upload")
	lost := &lostAssetReceiptWriter{assetWriter: newAssetWriter()}
	handler.ServeHTTP(lost, assetRequest(t, content))
	if lost.Code != 201 || len(saved) == 0 {
		t.Fatal("failed acknowledgement changed storage success")
	}
	retry := newAssetWriter()
	handler.ServeHTTP(retry, assetRequest(t, content))
	if retry.Code != 200 || calls != 2 || !bytes.Equal(saved, content) || fixture.requests.Load() != 0 {
		t.Fatal("lost acknowledgement retry skipped body verification or duplicated execution")
	}
}

type heldAssetBody struct {
	io.Reader
	closing, allowClose chan struct{}
	closeCount          atomic.Int32
}

func (body *heldAssetBody) Close() error {
	body.closeCount.Add(1)
	close(body.closing)
	<-body.allowClose
	return nil
}

func TestAssetUploadSuccessDoesNotAcknowledgeCancellationDuringBodyClose(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	handler := contentHandler(t, fixture, contentStoreFunc(func(
		_ context.Context, _ jobcontract.ContentIdentity, source io.Reader,
	) (bool, error) {
		_, err := io.Copy(io.Discard, source)
		return true, err
	}))
	body := &heldAssetBody{
		Reader: strings.NewReader("test"), closing: make(chan struct{}), allowClose: make(chan struct{}),
	}
	allowClose := sync.OnceFunc(func() { close(body.allowClose) })
	defer allowClose()
	request := assetRequest(t, []byte("test"))
	request.Body = body
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := newAssetWriter()
	finished := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request.WithContext(ctx))
		close(finished)
	}()
	select {
	case <-body.closing:
	case <-time.After(2 * time.Second):
		t.Fatal("successful upload did not close its body")
	}
	cancel()
	command := jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		Action: jobcontract.MaintenancePause,
	}
	if response := assetMaintenanceRequest(t, handler, command); response.Code != 409 {
		t.Fatal("body closing was not part of active upload ownership")
	}
	allowClose()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("successful upload did not finish closing")
	}
	if writer.Code != 408 || body.closeCount.Load() != 1 {
		t.Fatal("canceled close acknowledged a successful upload")
	}
	if response := assetMaintenanceRequest(t, handler, command); response.Code != 200 {
		t.Fatal("canceled close leaked upload ownership")
	}
}
