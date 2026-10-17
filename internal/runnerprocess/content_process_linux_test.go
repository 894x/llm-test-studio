package runnerprocess_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func processContentIdentity(document []byte) jobcontract.ContentIdentity {
	digest := sha256.Sum256(document)
	return jobcontract.ContentIdentity{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(document))}
}

func processContentRequest(
	t *testing.T,
	ready runnerprocess.Ready,
	identity jobcontract.ContentIdentity,
	body io.Reader,
) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, ready.URL+"/assets/"+identity.SHA256, body)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = identity.Size
	request.Header.Set("Authorization", "Bearer "+processToken)
	request.Header.Set(jobcontract.ContractHeader, "1")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Content-Encoding", "identity")
	return request
}

func processContentReceipt(t *testing.T, response *http.Response, expected int) jobcontract.AssetReceipt {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != expected {
		t.Fatalf("content response status=%d, want=%d", response.StatusCode, expected)
	}
	receipt, err := jobcontract.DecodeAssetReceipt(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func stopContentProcess(t *testing.T, child *childProcess) {
	t.Helper()
	if err := child.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.done:
		if child.err != nil {
			t.Fatalf("runner did not shut down cleanly: %v", child.err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("runner did not join its upload handlers")
	}
}

type slowContentSource struct {
	ctx     context.Context
	source  *bytes.Reader
	started chan struct{}
	once    sync.Once
	waited  bool
}

func (source *slowContentSource) Read(buffer []byte) (int, error) {
	if source.waited {
		source.waited = false
		timer := time.NewTimer(31 * time.Second)
		defer timer.Stop()
		select {
		case <-source.ctx.Done():
			return 0, source.ctx.Err()
		case <-timer.C:
		}
	}
	first := false
	source.once.Do(func() { close(source.started); first = true })
	n, err := source.source.Read(buffer)
	if first {
		source.waited = true
	}
	return n, err
}

func TestRunnerContentSlowTCPUploadSurvivesManagementDeadlineAndRestart(t *testing.T) {
	path, config := newProcessConfig(t)
	config.MaxStorageBytes = 512 << 20
	document, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, runnerprocess.ConfigName), document, 0600); err != nil {
		t.Fatal(err)
	}
	clear(document)
	child := startRunner(t, path, "")
	infoResponse := processRequest(t, child.ready, "/runner", nil)
	var info jobcontract.RunnerInfo
	if err := json.NewDecoder(infoResponse.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	_ = infoResponse.Body.Close()
	validFeatures := slices.Contains(info.Capabilities.Features, jobcontract.FeatureContentUpload) &&
		!slices.Contains(info.Capabilities.Features, "assets")
	if !validFeatures || !info.Ready || !info.Healthy {
		t.Fatal("runner advertised inaccurate upload/execution capability")
	}
	content := bytes.Repeat([]byte("bounded-content-"), 8192)
	identity := processContentIdentity(content)
	source := &slowContentSource{ctx: t.Context(), source: bytes.NewReader(content), started: make(chan struct{})}
	request := processContentRequest(t, child.ready, identity, source)
	type uploadResult struct {
		response *http.Response
		err      error
	}
	result := make(chan uploadResult, 1)
	start := time.Now()
	go func() {
		response, err := http.DefaultClient.Do(request)
		result <- uploadResult{response: response, err: err}
	}()
	<-source.started
	response := processRequest(t, child.ready, "/runner", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal("ordinary management JSON was blocked by a slow body")
	}
	_ = response.Body.Close()
	completed := <-result
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	receipt := processContentReceipt(t, completed.response, http.StatusCreated)
	validReceipt := receipt.NodeID == child.ready.NodeID && receipt.NodeInstallationID == child.ready.NodeInstallationID &&
		receipt.SHA256 == identity.SHA256 && receipt.Size == identity.Size
	if !validReceipt || time.Since(start) < 30*time.Second {
		t.Fatal("slow real TCP upload did not prove the complete receipt after 30 seconds")
	}
	t.Logf(
		"real Linux process TCP upload: child_pid=%d executable_sha256=%s bytes=%d elapsed=%s; no SSH/provider",
		child.command.Process.Pid, child.ready.EngineBuildID, identity.Size, time.Since(start),
	)
	response, err = http.DefaultClient.Do(processContentRequest(t, child.ready, identity, bytes.NewReader(content)))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate := processContentReceipt(t, response, http.StatusOK); duplicate != receipt {
		t.Fatal("duplicate content changed its typed installation receipt")
	}
	response, err = http.DefaultClient.Do(processContentRequest(t, child.ready, identity, bytes.NewReader(bytes.Repeat([]byte("x"), len(content)))))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad body status=%d", response.StatusCode)
	}
	_ = response.Body.Close()
	assertContentProcessCapacityRefusal(t, child.ready)
	response = processRequest(t, child.ready, "/runner", nil)
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil || !info.Healthy {
		t.Fatal("bad body or soft content quota isolated the host")
	}
	_ = response.Body.Close()
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { providerCalls.Add(1) }))
	t.Cleanup(upstream.Close)
	submission := processSubmission(t, child.ready, upstream.URL)
	submission.Job.Assets = []jobcontract.Asset{{
		Ref: "file", SHA256: identity.SHA256, Size: identity.Size, MediaType: "application/octet-stream",
	}}
	document, err = json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	response = processRequest(t, child.ready, "/jobs", document)
	clear(document)
	if response.StatusCode != http.StatusUnprocessableEntity || providerCalls.Load() != 0 {
		t.Fatal("upload was incorrectly treated as asset execution support")
	}
	_ = response.Body.Close()
	stopContentProcess(t, child)
	state, err := runnerstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := state.OpenContent(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	reopened, readErr := io.ReadAll(file)
	closeErr := file.Close()
	stateErr := state.Close()
	if readErr != nil || closeErr != nil || stateErr != nil || !bytes.Equal(reopened, content) {
		t.Fatal("acknowledged content was not readable after process shutdown")
	}
	restarted := startRunner(t, path, "")
	response, err = http.DefaultClient.Do(processContentRequest(t, restarted.ready, identity, bytes.NewReader(content)))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate := processContentReceipt(t, response, http.StatusOK); duplicate != receipt {
		t.Fatal("restart changed immutable content receipt identity")
	}
	stopContentProcess(t, restarted)
}

