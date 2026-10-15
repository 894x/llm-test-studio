package runnerhttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const nodeToken = "shared-node-test-token"

type testClock struct{}

func (testClock) Now() time.Time { return time.Now() }

type emptyReservation struct{}

func (emptyReservation) Close() error { return nil }

type testGuard struct{}

func (testGuard) Reserve(context.Context, jobcontract.JobSpec) (io.Closer, error) {
	return emptyReservation{}, nil
}

type httpFixture struct {
	repository  *sqlite.Repository
	host        *jobhost.Host
	maintenance *jobhost.Maintenance
	api         *httptest.Server
	upstream    *httptest.Server
	requests    atomic.Int64
	entered     chan struct{}
	release     func()
}

func capabilities() jobcontract.RunnerCapabilities {
	return jobcontract.RunnerCapabilities{
		JobKinds:  []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindPerformance, jobcontract.KindComparison},
		Protocols: []string{"openai-chat", "openai-responses", "anthropic-messages"},
		Features: []string{
			"warmup", "ramp", "capacity", "open_loop", "time_slices", "normal_workload", "random_input",
		},
	}
}

func newHTTPFixture(t *testing.T, blocked bool, caps jobcontract.RunnerCapabilities) *httpFixture {
	t.Helper()
	fixture := &httpFixture{entered: make(chan struct{})}
	release := make(chan struct{})
	fixture.release = sync.OnceFunc(func() { close(release) })
	fixture.upstream = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if fixture.requests.Add(1) == 1 {
			close(fixture.entered)
		}
		if blocked {
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = io.WriteString(writer,
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	t.Cleanup(fixture.upstream.Close)
	t.Cleanup(fixture.release)
	path := filepath.Join(t.TempDir(), "runner-http.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "http-test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.repository = repository
	t.Cleanup(func() { _ = repository.Close() })
	core, err := jobhost.NewCore(jobhost.CoreDependencies{
		Repository: repository, Runs: runs.NewProtocolExecutor(http.DefaultTransport),
		Transport: http.DefaultTransport, Clock: testClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	policy := jobhost.RemotePolicy()
	policy.ExecutionTimeout = 30 * time.Second
	fixture.host, err = jobhost.New(t.Context(), jobhost.Dependencies{
		Repository: repository, Executor: core, Guard: testGuard{}, Clock: testClock{}, Policy: policy,
		Target: jobcontract.ExecutionTarget{
			Kind: jobcontract.TargetRemote, NodeID: "11111111-1111-4111-8111-111111111111",
		},
		InstallationID: "22222222-2222-4222-8222-222222222222", EngineBuildID: strings.Repeat("b", 64),
		CommitmentKey: bytes.Repeat([]byte{3}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.host.Close() })
	fixture.maintenance, err = jobhost.NewMaintenance(t.Context(), fixture.host, &memoryMaintenanceStore{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: fixture.host, Maintenance: fixture.maintenance,
		Repository: repository, TokenSHA256: sha256.Sum256([]byte(nodeToken)),
		Version: "runner-test-1", Capabilities: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.api = httptest.NewServer(handler)
	t.Cleanup(fixture.api.Close)
	return fixture
}

func (fixture *httpFixture) submission(
	t *testing.T,
	configure func(*quicktest.PerformanceCommand),
) jobcontract.Submission {
	t.Helper()
	command := quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: fixture.upstream.URL, APIKey: "http-channel-test-secret", ModelID: "model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 5000, InputTokens: 32, OutputTokens: 2,
	}
	if configure != nil {
		configure(&command)
	}
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(), command)
	if code != "" {
		t.Fatal(code)
	}
	secret, err := lease.Bytes()
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	identity := fixture.host.Identity()
	submission := jobcontract.Submission{
		Job: jobcontract.JobSpec{
			Contract: jobcontract.CurrentContract, JobID: meta.ID,
			NodeInstallationID: identity.InstallationID, EngineBuildID: identity.EngineBuildID,
			Target: identity.Target, Kind: jobcontract.KindPerformance,
			Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
		},
		Secrets: []jobcontract.Secret{{Slot: prepared.ReportID, Value: secret}},
	}
	t.Cleanup(submission.Clear)
	return submission
}

func (fixture *httpFixture) request(t *testing.T, method, path string, document []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		fixture.api.URL+path,
		bytes.NewReader(document),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := fixture.api.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func waitHTTPJob(t *testing.T, fixture *httpFixture, id string, status domain.RunStatus) jobcontract.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := fixture.repository.GetJob(t.Context(), id)
		pending, pendingErr := fixture.repository.RecoveryJobs(t.Context())
		if err == nil && pendingErr == nil && len(pending) == 0 {
			if snapshot.Receipt.Status != status {
				t.Fatalf(
					"job=%s failure=%+v, want %s",
					snapshot.Receipt.Status,
					snapshot.Failure,
					status,
				)
			}
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("HTTP admitted job did not settle")
	return jobcontract.Snapshot{}
}

func TestHTTPNodeAuthorizationAndContractAreCheckedBeforeSubmission(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	for _, name := range []string{
		"missing", "wrong", "duplicate", "scheme", "long", "query",
		"contract-missing", "contract-duplicate", "contract-other", "contract-shape",
	} {
		t.Run(name, func(t *testing.T) {
			path := "/jobs"
			if name == "query" {
				path += "?token=" + nodeToken
			}
			request, _ := http.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				fixture.api.URL+path,
				strings.NewReader(`{"untrusted":"http-channel-test-secret"}`),
			)
			request.Header.Set("Authorization", "Bearer "+nodeToken)
			request.Header.Set(jobcontract.ContractHeader, "1")
			request.Header.Set("Content-Type", "application/json")
			want := http.StatusUnauthorized
			switch name {
			case "missing", "query":
				request.Header.Del("Authorization")
			case "wrong":
				request.Header.Set("Authorization", "Bearer incorrect")
			case "duplicate":
				request.Header.Add("Authorization", "Bearer "+nodeToken)
			case "scheme":
				request.Header.Set("Authorization", "Basic "+nodeToken)
			case "long":
				request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 257))
			default:
				want = http.StatusUpgradeRequired
				switch name {
				case "contract-missing":
					request.Header.Del(jobcontract.ContractHeader)
				case "contract-duplicate":
					request.Header.Add(jobcontract.ContractHeader, "1")
				case "contract-other":
					request.Header.Set(jobcontract.ContractHeader, "2")
				case "contract-shape":
					request.Header.Set(jobcontract.ContractHeader, "01")
				}
			}
			response, err := fixture.api.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != want || bytes.Contains(body, []byte(nodeToken)) ||
				bytes.Contains(body, []byte("http-channel-test-secret")) || fixture.requests.Load() != 0 {
				t.Fatalf("authorization or contract boundary failed: status=%d, want %d", response.StatusCode, want)
			}
		})
	}
}

func TestHTTPSharedNodeMetadataDoesNotRequireMatchingClientVersions(t *testing.T) {
	caps := capabilities()
	fixture := newHTTPFixture(t, false, caps)
	caps.Features[0], caps.Protocols[0] = "changed", "changed"
	for _, version := range []string{"client-1", "client-2-different-build"} {
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fixture.api.URL+"/runner",
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("User-Agent", version)
		response, err := fixture.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var info jobcontract.RunnerInfo
		err = json.NewDecoder(response.Body).Decode(&info)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || info.Validate() != nil ||
			info.RunnerVersion != "runner-test-1" || info.EngineBuildID != strings.Repeat("b", 64) {
			t.Fatalf("runner metadata changed for a different client version: %v", err)
		}
		if info.Capabilities.Protocols[0] == "changed" || info.Capabilities.Features[0] == "changed" {
			t.Fatal("metadata retained caller-owned capability slices")
		}
	}
}

func TestHTTPSubmitIsIdempotentAndIndependentOfSubmissionConnection(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	submission := fixture.submission(t, nil)
	document := encode(t, submission)
	ctx, cancel := context.WithCancel(t.Context())
	request, _ := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fixture.api.URL+"/jobs",
		bytes.NewReader(document),
	)
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := fixture.api.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	cancel()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("initial admission=%d", response.StatusCode)
	}
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted task did not run after its HTTP context ended")
	}
	retry := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		document,
	)
	if retry.StatusCode != http.StatusOK {
		t.Fatalf("identical submission=%d", retry.StatusCode)
	}
	submission.Secrets[0].Value = []byte("another-key")
	conflict := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	if conflict.StatusCode != http.StatusConflict {
		t.Fatal("same job ID with another secret was admitted")
	}
	fixture.release()
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	if fixture.requests.Load() != 2 {
		t.Fatal("submission retry repeated paid traffic")
	}
	state := fixture.request(
		t,
		http.MethodGet,
		"/jobs/"+submission.Job.JobID,
		nil,
	)
	if state.StatusCode != http.StatusOK {
		t.Fatal("accepted task could not be read")
	}
}

