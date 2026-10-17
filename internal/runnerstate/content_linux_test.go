package runnerstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"golang.org/x/sys/unix"
)

func contentTestState(t *testing.T) *State {
	t.Helper()
	state, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func contentTestIdentity(document []byte) jobcontract.ContentIdentity {
	digest := sha256.Sum256(document)
	return jobcontract.ContentIdentity{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(document))}
}

func contentTestRead(t *testing.T, state *State, identity jobcontract.ContentIdentity) []byte {
	t.Helper()
	file, err := state.OpenContent(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	document, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func contentTestPartials(t *testing.T, state *State) []string {
	t.Helper()
	entries, err := os.ReadDir(state.Path())
	if err != nil {
		t.Fatal(err)
	}
	partials := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".content-") && strings.HasSuffix(entry.Name(), ".partial") {
			partials = append(partials, entry.Name())
		}
	}
	return partials
}

type contentProbeReader struct {
	source  io.Reader
	calls   int
	maxRead int
}

func (reader *contentProbeReader) Read(buffer []byte) (int, error) {
	reader.calls++
	reader.maxRead = max(reader.maxRead, len(buffer))
	return reader.source.Read(buffer)
}

func TestContentStreamsPrivateBytesAndReopensSeekableContent(t *testing.T) {
	for _, size := range []int{1, 3*contentBufferBytes + 13} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			state := contentTestState(t)
			document := bytes.Repeat([]byte("p"), size)
			identity := contentTestIdentity(document)
			source := &contentProbeReader{source: bytes.NewReader(document)}
			created, err := state.PutContent(t.Context(), identity, source)
			if err != nil || !created || source.maxRead > contentBufferBytes {
				t.Fatalf(
					"bounded initial publication: created=%v max_read=%d error=%v",
					created,
					source.maxRead,
					err,
				)
			}
			if got := contentTestRead(t, state, identity); !bytes.Equal(got, document) {
				t.Fatal("published bytes changed")
			}
			file, err := state.OpenContent(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(-1, io.SeekEnd); err != nil {
				t.Fatal(err)
			}
			one := make([]byte, 1)
			if n, err := file.Read(one); n != 1 || err != nil || one[0] != 'p' {
				t.Fatalf("seekable stream lost its final byte: n=%d error=%v", n, err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			path := state.Path()
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if got := contentTestRead(t, reopened, identity); !bytes.Equal(got, document) {
				t.Fatal("reopen lost complete durable content")
			}
			if len(contentTestPartials(t, reopened)) != 0 {
				t.Fatal("successful publication left a duplicate scratch file")
			}
		})
	}
}

func TestContentRejectsInvalidIdentityBeforeReading(t *testing.T) {
	state := contentTestState(t)
	valid := contentTestIdentity([]byte("x"))
	for _, identity := range []jobcontract.ContentIdentity{
		{SHA256: valid.SHA256, Size: 0},
		{SHA256: valid.SHA256, Size: -1},
		{SHA256: valid.SHA256, Size: jobcontract.MaxAssetBytes + 1},
		{SHA256: strings.ToUpper(valid.SHA256), Size: 1},
		{SHA256: "../" + valid.SHA256, Size: 1},
		{SHA256: strings.Repeat("g", 64), Size: 1},
		{SHA256: "", Size: 1},
	} {
		source := &contentProbeReader{source: strings.NewReader("x")}
		if _, err := state.PutContent(t.Context(), identity, source); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid identity accepted: %v", err)
		}
		if _, err := state.OpenContent(t.Context(), identity); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid read identity accepted: %v", err)
		}
		if source.calls != 0 {
			t.Fatal("invalid identity consumed source bytes")
		}
	}
	if len(contentTestPartials(t, state)) != 0 {
		t.Fatal("invalid identity created scratch files")
	}
	if _, err := state.PutContent(nil, valid, strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil context accepted")
	}
	if _, err := state.PutContent(t.Context(), valid, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil body accepted")
	}
	maximum := jobcontract.ContentIdentity{SHA256: valid.SHA256, Size: jobcontract.MaxAssetBytes}
	source := &contentProbeReader{source: strings.NewReader("")}
	_, err := state.PutContent(t.Context(), maximum, source)
	if !errors.Is(err, io.ErrUnexpectedEOF) || source.calls != 1 {
		t.Fatalf("legal maximum was rejected before its short source: calls=%d error=%v", source.calls, err)
	}
}