func assertContentProcessCapacityRefusal(t *testing.T, ready runnerprocess.Ready) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(ready.URL, "http://"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintf(
		connection,
		"PUT /assets/%s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\n%s: 1\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n",
		strings.Repeat("a", 64), processToken, jobcontract.ContractHeader, jobcontract.MaxAssetBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("quota refusal waited for an unread 1GiB body: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || !response.Close {
		t.Fatalf("soft quota response=%d close=%v", response.StatusCode, response.Close)
	}
}

func TestRunnerContentShutdownJoinsBlockedUploadWithoutPublishing(t *testing.T) {
	path, _ := newProcessConfig(t)
	child := startRunner(t, path, "")
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(child.ready.URL, "http://"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = fmt.Fprintf(
		connection,
		"PUT /assets/%s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\n%s: 1\r\nContent-Type: application/octet-stream\r\nContent-Length: 1024\r\n\r\nx",
		strings.Repeat("a", 64), processToken, jobcontract.ContractHeader,
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		partials, err := filepath.Glob(filepath.Join(path, ".content-*.partial"))
		if err != nil {
			t.Fatal(err)
		}
		if len(partials) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload did not acquire a scratch file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopContentProcess(t, child)
	state, err := runnerstate.Open(path)
	if err != nil {
		t.Fatal("shutdown did not release state owner")
	}
	defer state.Close()
	count, err := state.ContentFiles(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("interrupted upload was deleted or published: count=%d err=%v", count, err)
	}
	complete, err := filepath.Glob(filepath.Join(path, "content-*.blob"))
	if err != nil || len(complete) != 0 {
		t.Fatal("blocked cancelled body was published")
	}
}

func TestRunnerContentDiskCorruptionIsolatesOwner(t *testing.T) {
	path, _ := newProcessConfig(t)
	child := startRunner(t, path, "")
	document := []byte("good")
	identity := processContentIdentity(document)
	response, err := http.DefaultClient.Do(processContentRequest(t, child.ready, identity, bytes.NewReader(document)))
	if err != nil {
		t.Fatal(err)
	}
	_ = processContentReceipt(t, response, http.StatusCreated)
	name := filepath.Join(path, "content-"+identity.SHA256+".blob")
	if err := os.WriteFile(name, []byte("bad!"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(processContentRequest(t, child.ready, identity, bytes.NewReader(document)))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("corrupt target was treated as a bad incoming upload: %d", response.StatusCode)
	}
	_ = response.Body.Close()
	select {
	case <-child.done:
		if child.err == nil {
			t.Fatal("storage isolation did not fail the process owner")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("real content corruption did not isolate the process owner")
	}
	retained, err := os.ReadFile(name)
	if err != nil || string(retained) != "bad!" {
		t.Fatal("uncertain corrupt content was replaced or deleted")
	}
}

func TestRunnerContentMaintenanceCannotConfirmWhileUploadOwnsState(t *testing.T) {
	path, _ := newProcessConfig(t)
	child := startRunner(t, path, "")
	identity := processContentIdentity([]byte("good"))
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(child.ready.URL, "http://"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = fmt.Fprintf(
		connection,
		"PUT /assets/%s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\n%s: 1\r\nContent-Type: application/octet-stream\r\nContent-Length: 4\r\n\r\ng",
		identity.SHA256, processToken, jobcontract.ContractHeader,
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		partials, err := filepath.Glob(filepath.Join(path, ".content-*.partial"))
		if err != nil {
			t.Fatal(err)
		}
		if len(partials) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload did not enter its state owner")
		}
		time.Sleep(10 * time.Millisecond)
	}
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	command := jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: meta.ID, Action: jobcontract.MaintenancePause,
	}
	document, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	response := processRequest(t, child.ready, "/runner/maintenance", document)
	var busy struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&busy); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict || busy.Code != "upload_in_progress" || !busy.Retryable {
		t.Fatal("in-flight upload was acknowledged as quiescent maintenance")
	}
	if _, err := os.Stat(filepath.Join(path, "maintenance.json")); !os.IsNotExist(err) {
		t.Fatal("busy pause wrote a maintenance record")
	}
	_ = connection.Close()
	deadline = time.Now().Add(3 * time.Second)
	for {
		response = processRequest(t, child.ready, "/runner/maintenance", document)
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK {
			break
		}
		if response.StatusCode != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("drained upload did not permit pause: %d", response.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err = http.DefaultClient.Do(processContentRequest(t, child.ready, identity, strings.NewReader("good")))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusLocked {
		t.Fatal("pause winner did not reject subsequent content admission")
	}
	partials, err := filepath.Glob(filepath.Join(path, ".content-*.partial"))
	if err != nil || len(partials) != 1 {
		t.Fatal("paused upload created another scratch file")
	}
	command.Action = jobcontract.MaintenanceResume
	document, err = json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	response = processRequest(t, child.ready, "/runner/maintenance", document)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("maintenance could not resume after the upload drained")
	}
	response, err = http.DefaultClient.Do(processContentRequest(t, child.ready, identity, strings.NewReader("good")))
	if err != nil {
		t.Fatal(err)
	}
	_ = processContentReceipt(t, response, http.StatusCreated)
	stopContentProcess(t, child)
}
