package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func compressedFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestJSONGzipPreservesCurrentTypedStateAndFixedErrors(t *testing.T) {
	want := fixtureSnapshot()
	raw, _ := json.Marshal(want)
	for _, encoding := range []string{"", "identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			wire := raw
			if encoding == "gzip" {
				wire = compressedFixture(t, raw)
			}
			client, _ := New(fixtureIdentity(), func(_ context.Context, input remotessh.Request) (*http.Response, error) {
				if !input.JSONGzip || input.RangeStart != nil {
					t.Fatal("JSON operation did not explicitly negotiate gzip")
				}
				response := fixtureResponse(http.StatusOK, wire)
				if encoding != "" {
					response.Header.Set("Content-Encoding", encoding)
				}
				return response, nil
			})
			got, err := client.GetJob(t.Context(), fixtureJob)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("typed state changed: %v", err)
			}
		})
	}
	wire := compressedFixture(t, []byte(
		`{"contract":1,"code":"job_conflict","message":"private-server-message","retryable":false}`,
	))
	client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
		response := fixtureResponse(http.StatusConflict, wire)
		response.Header.Set("Content-Encoding", "gzip")
		return response, nil
	})
	_, err := client.GetJob(t.Context(), fixtureJob)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "job_conflict" || strings.Contains(err.Error(), "private") {
		t.Fatal("compressed error lost its safe fixed code")
	}
}

func TestReadJSONRejectsInvalidCompressedFramingAndBudgets(t *testing.T) {
	raw := []byte(`{"private":"value"}`)
	wire := compressedFixture(t, raw)
	for _, mutation := range []string{
		"crc", "truncated", "header", "trailing", "multiple_members", "unsupported_encoding",
		"multiple_encodings", "stacked_encoding", "empty_encoding", "wire_length", "decoded_limit",
		"wire_limit", "wire_limit_unknown", "content_type", "automatic_decompression",
	} {
		t.Run(mutation, func(t *testing.T) {
			body := bytes.Clone(wire)
			limit := int64(len(raw))
			switch mutation {
			case "crc":
				body[len(body)-8] ^= 1
			case "truncated":
				body = body[:len(body)-1]
			case "header":
				body[0] = 0
			case "trailing":
				body = append(body, 0)
			case "multiple_members":
				body = append(body, wire...)
			case "decoded_limit":
				limit--
			case "wire_limit", "wire_limit_unknown":
				body = bytes.Repeat([]byte{0}, int(limit)+gzipWireAllowance+1)
			}
			response := fixtureResponse(http.StatusOK, body)
			response.Header.Set("Content-Encoding", "gzip")
			switch mutation {
			case "unsupported_encoding":
				response.Header.Set("Content-Encoding", "br")
			case "multiple_encodings":
				response.Header.Add("Content-Encoding", "gzip")
			case "stacked_encoding":
				response.Header.Set("Content-Encoding", "gzip, identity")
			case "empty_encoding":
				response.Header.Set("Content-Encoding", "")
			case "wire_length":
				response.ContentLength++
			case "wire_limit_unknown":
				response.ContentLength = -1
			case "content_type":
				response.Header.Set("Content-Type", "application/octet-stream")
			case "automatic_decompression":
				response.Uncompressed = true
			}
			if _, err := readJSON(response, limit); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("untrusted compressed response accepted: %v", err)
			}
		})
	}
	response := fixtureResponse(http.StatusOK, wire)
	response.Header.Set("Content-Encoding", "gzip")
	response.ContentLength = -1
	got, err := readJSON(response, int64(len(raw)))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("bounded chunked gzip was not decoded")
	}
}

type failedGzipBody struct{}

func (failedGzipBody) Read([]byte) (int, error) { return 0, errors.New("private transport failure") }
func (failedGzipBody) Close() error             { return nil }

func TestJSONGzipKeepsTransportErrorsSeparateAndDownloadCompatibility(t *testing.T) {
	response := fixtureResponse(http.StatusOK, []byte{})
	response.Header.Set("Content-Encoding", "gzip")
	response.ContentLength, response.Body = -1, failedGzipBody{}
	if _, err := readJSON(response, 100); err != ErrConnection {
		t.Fatal("wire transport error was not fixed connection error")
	}
	raw, reference := reportFixture()
	client, _ := New(fixtureIdentity(), func(_ context.Context, input remotessh.Request) (*http.Response, error) {
		if !input.JSONGzip {
			t.Fatal("fresh download did not offer gzip to the runner")
		}
		// Older runners ignore the offer and keep returning identity JSON.
		return reportResponse(raw, reference, 0), nil
	})
	file, err := os.CreateTemp(t.TempDir(), "partial")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := client.DownloadReport(t.Context(), fixtureJob, reference, file); err != nil {
		t.Fatal(err)
	}
	_, _ = file.Seek(0, io.SeekStart)
	got, _ := io.ReadAll(file)
	if !bytes.Equal(got, raw) {
		t.Fatal("download bytes changed")
	}
}
