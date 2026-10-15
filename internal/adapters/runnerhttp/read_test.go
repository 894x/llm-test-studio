package runnerhttp_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestHTTPControlsConfirmDurableCommandsAndDoNotRepeatRequests(t *testing.T) {
	for _, kind := range []jobcontract.CommandKind{jobcontract.CommandStopSending, jobcontract.CommandCancel} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newHTTPFixture(t, true, capabilities())
			submission := fixture.submission(t, nil)
			response := fixture.request(
				t,
				http.MethodPost,
				"/jobs",
				encode(t, submission),
			)
			if response.StatusCode != http.StatusAccepted {
				t.Fatal("control fixture was not admitted")
			}
			select {
			case <-fixture.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			current, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
			if err != nil {
				t.Fatal(err)
			}
			meta, _ := domain.NewEntityMeta(time.Now().UTC())
			command := jobcontract.Command{
				Contract: jobcontract.CurrentContract, CommandID: meta.ID, JobID: submission.Job.JobID,
				Kind: kind, ExpectedRevision: current.Receipt.Revision,
			}
			path := "/jobs/" + command.JobID + "/cancel"
			if kind == jobcontract.CommandStopSending {
				path = "/jobs/" + command.JobID + "/stop-sending"
			}
			first := fixture.request(
				t,
				http.MethodPost,
				path,
				encode(t, command),
			)
			firstBody, _ := io.ReadAll(first.Body)
			if first.StatusCode != http.StatusOK {
				t.Fatalf("control status=%d", first.StatusCode)
			}
			want := domain.RunCancelled
			if kind == jobcontract.CommandStopSending {
				fixture.release()
				want = domain.RunCompleted
			}
			waitHTTPJob(
				t,
				fixture,
				command.JobID,
				want,
			)
			retry := fixture.request(
				t,
				http.MethodPost,
				path,
				encode(t, command),
			)
			retryBody, _ := io.ReadAll(retry.Body)
			if retry.StatusCode != http.StatusOK || !bytes.Equal(firstBody, retryBody) || fixture.requests.Load() != 1 {
				t.Fatal("command retry changed confirmation or restarted provider traffic")
			}
			meta, _ = domain.NewEntityMeta(time.Now().UTC())
			command.CommandID = meta.ID
			terminalCommand := fixture.request(
				t,
				http.MethodPost,
				path,
				encode(t, command),
			)
			if terminalCommand.StatusCode != http.StatusConflict {
				t.Fatal("terminal job accepted a new control command")
			}
		})
	}
}

func TestHTTPEventsRejectInvalidQueriesAndAllowEmptyLongPoll(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission := fixture.submission(t, nil)
	fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	final := waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	path := "/jobs/" + submission.Job.JobID + "/events"
	for _, query := range []string{
		"", "?limit=1", "?after_sequence=0", "?after_sequence=0&limit=0", "?after_sequence=0&limit=201",
		"?after_sequence=-1&limit=1", "?after_sequence=0&limit=1&limit=2", "?after_sequence=0&limit=1&other=1",
		"?after_sequence=0&limit=1&wait_ms=-1", "?after_sequence=0&limit=1&wait_ms=20001",
		"?after_sequence=0&limit=1&wait_ms=", "?after_sequence=01&limit=1", "?after_sequence=0&limit=1;other=2",
		"?after_sequence=" + strconv.FormatUint(final.LatestSequence+1, 10) + "&limit=1",
	} {
		response := fixture.request(
			t,
			http.MethodGet,
			path+query,
			nil,
		)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid events query %q returned %d", query, response.StatusCode)
		}
	}
	for _, limit := range []string{"1", "200"} {
		response := fixture.request(
			t,
			http.MethodGet,
			path+"?after_sequence=0&limit="+limit,
			nil,
		)
		document, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK || bytes.Contains(document, []byte("http-channel-test-secret")) {
			t.Fatal("valid event page failed or contained task credentials")
		}
	}
	response := fixture.request(
		t,
		http.MethodGet,
		path+"?after_sequence="+strconv.FormatUint(final.LatestSequence, 10)+"&limit=1&wait_ms=10",
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatal("empty long poll was reported as a failure")
	}
	if fixture.requests.Load() != 2 {
		t.Fatal("event polling caused provider traffic")
	}
}