func TestHTTPSubmissionContentAndByteBoundariesDoNotExecuteRejectedRequests(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission := fixture.submission(t, nil)
	document := encode(t, submission)
	for _, contentType := range []string{
		"", "text/plain", "application/json; charset=latin1", "application/json; extra=value",
	} {
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fixture.api.URL+"/jobs",
			bytes.NewReader(document),
		)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response, err := fixture.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatal("unsupported submission content type was accepted")
		}
	}
	for _, invalid := range [][]byte{
		[]byte(`{"secret":"http-channel-test-secret"}`),
		append(append([]byte{}, document...), []byte(`{}`)...),
		bytes.Replace(
			document,
			[]byte(`"contract":1`),
			[]byte(`"contract":1,"contract":1`),
			1,
		),
		bytes.Replace(
			document,
			[]byte(`"contract":1`),
			[]byte(`"Contract":1`),
			1,
		),
	} {
		response := fixture.request(
			t,
			http.MethodPost,
			"/jobs",
			invalid,
		)
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusBadRequest || bytes.Contains(body, []byte("http-channel-test-secret")) {
			t.Fatal("malformed submission was accepted or echoed private input")
		}
	}
	maximum := append([]byte{}, document...)
	maximum = append(maximum, bytes.Repeat([]byte{' '}, jobcontract.MaxSubmissionBytes-len(document))...)
	oversized := append(append([]byte{}, maximum...), ' ')
	response := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		oversized,
	)
	if response.StatusCode != http.StatusRequestEntityTooLarge || fixture.requests.Load() != 0 {
		t.Fatal("oversized or otherwise rejected submission sent provider traffic")
	}
	response = fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		maximum,
	)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("exact submission byte limit rejected: %d", response.StatusCode)
	}
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
}

func TestHTTPClientsWithDifferentBuildsShareOneNodeQueueAndAuthorization(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	first, second := fixture.submission(t, nil), fixture.submission(t, nil)
	for index, submission := range []jobcontract.Submission{first, second} {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		client := &http.Client{Transport: transport}
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fixture.api.URL+"/jobs",
			bytes.NewReader(encode(t, submission)),
		)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", fmt.Sprintf("client-%d", index))
		request.Header.Set("X-Client-Build", strings.Repeat(fmt.Sprint(index), 64))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if response.StatusCode != http.StatusAccepted {
			t.Fatal("a different client build could not submit using the same node authorization")
		}
		if index == 0 {
			select {
			case <-fixture.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("first client task did not start")
			}
		}
	}
	if fixture.host.Status().Active != 1 || fixture.host.Status().Queued != 1 || fixture.requests.Load() != 1 {
		t.Fatal("clients received separate queues or escaped the shared activity limit")
	}
	fixture.release()
	waitHTTPJob(
		t,
		fixture,
		first.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPJob(
		t,
		fixture,
		second.Job.JobID,
		domain.RunCompleted,
	)
	if fixture.requests.Load() != 4 {
		t.Fatal("client disconnection or queue advancement lost or repeated requests")
	}
}