type contentZeroReader struct {
	read    int64
	maxRead int
}

func (reader *contentZeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	reader.read += int64(len(buffer))
	reader.maxRead = max(reader.maxRead, len(buffer))
	return len(buffer), nil
}

func TestContentMaximumLimiterStreamsExactlyExpectedPlusOneWithoutDisk(t *testing.T) {
	source := &contentZeroReader{}
	identity := jobcontract.ContentIdentity{SHA256: strings.Repeat("a", 64), Size: jobcontract.MaxAssetBytes}
	err := streamContent(
		t.Context(),
		identity,
		source,
		io.Discard,
	)
	validReads := source.read == jobcontract.MaxAssetBytes+1 && source.maxRead <= contentBufferBytes
	if !errors.Is(err, ErrContentCorrupt) || !validReads {
		t.Fatalf(
			"maximum stream bound: bytes=%d max_read=%d error=%v",
			source.read,
			source.maxRead,
			err,
		)
	}
}

func TestContentRejectsWrongBodiesAndPreservesEveryFailedPartial(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, body := range []string{"short", "long", "xxxx"} {
			t.Run(body+"/existing="+strconv.FormatBool(existing), func(t *testing.T) {
				state := contentTestState(t)
				document := []byte("good")
				identity := contentTestIdentity(document)
				if existing {
					if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
						t.Fatal(err)
					}
				}
				value := body
				if body == "short" {
					value = "g"
				}
				if body == "long" {
					value = "good-extra"
				}
				created, err := state.PutContent(t.Context(), identity, strings.NewReader(value))
				if err == nil || created {
					t.Fatal("wrong upload body was acknowledged")
				}
				if len(contentTestPartials(t, state)) != 1 {
					t.Fatal("failed upload did not retain its own partial")
				}
				if existing {
					if got := contentTestRead(t, state, identity); !bytes.Equal(got, document) {
						t.Fatal("wrong duplicate upload changed valid existing content")
					}
				} else if _, err := os.Lstat(filepath.Join(state.Path(), contentName(identity))); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed upload published a complete name")
				}
			})
		}
	}
}

func TestContentConcurrentDuplicateUploadsHaveOnePublisher(t *testing.T) {
	state := contentTestState(t)
	document := bytes.Repeat([]byte("x"), 2*contentBufferBytes+1)
	identity := contentTestIdentity(document)
	var created atomic.Int32
	var workers sync.WaitGroup
	failures := make(chan error, 16)
	for range 16 {
		workers.Go(func() {
			fresh, err := state.PutContent(t.Context(), identity, bytes.NewReader(document))
			if fresh {
				created.Add(1)
			}
			failures <- err
		})
	}
	workers.Wait()
	for range 16 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if created.Load() != 1 || len(contentTestPartials(t, state)) != 0 {
		t.Fatalf("duplicate publication is not idempotent: created=%d", created.Load())
	}
	if got := contentTestRead(t, state, identity); !bytes.Equal(got, document) {
		t.Fatal("concurrent publication changed complete bytes")
	}
}

type contentReaderFunc func([]byte) (int, error)

func (read contentReaderFunc) Read(buffer []byte) (int, error) { return read(buffer) }

