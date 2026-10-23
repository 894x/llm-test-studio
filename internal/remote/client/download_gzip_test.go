package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func TestFreshDownloadReducesWireBytesWithGzip(t *testing.T) {
	raw := []byte(strings.Repeat(`{"samples":[{"latency_ms":12.5,"success":true}]}`, 4096))
	digest := sha256.Sum256(raw)
	reference := jobcontract.ReportReference{
		ID: fixtureReport, Kind: jobcontract.KindRun,
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(raw)),
	}
	wireBytes := 0
	client, _ := New(fixtureIdentity(), func(_ context.Context, input remotessh.Request) (*http.Response, error) {
		if !input.JSONGzip {
			t.Error("fresh download did not negotiate gzip")
		}
		response := reportResponse(raw, reference, 0)
		if input.JSONGzip {
			wire := compressedFixture(t, raw)
			response.Body = io.NopCloser(bytes.NewReader(wire))
			response.ContentLength = int64(len(wire))
			response.Header.Set("Content-Encoding", "gzip")
		}
		wireBytes = int(response.ContentLength)
		return response, nil
	})
	file, err := os.Create(filepath.Join(t.TempDir(), "report"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := client.DownloadReport(t.Context(), fixtureJob, reference, file); err != nil {
		t.Fatal(err)
	}
	if wireBytes >= len(raw)/2 {
		t.Fatalf("download wire bytes = %d, raw bytes = %d", wireBytes, len(raw))
	}
	t.Logf("wire bytes reduced from %d to %d", len(raw), wireBytes)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	downloaded, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(downloaded, raw) {
		t.Fatalf("downloaded document changed: %v", err)
	}
}

func TestDownloadRejectsInvalidGzipStreams(t *testing.T) {
	raw, reference := reportFixture()
	valid := compressedFixture(t, raw)
	for _, test := range []struct {
		name string
		wire []byte
		want error
	}{
		{name: "header", wire: append([]byte{0}, valid[1:]...), want: ErrIntegrity},
		{name: "checksum", wire: bytes.Clone(valid), want: ErrIntegrity},
		{name: "trailing", wire: append(bytes.Clone(valid), 0), want: ErrIntegrity},
		{name: "multiple_members", wire: append(bytes.Clone(valid), valid...), want: ErrIntegrity},
		{
			name: "decoded_extra", wire: compressedFixture(t, append(bytes.Clone(raw), 0)), want: ErrIntegrity,
		},
		{name: "truncated", wire: valid[:len(valid)-1], want: ErrIntegrity},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := test.wire
			if test.name == "checksum" {
				wire[len(wire)-8] ^= 1
			}
			response := reportResponse(raw, reference, 0)
			response.Body = io.NopCloser(bytes.NewReader(wire))
			response.ContentLength = int64(len(wire))
			response.Header.Set("Content-Encoding", "gzip")
			client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
				return response, nil
			})
			file, err := os.Create(filepath.Join(t.TempDir(), "report"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := client.DownloadReport(t.Context(), fixtureJob, reference, file); !errors.Is(err, test.want) {
				t.Fatalf("download error = %v, want %v", err, test.want)
			}
		})
	}
}
