package runnerprocess_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestRunnerStartsAboveSoftQuotaAndRestoresAdmissionWithoutRestart(t *testing.T) {
	path, config := newProcessConfig(t)
	filler := writeBudgetFiller(t, path, config.MaxStorageBytes)
	child := startRunner(t, path, "")
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writePerformanceReply(writer)
	}))
	defer upstream.Close()
	submission := processSubmission(t, child.ready, upstream.URL)
	assertHealthyProcess(t, child)
	assertAdmissionRejected(t, child, submission)
	if requests.Load() != 0 {
		t.Fatal("quota-rejected submission sent provider traffic")
	}
	if err := os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	submitProcessJob(t, child, submission)
	waitProcessJobCompleted(t, child, submission)
	if requests.Load() != 2 {
		t.Fatalf("recovered admission sent %d requests, want two", requests.Load())
	}
	assertHealthyProcess(t, child)
}

func TestSoftQuotaKeepsWatchdogReadsAndAcceptedExecutionAlive(t *testing.T) {
	path, config := newProcessConfig(t)
	socket := filepath.Join(t.TempDir(), "s")
	connection, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	child := startRunner(t, path, socket)
	for _, expected := range []string{"READY=1", "WATCHDOG=1"} {
		waitProcessNotification(t, connection, expected)
	}
	var requests atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		writePerformanceReply(writer)
	}))
	t.Cleanup(upstream.Close)
	accepted := processSubmission(t, child.ready, upstream.URL)
	submitProcessJob(t, child, accepted)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted job did not start")
	}
	filler := writeBudgetFiller(t, path, config.MaxStorageBytes)
	// Two subsequent ticks exclude a notification already queued before the filler.
	for range 2 {
		waitProcessNotification(t, connection, "WATCHDOG=1")
	}
	assertHealthyProcess(t, child)
	snapshot := readProcessJob(t, child, accepted.Job.JobID)
	if snapshot.Receipt.Status != domain.RunRunning {
		t.Fatalf("soft quota changed the accepted job to %s", snapshot.Receipt.Status)
	}
	rejected := processSubmission(t, child.ready, upstream.URL)
	assertAdmissionRejected(t, child, rejected)
	if requests.Load() != 1 {
		t.Fatal("rejected admission sent traffic or cancelled the active job")
	}
	release <- struct{}{}
	waitProcessJobCompleted(t, child, accepted)
	waitProcessSettled(t, child)
	if requests.Load() != 2 {
		t.Fatalf("accepted job sent %d requests, want two", requests.Load())
	}
	if _, err := os.Stat(filler); err != nil {
		t.Fatal("runner removed data to satisfy its soft quota")
	}
	if err := os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	submitProcessJob(t, child, rejected)
	waitProcessJobCompleted(t, child, rejected)
	if requests.Load() != 4 {
		t.Fatalf("restored intake sent %d requests, want four", requests.Load())
	}
	assertHealthyProcess(t, child)
}

func TestWatchdogIsolatesRealStorageFaultEvenAboveSoftQuota(t *testing.T) {
	for _, fault := range []string{"usage_scan", "database_write"} {
		t.Run(fault, func(t *testing.T) {
			path, config := newProcessConfig(t)
			child := startRunner(t, path, "")
			writeBudgetFiller(t, path, config.MaxStorageBytes)
			if fault == "usage_scan" {
				if err := os.Symlink("runner.db", filepath.Join(path, "invalid-link")); err != nil {
					t.Fatal(err)
				}
			} else {
				database, err := sql.Open("sqlite", filepath.Join(path, "runner.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()
				if _, err := database.ExecContext(t.Context(), "DROP TABLE execution_jobs"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-child.done:
				if child.err == nil {
					t.Fatal("real storage fault did not isolate the service")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("soft quota masked a real storage fault")
			}
		})
	}
}

func writeBudgetFiller(t *testing.T, path string, limit uint64) string {
	t.Helper()
	name := filepath.Join(path, "budget-test")
	filler, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := filler.Truncate(int64(limit) + 1); err != nil {
		_ = filler.Close()
		t.Fatal(err)
	}
	if err := filler.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func waitProcessNotification(t *testing.T, connection *net.UnixConn, expected string) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	message := make([]byte, 128)
	size, _, err := connection.ReadFromUnix(message)
	if err != nil || string(message[:size]) != expected {
		t.Fatalf("notification=%q, want %q: %v", message[:size], expected, err)
	}
}

func assertHealthyProcess(t *testing.T, child *childProcess) jobcontract.RunnerInfo {
	t.Helper()
	select {
	case <-child.done:
		t.Fatalf("runner stopped: %v", child.err)
	default:
	}
	response := processRequest(t, child.ready, "/runner", nil)
	defer response.Body.Close()
	info, err := jobcontract.DecodeRunnerInfo(response.Body)
	validResponse := response.StatusCode == http.StatusOK && err == nil
	if !validResponse || !info.Healthy || !info.Ready {
		t.Fatalf("runner lost health: HTTP %d, %v", response.StatusCode, err)
	}
	return info
}

func waitProcessSettled(t *testing.T, child *childProcess) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		info := assertHealthyProcess(t, child)
		if info.Pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("accepted job did not settle: pending=%d", info.Pending)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertAdmissionRejected(t *testing.T, child *childProcess, submission jobcontract.Submission) {
	t.Helper()
	document, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(document)
	response := processRequest(t, child.ready, "/jobs", document)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("soft quota admission HTTP %d, want 429", response.StatusCode)
	}
	response = processRequest(t, child.ready, "/jobs/"+submission.Job.JobID, nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("quota-rejected submission left a durable job")
	}
}

func submitProcessJob(t *testing.T, child *childProcess, submission jobcontract.Submission) {
	t.Helper()
	document, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(document)
	response := processRequest(t, child.ready, "/jobs", document)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("admission HTTP %d, want 202", response.StatusCode)
	}
}

func readProcessJob(t *testing.T, child *childProcess, id string) jobcontract.Snapshot {
	t.Helper()
	response := processRequest(t, child.ready, "/jobs/"+id, nil)
	defer response.Body.Close()
	var snapshot jobcontract.Snapshot
	if response.StatusCode != http.StatusOK {
		t.Fatalf("job read HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func waitProcessJobCompleted(t *testing.T, child *childProcess, submission jobcontract.Submission) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot := readProcessJob(t, child, submission.Job.JobID)
		if snapshot.Receipt.Status == domain.RunCompleted {
			break
		}
		failed := snapshot.Receipt.Status == domain.RunFailed || snapshot.Receipt.Status == domain.RunCancelled
		if failed || time.Now().After(deadline) {
			t.Fatalf("accepted job did not complete: %s", snapshot.Receipt.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	path := "/jobs/" + submission.Job.JobID + "/reports/" + submission.Job.Spec.Performance.ReportID
	for {
		response := processRequest(t, child.ready, path, nil)
		if response.StatusCode == http.StatusOK {
			var result jobcontract.PerformanceResult
			err := json.NewDecoder(response.Body).Decode(&result)
			_ = response.Body.Close()
			validResult := err == nil && result.Validate() == nil && result.Terminal.Validate(submission.Job) == nil
			if !validResult {
				t.Fatalf("invalid completed report: %v", err)
			}
			return
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound || time.Now().After(deadline) {
			t.Fatalf("accepted job report HTTP %d", response.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writePerformanceReply(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
	_, _ = io.WriteString(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n")
	_, _ = io.WriteString(writer, "data: [DONE]\n\n")
}
