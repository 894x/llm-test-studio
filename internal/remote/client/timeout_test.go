package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func TestOpenAndJSONBodyKeepTimeoutsSeparateFromConnectionFailure(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		want  error
	}{
		{name: "nested deadline", cause: fmt.Errorf("channel open: %w", context.DeadlineExceeded),
			want: context.DeadlineExceeded},
		{name: "nested cancellation", cause: fmt.Errorf("channel open: %w", context.Canceled), want: context.Canceled},
		{name: "network timeout", cause: &net.DNSError{IsTimeout: true}, want: context.DeadlineExceeded},
		{name: "connection failure", cause: io.ErrUnexpectedEOF, want: ErrConnection},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(fixtureIdentity(), func(ctx context.Context, _ remotessh.Request) (*http.Response, error) {
				if ctx.Err() != nil {
					t.Fatal("outer request expired before its nested transport error")
				}
				return nil, test.cause
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := client.open(t.Context(), remotessh.Request{}, time.Second); !errors.Is(err, test.want) {
				t.Fatalf("open error = %v, want %v", err, test.want)
			}
			response := fixtureResponse(http.StatusOK, []byte("{}"))
			response.Body = io.NopCloser(timeoutFailureReader{err: test.cause})
			if _, err := readJSON(response, 100); !errors.Is(err, test.want) {
				t.Fatalf("body error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOpenKeepsItsOwnDeadlineWhenCallerRemainsActive(t *testing.T) {
	client, err := New(fixtureIdentity(), func(ctx context.Context, _ remotessh.Request) (*http.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.open(t.Context(), remotessh.Request{}, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || t.Context().Err() != nil {
		t.Fatalf("request deadline changed into connection failure or cancelled its caller: %v", err)
	}
}

type timeoutFailureReader struct{ err error }

func (reader timeoutFailureReader) Read([]byte) (int, error) { return 0, reader.err }

func TestJSONKeepsInnerDeadlineWhenTransportReturnsEOF(t *testing.T) {
	for _, test := range []struct {
		name string
		wait bool
		raw  string
		err  error
		want error
	}{
		{name: "deadline EOF", wait: true, err: io.EOF, want: context.DeadlineExceeded},
		{name: "deadline EOF with data", wait: true, raw: "{}", err: io.EOF, want: context.DeadlineExceeded},
		{name: "normal EOF", raw: "{}", err: io.EOF},
		{name: "early connection failure", err: io.ErrClosedPipe, want: ErrConnection},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &contextEOFBody{wait: test.wait, raw: test.raw, err: test.err}
			client, _ := New(fixtureIdentity(), func(ctx context.Context, _ remotessh.Request) (*http.Response, error) {
				body.ctx = ctx
				response := fixtureResponse(http.StatusOK, []byte("{}"))
				response.Body = body
				return response, nil
			})
			raw, err := client.json(
				t.Context(),
				remotessh.Request{},
				100,
				20*time.Millisecond,
			)
			if !errors.Is(err, test.want) || t.Context().Err() != nil {
				t.Fatalf("body error = %v, want %v with active caller", err, test.want)
			}
			if test.want == nil && string(raw) != test.raw {
				t.Fatal("normal EOF changed successful body bytes")
			}
			if body.reads != 1 || body.closes != 1 {
				t.Fatal("context annotation changed underlying Read/Close counts")
			}
			if !test.wait && body.closedContextErr != nil {
				t.Fatal("cleanup cancelled the request before closing its body")
			}
		})
	}
}

type contextEOFBody struct {
	ctx              context.Context
	wait             bool
	raw              string
	err              error
	reads            int
	closes           int
	closedContextErr error
}

func (body *contextEOFBody) Read(destination []byte) (int, error) {
	body.reads++
	if body.wait {
		<-body.ctx.Done()
	}
	return copy(destination, body.raw), body.err
}

func (body *contextEOFBody) Close() error {
	body.closes++
	body.closedContextErr = body.ctx.Err()
	return nil
}

func TestJSONOperationsUseExplicitBoundedBudgets(t *testing.T) {
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test", ModelID: "model", APIKey: "synthetic-deadline-test",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	prepared.ReportID = fixtureReport
	submission := &jobcontract.Submission{
		Job: jobcontract.JobSpec{
			Contract: jobcontract.CurrentContract, JobID: fixtureJob, NodeInstallationID: fixtureIdentity().InstallationID,
			EngineBuildID: strings.Repeat("a", 64), Target: jobcontract.ExecutionTarget{
				Kind: jobcontract.TargetRemote, NodeID: fixtureIdentity().NodeID,
			},
			Kind: jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
		},
		Secrets: []jobcontract.Secret{{Slot: fixtureReport, Value: []byte("synthetic-deadline-test")}},
	}
	defer submission.Clear()
	for _, test := range []struct {
		name string
		want time.Duration
		call func(context.Context, *Client) error
	}{
		{name: "events", want: 2 * time.Minute, call: func(ctx context.Context, client *Client) error {
			_, err := client.ReadEvents(ctx, EventQuery{JobID: fixtureJob, Limit: 1})
			return err
		}},
		{name: "runner", want: 30 * time.Second, call: func(ctx context.Context, client *Client) error {
			_, err := client.Probe(ctx)
			return err
		}},
		{name: "get job", want: 30 * time.Second, call: func(ctx context.Context, client *Client) error {
			_, err := client.GetJob(ctx, fixtureJob)
			return err
		}},
		{name: "list jobs", want: 30 * time.Second, call: func(ctx context.Context, client *Client) error {
			_, err := client.ListJobs(ctx, 0, 1)
			return err
		}},
		{name: "submit", want: 30 * time.Second, call: func(ctx context.Context, client *Client) error {
			_, err := client.Submit(ctx, submission)
			return err
		}},
		{name: "command", want: 30 * time.Second, call: func(ctx context.Context, client *Client) error {
			_, err := client.Command(ctx, jobcontract.Command{Contract: jobcontract.CurrentContract,
				CommandID: fixtureReport, JobID: fixtureJob, Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			client, _ := New(fixtureIdentity(), func(ctx context.Context, _ remotessh.Request) (*http.Response, error) {
				calls++
				deadline, ok := ctx.Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > test.want || remaining < test.want-time.Second {
					t.Fatalf("request budget = %v, want %v", remaining, test.want)
				}
				return nil, io.ErrClosedPipe
			})
			if err := test.call(t.Context(), client); !errors.Is(err, ErrConnection) || calls != 1 {
				t.Fatalf("operation did not reach its one bounded transport call: %v", err)
			}
		})
	}
}
