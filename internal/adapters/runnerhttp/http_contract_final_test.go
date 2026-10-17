package runnerhttp_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestHTTPPayloadUnionRejectionsDoNotCreateJobsOrExecuteRequests(t *testing.T) {
	for _, kind := range []jobcontract.Kind{
		jobcontract.KindRun, jobcontract.KindPerformance, jobcontract.KindComparison,
	} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newHTTPFixture(t, false, capabilities())
			submission := fixture.submission(t, nil)
			if kind != jobcontract.KindPerformance {
				submission = fixture.runSubmission(t, kind == jobcontract.KindComparison)
			}
			document := encode(t, submission)
			baseline, err := jobcontract.DecodeSubmission(bytes.NewReader(document))
			if err != nil {
				t.Fatal("payload fixture does not satisfy the current submission contract")
			}
			baseline.Clear()
			extra := fixture.runSubmission(t, false)
			extraKey := "run"
			extraPayload := encode(t, extra.Job.Spec.Run)
			if kind == jobcontract.KindRun {
				extra = fixture.submission(t, nil)
				extraKey = "performance"
				extraPayload = encode(t, extra.Job.Spec.Performance)
			}
			for _, test := range []struct {
				name  string
				value json.RawMessage
				extra bool
			}{
				{name: "null", value: json.RawMessage(`null`)},
				{name: "array_instead_of_object", value: json.RawMessage(`[]`)},
				{name: "multiple_payloads", extra: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					var root, job, payload map[string]json.RawMessage
					if err := json.Unmarshal(document, &root); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(root["job"], &job); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(job["spec"], &payload); err != nil {
						t.Fatal(err)
					}
					if test.extra {
						payload[extraKey] = extraPayload
					} else {
						payload[string(kind)] = test.value
					}
					job["spec"] = encode(t, payload)
					root["job"] = encode(t, job)
					response := fixture.request(
						t,
						http.MethodPost,
						"/jobs",
						encode(t, root),
					)
					var rejection runnerhttp.ErrorResponse
					err := json.NewDecoder(response.Body).Decode(&rejection)
					_ = response.Body.Close()
					validRejection := response.StatusCode == http.StatusBadRequest && rejection.Code == "invalid_request"
					if err != nil || !validRejection {
						t.Fatalf("invalid payload status=%d code=%q decode=%v", response.StatusCode, rejection.Code, err)
					}
					_, err = fixture.repository.GetJob(t.Context(), submission.Job.JobID)
					if !errors.Is(err, jobhost.ErrNotFound) {
						t.Fatalf("rejected payload left a durable job: %v", err)
					}
					listing := fixture.request(
						t,
						http.MethodGet,
						"/jobs?after=0&limit=1",
						nil,
					)
					var page jobhost.JobPage
					err = json.NewDecoder(listing.Body).Decode(&page)
					_ = listing.Body.Close()
					validListing := listing.StatusCode == http.StatusOK && len(page.Jobs) == 0 && !page.HasMore
					if err != nil || !validListing {
						t.Fatal("payload rejection changed the node job listing")
					}
					status := fixture.host.Status()
					idle := status.Active == 0 && status.Queued == 0
					if !idle || fixture.requests.Load() != 0 {
						t.Fatal("payload rejection started or queued execution")
					}
				})
			}
		})
	}
}

