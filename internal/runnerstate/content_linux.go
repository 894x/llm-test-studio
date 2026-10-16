package runnerstate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"golang.org/x/sys/unix"
)

const contentBufferBytes = 32 << 10

var ErrContentCorrupt = errors.New("invalid_runner_content")

type ContentIdentity struct {
	SHA256 string
	Size   int64
}

func (identity ContentIdentity) Validate() error {
	validSize := identity.Size >= 1 && identity.Size <= jobcontract.MaxAssetBytes
	if !jobcontract.IsSHA256(identity.SHA256) || !validSize {
		return ErrInvalid
	}
	return nil
}

// ContentReader is a verified, seekable private file. State.Close closes its
// descriptor; cancellation affects only this stream. Read and Seek reject a
// replaced file or state directory without rehashing the complete file.
type ContentReader struct {
	mu       sync.Mutex
	state    *State
	ctx      context.Context
	file     *os.File
	name     string
	identity ContentIdentity
	info     os.FileInfo
}

var _ io.ReadSeekCloser = (*ContentReader)(nil)

// PutContent verifies every incoming body, including duplicate uploads. Only a
// nil error confirms durable publication; created then distinguishes a new file
// from existing identical content. Failed partials and corrupt files are kept.
// A blocking source reader must honor ctx itself, as an HTTP request body does.
func (state *State) PutContent(
	ctx context.Context,
	identity ContentIdentity,
	source io.Reader,
) (created bool, err error) {
	if ctx == nil || source == nil || identity.Validate() != nil {
		return false, ErrInvalid
	}
	if err := state.contentReady(ctx); err != nil {
		return false, err
	}
	temporary := ".content-" + identity.SHA256 + "-" + rand.Text() + ".partial"
	file, err := openPrivate(state.root, temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return false, err
	}
	if err := state.registerContentFile(file); err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, state.releaseContentFile(file)) }()
	if err := streamContent(
		ctx,
		identity,
		source,
		file,
	); err != nil {
		return false, err
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !validContentInfo(info, identity.Size) {
		return false, ErrContentCorrupt
	}
	if err := state.syncContentFile(file); err != nil {
		return false, err
	}
	created, err = state.publishContent(
		ctx,
		identity,
		temporary,
		file,
		info,
	)
	if err != nil || created {
		return created, err
	}
	existing, err := state.OpenContent(ctx, identity)
	if err != nil {
		return false, err
	}
	defer existing.Close()
	state.contentMu.Lock()
	defer state.contentMu.Unlock()
	if err := state.contentReadyLocked(ctx); err != nil {
		return false, err
	}
	if err := state.matchContentFile(
		existing.name,
		existing.file,
		existing.info,
		identity.Size,
	); err != nil {
		return false, err
	}
	if err := state.matchContentFile(
		temporary,
		file,
		info,
		identity.Size,
	); err != nil {
		return false, err
	}
	if err := state.syncContentFile(existing.file); err != nil {
		return false, err
	}
	if err := state.syncContentDirectory(); err != nil {
		return false, err
	}
	if err := state.contentReadyLocked(ctx); err != nil {
		return false, err
	}
	if err := state.matchContentFile(
		existing.name,
		existing.file,
		existing.info,
		identity.Size,
	); err != nil {
		return false, err
	}
	if err := state.matchContentFile(
		temporary,
		file,
		info,
		identity.Size,
	); err != nil {
		return false, err
	}
	// Only this operation's verified scratch copy is removed, after the existing
	// content has been durably confirmed. No complete content deletion is exposed.
	if err := state.root.Remove(temporary); err != nil {
		return false, err
	}
	return false, nil
}

