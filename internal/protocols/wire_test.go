package protocols

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type wireTransport func(*http.Request) (*http.Response, error)

func (transport wireTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func newWireClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	lease, err := credentials.NewTemporaryLease([]byte("wire-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	client, err := NewClient(
		NewRegistry(),
		lease,
		wireChannel(),
		transport,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func wireChannel() domain.ChannelSnapshot {
	return domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{
			ID: "00000000-0000-4000-8000-000000000001", Revision: 1,
		},
		Name: "wire", Protocol: domain.ProtocolOpenAIChat,
		BaseURL: "http://127.0.0.1", UpstreamModelName: "wire-model",
	}
}

func fileRequest(t *testing.T) (testspec.Spec, testspec.RenderedRequest) {
	t.Helper()
	spec, err := testspec.Decode(json.RawMessage(`{
		"inputs":{"image":{"type":"file","required":true,
			"file":{"encoding":"image_data_url","media_types":["image/png","image/jpeg"],"max_bytes":1048576}}},
		"request":{"body":{"messages":[{"role":"user","content":[
			{"type":"image_url","image_url":{"url":{"$input":"image"}}}]}]}},
		"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := testspec.Generate(spec, map[string]json.RawMessage{
		"image": json.RawMessage(`{"asset_ref":"image-1"}`),
	}, testspec.RandomContext{})
	if err != nil {
		t.Fatal(err)
	}
	return spec, body
}

func testImage(t *testing.T, mediaType string) []byte {
	t.Helper()
	var output bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var err error
	switch mediaType {
	case "image/png":
		err = png.Encode(&output, picture)
	case "image/jpeg":
		err = jpeg.Encode(&output, picture, nil)
	default:
		t.Fatal("invalid test media type")
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func imageMetadata(data []byte, mediaType string) runtime.AssetMetadata {
	digest := sha256.Sum256(data)
	return runtime.AssetMetadata{
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), MediaType: mediaType,
	}
}

type fileAssetResolver struct {
	path     string
	metadata runtime.AssetMetadata
	opened   atomic.Int32
	closed   atomic.Int32
}

func (resolver *fileAssetResolver) Open(_ context.Context, ref string) (runtime.AssetStream, error) {
	if ref != "image-1" {
		return nil, errors.New("unexpected test ref")
	}
	file, err := os.Open(resolver.path)
	if err != nil {
		return nil, err
	}
	resolver.opened.Add(1)
	return &testAsset{
		ReadCloser: file, metadata: resolver.metadata,
		onClose: func() { resolver.closed.Add(1) },
	}, nil
}

type testAsset struct {
	io.ReadCloser
	metadata runtime.AssetMetadata
	onClose  func()
}

func (asset *testAsset) Metadata() runtime.AssetMetadata { return asset.metadata }

func (asset *testAsset) Close() error {
	if asset.onClose != nil {
		asset.onClose()
	}
	return asset.ReadCloser.Close()
}

func tempAsset(t *testing.T, data []byte, mediaType string) *fileAssetResolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "selected-image")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return &fileAssetResolver{path: path, metadata: imageMetadata(data, mediaType)}
}

func TestFileWireMatchesMockBytesAndVerifiedContent(t *testing.T) {
	for _, mediaType := range []string{"image/png", "image/jpeg"} {
		t.Run(mediaType, func(t *testing.T) {
			imageBytes := testImage(t, mediaType)
			resolver := tempAsset(t, imageBytes, mediaType)
			receivedBody := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				received, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				if request.ContentLength != int64(len(received)) {
					t.Error("wire length does not match transmitted body")
				}
				receivedBody <- received
				_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer server.Close()
			client := newWireClient(t, server.Client().Transport)
			client.state.channel.BaseURL = server.URL
			_, rendered := fileRequest(t)
			response, err := client.Send(context.Background(), runtime.Request{
				Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer response.HTTP.Body.Close()
			received := <-receivedBody
			digest := sha256.Sum256(received)
			wire := response.PreparedWire
			if wire == nil || wire.SHA256 != hex.EncodeToString(digest[:]) ||
				wire.Bytes != int64(len(received)) || len(wire.Files) != 1 {
				t.Fatalf("prepared wire does not describe received bytes: %+v", wire)
			}
			var object map[string]any
			if err := json.Unmarshal(received, &object); err != nil {
				t.Fatal(err)
			}
			messages := object["messages"].([]any)
			content := messages[0].(map[string]any)["content"].([]any)
			dataURL := content[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
			prefix := "data:" + mediaType + ";base64,"
			if !strings.HasPrefix(dataURL, prefix) || object["model"] != "wire-model" {
				t.Fatal("file encoding or model binding is missing")
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
			if err != nil || !bytes.Equal(decoded, imageBytes) {
				t.Fatal("wire does not contain the verified original file")
			}
			if wire.Files[0].SHA256 != resolver.metadata.SHA256 || wire.Files[0].Size != resolver.metadata.Size {
				t.Fatal("file metadata differs from bytes actually read")
			}
			view := string(response.RequestBody)
			if strings.Contains(view, ";base64,") || !strings.Contains(view, "redacted_asset") {
				t.Fatal("request evidence retained file bytes or lost its explicit redaction")
			}
			if resolver.opened.Load() != 1 || resolver.closed.Load() != 1 {
				t.Fatalf("asset ownership: opened=%d closed=%d", resolver.opened.Load(), resolver.closed.Load())
			}
		})
	}
}

func TestFileExecuteKeepsImagesOutOfPersistableObservation(t *testing.T) {
	data := testImage(t, "image/png")
	resolver := tempAsset(t, data, "image/png")
	client := newWireClient(t, wireTransport(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
		}, nil
	}))
	spec, _ := fileRequest(t)
	result, err := client.Execute(context.Background(), Execution{
		Spec: spec, Inputs: map[string]json.RawMessage{"image": json.RawMessage(`{"asset_ref":"image-1"}`)},
		Assets: resolver,
	})
	if err != nil || result.Verdict.Status != testspec.VerdictPassed {
		t.Fatalf("file execution: error=%v verdict=%+v", err, result.Verdict)
	}
	document, err := json.Marshal(result)
	if err != nil || bytes.Contains(document, []byte(";base64,")) || bytes.Contains(document, []byte(resolver.path)) {
		t.Fatal("persistable observation contains a file payload or host path")
	}
}

func TestFileWireUsesExactBase64AndFileByteBoundaries(t *testing.T) {
	original := testImage(t, "image/png")
	_, rendered := fileRequest(t)
	sizes := []int{
		len(original), len(original) + 1, len(original) + 2,
		testspec.MaxFileInputBytes - 1, testspec.MaxFileInputBytes, testspec.MaxFileInputBytes + 1,
	}
	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			// The format contract checks bounded image headers/configuration;
			// it deliberately does not claim to decode all compressed pixels.
			data := make([]byte, size)
			copy(data, original)
			resolver := tempAsset(t, data, "image/png")
			wire, err := prepareWire(context.Background(), runtime.Request{
				Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
			}, wireChannel())
			if size > testspec.MaxFileInputBytes {
				if err == nil {
					_ = wire.Close()
					t.Fatal("oversize source accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer wire.Close()
			raw, err := io.ReadAll(wire)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			messages := object["messages"].([]any)
			content := messages[0].(map[string]any)["content"].([]any)
			url := content[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
			encoded := strings.TrimPrefix(url, "data:image/png;base64,")
			if len(encoded) != base64.StdEncoding.EncodedLen(size) {
				t.Fatal("base64 rounding changed the prepared byte count")
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || !bytes.Equal(decoded, data) || int64(len(raw)) != wire.metadata.Bytes {
				t.Fatal("file bytes or prepared wire length differ")
			}
		})
	}
}

func TestFileWireRejectsUnverifiedAssetsWithoutHTTP(t *testing.T) {
	original := testImage(t, "image/png")
	largeDimensions := bytes.Clone(original)
	binary.BigEndian.PutUint32(largeDimensions[16:20], maxImageDimension+1)
	binary.BigEndian.PutUint32(largeDimensions[29:33], crc32.ChecksumIEEE(largeDimensions[12:29]))
	tests := []struct {
		name   string
		data   []byte
		change func(*runtime.AssetMetadata)
	}{
		{name: "wrong hash", data: original, change: func(meta *runtime.AssetMetadata) {
			meta.SHA256 = strings.Repeat("0", 64)
		}},
		{name: "wrong length", data: original, change: func(meta *runtime.AssetMetadata) { meta.Size++ }},
		{name: "wrong MIME", data: original, change: func(meta *runtime.AssetMetadata) { meta.MediaType = "image/jpeg" }},
		{name: "invalid image", data: []byte("bounded but not an image")},
		{name: "dimensions", data: largeDimensions},
		{name: "too large", data: append(bytes.Clone(original), make([]byte, testspec.MaxFileInputBytes)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := tempAsset(t, test.data, "image/png")
			if test.change != nil {
				test.change(&resolver.metadata)
			}
			var httpCalls atomic.Int32
			client := newWireClient(t, wireTransport(func(*http.Request) (*http.Response, error) {
				httpCalls.Add(1)
				return nil, errors.New("must not reach HTTP")
			}))
			_, rendered := fileRequest(t)
			_, err := client.Send(context.Background(), runtime.Request{
				Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
			})
			if err == nil || httpCalls.Load() != 0 {
				t.Fatalf("invalid file reached HTTP: error=%v calls=%d", err, httpCalls.Load())
			}
			if resolver.opened.Load() != resolver.closed.Load() || len(sharedFileEncoders) != 0 {
				t.Fatal("rejected file leaked a reader or encoding reservation")
			}
		})
	}
}

func TestWireCapIncludesModelAndExpandedFile(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		for _, delta := range []int{-1, 0, 1} {
			t.Run(stringName(withFile, delta), func(t *testing.T) {
				rendered := testspec.RenderedRequest{Body: json.RawMessage(`{"padding":""}`), Files: []testspec.FileOccurrence{}}
				var resolver runtime.AssetResolver
				if withFile {
					_, rendered = fileRequest(t)
					resolver = tempAsset(t, testImage(t, "image/png"), "image/png")
				}
				var root map[string]json.RawMessage
				_ = json.Unmarshal(rendered.Body, &root)
				root["padding"] = json.RawMessage(`""`)
				rendered.Body, _ = json.Marshal(root)
				request := runtime.Request{
					Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
				}
				baseline, err := prepareWire(context.Background(), request, wireChannel())
				if err != nil {
					t.Fatal(err)
				}
				overhead := int(baseline.metadata.Bytes)
				_ = baseline.Close()
				wantBytes := testspec.MaxDocumentBytes + delta
				root["padding"], _ = json.Marshal(strings.Repeat("x", wantBytes-overhead))
				rendered.Body, _ = json.Marshal(root)
				request.Body = rendered
				var httpCalls atomic.Int32
				client := newWireClient(t, wireTransport(func(httpRequest *http.Request) (*http.Response, error) {
					httpCalls.Add(1)
					n, err := io.Copy(io.Discard, httpRequest.Body)
					_ = httpRequest.Body.Close()
					if err != nil || n != int64(wantBytes) {
						t.Errorf("wire bytes=%d want=%d error=%v", n, wantBytes, err)
					}
					return &http.Response{
						StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{},
					}, nil
				}))
				response, err := client.Send(context.Background(), request)
				if delta > 0 {
					if !errors.Is(err, errWireLimit) || httpCalls.Load() != 0 {
						t.Fatalf("oversize wire: error=%v HTTP=%d", err, httpCalls.Load())
					}
					return
				}
				if err != nil || httpCalls.Load() != 1 || response.PreparedWire.Bytes != int64(wantBytes) {
					t.Fatalf("bounded wire: error=%v HTTP=%d", err, httpCalls.Load())
				}
				_ = response.HTTP.Body.Close()
			})
		}
	}
}

func stringName(file bool, delta int) string {
	prefix := "json"
	if file {
		prefix = "file"
	}
	switch delta {
	case -1:
		return prefix + "/below"
	case 0:
		return prefix + "/exact"
	default:
		return prefix + "/above"
	}
}

func TestEarlyResponseClosesWireBeforeLateTransportRead(t *testing.T) {
	_, rendered := fileRequest(t)
	resolver := tempAsset(t, testImage(t, "image/png"), "image/png")
	lateRead := make(chan struct{})
	readDone := make(chan error, 1)
	client := newWireClient(t, wireTransport(func(request *http.Request) (*http.Response, error) {
		go func() {
			<-lateRead
			_, err := request.Body.Read(make([]byte, 1))
			readDone <- err
		}()
		return &http.Response{
			StatusCode: http.StatusRequestEntityTooLarge, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader("{}")),
		}, nil
	}))
	response, err := client.Send(context.Background(), runtime.Request{
		Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
	})
	if err != nil || response.PreparedWire == nil {
		t.Fatalf("early response: error=%v metadata=%+v", err, response.PreparedWire)
	}
	close(lateRead)
	if err := <-readDone; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("transport read bytes after request ownership ended: %v", err)
	}
	if resolver.closed.Load() != 1 || len(sharedFileEncoders) != 0 {
		t.Fatal("early response retained source reader or encoding reservation")
	}
	_ = response.HTTP.Body.Close()
}

func TestCancelledPreparedWireDoesNotExposeFurtherBytes(t *testing.T) {
	_, rendered := fileRequest(t)
	resolver := tempAsset(t, testImage(t, "image/png"), "image/png")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, err := prepareWire(ctx, runtime.Request{
		Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
	}, wireChannel())
	if err != nil {
		t.Fatal(err)
	}
	defer wire.Close()
	cancel()
	if count, err := wire.Read(make([]byte, 1)); count != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled body exposed bytes: count=%d error=%v", count, err)
	}
}

type blockedAsset struct {
	metadata runtime.AssetMetadata
	entered  chan struct{}
	closed   chan struct{}
	readOnce sync.Once
	once     sync.Once
}

func (asset *blockedAsset) Metadata() runtime.AssetMetadata { return asset.metadata }

func (asset *blockedAsset) Read([]byte) (int, error) {
	asset.readOnce.Do(func() { close(asset.entered) })
	<-asset.closed
	return 0, io.ErrClosedPipe
}

func (asset *blockedAsset) Close() error {
	asset.once.Do(func() { close(asset.closed) })
	return nil
}

type fixedAssetResolver struct {
	stream runtime.AssetStream
	opens  atomic.Int32
}

func (resolver *fixedAssetResolver) Open(context.Context, string) (runtime.AssetStream, error) {
	resolver.opens.Add(1)
	return resolver.stream, nil
}

func TestFileReadCancellationAndClientCloseJoin(t *testing.T) {
	for _, closeClient := range []bool{false, true} {
		t.Run(stringName(closeClient, 0), func(t *testing.T) {
			asset := &blockedAsset{
				metadata: imageMetadata(testImage(t, "image/png"), "image/png"),
				entered:  make(chan struct{}), closed: make(chan struct{}),
			}
			resolver := &fixedAssetResolver{stream: asset}
			client := newWireClient(t, wireTransport(func(*http.Request) (*http.Response, error) {
				t.Error("blocked file reached HTTP")
				return nil, errors.New("unexpected HTTP")
			}))
			_, rendered := fileRequest(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := client.Send(ctx, runtime.Request{
					Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
				})
				done <- err
			}()
			select {
			case <-asset.entered:
			case <-time.After(time.Second):
				t.Fatal("file read did not begin")
			}
			if closeClient {
				_ = client.Close()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancel/Close did not join the blocked file read")
			}
			if len(sharedFileEncoders) != 0 {
				t.Fatal("cancel leaked the encoding reservation")
			}
		})
	}
}

func TestDirectSendConcurrentCloseJoinsResponseReaders(t *testing.T) {
	raw := &blockedAsset{entered: make(chan struct{}), closed: make(chan struct{})}
	client := newWireClient(t, wireTransport(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
		return &http.Response{StatusCode: 200, Body: raw, Header: http.Header{}}, nil
	}))
	response, err := client.Send(context.Background(), runtime.Request{
		Method: http.MethodPost, Path: protocol.OpenAIChatPath,
		Body: testspec.RenderedRequest{Body: json.RawMessage("{}"), Files: []testspec.FileOccurrence{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.ReadAll(response.HTTP.Body)
		close(readDone)
	}()
	<-raw.entered
	var closes sync.WaitGroup
	for range 2 {
		closes.Go(func() { _ = client.Close() })
	}
	closes.Wait()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Close returned without joining direct response reads")
	}
}

type gateContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (ctx *gateContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}

func TestFileEncodingBudgetIsSharedAndGateCancellationIsJoined(t *testing.T) {
	_, rendered := fileRequest(t)
	data := testImage(t, "image/png")
	entered := make(chan struct{}, maxFileEncoders)
	clients := []*Client{}
	done := make(chan error, maxFileEncoders+1)
	for range maxFileEncoders {
		client := newWireClient(t, wireTransport(func(request *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			<-request.Context().Done()
			return nil, request.Context().Err()
		}))
		clients = append(clients, client)
		resolver := &fixedAssetResolver{stream: &testAsset{
			ReadCloser: io.NopCloser(bytes.NewReader(data)), metadata: imageMetadata(data, "image/png"),
		}}
		go func() {
			_, err := client.Send(context.Background(), runtime.Request{
				Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: resolver,
			})
			done <- err
		}()
	}
	for range maxFileEncoders {
		<-entered
	}
	waiter := &fixedAssetResolver{stream: &testAsset{
		ReadCloser: io.NopCloser(bytes.NewReader(data)), metadata: imageMetadata(data, "image/png"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	waitContext := &gateContext{Context: ctx, entered: make(chan struct{})}
	go func() {
		_, err := prepareWire(waitContext, runtime.Request{
			Method: http.MethodPost, Path: protocol.OpenAIChatPath, Body: rendered, Assets: waiter,
		}, wireChannel())
		done <- err
	}()
	<-waitContext.entered
	if waiter.opens.Load() != 0 || len(sharedFileEncoders) != maxFileEncoders ||
		maxFilePayloadBytes != 64<<20 {
		t.Fatal("file payload limit did not cover the full transport lifetime")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("gate cancellation=%v", err)
	}
	for _, client := range clients {
		_ = client.Close()
	}
	for range maxFileEncoders {
		<-done
	}
	if len(sharedFileEncoders) != 0 {
		t.Fatal("transport shutdown leaked shared reservations")
	}
}
