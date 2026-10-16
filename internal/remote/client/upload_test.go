package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func uploadInfo() []byte {
	identity := fixtureIdentity()
	info := jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: identity.NodeID, NodeInstallationID: identity.InstallationID,
		EngineBuildID: strings.Repeat("a", 64), RunnerVersion: "upload-test", OS: "linux", Arch: "amd64",
		Capabilities: jobcontract.RunnerCapabilities{
			JobKinds: []jobcontract.Kind{jobcontract.KindRun}, Protocols: []string{"openai-chat"},
			Features: []string{jobcontract.FeatureExecutionOrigin, jobcontract.FeatureContentUpload},
		},
		Budget: jobcontract.RunnerBudget{MaxActiveJobs: 1, MaxConcurrency: 1,
			QueueTimeoutNS: int64(time.Hour), ExecutionTimeoutNS: int64(time.Hour)},
		Ready: true, Healthy: true,
	}
	raw, _ := json.Marshal(info)
	return raw
}

func uploadFixture(raw []byte) (jobcontract.ContentIdentity, jobcontract.AssetReceipt) {
	digest := sha256.Sum256(raw)
	identity := jobcontract.ContentIdentity{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(raw))}
	receipt := jobcontract.AssetReceipt{
		Contract: jobcontract.CurrentContract, NodeID: fixtureIdentity().NodeID,
		NodeInstallationID: fixtureIdentity().InstallationID, SHA256: identity.SHA256, Size: identity.Size,
	}
	return identity, receipt
}

func TestUploadContentUsesCurrentCapabilityAndIndependentBoundedBudget(t *testing.T) {
	content := []byte{0, 255, 1, 2, 0, 10}
	identity, receipt := uploadFixture(content)
	for _, status := range []int{http.StatusCreated, http.StatusOK} {
		source := &observedUploadSource{Reader: bytes.NewReader(content)}
		calls := 0
		client, _ := New(fixtureIdentity(), func(ctx context.Context, request remotessh.Request) (*http.Response, error) {
			calls++
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("unbounded request")
			}
			remaining := time.Until(deadline)
			if request.Route == "/runner" {
				if remaining > 30*time.Second || !request.JSONGzip || request.Content != nil {
					t.Fatal("probe changed the management budget")
				}
				return fixtureResponse(200, uploadInfo()), nil
			}
			valid := request.Method == http.MethodPut && request.Route == "/assets/"+identity.SHA256 &&
				request.Content != nil && *request.Content == identity
			if !valid || request.JSONGzip || request.RangeStart != nil || remaining < 9*time.Minute {
				t.Fatal("upload used JSON framing, a stale identity, or the management timeout")
			}
			got, err := io.ReadAll(request.Body)
			if err != nil || !bytes.Equal(got, content) {
				t.Fatal("upload changed native bytes")
			}
			raw, _ := json.Marshal(receipt)
			return fixtureResponse(status, raw), nil
		})
		got, err := client.UploadContent(t.Context(), identity, source)
		if err != nil || got != receipt || calls != 2 || source.closes.Load() != 1 {
			t.Fatalf("durable upload failed or did not close its source: %v", err)
		}
	}
}

func TestUploadContentRejectsAbsentCapabilityBeforeReadingSource(t *testing.T) {
	identity, _ := uploadFixture([]byte("a"))
	source := &observedUploadSource{Reader: strings.NewReader("a")}
	calls := 0
	client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.Route != "/runner" {
			t.Fatal("unsupported runner received PUT")
		}
		raw := bytes.ReplaceAll(uploadInfo(), []byte(`,"content_upload"`), nil)
		return fixtureResponse(200, raw), nil
	})
	if _, err := client.Probe(t.Context()); err != nil || calls != 1 {
		t.Fatal("valid current runner without content_upload became unprobeable")
	}
	_, err := client.UploadContent(t.Context(), identity, source)
	if !errors.Is(err, jobcontract.ErrUnsupported) || calls != 2 || source.reads.Load() != 0 || source.closes.Load() != 1 {
		t.Fatal("unsupported runner reached content IO or retained its source")
	}
}

