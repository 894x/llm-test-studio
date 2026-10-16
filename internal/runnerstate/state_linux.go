// Package runnerstate owns the private Linux runner directory and runtime lock.
package runnerstate

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	ErrInvalid = errors.New("invalid_runner_state")
	ErrLocked  = errors.New("runner_already_running")
)

type State struct {
	root *os.Root
	lock *os.File
	path string
	once sync.Once
	err  error

	contentMu     sync.Mutex
	contentClosed bool
	contentFiles  map[*os.File]struct{}
	contentSync   func(*os.File) error
}

// Open holds an advisory runtime lock until Close. The inode is retained after
// release so another process cannot accidentally lock a replacement inode.
func Open(path string) (*State, error) {
	validPath := filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/"
	if !validPath {
		return nil, ErrInvalid
	}
	if err := verifyExistingAncestor(path); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if resolved != path {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !privateOwned(info) || !info.IsDir() {
		return nil, ErrInvalid
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	lock, err := openPrivate(root, "runner.lock", os.O_CREATE|os.O_RDWR)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	lockInfo, err := lock.Stat()
	if err == nil {
		if !privateOwned(lockInfo) || !lockInfo.Mode().IsRegular() {
			err = ErrInvalid
		}
	}
	if err == nil {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		_ = lock.Close()
		_ = root.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &State{root: root, lock: lock, path: path}, nil
}

func privateOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}

func verifyExistingAncestor(path string) error {
	for candidate := path; ; candidate = filepath.Dir(candidate) {
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return err
		}
		if resolved != candidate {
			return ErrInvalid
		}
		return nil
	}
}

func (state *State) Path() string { return state.path }

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "/\\\x00") && len(name) <= 128
}

func (state *State) ReadPrivate(name string, limit int64) ([]byte, error) {
	validInput := validName(name) && limit > 0 && limit <= 1<<20
	if !validInput {
		return nil, ErrInvalid
	}
	file, err := openPrivate(state.root, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	valid := info.Mode().IsRegular() && privateOwned(info) && info.Size() > 0 && info.Size() <= limit
	if !valid {
		return nil, ErrInvalid
	}
	document, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(document)) > limit {
		return nil, ErrInvalid
	}
	return document, nil
}

// WriteNew commits one private file without replacing an existing identity.
func (state *State) WriteNew(name string, document []byte) error {
	return state.write(name, document, false)
}

// ReplacePrivate atomically replaces a mutable private control document. The
// runtime lock and its owning controller serialize writers; identities use WriteNew.
func (state *State) ReplacePrivate(name string, document []byte) error {
	if _, err := state.ReadPrivate(name, 1<<20); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return state.write(name, document, true)
}

// SyncPrivate confirms both file content and its directory entry are durable.
func (state *State) SyncPrivate(name string) error {
	if !validName(name) {
		return ErrInvalid
	}
	file, err := openPrivate(state.root, name, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !privateOwned(info) || !info.Mode().IsRegular() {
		return ErrInvalid
	}
	if err := file.Sync(); err != nil {
		return err
	}
	directory, err := state.root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (state *State) write(name string, document []byte, replace bool) error {
	validInput := validName(name) && len(document) > 0 && len(document) <= 1<<20
	if !validInput {
		return ErrInvalid
	}
	temporary := ".new-" + rand.Text()
	file, err := openPrivate(state.root, temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	defer state.root.Remove(temporary)
	if _, err := file.Write(document); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if replace {
		if err := state.root.Rename(temporary, name); err != nil {
			return err
		}
	} else {
		if err := state.root.Link(temporary, name); err != nil {
			return err
		}
		if err := state.root.Remove(temporary); err != nil {
			return err
		}
	}
	directory, err := state.root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (state *State) AvailableBytes() (uint64, error) {
	directory, err := state.root.Open(".")
	if err != nil {
		return 0, err
	}
	defer directory.Close()
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(directory.Fd()), &stat); err != nil {
		return 0, err
	}
	if stat.Bsize <= 0 || stat.Bavail > math.MaxUint64/uint64(stat.Bsize) {
		return 0, ErrInvalid
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func (state *State) UsedBytes(ctx context.Context) (uint64, error) {
	var total uint64
	var entries uint32
	err := fs.WalkDir(state.root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 100000 || entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		valid := info.Mode().IsRegular() && info.Size() >= 0 && uint64(info.Size()) <= math.MaxUint64-total
		if !valid {
			return ErrInvalid
		}
		total += uint64(info.Size())
		return nil
	})
	return total, err
}

func (state *State) Close() error {
	state.once.Do(func() {
		state.err = errors.Join(state.closeContent(), state.lock.Close(), state.root.Close())
	})
	return state.err
}

func openPrivate(root *os.Root, name string, flags int) (*os.File, error) {
	// Root.OpenFile resolves links within its root. Openat on its pinned
	// directory descriptor enforces that the final private file is not a link.
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	raw, err := directory.SyscallConn()
	if err != nil {
		return nil, err
	}
	var fileDescriptor int
	var openErr error
	err = raw.Control(func(parent uintptr) {
		fileDescriptor, openErr = unix.Openat(
			int(parent),
			name,
			flags|unix.O_NOFOLLOW|unix.O_CLOEXEC,
			0600,
		)
	})
	if err != nil {
		return nil, err
	}
	if openErr != nil {
		return nil, openErr
	}
	return os.NewFile(uintptr(fileDescriptor), name), nil
}
