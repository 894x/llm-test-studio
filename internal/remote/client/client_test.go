package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

const (
	fixtureJob    = "33333333-3333-4333-8333-333333333333"
	fixtureReport = "44444444-4444-4444-8444-444444444444"
)

func fixtureIdentity() Identity {
	return Identity{NodeID: "11111111-1111-4111-8111-111111111111",
		InstallationID: "22222222-2222-4222-8222-222222222222"}
}

func fixtureSnapshot() jobcontract.Snapshot {
	at := time.Now().UTC()
	return jobcontract.Snapshot{Receipt: jobcontract.Receipt{
		Contract: 1, JobID: fixtureJob, Kind: jobcontract.KindPerformance, AcceptedAt: at,
		Origin: domain.ExecutionOrigin{Kind: "remote", NodeID: fixtureIdentity().NodeID,
			InstallationID: fixtureIdentity().InstallationID, EngineBuildID: strings.Repeat("b", 64),
			RunnerVersion: "client-test-1", OS: "linux", Arch: "amd64"},
		Status: domain.RunQueued, Revision: 1, RunIDs: []string{}, ReportIDs: []string{fixtureReport},
	}, LatestSequence: 1, UpdatedAt: at}
}

func fixtureResponse(status int, raw []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		ContentLength: int64(len(raw)), Body: io.NopCloser(bytes.NewReader(raw))}
}