func TestUploadContentRejectsInvalidIdentityWithoutIOAndClosesOwnedSource(t *testing.T) {
	valid, _ := uploadFixture([]byte("a"))
	for _, identity := range []jobcontract.ContentIdentity{
		{}, {SHA256: valid.SHA256, Size: 0}, {SHA256: valid.SHA256, Size: jobcontract.MaxAssetBytes + 1},
		{SHA256: strings.ToUpper(valid.SHA256), Size: 1},
	} {
		source := &observedUploadSource{Reader: strings.NewReader("a")}
		client, _ := New(fixtureIdentity(), func(context.Context, remotessh.Request) (*http.Response, error) {
			t.Fatal("invalid content reached the transport")
			return nil, nil
		})
		if _, err := client.UploadContent(t.Context(), identity, source); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid content identity accepted")
		}
		if source.closes.Load() != 1 || source.reads.Load() != 0 {
			t.Fatal("invalid content read or retained its source")
		}
	}
}

func TestUploadContentStrictlyBindsReceiptAndRejectsUntrustedResponses(t *testing.T) {
	content := []byte("content")
	identity, original := uploadFixture(content)
	for _, mutation := range []string{
		"node", "installation", "hash", "size", "contract", "missing", "duplicate", "unknown", "null",
		"too_large", "gzip", "status", "short_source", "wrong_source", "close_failure",
		"early_ack_unread", "early_ack_trailing_source",
	} {
		t.Run(mutation, func(t *testing.T) {
			receipt := original
			sourceContent := content
			source := &observedUploadSource{}
			switch mutation {
			case "node":
				receipt.NodeID = fixtureReport
			case "installation":
				receipt.NodeInstallationID = fixtureReport
			case "hash":
				receipt.SHA256 = strings.Repeat("b", 64)
			case "size":
				receipt.Size++
			case "contract":
				receipt.Contract++
			case "short_source":
				sourceContent = content[:len(content)-1]
			case "wrong_source":
				sourceContent = bytes.Repeat([]byte("x"), len(content))
			case "close_failure":
				source.closeErr = errors.New("private-source-close-error")
			case "early_ack_trailing_source":
				sourceContent = append(bytes.Clone(content), '!')
			}
			source.Reader = bytes.NewReader(sourceContent)
			client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
				if request.Route == "/runner" {
					return fixtureResponse(200, uploadInfo()), nil
				}
				switch mutation {
				case "early_ack_unread":
				case "early_ack_trailing_source":
					_, _ = io.CopyN(io.Discard, request.Body, identity.Size)
				default:
					_, _ = io.Copy(io.Discard, request.Body)
				}
				raw, _ := json.Marshal(receipt)
				switch mutation {
				case "missing":
					raw = bytes.Replace(raw, []byte(`,"size":7`), nil, 1)
				case "duplicate":
					raw = append(raw[:len(raw)-1], []byte(`,"size":7}`)...)
				case "unknown":
					raw = append(raw[:len(raw)-1], []byte(`,"extra":true}`)...)
				case "null":
					raw = bytes.Replace(raw, []byte(`"size":7`), []byte(`"size":null`), 1)
				case "too_large":
					raw = append(raw, bytes.Repeat([]byte(" "), jobcontract.MaxAssetReceiptBytes)...)
				}
				response := fixtureResponse(201, raw)
				if mutation == "gzip" {
					response.Header.Set("Content-Encoding", "gzip")
				}
				if mutation == "status" {
					response.StatusCode = 202
				}
				return response, nil
			})
			if _, err := client.UploadContent(t.Context(), identity, source); err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("untrusted upload confirmation or private error accepted")
			}
			if source.closes.Load() != 1 {
				t.Fatal("rejected confirmation retained source ownership")
			}
		})
	}
}

