package runnerhttp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestReportHTTPRangeStaysIdentityEvenWhenGzipIsRequested(t *testing.T) {
	document := []byte(strings.Repeat("immutable report ", 100))
	digest := sha256.Sum256(document)
	report := reportDownload{Document: document, SHA256: hex.EncodeToString(digest[:]), Kind: jobcontract.KindRun}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveReport(&jsonResponseWriter{ResponseWriter: writer, gzip: acceptsJSONGzip(request)}, request, report)
	}))
	defer server.Close()
	for _, offset := range []int{0, 19} {
		request, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			server.URL,
			nil,
		)
		request.Header.Set("Accept-Encoding", "gzip")
		if offset > 0 {
			request.Header.Set("Range", "bytes="+strconv.Itoa(offset)+"-")
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		unchanged := err == nil && bytes.Equal(body, document[offset:]) && response.Header.Get("Content-Encoding") == ""
		if !unchanged || response.Header.Get("X-Content-SHA256") != report.SHA256 {
			t.Fatal("immutable report representation changed")
		}
		if offset > 0 {
			wantRange := "bytes 19-" + strconv.Itoa(len(document)-1) + "/" + strconv.Itoa(len(document))
			if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != wantRange {
				t.Fatal("report resume semantics changed")
			}
		}
	}
}

func TestJSONGzipRequiresExplicitAcceptableCoding(t *testing.T) {
	for _, item := range []struct {
		accept string
		want   bool
	}{
		{accept: "gzip", want: true}, {accept: "identity, gzip;q=0.5", want: true},
		{accept: "GZIP; q=1", want: true}, {accept: "gzip;q=0"}, {accept: "*"},
		{accept: "identity"}, {accept: "gzip;q=2"}, {accept: "gzip;unknown=1"},
		{accept: "gzip, gzip;q=0"}, {accept: "gzip;q=NaN"}, {accept: ""},
	} {
		request := httptest.NewRequest(http.MethodGet, "/runner", nil)
		request.Header.Set("Accept-Encoding", item.accept)
		if got := acceptsJSONGzip(request); got != item.want {
			t.Errorf("Accept-Encoding %q: got %v", item.accept, got)
		}
	}
}