func TestClientRejectsForeignOrMalformedStateAndFixedErrorsDoNotLeakMessages(t *testing.T) {
	for _, mutation := range []string{
		"foreign", "foreign_node", "local_origin", "wrong_job", "missing", "duplicate", "unknown",
		"old_contract", "old_flat_receipt", "old_flat_with_origin", "missing_origin_version", "duplicate_origin_version",
	} {
		t.Run(mutation, func(t *testing.T) {
			state := fixtureSnapshot()
			if mutation == "foreign" {
				state.Receipt.Origin.InstallationID = "55555555-5555-4555-8555-555555555555"
			}
			if mutation == "foreign_node" {
				state.Receipt.Origin.NodeID = fixtureReport
			}
			if mutation == "local_origin" {
				state.Receipt.Origin.Kind = "local"
			}
			if mutation == "wrong_job" {
				state.Receipt.JobID = "55555555-5555-4555-8555-555555555555"
			}
			if mutation == "old_contract" {
				state.Receipt.Contract = 7
			}
			raw, _ := json.Marshal(state)
			switch mutation {
			case "old_flat_receipt", "old_flat_with_origin":
				var document map[string]json.RawMessage
				var receipt map[string]json.RawMessage
				_ = json.Unmarshal(raw, &document)
				_ = json.Unmarshal(document["receipt"], &receipt)
				if mutation == "old_flat_receipt" {
					delete(receipt, "origin")
				}
				receipt["node_installation_id"], _ = json.Marshal(state.Receipt.Origin.InstallationID)
				receipt["engine_build_id"], _ = json.Marshal(state.Receipt.Origin.EngineBuildID)
				document["receipt"], _ = json.Marshal(receipt)
				raw, _ = json.Marshal(document)
			case "missing_origin_version":
				raw = bytes.Replace(raw, []byte(`"runner_version":"client-test-1",`), nil, 1)
			case "duplicate_origin_version":
				raw = bytes.Replace(raw, []byte(`"runner_version":"client-test-1"`),
					[]byte(`"runner_version":"client-test-1","runner_version":"client-test-1"`), 1)
			case "missing":
				raw = bytes.Replace(
					raw,
					[]byte(`"latest_sequence":1,`),
					nil,
					1,
				)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"latest_sequence":1}`)...)
			case "unknown":
				raw = append(raw[:len(raw)-1], []byte(`,"extra":true}`)...)
			}
			client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
				return fixtureResponse(200, raw), nil
			})
			if _, err := client.GetJob(t.Context(), fixtureJob); err == nil {
				t.Fatal("untrusted state accepted")
			}
		})
	}
	raw := []byte(`{"contract":1,"code":"job_conflict","message":"secret-provider-echo","retryable":false}`)
	client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
		return fixtureResponse(409, raw), nil
	})
	_, err := client.GetJob(t.Context(), fixtureJob)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "job_conflict" || strings.Contains(err.Error(), "secret") {
		t.Fatal("remote error exposed message text or lost its fixed code")
	}
}

func TestClientRejectsCursorGapsForeignEventsAndUnboundedCollections(t *testing.T) {
	valid := jobcontract.Event{Contract: 1, JobID: fixtureJob, Sequence: 1,
		Kind: jobcontract.EventProgress, CreatedAt: time.Now().UTC(), Payload: json.RawMessage(`{"count":1}`)}
	for _, mutation := range []string{"gap", "foreign", "missing_bool", "null", "too_many", "empty_more"} {
		t.Run(mutation, func(t *testing.T) {
			event := valid
			if mutation == "gap" {
				event.Sequence = 2
			}
			if mutation == "foreign" {
				event.JobID = fixtureReport
			}
			events := []jobcontract.Event{event}
			if mutation == "too_many" {
				events = append(events, event)
			}
			page := map[string]any{"events": events, "last_sequence": 1, "has_more": false}
			switch mutation {
			case "missing_bool":
				delete(page, "has_more")
			case "null":
				page["events"] = nil
			case "empty_more":
				page["events"], page["last_sequence"], page["has_more"] = []any{}, 0, true
			}
			raw, _ := json.Marshal(page)
			client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
				return fixtureResponse(200, raw), nil
			})
			if _, err := client.ReadEvents(t.Context(), EventQuery{
				JobID: fixtureJob, Limit: 1,
			}); !errors.Is(err, ErrIntegrity) {
				t.Fatal(err)
			}
		})
	}
}

func reportFixture() ([]byte, jobcontract.ReportReference) {
	raw := []byte(strings.Repeat("stored-report-data-", 20))
	digest := sha256.Sum256(raw)
	return raw, jobcontract.ReportReference{ID: fixtureReport, Kind: jobcontract.KindRun,
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(raw))}
}

func reportResponse(raw []byte, reference jobcontract.ReportReference, offset int64) *http.Response {
	response := fixtureResponse(200, raw[offset:])
	response.Header.Set("ETag", `"`+reference.SHA256+`"`)
	response.Header.Set("X-Content-SHA256", reference.SHA256)
	response.Header.Set("X-LLM-Test-Report-Kind", string(reference.Kind))
	if offset > 0 {
		response.StatusCode = 206
		response.Header.Set("Content-Range", "bytes "+strconv.FormatInt(offset, 10)+"-"+
			strconv.Itoa(len(raw)-1)+"/"+strconv.Itoa(len(raw)))
	}
	return response
}

func TestDownloadResumesInterruptedBodyAndVerifiesCompleteCacheOffline(t *testing.T) {
	raw, reference := reportFixture()
	calls := 0
	client, _ := New(fixtureIdentity(), func(ctx context.Context, request remotessh.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			response := reportResponse(raw, reference, 0)
			response.Body = io.NopCloser(bytes.NewReader(raw[:19]))
			return response, nil
		}
		if calls != 2 || request.RangeStart == nil || *request.RangeStart != 19 {
			t.Error("resume did not use its exact existing prefix")
			return nil, ErrConnection
		}
		return reportResponse(raw, reference, 19), nil
	})
	file, err := os.Create(filepath.Join(t.TempDir(), "partial"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := client.DownloadReport(
		t.Context(),
		fixtureJob,
		reference,
		file,
	); !errors.Is(err, ErrConnection) {
		t.Fatal(err)
	}
	for range 2 {
		if err := client.DownloadReport(
			t.Context(),
			fixtureJob,
			reference,
			file,
		); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("complete cache re-fetched the report")
	}
}

func TestDownloadRejectsBadHeadersAndCorruptPrefixesWithoutResettingData(t *testing.T) {
	raw, reference := reportFixture()
	for _, mutation := range []string{"digest", "etag_duplicate", "range", "kind", "length", "encoding", "prefix"} {
		t.Run(mutation, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "partial"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			prefix := raw[:19]
			if mutation == "prefix" {
				prefix = bytes.Repeat([]byte("x"), 19)
			}
			_, _ = file.Write(prefix)
			client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
				response := reportResponse(raw, reference, 19)
				switch mutation {
				case "digest":
					response.Header.Set("X-Content-SHA256", strings.Repeat("a", 64))
				case "etag_duplicate":
					response.Header.Add("ETag", response.Header.Get("ETag"))
				case "range":
					response.Header.Set("Content-Range", "bytes 0-1/2")
				case "kind":
					response.Header.Set("X-LLM-Test-Report-Kind", "performance")
				case "length":
					response.ContentLength--
				case "encoding":
					response.Header.Set("Content-Encoding", "gzip")
				}
				return response, nil
			})
			if err := client.DownloadReport(
				t.Context(),
				fixtureJob,
				reference,
				file,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatal(err)
			}
			retained, _ := os.ReadFile(file.Name())
			if !bytes.HasPrefix(retained, prefix) {
				t.Fatal("failed download reset existing data")
			}
			if mutation != "prefix" && len(retained) != len(prefix) {
				t.Fatal("invalid response metadata was written")
			}
		})
	}
}

func TestSubmissionBodyCleanupSerializesLateReads(t *testing.T) {
	raw := bytes.Repeat([]byte("secret"), 128)
	body := &submissionBody{document: raw}
	if strings.Contains(fmt.Sprintf("%+v %#v", body, body), "secret") {
		t.Fatal("request body formatting exposed credentials")
	}
	if _, err := json.Marshal(body); err == nil {
		t.Fatal("request body became a serializable diagnostic")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			buffer := make([]byte, 3)
			for {
				if _, err := body.Read(buffer); err == io.EOF {
					return
				}
			}
		}()
	}
	_ = body.Close()
	workers.Wait()
	if !bytes.Equal(raw, make([]byte, len(raw))) {
		t.Fatal("serialized request copy was not erased")
	}
}

func TestJobListRejectsDuplicateOwnersAndInvalidCursors(t *testing.T) {
	for _, mutation := range []string{"leading_zero", "stalled", "overflow", "duplicate", "foreign", "missing_boolean"} {
		t.Run(mutation, func(t *testing.T) {
			state := fixtureSnapshot()
			if mutation == "foreign" {
				state.Receipt.Origin.InstallationID = fixtureReport
			}
			jobs := []jobcontract.Snapshot{state}
			if mutation == "duplicate" {
				jobs = append(jobs, state)
			}
			page := map[string]any{"contract": 1, "jobs": jobs, "next_cursor": "2", "has_more": false}
			switch mutation {
			case "leading_zero":
				page["next_cursor"] = "02"
			case "stalled":
				page["next_cursor"] = "0"
			case "overflow":
				page["next_cursor"] = "9223372036854775808"
			case "missing_boolean":
				delete(page, "has_more")
			}
			raw, _ := json.Marshal(page)
			client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
				return fixtureResponse(200, raw), nil
			})
			if _, err := client.ListJobs(t.Context(), 0, 2); !errors.Is(err, ErrIntegrity) {
				t.Fatal(err)
			}
		})
	}
}

type cancelledResponseBody struct{ cancel context.CancelFunc }

func (body cancelledResponseBody) Read([]byte) (int, error) {
	body.cancel()
	return 0, context.Canceled
}

func (cancelledResponseBody) Close() error { return nil }

func TestClientPreservesCallerCancellationDuringResponseRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
		response := fixtureResponse(200, nil)
		response.Body, response.ContentLength = cancelledResponseBody{cancel: cancel}, -1
		return response, nil
	})
	if _, err := client.GetJob(ctx, fixtureJob); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation became a remote outage")
	}
}