func TestUploadContentCancellationClosesAndJoinsCooperativeSource(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		source := &blockedUploadSource{started: make(chan struct{}), closed: make(chan struct{}), exited: make(chan struct{})}
		identity, _ := uploadFixture([]byte("a"))
		client, _ := New(fixtureIdentity(), func(ctx context.Context, request remotessh.Request) (*http.Response, error) {
			if request.Route == "/runner" {
				return fixtureResponse(200, uploadInfo()), nil
			}
			_, _ = io.Copy(io.Discard, request.Body)
			return nil, ctx.Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithTimeout(t.Context(), 30*time.Millisecond)
			want = context.DeadlineExceeded
		}
		finished := make(chan error, 1)
		go func() {
			_, err := client.UploadContent(ctx, identity, source)
			finished <- err
		}()
		select {
		case <-source.started:
		case <-time.After(time.Second):
			t.Fatal("source read did not start")
		}
		if !deadline {
			cancel()
		}
		select {
		case err := <-finished:
			if !errors.Is(err, want) {
				t.Fatalf("cancellation became a storage/read failure: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("upload did not join its cancelled source")
		}
		cancel()
		select {
		case <-source.exited:
		default:
			t.Fatal("upload returned before its source read exited")
		}
		if source.closes.Load() != 1 {
			t.Fatal("source closed more than once")
		}
	}
}

func TestUploadContentLostAcknowledgementRequiresExplicitCompleteRetry(t *testing.T) {
	content := []byte("whole-source")
	identity, receipt := uploadFixture(content)
	puts := 0
	client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
		if request.Route == "/runner" {
			return fixtureResponse(200, uploadInfo()), nil
		}
		if request.Method != http.MethodPut {
			t.Fatal("content retry submitted a job")
		}
		puts++
		got, _ := io.ReadAll(request.Body)
		if !bytes.Equal(got, content) {
			t.Fatal("retry omitted the complete source")
		}
		if puts == 1 {
			return nil, errors.New("private-lost-ack")
		}
		raw, _ := json.Marshal(receipt)
		return fixtureResponse(200, raw), nil
	})
	first := &observedUploadSource{Reader: bytes.NewReader(content)}
	if _, err := client.UploadContent(t.Context(), identity, first); !errors.Is(err, ErrConnection) {
		t.Fatal("lost acknowledgement was treated as success")
	}
	if puts != 1 || first.closes.Load() != 1 {
		t.Fatal("lost acknowledgement retried automatically or retained its source")
	}
	second := &observedUploadSource{Reader: bytes.NewReader(content)}
	if _, err := client.UploadContent(t.Context(), identity, second); err != nil || puts != 2 || second.closes.Load() != 1 {
		t.Fatal("explicit idempotent retry failed")
	}
}

func TestUploadContentFixedErrorCodesDoNotLeakServerMessages(t *testing.T) {
	identity, _ := uploadFixture([]byte("a"))
	for code, status := range map[string]int{
		"asset_integrity_mismatch": 422, "asset_read_failed": 408, "invalid_content_encoding": 415,
		"capacity_exceeded": 429, "storage_unavailable": 503, "upload_in_progress": 409,
	} {
		client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
			if request.Route == "/runner" {
				return fixtureResponse(200, uploadInfo()), nil
			}
			raw, _ := json.Marshal(map[string]any{"contract": 1, "code": code,
				"message": "private-server-message", "retryable": status == 429 || status == 503 || code == "upload_in_progress"})
			return fixtureResponse(status, raw), nil
		})
		_, err := client.UploadContent(t.Context(), identity, io.NopCloser(strings.NewReader("a")))
		var remote *RemoteError
		if !errors.As(err, &remote) || remote.Code != code || strings.Contains(err.Error(), "private") {
			t.Fatalf("fixed upload code was lost or leaked message: %v", err)
		}
		if remote.Retryable != (status == 429 || status == 503 || code == "upload_in_progress") {
			t.Fatal("upload error lost its exact retry advice")
		}
	}
}

func TestUploadBusyRetryAdviceDoesNotChangeJobConflict(t *testing.T) {
	for _, scenario := range []struct {
		code      string
		retryable bool
		valid     bool
	}{
		{code: "upload_in_progress", retryable: true, valid: true},
		{code: "upload_in_progress", retryable: false},
		{code: "job_conflict", retryable: false, valid: true},
		{code: "job_conflict", retryable: true},
	} {
		raw, _ := json.Marshal(map[string]any{"contract": 1, "code": scenario.code,
			"message": "private-message", "retryable": scenario.retryable})
		err := decodeError(fixtureResponse(409, raw))
		var remote *RemoteError
		if scenario.valid {
			if !errors.As(err, &remote) || remote.Code != scenario.code || remote.Retryable != scenario.retryable {
				t.Fatal("current 409 response was rejected or changed retry advice")
			}
			continue
		}
		if !errors.Is(err, ErrIntegrity) {
			t.Fatal("wrong 409 retry advice accepted")
		}
	}
}

func TestUploadContentBoundsSourceReadsAndSanitizesReadFailures(t *testing.T) {
	identity, _ := uploadFixture([]byte("a"))
	for _, readFailure := range []bool{false, true} {
		reader := &unendingUploadReader{fail: readFailure}
		source := &observedUploadSource{Reader: reader}
		client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
			if request.Route == "/runner" {
				return fixtureResponse(200, uploadInfo()), nil
			}
			_, err := io.Copy(io.Discard, request.Body)
			return nil, err
		})
		_, err := client.UploadContent(t.Context(), identity, source)
		want := jobcontract.ErrAssetIntegrity
		if readFailure {
			want = jobcontract.ErrAssetRead
		}
		if !errors.Is(err, want) || strings.Contains(err.Error(), "private") || source.closes.Load() != 1 {
			t.Fatalf("source failure was not bounded, closed and sanitized: %v", err)
		}
		if reader.bytes > identity.Size+1 {
			t.Fatal("source over-read beyond declared length plus one")
		}
	}
}