func TestContentCancellationAndReaderFailureAffectOnlyTheirOwnIO(t *testing.T) {
	state := contentTestState(t)
	document := bytes.Repeat([]byte("a"), 2*contentBufferBytes)
	identity := contentTestIdentity(document)
	ctx, cancel := context.WithCancel(t.Context())
	var calls int
	source := contentReaderFunc(func(buffer []byte) (int, error) {
		calls++
		if calls == 2 {
			cancel()
		}
		return copy(buffer, document), nil
	})
	if _, err := state.PutContent(ctx, identity, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("stream ignored cancellation: %v", err)
	}
	if len(contentTestPartials(t, state)) != 1 {
		t.Fatal("cancelled stream discarded its partial")
	}
	bodyError := errors.New("reader failed")
	failed := contentReaderFunc(func(buffer []byte) (int, error) { return copy(buffer, document[:17]), bodyError })
	if _, err := state.PutContent(t.Context(), identity, failed); !errors.Is(err, bodyError) {
		t.Fatalf("reader error was hidden: %v", err)
	}
	if len(contentTestPartials(t, state)) != 2 {
		t.Fatal("reader failure discarded a partial")
	}
	if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
		t.Fatal(err)
	}
	readContext, stop := context.WithCancel(t.Context())
	file, err := state.OpenContent(readContext, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stop()
	if n, err := file.Read(make([]byte, 1)); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reader remained usable")
	}
	if got := contentTestRead(t, state, identity); !bytes.Equal(got, document) {
		t.Fatal("one cancelled reader affected another stream")
	}
}

