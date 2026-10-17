package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func testCache(t *testing.T) (*Cache, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "documents")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cache, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return cache, root
}

func testReference(value []byte) jobcontract.ReportReference {
	digest := sha256.Sum256(value)
	return jobcontract.ReportReference{ID: "11111111-1111-4111-8111-111111111111", Kind: jobcontract.KindPerformance,
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(value))}
}

func resumeBytes(value []byte) func(context.Context, io.ReadWriteSeeker) error {
	return func(_ context.Context, file io.ReadWriteSeeker) error {
		offset, err := file.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
		_, err = file.Write(value[offset:])
		return err
	}
}

func TestCacheResumesPartialAcrossInstancesAndReusesCompleteDocumentOffline(t *testing.T) {
	cache, root := testCache(t)
	value := []byte(`{"original_measurement":"0123456789"}`)
	reference := testReference(value)
	interrupted := errors.New("interrupted fixture")
	_, err := cache.Read(t.Context(), reference, func(_ context.Context, file io.ReadWriteSeeker) error {
		if _, err := file.Write(value[:17]); err != nil {
			return err
		}
		return interrupted
	})
	if !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	partialPath := filepath.Join(root, reference.SHA256+".partial")
	partial, err := os.ReadFile(partialPath)
	if err != nil || !bytes.Equal(partial, value[:17]) {
		t.Fatal("interrupted transfer did not retain its exact prefix")
	}
	if _, err := os.Stat(filepath.Join(root, reference.SHA256+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial document was published")
	}
	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := reopened.Read(t.Context(), reference, func(ctx context.Context, file io.ReadWriteSeeker) error {
		offset, err := file.Seek(0, io.SeekEnd)
		if err != nil || offset != 17 {
			t.Fatal("resume offset changed")
		}
		return resumeBytes(value)(ctx, file)
	})
	if err != nil || !bytes.Equal(actual, value) {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful publication retained duplicate partial bytes")
	}
	actual, err = cache.Read(t.Context(), reference, nil)
	if err != nil || !bytes.Equal(actual, value) {
		t.Fatal("complete document could not be read offline")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, reference.SHA256+".json"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("document permissions are not private")
		}
	}
}

func TestPublicationFailureDoesNotAcknowledgeAndRetryUsesVerifiedBytes(t *testing.T) {
	for _, afterRename := range []bool{false, true} {
		t.Run(map[bool]string{false: "before rename", true: "after rename"}[afterRename], func(t *testing.T) {
			cache, root := testCache(t)
			value := []byte(`{"stable":"original bytes"}`)
			reference := testReference(value)
			failure := errors.New("publication confirmation failed")
			cache.publish = func(source, target string) error {
				if afterRename {
					if err := publishFile(source, target); err != nil {
						return err
					}
				}
				return failure
			}
			actual, err := cache.Read(t.Context(), reference, resumeBytes(value))
			if !errors.Is(err, failure) || actual != nil {
				t.Fatal("failed publication was acknowledged")
			}
			reopened, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			actual, err = reopened.Read(t.Context(), reference, func(context.Context, io.ReadWriteSeeker) error {
				t.Fatal("retry downloaded already complete bytes")
				return interruptedRead
			})
			if err != nil || !bytes.Equal(actual, value) {
				t.Fatal(err)
			}
		})
	}
}

var interruptedRead = errors.New("unexpected cache fetch")

func TestIndependentCachesSerializeDownloadsAndCancellationOnlyStopsWaiting(t *testing.T) {
	first, root := testCache(t)
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte(strings.Repeat("stable-original-document", 100))
	reference := testReference(value)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	fetch := func(ctx context.Context, file io.ReadWriteSeeker) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return resumeBytes(value)(ctx, file)
	}
	done := make(chan error, 1)
	go func() { _, err := first.Read(t.Context(), reference, fetch); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first cache did not enter download")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := second.Read(ctx, reference, fetch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting cache ignored cancellation: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			actual, err := second.Read(t.Context(), reference, fetch)
			if err != nil || !bytes.Equal(actual, value) {
				t.Errorf("cache replay failed: %v", err)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatal("independent cache instances repeated a completed download")
	}
}

func TestCorruptDocumentsAndPrefixesArePreservedWithoutReset(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "complete"}[complete], func(t *testing.T) {
			cache, root := testCache(t)
			value := []byte("original-document-with-stable-metrics")
			reference := testReference(value)
			suffix := ".partial"
			corrupted := []byte("WRONG")
			if complete {
				suffix = ".json"
				corrupted = bytes.Repeat([]byte("X"), len(value))
			}
			path := filepath.Join(root, reference.SHA256+suffix)
			if err := os.WriteFile(path, corrupted, 0o600); err != nil {
				t.Fatal(err)
			}
			fetch := resumeBytes(value)
			if complete {
				fetch = func(context.Context, io.ReadWriteSeeker) error {
					t.Fatal("corrupt complete document triggered an overwrite")
					return interruptedRead
				}
			}
			if _, err := cache.Read(t.Context(), reference, fetch); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("corruption accepted: %v", err)
			}
			retained, err := os.ReadFile(path)
			if err != nil || !bytes.HasPrefix(retained, corrupted) {
				t.Fatal("corrupt data was reset")
			}
			if !complete {
				if _, err := os.Stat(filepath.Join(root, reference.SHA256+".json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("corrupt resumed prefix was published")
				}
			}
		})
	}
}

func TestCacheBoundsWriterAndRejectsChangedRootAndSymlinks(t *testing.T) {
	t.Run("writer cannot bypass byte budget through io copy", func(t *testing.T) {
		cache, root := testCache(t)
		value := []byte("expected")
		reference := testReference(value)
		_, err := cache.Read(t.Context(), reference, func(_ context.Context, file io.ReadWriteSeeker) error {
			_, err := io.CopyN(file, struct{ io.Reader }{Reader: strings.NewReader("overlong-input")}, reference.Size+1)
			return err
		})
		if !errors.Is(err, ErrIntegrity) {
			t.Fatalf("unbounded writer accepted: %v", err)
		}
		info, err := os.Stat(filepath.Join(root, reference.SHA256+".partial"))
		if err != nil || info.Size() > reference.Size {
			t.Fatal("failed writer exceeded its reserved document size")
		}
	})
	t.Run("root identity", func(t *testing.T) {
		cache, root := testCache(t)
		if err := os.Rename(root, root+"-retained"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		value := []byte("original")
		if _, err := cache.Read(t.Context(), testReference(value), resumeBytes(value)); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("changed cache root was accepted: %v", err)
		}
	})
	for _, suffix := range []string{".json", ".partial", ".lock"} {
		t.Run("symlink"+suffix, func(t *testing.T) {
			cache, root := testCache(t)
			value := []byte("original")
			reference := testReference(value)
			outside := filepath.Join(filepath.Dir(root), "outside")
			if err := os.WriteFile(outside, value, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, reference.SHA256+suffix)); err != nil {
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege unavailable")
				}
				t.Fatal(err)
			}
			if _, err := cache.Read(t.Context(), reference, resumeBytes(value)); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("symlink accepted: %v", err)
			}
			retained, err := os.ReadFile(outside)
			if err != nil || !bytes.Equal(retained, value) {
				t.Fatal("symlink target was changed")
			}
		})
	}
}