func TestUploadContentRejectsInvalidReaderCountsWithoutPanic(t *testing.T) {
	identity, _ := uploadFixture([]byte("a"))
	for _, count := range []int{-1, 2} {
		source := &observedUploadSource{Reader: invalidCountReader{count: count}}
		client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
			if request.Route == "/runner" {
				return fixtureResponse(200, uploadInfo()), nil
			}
			_, err := io.Copy(io.Discard, request.Body)
			return nil, err
		})
		if _, err := client.UploadContent(t.Context(), identity, source); !errors.Is(err, jobcontract.ErrAssetRead) {
			t.Fatal("invalid Reader count was not rejected safely")
		}
		if source.closes.Load() != 1 {
			t.Fatal("invalid Reader retained its source")
		}
	}
}

type invalidCountReader struct{ count int }

func (reader invalidCountReader) Read([]byte) (int, error) { return reader.count, nil }

func TestUploadContentBoundsEmptyReadsAndResetsAfterProgress(t *testing.T) {
	content := []byte("ab")
	identity, receipt := uploadFixture(content)
	for _, foreverEmpty := range []bool{true, false} {
		name := "intermittent_progress_resets_counter"
		if foreverEmpty {
			name = "all_empty"
		}
		t.Run(name, func(t *testing.T) {
			reader := &emptyUploadReader{content: bytes.Clone(content), forever: foreverEmpty}
			source := &observedUploadSource{Reader: reader}
			client, _ := New(fixtureIdentity(), func(_ context.Context, request remotessh.Request) (*http.Response, error) {
				if request.Route == "/runner" {
					return fixtureResponse(200, uploadInfo()), nil
				}
				if _, err := io.Copy(io.Discard, request.Body); err != nil {
					return nil, err
				}
				raw, _ := json.Marshal(receipt)
				return fixtureResponse(201, raw), nil
			})
			got, err := client.UploadContent(t.Context(), identity, source)
			wantReads := maxEmptyUploadReads * (len(content) + 1)
			if foreverEmpty {
				wantReads = maxEmptyUploadReads
				if !errors.Is(err, jobcontract.ErrAssetRead) {
					t.Fatalf("consecutive empty reads were not rejected: %v", err)
				}
			} else if err != nil || got != receipt {
				t.Fatalf("progress did not reset the empty-read counter: %v", err)
			}
			if reader.calls != wantReads || source.closes.Load() != 1 {
				t.Fatalf("wrong read bound or retained source: calls=%d, want=%d", reader.calls, wantReads)
			}
		})
	}
}

type emptyUploadReader struct {
	content []byte
	forever bool
	empty   int
	calls   int
}

func (reader *emptyUploadReader) Read(destination []byte) (int, error) {
	reader.calls++
	if reader.forever || reader.empty < maxEmptyUploadReads-1 {
		reader.empty++
		return 0, nil
	}
	reader.empty = 0
	if len(reader.content) == 0 {
		return 0, io.EOF
	}
	destination[0] = reader.content[0]
	reader.content = reader.content[1:]
	return 1, nil
}

type unendingUploadReader struct {
	bytes int64
	fail  bool
}

func (reader *unendingUploadReader) Read(destination []byte) (int, error) {
	if reader.fail {
		return 0, errors.New("private-source-read-failure")
	}
	clear(destination)
	reader.bytes += int64(len(destination))
	return len(destination), nil
}

type observedUploadSource struct {
	io.Reader
	reads    atomic.Int32
	closes   atomic.Int32
	closeErr error
}

func (source *observedUploadSource) Read(destination []byte) (int, error) {
	source.reads.Add(1)
	return source.Reader.Read(destination)
}

func (source *observedUploadSource) Close() error {
	source.closes.Add(1)
	return source.closeErr
}

type blockedUploadSource struct {
	started chan struct{}
	closed  chan struct{}
	exited  chan struct{}
	start   sync.Once
	close   sync.Once
	closes  atomic.Int32
}

func (source *blockedUploadSource) Read([]byte) (int, error) {
	source.start.Do(func() { close(source.started) })
	<-source.closed
	close(source.exited)
	return 0, errors.New("private-read-after-close")
}

func (source *blockedUploadSource) Close() error {
	source.close.Do(func() { source.closes.Add(1); close(source.closed) })
	return nil
}