func TestContentRejectsUnsafeOrCorruptCompleteFilesWithoutReplacingThem(t *testing.T) {
	for _, kind := range []string{
		"digest", "size", "mode", "special mode", "owner", "hardlink", "symlink", "directory", "fifo",
	} {
		t.Run(kind, func(t *testing.T) {
			state := contentTestState(t)
			document := []byte("good")
			identity := contentTestIdentity(document)
			path := filepath.Join(state.Path(), contentName(identity))
			value := document
			if kind == "digest" {
				value = []byte("evil")
			}
			if kind == "size" {
				value = []byte("shorter")
			}
			switch kind {
			case "symlink":
				if err := os.WriteFile(filepath.Join(state.Path(), "retained"), document, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("retained", path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(path, value, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "special mode":
				if err := os.Chmod(path, 0600|os.ModeSetuid); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if os.Geteuid() != 0 {
					t.Skip("owner test requires root")
				}
				if err := os.Chown(path, 1, -1); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(state.Path(), "retained")); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.OpenContent(t.Context(), identity); err == nil {
				t.Fatal("unsafe complete content was opened")
			}
			if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err == nil {
				t.Fatal("unsafe complete content was automatically replaced")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("rejecting corrupt content changed its inode or permissions")
			}
			if before.Mode().IsRegular() {
				retained, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(retained, value) {
					t.Fatal("rejecting corrupt content changed its bytes")
				}
			}
		})
	}
}

func TestContentRejectsReplacedRootAndFileDuringOpenStreams(t *testing.T) {
	for _, replacement := range []string{"root", "root symlink", "file", "in place"} {
		t.Run(replacement, func(t *testing.T) {
			state := contentTestState(t)
			document := []byte("good")
			identity := contentTestIdentity(document)
			if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
				t.Fatal(err)
			}
			stream, err := state.OpenContent(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			path := filepath.Join(state.Path(), contentName(identity))
			if strings.HasPrefix(replacement, "root") {
				if err := os.Rename(state.Path(), state.Path()+".retained"); err != nil {
					t.Fatal(err)
				}
				if replacement == "root symlink" {
					if err := os.Symlink(state.Path()+".retained", state.Path()); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(state.Path(), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if replacement == "file" {
					if err := os.Rename(path, path+".retained"); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, []byte("evil"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := stream.Read(make([]byte, 4)); n != 0 || err == nil {
				t.Fatal("open stream read through a replaced identity")
			}
			if _, err := stream.Seek(0, io.SeekStart); err == nil {
				t.Fatal("open stream sought through a replaced identity")
			}
			if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err == nil {
				t.Fatal("new upload accepted a replaced state or corrupt complete file")
			}
		})
	}
}

func TestContentDurabilityFailuresNeverAcknowledgeAnExistingName(t *testing.T) {
	for _, failDirectory := range []bool{false, true} {
		state := contentTestState(t)
		document := []byte("good")
		identity := contentTestIdentity(document)
		failure := errors.New("durability failed")
		state.contentSync = func(file *os.File) error {
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if info.IsDir() == failDirectory {
				return failure
			}
			return file.Sync()
		}
		_, err := state.PutContent(t.Context(), identity, bytes.NewReader(document))
		if !errors.Is(err, failure) {
			t.Fatalf("failed durability confirmation was acknowledged: %v", err)
		}
		_, err = state.PutContent(t.Context(), identity, bytes.NewReader(document))
		if !errors.Is(err, failure) {
			t.Fatalf("existence after failure became a success acknowledgement: %v", err)
		}
		if failDirectory {
			if got := contentTestRead(t, state, identity); !bytes.Equal(got, document) {
				t.Fatal("unconfirmed publication was discarded")
			}
		} else if len(contentTestPartials(t, state)) != 2 {
			t.Fatal("file sync failures discarded partials")
		}
	}
}

func TestContentRevalidatesPublicationAfterDirectoryConfirmation(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, mutation := range []string{"root", "file", "cancel"} {
			t.Run(mutation+"/existing="+strconv.FormatBool(existing), func(t *testing.T) {
				state := contentTestState(t)
				document := []byte("good")
				identity := contentTestIdentity(document)
				if existing {
					if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				state.contentSync = func(file *os.File) error {
					info, err := file.Stat()
					if err != nil {
						return err
					}
					if !info.IsDir() {
						return file.Sync()
					}
					switch mutation {
					case "root":
						if err := os.Rename(state.Path(), state.Path()+".retained"); err != nil {
							return err
						}
						if err := os.Mkdir(state.Path(), 0700); err != nil {
							return err
						}
					case "file":
						path := filepath.Join(state.Path(), contentName(identity))
						if err := os.Rename(path, path+".retained"); err != nil {
							return err
						}
						if err := os.WriteFile(path, []byte("evil"), 0600); err != nil {
							return err
						}
					case "cancel":
						cancel()
					}
					return file.Sync()
				}
				if _, err := state.PutContent(ctx, identity, bytes.NewReader(document)); err == nil {
					t.Fatal("publication acknowledged an identity or context changed during confirmation")
				}
				if mutation == "file" {
					retained, err := os.ReadFile(filepath.Join(state.Path(), contentName(identity)) + ".retained")
					if err != nil || !bytes.Equal(retained, document) {
						t.Fatal("rejected replacement discarded the verified original content")
					}
				}
			})
		}
	}
}

func TestContentCloseDoesNotWaitForOpaqueBodyAndPreventsLaterPublication(t *testing.T) {
	state := contentTestState(t)
	document := []byte("good")
	identity := contentTestIdentity(document)
	started, release := make(chan struct{}), make(chan struct{})
	body := contentReaderFunc(func(buffer []byte) (int, error) {
		close(started)
		<-release
		return copy(buffer, document), io.EOF
	})
	done := make(chan error, 1)
	go func() {
		_, err := state.PutContent(t.Context(), identity, body)
		done <- err
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- state.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("State.Close waited on the caller-owned opaque reader")
	}
	close(release)
	if err := <-done; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed state continued publication: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(state.Path(), contentName(identity))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("closed state published complete content")
	}
	if len(contentTestPartials(t, state)) != 1 {
		t.Fatal("close discarded the in-flight partial")
	}
	if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed state accepted an upload")
	}
	if _, err := state.OpenContent(t.Context(), identity); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed state accepted a reader")
	}
}

func TestContentReadCloseAndFailurePathsDoNotLeakDescriptors(t *testing.T) {
	state := contentTestState(t)
	document := []byte("good")
	identity := contentTestIdentity(document)
	if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
		t.Fatal(err)
	}
	countDescriptors := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := countDescriptors()
	for range 100 {
		stream, err := state.OpenContent(t.Context(), identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
			t.Fatal("closed stream remained usable")
		}
		wrong := identity
		wrong.Size++
		if _, err := state.OpenContent(t.Context(), wrong); err == nil {
			t.Fatal("wrong-sized content was opened")
		}
	}
	if after := countDescriptors(); after != before {
		t.Fatalf("content operations leaked descriptors: before=%d after=%d", before, after)
	}
	stream, err := state.OpenContent(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatal("state close did not invalidate its reader")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}
