package ssh_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remoteclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

const responseJobID = "11111111-1111-4111-8111-111111111111"
const responseReportID = "22222222-2222-4222-8222-222222222222"

func TestRunnerHTTPCurrentReportDownloadsPreserveTailEOF(t *testing.T) {
	document := append([]byte(`{"value":"`), bytes.Repeat([]byte("r"), (1<<20)+17)...)
	document = append(document, []byte(`"}`)...)
	digest := sha256.Sum256(document)
	sha := hex.EncodeToString(digest[:])
	var requests atomic.Int32
	client, _, channels := responseClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		kind := jobcontract.KindRun
		if strings.Contains(request.URL.Path, "/archives/") {
			kind = jobcontract.KindPerformance
		}
		offset := 0
		if value := request.Header.Get("Range"); value != "" {
			var err error
			offset, err = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(value, "bytes="), "-"))
			if err != nil || offset < 1 || offset >= len(document) {
				t.Error("invalid range request")
				writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Content-SHA256", sha)
		writer.Header().Set("ETag", `"`+sha+`"`)
		writer.Header().Set("X-LLM-Test-Report-Kind", string(kind))
		writer.Header().Set("Content-Length", strconv.Itoa(len(document)-offset))
		if offset != 0 {
			writer.Header().Set("Content-Range", "bytes "+strconv.Itoa(offset)+"-"+
				strconv.Itoa(len(document)-1)+"/"+strconv.Itoa(len(document)))
			writer.WriteHeader(http.StatusPartialContent)
		}
		_, _ = writer.Write(document[offset:])
	})
	for _, archive := range []bool{false, true} {
		for _, offset := range []int{0, 17, len(document) - 1} {
			t.Run(strconv.FormatBool(archive)+"/"+strconv.Itoa(offset), func(t *testing.T) {
				file, err := os.CreateTemp(t.TempDir(), "report-")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if _, err := file.Write(document[:offset]); err != nil {
					t.Fatal(err)
				}
				reference := jobcontract.ReportReference{
					Kind: jobcontract.KindRun, ID: responseReportID, SHA256: sha, Size: int64(len(document)),
				}
				if archive {
					reference.Kind = jobcontract.KindPerformance
					err = client.DownloadPerformanceArchive(t.Context(), responseJobID, reference, file)
				} else {
					err = client.DownloadReport(t.Context(), responseJobID, reference, file)
				}
				if err != nil {
					t.Fatalf("complete current download failed its strict EOF/hash verification: %v", err)
				}
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(got, document) {
					t.Fatal("verified download changed immutable bytes")
				}
			})
		}
	}
	if requests.Load() != 6 || channels.Load() != 1 {
		t.Fatalf("downloads retried or failed to reuse their pool: requests=%d channels=%d", requests.Load(), channels.Load())
	}
}

func TestRunnerHTTPCompleteJSONAndDirectGzipReaders(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(strconv.FormatBool(compressed), func(t *testing.T) {
			document := bytes.Repeat([]byte("json-content"), 256<<10)
			wire := document
			if compressed {
				var output bytes.Buffer
				encoder := gzip.NewWriter(&output)
				_, _ = encoder.Write(document)
				_ = encoder.Close()
				wire = output.Bytes()
			}
			_, client, _ := responseClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Length", strconv.Itoa(len(wire)))
				if compressed {
					writer.Header().Set("Content-Encoding", "gzip")
				}
				_, _ = writer.Write(wire)
			})
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+client.address+"/json", nil)
			request.Header.Set("Accept-Encoding", "identity")
			response, err := client.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var reader io.Reader = response.Body
			if compressed {
				decoder, err := gzip.NewReader(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer decoder.Close()
				reader = decoder // Keep gzip's default Multistream(true) tail probe.
			}
			got, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(got, document) {
				t.Fatal("complete JSON/gzip stream failed validation")
			}
		})
	}
}

