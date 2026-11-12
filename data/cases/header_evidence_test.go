package casebundle_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
)

func TestHeaderEvidenceCapturesSanitizedJSONAndSSEForAllProtocols(t *testing.T) {
	for _, directory := range []string{"Kimi-cache-headers-write-5m", "Kimi-cache-headers-write-5m-stream"} {
		raw, err := fs.ReadFile(casebundle.Bundle, "kimi-cache-usage/"+directory+"/case.json")
		if err != nil {
			t.Fatal(err)
		}
		item, err := casecodec.DecodeFilesystemCase(directory, raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, proto := range item.Protocols() {
			for _, capture := range []bool{false, true} {
				t.Run(directory+"/"+string(proto)+map[bool]string{false: "/legacy", true: "/capture"}[capture], func(t *testing.T) {
					fixture := &kimiCacheHTTP{t: t, key: item.Key, protocol: proto, stream: strings.HasSuffix(item.Key, "_stream")}
					fixture.mutateHeaders = func(headers http.Header, _ int) {
						headers.Add("Set-Cookie", "private-cookie")
						headers.Set("X-Api-Key", "private-upstream-key")
					}
					server := httptest.NewServer(fixture)
					defer server.Close()
					lease, err := credentials.NewTemporaryLease([]byte("private-client-key"))
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Close()
					client, err := protocols.NewClient(
						protocols.NewRegistry(),
						lease,
						domain.ChannelSnapshot{
							EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
							Name:              "local headers", Protocol: proto, BaseURL: server.URL, UpstreamModelName: "kimi-k3.1-pro",
						},
						server.Client().Transport,
					)
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					spec, err := item.SpecFor(proto)
					if err != nil {
						t.Fatal(err)
					}
					ctx := context.Background()
					if capture {
						ctx = protocols.WithHeaderEvidence(ctx)
					}
					result, err := client.Execute(ctx, protocols.Execution{Spec: spec})
					if err != nil {
						t.Fatal(err)
					}
					x := result.Observation.Exchanges[0]
					if !capture {
						if x.RequestHeaders != nil || x.ResponseHeaders != nil {
							t.Fatal("older unmarked execution gained new header fields")
						}
						return
					}
					if x.RequestHeaders == nil || x.ResponseHeaders == nil {
						t.Fatal("negotiated headers not carried to exchange")
					}
					if x.RequestHeaders.Values["Authorization"][0] != "[redacted]" ||
						x.ResponseHeaders.Values[protocol.MSHCacheWriteHeader5m][0] != "512" {
						t.Fatal("request redaction or response TTL header lost")
					}
					encoded, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(encoded), "private-") {
						t.Fatal("header credential persisted in evidence")
					}
				})
			}
		}
	}
}