func TestHTTPPerformanceDownloadUsesOriginalCanonicalBytesAndRanges(t *testing.T) {
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
	sealed, err := fixture.repository.ReadPerformanceResult(t.Context(), submission.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/jobs/" + submission.Job.JobID + "/reports/" + submission.Job.Spec.Performance.ReportID
	response := fixture.request(
		t,
		http.MethodGet,
		path,
		nil,
	)
	document, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Equal(document, sealed.Document) ||
		response.Header.Get("X-Content-SHA256") != sealed.Reference.SHA256 ||
		response.ContentLength != sealed.Reference.Size {
		t.Fatal("download did not use exactly the stored result bytes and integrity metadata")
	}
	for _, value := range []string{"bytes=0-15", "bytes=-16", "bytes=16-", "bytes=0-0"} {
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fixture.api.URL+path,
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Range", value)
		partial, err := fixture.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(partial.Body)
		_ = partial.Body.Close()
		expected := document[:16]
		switch value {
		case "bytes=-16":
			expected = document[len(document)-16:]
		case "bytes=16-":
			expected = document[16:]
		case "bytes=0-0":
			expected = document[:1]
		}
		if partial.StatusCode != http.StatusPartialContent || !bytes.Equal(body, expected) {
			t.Fatalf("byte range %q did not return original bytes", value)
		}
	}
	for _, value := range []string{
		"bytes=-0", "bytes=2-1", "bytes=0-1,3-4", "bytes=" + strconv.Itoa(len(document)) + "-",
	} {
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fixture.api.URL+path,
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Range", value)
		invalid, err := fixture.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = invalid.Body.Close()
		if invalid.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("invalid range %q returned %d", value, invalid.StatusCode)
		}
	}
	other := fixture.request(
		t,
		http.MethodGet,
		"/jobs/"+submission.Job.JobID+"/reports/33333333-3333-4333-8333-333333333333",
		nil,
	)
	if other.StatusCode != http.StatusNotFound || fixture.requests.Load() != 2 {
		t.Fatal("another report was disclosed or download repeated provider traffic")
	}
}

func TestHTTPRejectsMissingTaskCapabilitiesBeforeAdmission(t *testing.T) {
	for _, feature := range []string{
		"kind", "protocol", "warmup", "ramp", "capacity", "open_loop", "time_slices", "normal_workload", "random_input",
	} {
		t.Run(feature, func(t *testing.T) {
			caps := capabilities()
			for index, name := range caps.Features {
				if name == feature {
					caps.Features = append(caps.Features[:index], caps.Features[index+1:]...)
					break
				}
			}
			if feature == "kind" {
				caps.JobKinds = []jobcontract.Kind{jobcontract.KindRun}
			}
			if feature == "protocol" {
				caps.Protocols = []string{"anthropic-messages"}
			}
			fixture := newHTTPFixture(t, false, caps)
			submission := fixture.submission(t, func(command *quicktest.PerformanceCommand) {
				switch feature {
				case "warmup":
					command.WarmupRequests = 1
				case "ramp":
					command.RampDurationMS, command.RampRequestCap = 20, 20
				case "capacity":
					command.CapacityEnabled, command.CapacityStart, command.CapacityStep = true, 1, 1
					command.SLOE2EMS, command.SLOTargetPercent = 1000, 100
				case "open_loop":
					command.LoadMode, command.Concurrency = domain.LoadOpenLoop, 0
					command.RatePerSecond, command.MaxInFlight = 100, 2
				case "time_slices":
					command.SliceDurationMS = 10
				case "normal_workload":
					command.WorkloadMode, command.RandomSeed = quicktest.PerformanceWorkloadNormal, 7
					command.InputTokensStdDev, command.OutputTokensStdDev = 2, 1
				case "random_input":
					command.RandomInput = true
				}
			})
			response := fixture.request(
				t,
				http.MethodPost,
				"/jobs",
				encode(t, submission),
			)
			if response.StatusCode != http.StatusUnprocessableEntity || fixture.requests.Load() != 0 {
				t.Fatal("a task missing runner capabilities was executed")
			}
			_, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
			if !errors.Is(err, jobhost.ErrNotFound) {
				t.Fatal("capability rejection created a job")
			}
		})
	}
}

func TestHTTPOwnerChecksAllowPreviousRunnerBuildButExcludeOtherInstallations(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	for _, foreign := range []bool{false, true} {
		submission := fixture.submission(t, nil)
		submission.Job.EngineBuildID = strings.Repeat("c", 64)
		if foreign {
			submission.Job.NodeInstallationID = "33333333-3333-4333-8333-333333333333"
		}
		if _, _, err := fixture.repository.AcceptJob(t.Context(), jobhost.Acceptance{
			Spec: submission.Job, SecretCommitment: strings.Repeat("d", 64), AcceptedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		response := fixture.request(
			t,
			http.MethodGet,
			"/jobs/"+submission.Job.JobID,
			nil,
		)
		want := http.StatusOK
		if foreign {
			want = http.StatusNotFound
		}
		if response.StatusCode != want {
			t.Fatal("read ownership incorrectly depended on the current runner build or exposed another installation")
		}
	}
}
