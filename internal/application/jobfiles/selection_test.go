package jobfiles

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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func selectionImage(t *testing.T, mediaType string, size int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var err error
	if mediaType == "image/png" {
		err = png.Encode(&buffer, image.NewRGBA(image.Rectangle{Max: image.Point{X: 1, Y: 1}}))
	} else {
		err = jpeg.Encode(&buffer, image.NewRGBA(image.Rectangle{Max: image.Point{X: 1, Y: 1}}), nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	if size > len(data) {
		data = append(data, make([]byte, size-len(data))...)
	}
	return data
}

func selectionDocument(t *testing.T, data []byte, mediaType string) string {
	t.Helper()
	document, err := json.Marshal(map[string]string{
		"data_base64": base64.StdEncoding.EncodeToString(data), "media_type": mediaType,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

func assertEmptyRegistry(t *testing.T, registry *Registry) {
	t.Helper()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.usedBytes != 0 || len(registry.entries) != 0 || registry.importing {
		t.Fatalf(
			"registry retained resources: bytes=%d entries=%d importing=%t",
			registry.usedBytes,
			len(registry.entries),
			registry.importing,
		)
	}
}

func TestSelectionOwnsBytesAndIndependentReaders(t *testing.T) {
	registry := NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	for _, mediaType := range []string{"image/png", "image/jpeg"} {
		t.Run(mediaType, func(t *testing.T) {
			data := selectionImage(t, mediaType, 0)
			expected := append([]byte{}, data...)
			selection, err := registry.Import(context.Background(), selectionDocument(t, data, mediaType))
			if err != nil {
				t.Fatal(err)
			}
			clear(data)
			digest := sha256.Sum256(expected)
			if selection.SHA256 != hex.EncodeToString(digest[:]) || selection.Size != int64(len(expected)) {
				t.Fatal("selection does not identify copied bytes")
			}
			first, err := registry.Borrow(context.Background(), selection.ID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := registry.Borrow(context.Background(), selection.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.Release(selection.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := registry.Borrow(context.Background(), selection.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("released selection allowed a new borrow")
			}
			for _, reader := range []io.ReadCloser{first, second} {
				actual, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("borrowed snapshot changed: %v", err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
			}
			assertEmptyRegistry(t, registry)
		})
	}
}

func TestSelectionStrictDTOAndSizeBoundaries(t *testing.T) {
	registry := NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	valid := selectionDocument(t, selectionImage(t, "image/png", 0), "image/png")
	tests := []struct {
		name     string
		document string
		want     error
	}{
		{name: "empty", document: "", want: ErrInvalid},
		{name: "null", document: "null", want: ErrInvalid},
		{name: "unknown", document: strings.TrimSuffix(valid, "}") + `,"path":"C:/private.png"}`, want: ErrInvalid},
		{name: "duplicate", document: strings.TrimSuffix(valid, "}") + `,"media_type":"image/png"}`, want: ErrInvalid},
		{name: "case alias", document: strings.Replace(valid, "media_type", "Media_Type", 1), want: ErrInvalid},
		{name: "missing", document: `{"data_base64":"YQ=="}`, want: ErrInvalid},
		{name: "null media", document: `{"data_base64":"YQ==","media_type":null}`, want: ErrInvalid},
		{name: "null bytes", document: `{"data_base64":null,"media_type":"image/png"}`, want: ErrInvalid},
		{name: "trailing", document: valid + `{}`, want: ErrInvalid},
		{name: "whitespace in base64", document: `{"data_base64":"YQ==\n","media_type":"image/png"}`, want: ErrInvalid},
		{name: "noncanonical padding bits", document: `{"data_base64":"YR==","media_type":"image/png"}`, want: ErrInvalid},
		{name: "empty bytes", document: `{"data_base64":"","media_type":"image/png"}`, want: ErrInvalid},
		{
			name: "wrong MIME", want: ErrInvalid,
			document: selectionDocument(t, selectionImage(t, "image/png", 0), "image/jpeg"),
		},
		{name: "unsupported MIME", document: selectionDocument(t, []byte("GIF89a"), "image/gif"), want: ErrInvalid},
		{name: "one invalid byte", document: selectionDocument(t, []byte{1}, "image/png"), want: ErrInvalid},
		{
			name: "max plus one", want: ErrTooLarge,
			document: selectionDocument(t, make([]byte, MaxSelectionBytes+1), "image/png"),
		},
		{name: "document max plus one", document: strings.Repeat(" ", MaxSelectionDocumentBytes+1), want: ErrTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registry.Import(context.Background(), test.document); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			assertEmptyRegistry(t, registry)
		})
	}
	for _, size := range []int{MaxSelectionBytes - 2, MaxSelectionBytes - 1, MaxSelectionBytes} {
		data := selectionImage(t, "image/png", size)
		selection, err := registry.Import(context.Background(), selectionDocument(t, data, "image/png"))
		if err != nil || selection.Size != int64(size) {
			t.Fatalf("valid bounded payload %d: %v", size, err)
		}
		if err := registry.Release(selection.ID); err != nil {
			t.Fatal(err)
		}
	}
	assertEmptyRegistry(t, registry)
}

func TestSelectionValidatesOnlyBoundedImageConfiguration(t *testing.T) {
	registry := NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	for _, width := range []uint32{4096, 4097} {
		data := selectionImage(t, "image/png", 0)
		binary.BigEndian.PutUint32(data[16:20], width)
		binary.BigEndian.PutUint32(data[20:24], 4096)
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		selection, err := registry.Import(context.Background(), selectionDocument(t, data, "image/png"))
		if width == 4097 {
			if !errors.Is(err, ErrInvalid) {
				t.Fatal("overlarge dimensions accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		// The header is accepted without allocating a decoded 4096x4096 image.
		// This intentionally does not claim that the compressed pixels are valid.
		_ = registry.Release(selection.ID)
	}
	assertEmptyRegistry(t, registry)
}

func TestSelectionQuotaIncludesReleasedBorrowedBytes(t *testing.T) {
	registry := NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	document := selectionDocument(t, selectionImage(t, "image/png", MaxSelectionBytes), "image/png")
	selections := []Selection{}
	for range MaxSelections {
		selection, err := registry.Import(context.Background(), document)
		if err != nil {
			t.Fatal(err)
		}
		selections = append(selections, selection)
	}
	borrowed, err := registry.Borrow(context.Background(), selections[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Release(selections[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Import(context.Background(), document); !errors.Is(err, ErrLimit) {
		t.Fatal("released but borrowed bytes escaped the ledger")
	}
	_ = borrowed.Close()
	replacement, err := registry.Import(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	_ = registry.Release(replacement.ID)
	for _, selection := range selections {
		_ = registry.Release(selection.ID)
	}
	assertEmptyRegistry(t, registry)
}

func TestSelectionCancellationAndCloseInvalidateReaders(t *testing.T) {
	registry := NewRegistry()
	document := selectionDocument(t, selectionImage(t, "image/png", 0), "image/png")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Import(cancelled, document); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled import accepted")
	}
	assertEmptyRegistry(t, registry)
	selection, err := registry.Import(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	borrowContext, stopBorrow := context.WithCancel(context.Background())
	reader, err := registry.Borrow(borrowContext, selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	stopBorrow()
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal("reader ignored cancellation")
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Fatal("reader outlived registry Close")
	}
	_ = reader.Close()
	_ = registry.Close()
	assertEmptyRegistry(t, registry)
	if _, err := registry.Import(context.Background(), document); !errors.Is(err, ErrClosed) {
		t.Fatal("closed registry admitted import")
	}
}

func TestSelectionConcurrentReadReleaseAndClose(t *testing.T) {
	registry := NewRegistry()
	data := selectionImage(t, "image/png", MaxSelectionBytes)
	selection, err := registry.Import(context.Background(), selectionDocument(t, data, "image/png"))
	if err != nil {
		t.Fatal(err)
	}
	readers := []io.ReadCloser{}
	for range 8 {
		reader, err := registry.Borrow(context.Background(), selection.ID)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, reader)
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for _, reader := range readers {
		workers.Go(func() {
			<-start
			_, readErr := io.Copy(io.Discard, reader)
			if readErr != nil && !errors.Is(readErr, ErrClosed) {
				t.Errorf("read: %v", readErr)
			}
			_ = reader.Close()
		})
	}
	close(start)
	_ = registry.Release(selection.ID)
	_ = registry.Close()
	workers.Wait()
	assertEmptyRegistry(t, registry)
}

func TestSelectionCloseJoinsTheImportOwner(t *testing.T) {
	registry := NewRegistry()
	registry.mu.Lock()
	registry.importing = true
	registry.mu.Unlock()
	if _, err := registry.Import(context.Background(), "{}"); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent import was not rejected")
	}
	closed := make(chan struct{})
	go func() {
		_ = registry.Close()
		close(closed)
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		registry.mu.Lock()
		closing := registry.closed
		registry.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("Close did not stop admission")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the import owner finished")
	default:
	}
	registry.mu.Lock()
	registry.importing = false
	registry.drained.Broadcast()
	registry.mu.Unlock()
	select {
	case <-closed:
	case <-deadline.C:
		t.Fatal("Close did not join the finished import owner")
	}
	assertEmptyRegistry(t, registry)
}
