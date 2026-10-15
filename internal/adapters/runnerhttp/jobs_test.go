package runnerhttp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (fixture *httpFixture) runSubmission(t *testing.T, comparison bool) jobcontract.Submission {
	t.Helper()
	submission := fixture.submission(t, nil)
	first := httpRunInput(t, fixture.upstream.URL)
	inputs := []runs.PreparedRun{first}
	submission.Clear()
	submission.Job.Kind, submission.Job.Spec = jobcontract.KindRun, jobcontract.Payload{Run: &first}
	if comparison {
		second := httpRunInput(t, fixture.upstream.URL)
		// Both channels execute the exact same frozen plan, cases and model.
		channel, mapping := second.Snapshot.Channel, second.Snapshot.Mapping
		second.Snapshot = first.Snapshot
		second.Snapshot.Channel, second.Snapshot.Mapping = channel, mapping
		second.Snapshot.Mapping.ModelID = first.Snapshot.Model.ID
		second.PlanID = first.PlanID
		inputs = append(inputs, second)
		submission.Job.Kind = jobcontract.KindComparison
		submission.Job.Spec = jobcontract.Payload{Comparison: &comparisons.PreparedComparison{
			Meta: httpMeta(t), Plan: first.Snapshot.Plan, Model: first.Snapshot.Model.EntityRevisionRef, Runs: inputs,
		}}
	}
	for _, input := range inputs {
		submission.Secrets = append(submission.Secrets, jobcontract.Secret{
			Slot: input.Meta.ID, Value: []byte("http-channel-test-secret"),
		})
	}
	t.Cleanup(submission.Clear)
	if err := submission.Validate(); err != nil {
		t.Fatal(err)
	}
	return submission
}

func TestHTTPRunAndComparisonReportsMatchJournalAndOriginalStoredBytes(t *testing.T) {
	for _, comparison := range []bool{false, true} {
		t.Run(strconv.FormatBool(comparison), func(t *testing.T) {
			fixture := newHTTPFixture(t, false, capabilities())
			submission := fixture.runSubmission(t, comparison)
			response := fixture.request(
				t,
				http.MethodPost,
				"/jobs",
				encode(t, submission),
			)
			if response.StatusCode != http.StatusAccepted {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("run admission=%d: %s", response.StatusCode, body)
			}
			final := waitHTTPJob(
				t,
				fixture,
				submission.Job.JobID,
				domain.RunCompleted,
			)
			reports, err := fixture.repository.ListReportsForRuns(t.Context(), final.Receipt.RunIDs)
			if err != nil || len(reports) != len(final.Receipt.RunIDs) {
				t.Fatalf("reports=%d: %v", len(reports), err)
			}
			page, err := fixture.repository.JobEvents(
				t.Context(),
				submission.Job.JobID,
				0,
				200,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range reports {
				stored, err := fixture.repository.ReadRunReport(t.Context(), submission.Job.JobID, report.ID)
				if err != nil {
					t.Fatal(err)
				}
				matchedEvent := false
				for _, event := range page.Events {
					if event.Kind != jobcontract.EventReport {
						continue
					}
					var reference jobcontract.ReportReference
					if json.Unmarshal(event.Payload, &reference) == nil && reference == stored.Reference {
						matchedEvent = true
					}
				}
				path := "/jobs/" + submission.Job.JobID + "/reports/" + report.ID
				response := fixture.request(
					t,
					http.MethodGet,
					path,
					nil,
				)
				body, _ := io.ReadAll(response.Body)
				matchingBytes := bytes.Equal(body, stored.Document) && response.ContentLength == stored.Reference.Size
				matchingHeaders := response.Header.Get("X-Content-SHA256") == stored.Reference.SHA256 &&
					response.Header.Get("X-LLM-Test-Report-Kind") == "run"
				if response.StatusCode != http.StatusOK || !matchedEvent || !matchingBytes || !matchingHeaders {
					t.Fatal("Run report download did not match durable journal and bytes")
				}
				other := fixture.runSubmission(t, false)
				fixture.request(
					t,
					http.MethodPost,
					"/jobs",
					encode(t, other),
				)
				wrong := fixture.request(
					t,
					http.MethodGet,
					"/jobs/"+other.Job.JobID+"/reports/"+report.ID,
					nil,
				)
				if wrong.StatusCode != http.StatusNotFound {
					t.Fatal("another job disclosed a Run report")
				}
				waitHTTPJob(
					t,
					fixture,
					other.Job.JobID,
					domain.RunCompleted,
				)
			}
			wantRequests := int64(len(reports) * 2)
			if fixture.requests.Load() != wantRequests {
				t.Fatal("report polling repeated provider traffic")
			}
		})
	}
}

func TestHTTPJobListingUsesStableCursorAndRejectsMalformedPageQueries(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	for _, query := range []string{
		"", "?limit=0", "?limit=201", "?limit=-1", "?limit=01", "?limit=1&limit=2",
		"?limit=1&other=1", "?limit=1&after=", "?limit=1&after=-1", "?limit=1&after=01",
		"?limit=1&after=9223372036854775808",
	} {
		response := fixture.request(
			t,
			http.MethodGet,
			"/jobs"+query,
			nil,
		)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid page query %s returned %d", query, response.StatusCode)
		}
	}
	expected := []string{}
	for range 2 {
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
		expected = append(expected, submission.Job.JobID)
	}
	cursor := "0"
	for index := range 3 {
		response := fixture.request(
			t,
			http.MethodGet,
			"/jobs?limit=1&after="+cursor,
			nil,
		)
		var page jobhost.JobPage
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("page read: %v", err)
		}
		if page.Contract != jobcontract.CurrentContract || page.Jobs == nil {
			t.Fatal("invalid current page envelope")
		}
		if index == 2 {
			if len(page.Jobs) != 0 || page.HasMore || page.NextCursor != cursor {
				t.Fatal("empty page changed the accepted cursor")
			}
			break
		}
		if len(page.Jobs) != 1 || page.Jobs[0].Receipt.JobID != expected[index] || page.HasMore != (index == 0) {
			t.Fatal("job listing changed acceptance order or page boundary")
		}
		cursor = page.NextCursor
	}
	if fixture.requests.Load() != 4 {
		t.Fatal("job listing executed provider requests")
	}
}
