package runnerhttp_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remoteclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func TestHTTPHandlerNegotiatesJSONAndKeepsStatusAndAuthorization(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	for _, validToken := range []bool{true, false} {
		for _, encoding := range []string{"identity", "gzip"} {
			request, _ := http.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				fixture.api.URL+"/runner",
				nil,
			)
			request.Header.Set(jobcontract.ContractHeader, "1")
			request.Header.Set("Accept-Encoding", encoding)
			if validToken {
				request.Header.Set("Authorization", "Bearer "+nodeToken)
			}
			response, err := fixture.api.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := http.StatusOK
			if !validToken {
				wantStatus = http.StatusUnauthorized
			}
			if response.StatusCode != wantStatus || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("JSON negotiation changed authorization or response status")
			}
			var reader io.Reader = response.Body
			if encoding == "gzip" {
				if response.Header.Get("Content-Encoding") != "gzip" {
					t.Fatal("handler did not negotiate compression")
				}
				compressed, err := gzip.NewReader(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer compressed.Close()
				reader = compressed
			} else if response.Header.Get("Content-Encoding") != "" {
				t.Fatal("identity management probe was compressed")
			}
			if validToken {
				info, err := jobcontract.DecodeRunnerInfo(reader)
				if err != nil || info.NodeID != fixture.host.Identity().Target.NodeID {
					t.Fatal("current RunnerInfo changed")
				}
			} else if body, err := io.ReadAll(reader); err != nil || len(body) == 0 {
				t.Fatal("compressed authorization error was lost")
			}
			_ = response.Body.Close()
		}
	}
}

type gzipEventRepository struct {
	runnerhttp.ReadRepository
	page jobhost.EventPage
}

// This fixture isolates transport of a legal current event envelope. Actual
// event payload semantics and persistence are covered by the synchronization tests.
func (repository gzipEventRepository) JobEvents(context.Context, string, uint64, uint32) (jobhost.EventPage, error) {
	return repository.page, nil
}

func TestJSONCompressionOverHTTPPreservesPageCanonicalBytesAndTypedEvents(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	submission := fixture.submission(t, nil)
	if _, _, err := fixture.host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	id := submission.Job.JobID
	payload, _ := json.Marshal(map[string]string{"detail": strings.Repeat("repeated event detail ", 250000)})
	page := jobhost.EventPage{Events: []jobcontract.Event{{
		Contract: jobcontract.CurrentContract, JobID: id, Sequence: 1, Kind: jobcontract.EventProgress,
		CreatedAt: time.Now().UTC(), Payload: payload,
	}}, LastSequence: 1, HasMore: false}
	canonical, err := json.Marshal(page)
	if err != nil || len(canonical) < 4<<20 || len(canonical) > jobcontract.MaxEventPageBytes {
		t.Fatal("large current page fixture invalid")
	}
	handler, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: fixture.host, Maintenance: fixture.maintenance,
		Repository:  gzipEventRepository{ReadRepository: fixture.repository, page: page},
		TokenSHA256: sha256.Sum256([]byte(nodeToken)), Capabilities: capabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, encoding := range []string{"identity", "gzip"} {
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			server.URL+"/jobs/"+id+"/events?after_sequence=0&limit=200&wait_ms=0",
			nil,
		)
		request.Header.Set("Accept-Encoding", encoding)
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.ContentLength != int64(len(wire)) {
			t.Fatal("HTTP wire framing changed")
		}
		decoded := wire
		if encoding == "gzip" {
			if response.Header.Get("Content-Encoding") != "gzip" || len(wire)*20 >= len(canonical) {
				t.Fatal("large repeated page was not significantly compressed")
			}
			t.Logf(
				"canonical_bytes=%d gzip_wire_bytes=%d canonical_sha256=%x",
				len(canonical),
				len(wire),
				sha256.Sum256(canonical),
			)
			reader, err := gzip.NewReader(bytes.NewReader(wire))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err = io.ReadAll(reader)
			_ = reader.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(decoded, canonical) || sha256.Sum256(decoded) != sha256.Sum256(canonical) {
			t.Fatal("canonical page bytes changed in transit")
		}
	}
	client, err := remoteclient.New(remoteclient.Identity{
		NodeID:         "11111111-1111-4111-8111-111111111111",
		InstallationID: "22222222-2222-4222-8222-222222222222",
	}, func(ctx context.Context, input remotessh.Request) (*http.Response, error) {
		if !input.JSONGzip {
			t.Fatal("typed JSON caller did not request gzip")
		}
		request, _ := http.NewRequestWithContext(
			ctx,
			input.Method,
			server.URL+input.Route,
			nil,
		)
		request.Header.Set("Accept-Encoding", "gzip")
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		return server.Client().Do(request)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ReadEvents(t.Context(), remoteclient.EventQuery{JobID: id, Limit: 200})
	if err != nil || !reflect.DeepEqual(got, page) {
		t.Fatalf("typed event page changed: %v", err)
	}
}