func TestHTTPConcurrentDifferentControlsAtOneRevisionConfirmOnlyOneDecision(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	submission := fixture.submission(t, nil)
	admitted := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	_ = admitted.Body.Close()
	if admitted.StatusCode != http.StatusAccepted {
		t.Fatal("control race fixture was not admitted")
	}
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("control race did not hold an in-flight provider request")
	}
	before, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	if err != nil || before.Receipt.Status != domain.RunRunning {
		t.Fatalf("control race requires running state: %v", err)
	}
	type outcome struct {
		command jobcontract.Command
		path    string
		status  int
		body    []byte
		err     error
	}
	ready, start := make(chan struct{}, 2), make(chan struct{})
	results := make(chan outcome, 2)
	for _, kind := range []jobcontract.CommandKind{jobcontract.CommandCancel, jobcontract.CommandStopSending} {
		meta, err := domain.NewEntityMeta(time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		command := jobcontract.Command{
			Contract: jobcontract.CurrentContract, CommandID: meta.ID, JobID: submission.Job.JobID,
			Kind: kind, ExpectedRevision: before.Receipt.Revision,
		}
		path := "/jobs/" + command.JobID + "/cancel"
		if kind == jobcontract.CommandStopSending {
			path = "/jobs/" + command.JobID + "/stop-sending"
		}
		request, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fixture.api.URL+path,
			bytes.NewReader(encode(t, command)),
		)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Content-Type", "application/json")
		transport := http.DefaultTransport.(*http.Transport).Clone()
		t.Cleanup(transport.CloseIdleConnections)
		client := &http.Client{Transport: transport}
		go func() {
			ready <- struct{}{}
			<-start
			result := outcome{command: command, path: path}
			response, err := client.Do(request)
			if err != nil {
				result.err = err
				results <- result
				return
			}
			result.status = response.StatusCode
			result.body, result.err = io.ReadAll(response.Body)
			_ = response.Body.Close()
			results <- result
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-t.Context().Done():
			t.Fatal("control clients did not reach the start barrier")
		}
	}
	close(start)
	var winner, loser outcome
	var confirmed, conflicted int
	for range 2 {
		var result outcome
		select {
		case result = <-results:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent HTTP controls did not return")
		}
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.status {
		case http.StatusOK:
			winner = result
			confirmed++
		case http.StatusConflict:
			loser = result
			conflicted++
		default:
			t.Fatalf("unexpected concurrent control status=%d", result.status)
		}
	}
	oneDecision := confirmed == 1 && conflicted == 1
	distinctCommands := winner.command.CommandID != loser.command.CommandID
	if !oneDecision || !distinctCommands {
		t.Fatal("same-revision distinct HTTP commands did not yield one winner and one conflict")
	}
	confirmation, err := jobcontract.DecodeCommandConfirmation(bytes.NewReader(winner.body))
	if err != nil || confirmation.Command != winner.command {
		t.Fatalf("winner did not return its exact command confirmation: %v", err)
	}
	if confirmation.Snapshot.Receipt.Revision != before.Receipt.Revision+1 {
		t.Fatal("winning command did not advance the expected revision exactly once")
	}
	var rejection runnerhttp.ErrorResponse
	if err := json.Unmarshal(loser.body, &rejection); err != nil || rejection.Code != "job_conflict" {
		t.Fatal("losing control did not return the current conflict contract")
	}
	stored, err := fixture.repository.GetJobCommand(t.Context(), winner.command.CommandID)
	if err != nil || !bytes.Equal(encode(t, stored), encode(t, confirmation)) {
		t.Fatalf("HTTP winner differs from its durable confirmation: %v", err)
	}
	_, err = fixture.repository.GetJobCommand(t.Context(), loser.command.CommandID)
	if !errors.Is(err, jobhost.ErrNotFound) {
		t.Fatal("conflicting HTTP command left a durable confirmation")
	}
	want := domain.RunCancelled
	if winner.command.Kind == jobcontract.CommandStopSending {
		if confirmation.Snapshot.Receipt.Status != domain.RunDraining {
			t.Fatal("winning stop-sending command did not drain the in-flight request")
		}
		want = domain.RunCompleted
		fixture.release()
	}
	final := waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		want,
	)
	page, err := fixture.repository.JobEvents(
		t.Context(),
		submission.Job.JobID,
		0,
		200,
	)
	if err != nil || page.HasMore {
		t.Fatalf("could not read the complete command journal: %v", err)
	}
	var decisions int
	for _, event := range page.Events {
		if event.Kind != jobcontract.EventCommand {
			continue
		}
		decisions++
		journaled, err := jobcontract.DecodeCommandConfirmation(bytes.NewReader(event.Payload))
		if err != nil || !bytes.Equal(encode(t, journaled), encode(t, confirmation)) {
			t.Fatal("journal contains a confirmation other than the HTTP winner")
		}
	}
	if decisions != 1 || fixture.requests.Load() != 1 {
		t.Fatal("concurrent controls duplicated a durable decision or provider request")
	}
	for _, result := range []outcome{winner, loser} {
		replay := fixture.request(
			t,
			http.MethodPost,
			result.path,
			encode(t, result.command),
		)
		body, err := io.ReadAll(replay.Body)
		_ = replay.Body.Close()
		sameResponse := replay.StatusCode == result.status && bytes.Equal(body, result.body)
		if err != nil || !sameResponse {
			t.Fatal("same-ID replay changed the confirmed or conflicting HTTP decision")
		}
	}
	after, err := fixture.repository.GetJob(t.Context(), submission.Job.JobID)
	unchanged := err == nil && bytes.Equal(encode(t, after), encode(t, final))
	if !unchanged || fixture.requests.Load() != 1 {
		t.Fatal("control replays changed the settled job, journal sequence or provider traffic")
	}
	_, err = fixture.repository.GetJobCommand(t.Context(), loser.command.CommandID)
	if !errors.Is(err, jobhost.ErrNotFound) {
		t.Fatal("conflicting replay persisted a confirmation")
	}
}