func TestRunnerHTTPTruncatedDownloadsRemainFailuresWithoutRetry(t *testing.T) {
	document := []byte("complete")
	digest := sha256.Sum256(document)
	sha := hex.EncodeToString(digest[:])
	var requests atomic.Int32
	client, _, _ := responseClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Length", strconv.Itoa(len(document)))
		writer.Header().Set("X-Content-SHA256", sha)
		writer.Header().Set("ETag", `"`+sha+`"`)
		writer.Header().Set("X-LLM-Test-Report-Kind", string(jobcontract.KindRun))
		_, _ = writer.Write(document[:3])
	})
	file, err := os.CreateTemp(t.TempDir(), "partial-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	err = client.DownloadReport(t.Context(), responseJobID, jobcontract.ReportReference{
		Kind: jobcontract.KindRun, ID: responseReportID, SHA256: sha, Size: int64(len(document)),
	}, file)
	if !errors.Is(err, remoteclient.ErrConnection) || requests.Load() != 1 {
		t.Fatalf("truncated download became success or was automatically retried: %v", err)
	}
}

type responseHTTP struct {
	http    *http.Client
	address string
}

func responseClient(
	t *testing.T,
	handler http.HandlerFunc,
) (*remoteclient.Client, responseHTTP, *atomic.Int32) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	address := server.Listener.Addr().String()
	management, channels := forwardedResponseHTTP(t, address)
	client, err := remoteclient.New(remoteclient.Identity{
		NodeID: "33333333-3333-4333-8333-333333333333", InstallationID: "44444444-4444-4444-8444-444444444444",
	}, func(ctx context.Context, input remotessh.Request) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, input.Method, server.URL+input.Route, input.Body)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept-Encoding", "identity")
		if input.RangeStart != nil {
			request.Header.Set("Range", "bytes="+strconv.FormatInt(*input.RangeStart, 10)+"-")
		}
		return management.Do(request)
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, responseHTTP{http: management, address: address}, channels
}

// This peer forwards one fixed loopback HTTP endpoint, without shell, SFTP,
// provider access, host-pin prompts, or external network destinations.
func forwardedResponseHTTP(t *testing.T, address string) (*http.Client, *atomic.Int32) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := cryptossh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &cryptossh.ServerConfig{
		PasswordCallback: func(cryptossh.ConnMetadata, []byte) (*cryptossh.Permissions, error) { return nil, nil },
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	var mu sync.Mutex
	var accepted net.Conn
	stopped := false
	channels := &atomic.Int32{}
	workers.Go(func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		if stopped {
			mu.Unlock()
			_ = raw.Close()
			return
		}
		accepted = raw
		mu.Unlock()
		defer raw.Close()
		connection, incoming, requests, err := cryptossh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer connection.Close()
		workers.Go(func() {
			for request := range requests {
				_ = request.Reply(false, nil)
			}
		})
		for open := range incoming {
			workers.Go(func() { forwardResponseChannel(open, address, channels) })
		}
	})
	t.Cleanup(func() {
		mu.Lock()
		stopped = true
		raw := accepted
		mu.Unlock()
		_ = listener.Close()
		if raw != nil {
			_ = raw.Close()
		}
		workers.Wait()
	})
	password, err := credentials.NewTemporaryLease([]byte("local-response-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer password.Close()
	client, err := remotessh.Dial(t.Context(), remotessh.Endpoint{
		Host: "127.0.0.1", Port: uint16(listener.Addr().(*net.TCPAddr).Port), User: "tester",
		Fingerprint: cryptossh.FingerprintSHA256(signer.PublicKey()), HostKeyType: signer.PublicKey().Type(),
	}, remotessh.PasswordAuthentication(password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	management, err := client.RunnerHTTP(uint16(number))
	if err != nil {
		t.Fatal(err)
	}
	return management, channels
}

func forwardResponseChannel(open cryptossh.NewChannel, address string, count *atomic.Int32) {
	var destination struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if open.ChannelType() != "direct-tcpip" || cryptossh.Unmarshal(open.ExtraData(), &destination) != nil {
		_ = open.Reject(cryptossh.UnknownChannelType, "unsupported")
		return
	}
	if net.JoinHostPort(destination.Host, strconv.Itoa(int(destination.Port))) != address {
		_ = open.Reject(cryptossh.Prohibited, "unexpected endpoint")
		return
	}
	remote, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		_ = open.Reject(cryptossh.ConnectionFailed, "unavailable")
		return
	}
	defer remote.Close()
	channel, requests, err := open.Accept()
	if err != nil {
		return
	}
	count.Add(1)
	defer channel.Close()
	requestsDone := make(chan struct{})
	go func() { cryptossh.DiscardRequests(requests); close(requestsDone) }()
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(remote, channel); _ = remote.Close(); close(copied) }()
	_, _ = io.Copy(channel, remote)
	_ = remote.Close()
	_ = channel.Close()
	<-copied
	<-requestsDone
}