func (state *State) OpenContent(ctx context.Context, identity ContentIdentity) (*ContentReader, error) {
	if ctx == nil || identity.Validate() != nil {
		return nil, ErrInvalid
	}
	if err := state.contentReady(ctx); err != nil {
		return nil, err
	}
	name := contentName(identity)
	file, err := openPrivate(state.root, name, os.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	if err := state.registerContentFile(file); err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			state.releaseContentFile(file)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !validContentInfo(info, identity.Size) {
		return nil, ErrContentCorrupt
	}
	if err := streamContent(
		ctx,
		identity,
		file,
		io.Discard,
	); err != nil {
		return nil, err
	}
	if err := state.contentReady(ctx); err != nil {
		return nil, err
	}
	if err := state.matchContentFile(
		name,
		file,
		info,
		identity.Size,
	); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	valid = true
	return &ContentReader{state: state, ctx: ctx, file: file, name: name, identity: identity, info: info}, nil
}

func (reader *ContentReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if err := reader.ready(); err != nil {
		return 0, err
	}
	n, err := reader.file.Read(buffer)
	if checkErr := reader.ready(); checkErr != nil {
		return 0, checkErr
	}
	return n, err
}

func (reader *ContentReader) Seek(offset int64, whence int) (int64, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if err := reader.ready(); err != nil {
		return 0, err
	}
	position, err := reader.file.Seek(offset, whence)
	if checkErr := reader.ready(); checkErr != nil {
		return 0, checkErr
	}
	return position, err
}

func (reader *ContentReader) Close() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.file == nil {
		return nil
	}
	err := reader.state.releaseContentFile(reader.file)
	reader.file = nil
	return err
}

func (reader *ContentReader) ready() error {
	if reader.file == nil {
		return os.ErrClosed
	}
	if err := reader.state.contentReady(reader.ctx); err != nil {
		return err
	}
	return reader.state.matchContentFile(
		reader.name,
		reader.file,
		reader.info,
		reader.identity.Size,
	)
}

func contentName(identity ContentIdentity) string { return "content-" + identity.SHA256 + ".blob" }

func validContentInfo(info os.FileInfo, size int64) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	validMode := info.Mode() == 0600
	validStat := ok && stat.Uid == uint32(os.Geteuid())
	validOwner := validStat && stat.Nlink == 1
	return validMode && validOwner && info.Size() == size
}

func (state *State) contentReady(ctx context.Context) error {
	if state == nil {
		return ErrInvalid
	}
	state.contentMu.Lock()
	defer state.contentMu.Unlock()
	return state.contentReadyLocked(ctx)
}

