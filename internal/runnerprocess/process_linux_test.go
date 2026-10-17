package runnerprocess_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/enginebuild"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
)

const processToken = "runner-process-test-token-0000000000000000000000000000000000000000"

func TestRunnerProcessHelper(t *testing.T) {
	if os.Getenv("LLM_TEST_RUNNER_CHILD") != "1" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	err := runnerprocess.Run(ctx, runnerprocess.Options{
		StatePath: os.Getenv("LLM_TEST_RUNNER_STATE"), Version: "process-test",
		OnReady: func(ready runnerprocess.Ready) error { return json.NewEncoder(os.Stdout).Encode(ready) },
	})
	if err != nil {
		os.Exit(75)
	}
}

type childProcess struct {
	command *exec.Cmd
	ready   runnerprocess.Ready
	done    chan struct{}
	err     error
}

func startRunner(t *testing.T, path string, notifySocket string) *childProcess {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestRunnerProcessHelper$")
	command.Env = append(
		os.Environ(),
		"LLM_TEST_RUNNER_CHILD=1",
		"LLM_TEST_RUNNER_STATE="+path,
		"NOTIFY_SOCKET="+notifySocket,
	)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	child := &childProcess{command: command, done: make(chan struct{})}
	go func() { child.err = command.Wait(); close(child.done) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-child.done })
	readyResult := make(chan error, 1)
	go func() { readyResult <- json.NewDecoder(output).Decode(&child.ready) }()
	select {
	case err := <-readyResult:
		if err != nil {
			t.Fatalf("runner readiness failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runner did not report readiness")
	}
	return child
}

func newProcessConfig(t *testing.T) (string, runnerconfig.Config) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	config, err := runnerconfig.New([]byte(processToken))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(config.Clear)
	config.ListenPort = uint16(port)
	path := filepath.Join(t.TempDir(), "state")
	if err := runnerprocess.Initialize(path, config); err != nil {
		t.Fatal(err)
	}
	if err := runnerprocess.Initialize(path, config); err == nil {
		t.Fatal("second initializer replaced shared node identity")
	}
	return path, config
}

func processRequest(t *testing.T, ready runnerprocess.Ready, path string, document []byte) *http.Response {
	t.Helper()
	method := http.MethodGet
	if document != nil {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		ready.URL+path,
		bytes.NewReader(document),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+processToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func processSubmission(t *testing.T, ready runnerprocess.Ready, url string) jobcontract.Submission {
	t.Helper()
	service := quicktest.New(quicktest.Dependencies{})
	prepared, lease, code := service.BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: url, APIKey: "runner-process-channel-secret", ModelID: "model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 30_000, InputTokens: 32, OutputTokens: 2,
	})
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
	submission := jobcontract.Submission{
		Job: jobcontract.JobSpec{
			Contract: jobcontract.CurrentContract, JobID: meta.ID, NodeInstallationID: ready.NodeInstallationID,
			EngineBuildID: ready.EngineBuildID,
			Target:        jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: ready.NodeID},
			Kind:          jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared},
			Assets: []jobcontract.Asset{},
		},
		Secrets: []jobcontract.Secret{{Slot: prepared.ReportID, Value: secret}},
	}
	t.Cleanup(submission.Clear)
	return submission
}

func TestIndependentRunnerRecoversKilledOwnerWithoutRepeatingPaidWork(t *testing.T) {
	path, config := newProcessConfig(t)
	var requests atomic.Int64
	entered := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if requests.Add(1) == 1 {
			close(entered)
		}
		<-request.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	child := startRunner(t, path, "")
	if child.ready.NodeID != config.NodeID || child.ready.NodeInstallationID != config.InstallationID {
		t.Fatal("process replaced persistent node identity")
	}
	submission := processSubmission(t, child.ready, upstream.URL)
	document, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(document)
	response := processRequest(
		t,
		child.ready,
		"/jobs",
		document,
	)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("admission failed: %d", response.StatusCode)
	}
	_ = response.Body.Close()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("independent process did not send the request")
	}
	if err := child.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-child.done
	restarted := startRunner(t, path, "")
	if restarted.ready.NodeInstallationID != child.ready.NodeInstallationID ||
		restarted.ready.EngineBuildID != child.ready.EngineBuildID {
		t.Fatal("restart changed installation or actual executable identity")
	}
	stateResponse := processRequest(
		t,
		restarted.ready,
		"/jobs/"+submission.Job.JobID,
		nil,
	)
	var snapshot jobcontract.Snapshot
	if err := json.NewDecoder(stateResponse.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	interrupted := snapshot.Receipt.Status == domain.RunFailed && snapshot.Failure != nil &&
		snapshot.Failure.ErrorCode == "runner_interrupted"
	if !interrupted {
		t.Fatal("restart did not preserve explicit owner interruption")
	}
	reportResponse := processRequest(
		t,
		restarted.ready,
		"/jobs/"+submission.Job.JobID+"/reports/"+submission.Job.Spec.Performance.ReportID,
		nil,
	)
	var result jobcontract.PerformanceResult
	if err := json.NewDecoder(reportResponse.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	validResult := reportResponse.StatusCode == http.StatusOK &&
		result.ObservationState == jobcontract.PerformanceInterrupted && requests.Load() == 1
	if !validResult {
		t.Fatal("restart lost the partial observation or repeated provider traffic")
	}
	if err := restarted.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted.done:
		if restarted.err != nil {
			t.Fatalf("graceful shutdown failed: %v", restarted.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "runner.db") {
			stored, err := os.ReadFile(filepath.Join(path, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(stored, []byte("runner-process-channel-secret")) {
				t.Fatal("process persisted an accepted channel credential")
			}
		}
	}
}

func TestIndependentRunnerPublishesActualBuildAndLoopbackMetadata(t *testing.T) {
	path, _ := newProcessConfig(t)
	child := startRunner(t, path, "")
	response := processRequest(
		t,
		child.ready,
		"/runner",
		nil,
	)
	var info jobcontract.RunnerInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil || info.Validate() != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	buildID, err := enginebuild.Current()
	validIdentity := err == nil && buildID == info.EngineBuildID && info.RunnerVersion == "process-test" && info.Ready
	if !validIdentity {
		t.Fatal("process did not report its actual executable and readiness")
	}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(child.ready.URL, "http://"))
	number, _ := strconv.Atoi(port)
	validListener := err == nil && host == "127.0.0.1" && number >= 1024
	if !validListener {
		t.Fatal("management listener is not confined to loopback")
	}
}
