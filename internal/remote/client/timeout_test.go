package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

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