func (state *State) contentReadyLocked(ctx context.Context) error {
	if state.contentClosed {
		return os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(state.path)
	if err != nil {
		return err
	}
	if resolved != state.path {
		return ErrContentCorrupt
	}
	current, err := os.Lstat(state.path)
	if err != nil {
		return err
	}
	pinned, err := state.root.Stat(".")
	if err != nil {
		return err
	}
	privateDirectory := current.IsDir() && privateOwned(current)
	validRoot := privateDirectory && os.SameFile(current, pinned)
	if !validRoot {
		return ErrContentCorrupt
	}
	return nil
}

func (state *State) registerContentFile(file *os.File) error {
	state.contentMu.Lock()
	defer state.contentMu.Unlock()
	if state.contentClosed {
		_ = file.Close()
		return os.ErrClosed
	}
	if state.contentFiles == nil {
		state.contentFiles = map[*os.File]struct{}{}
	}
	state.contentFiles[file] = struct{}{}
	return nil
}

func (state *State) releaseContentFile(file *os.File) error {
	state.contentMu.Lock()
	delete(state.contentFiles, file)
	state.contentMu.Unlock()
	err := file.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func (state *State) closeContent() error {
	state.contentMu.Lock()
	state.contentClosed = true
	files := make([]*os.File, 0, len(state.contentFiles))
	for file := range state.contentFiles {
		files = append(files, file)
	}
	state.contentFiles = map[*os.File]struct{}{}
	state.contentMu.Unlock()
	var result error
	for _, file := range files {
		if err := file.Close(); !errors.Is(err, os.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (state *State) matchContentFile(
	name string,
	file *os.File,
	before os.FileInfo,
	size int64,
) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	named, err := state.root.Lstat(name)
	if err != nil {
		return err
	}
	validFiles := validContentInfo(after, size) && validContentInfo(named, size)
	valid := validFiles && os.SameFile(after, named)
	oldStat, oldOK := before.Sys().(*syscall.Stat_t)
	newStat, newOK := after.Sys().(*syscall.Stat_t)
	var unchangedTimes bool
	if oldOK && newOK {
		unchangedTimes = oldStat.Mtim == newStat.Mtim && oldStat.Ctim == newStat.Ctim
	}
	unchanged := os.SameFile(before, after) && unchangedTimes
	if !valid || !unchanged {
		return ErrContentCorrupt
	}
	return nil
}

func (state *State) syncContentFile(file *os.File) error {
	if state.contentSync != nil {
		return state.contentSync(file)
	}
	return file.Sync()
}

func (state *State) syncContentDirectory() error {
	directory, err := state.root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return state.syncContentFile(directory)
}

func (state *State) publishContent(
	ctx context.Context,
	identity ContentIdentity,
	temporary string,
	file *os.File,
	info os.FileInfo,
) (bool, error) {
	state.contentMu.Lock()
	defer state.contentMu.Unlock()
	if err := state.contentReadyLocked(ctx); err != nil {
		return false, err
	}
	if err := state.matchContentFile(
		temporary,
		file,
		info,
		identity.Size,
	); err != nil {
		return false, err
	}
	directory, err := state.root.Open(".")
	if err != nil {
		return false, err
	}
	defer directory.Close()
	raw, err := directory.SyscallConn()
	if err != nil {
		return false, err
	}
	var publishErr error
	err = raw.Control(func(parent uintptr) {
		publishErr = unix.Renameat2(
			int(parent),
			temporary,
			int(parent),
			contentName(identity),
			unix.RENAME_NOREPLACE,
		)
	})
	if err != nil {
		return false, err
	}
	if errors.Is(publishErr, os.ErrExist) {
		return false, nil
	}
	if publishErr != nil {
		return false, publishErr
	}
	if err := state.contentReadyLocked(ctx); err != nil {
		return true, err
	}
	publishedInfo, err := file.Stat()
	if err != nil {
		return true, err
	}
	beforeStat, beforeOK := info.Sys().(*syscall.Stat_t)
	afterStat, afterOK := publishedInfo.Sys().(*syscall.Stat_t)
	validStats := beforeOK && afterOK
	unchangedData := validStats && beforeStat.Mtim == afterStat.Mtim
	validPublished := os.SameFile(info, publishedInfo) && validContentInfo(publishedInfo, identity.Size)
	if !unchangedData || !validPublished {
		return true, ErrContentCorrupt
	}
	if err := state.matchContentFile(
		contentName(identity),
		file,
		publishedInfo,
		identity.Size,
	); err != nil {
		return true, err
	}
	if err := state.syncContentFile(directory); err != nil {
		return true, err
	}
	if err := state.contentReadyLocked(ctx); err != nil {
		return true, err
	}
	return true, state.matchContentFile(
		contentName(identity),
		file,
		publishedInfo,
		identity.Size,
	)
}

func streamContent(ctx context.Context, identity ContentIdentity, source io.Reader, target io.Writer) error {
	limited := io.LimitReader(source, identity.Size+1)
	digest := sha256.New()
	buffer := make([]byte, contentBufferBytes)
	var count int64
	var emptyReads uint32
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := limited.Read(buffer)
		if n < 0 || n > len(buffer) {
			return ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if n > 0 {
			emptyReads = 0
			written, err := target.Write(buffer[:n])
			if err != nil {
				return err
			}
			if written != n {
				return io.ErrShortWrite
			}
			_, _ = digest.Write(buffer[:n])
			count += int64(n)
			if count > identity.Size {
				return fmt.Errorf("%w: content length", ErrContentCorrupt)
			}
		} else {
			emptyReads++
			if emptyReads >= 100 && readErr == nil {
				return io.ErrNoProgress
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if count != identity.Size {
		return io.ErrUnexpectedEOF
	}
	if hex.EncodeToString(digest.Sum(nil)) != identity.SHA256 {
		return fmt.Errorf("%w: content digest", ErrContentCorrupt)
	}
	return ctx.Err()
}
