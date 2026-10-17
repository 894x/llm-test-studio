package runnerstate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const ContentStoreName = "content-store.json"
const MaxContentStoreBytes = 2 << 10

type ContentStoreIdentity struct {
	Contract       int       `json:"contract"`
	StoreID        string    `json:"store_id"`
	InstallationID string    `json:"installation_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type ContentStoreMarker struct {
	state    *State
	identity ContentStoreIdentity
	info     os.FileInfo
}

func (marker *ContentStoreMarker) Identity() ContentStoreIdentity { return marker.identity }

func (marker *ContentStoreMarker) Check(ctx context.Context) error {
	if marker == nil || marker.state == nil {
		return ErrInvalid
	}
	current, err := marker.state.readContentStore(ctx)
	if err != nil {
		return err
	}
	sameIdentity := current.identity == marker.identity && os.SameFile(marker.info, current.info)
	old, oldOK := marker.info.Sys().(*syscall.Stat_t)
	now, nowOK := current.info.Sys().(*syscall.Stat_t)
	sameTimes := oldOK && nowOK && old.Mtim == now.Mtim && old.Ctim == now.Ctim
	if !sameIdentity || !sameTimes {
		return ErrContentCorrupt
	}
	return nil
}

func (state *State) EnsureContentStore(
	ctx context.Context,
	installationID string,
	at time.Time,
) (*ContentStoreMarker, error) {
	if ctx == nil || !domain.IsUUID(installationID) {
		return nil, ErrInvalid
	}
	if err := state.contentStoreReady(ctx); err != nil {
		return nil, err
	}
	marker, err := state.readContentStore(ctx)
	if errors.Is(err, os.ErrNotExist) {
		if at.IsZero() {
			return nil, ErrInvalid
		}
		id, randomErr := uuid.NewRandom()
		if randomErr != nil {
			return nil, randomErr
		}
		identity := ContentStoreIdentity{
			Contract: jobcontract.CurrentContract, StoreID: id.String(), InstallationID: installationID, CreatedAt: at.UTC(),
		}
		if err := state.createContentStore(ctx, identity); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		marker, err = state.readContentStore(ctx)
	}
	if err != nil {
		return nil, err
	}
	if marker.identity.InstallationID != installationID {
		return nil, ErrContentCorrupt
	}
	if err := state.confirmContentStore(ctx, marker.info); err != nil {
		return nil, err
	}
	if err := marker.Check(ctx); err != nil {
		return nil, err
	}
	return marker, nil
}

func (state *State) contentStoreReady(ctx context.Context) error {
	if ctx == nil || state == nil {
		return ErrInvalid
	}
	state.contentMu.Lock()
	defer state.contentMu.Unlock()
	return state.contentStoreReadyLocked(ctx)
}

func (state *State) contentStoreReadyLocked(ctx context.Context) error {
	if err := state.contentReadyLocked(ctx); err != nil {
		return err
	}
	info, err := state.root.Stat(".")
	if err != nil {
		return err
	}
	if info.Mode() != os.ModeDir|0700 || !privateOwned(info) {
		return ErrContentCorrupt
	}
	return nil
}

func (state *State) readContentStore(ctx context.Context) (marker *ContentStoreMarker, err error) {
	if err := state.contentStoreReady(ctx); err != nil {
		return nil, err
	}
	file, err := openPrivate(state.root, ContentStoreName, os.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	if err := state.registerContentFile(file); err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, state.releaseContentFile(file)) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	validSize := info.Size() >= 1 && info.Size() <= MaxContentStoreBytes
	if !validSize || !validContentInfo(info, info.Size()) {
		return nil, ErrContentCorrupt
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxContentStoreBytes+1))
	if err != nil {
		return nil, err
	}
	identity, err := decodeContentStore(raw)
	if err != nil {
		return nil, err
	}
	if err := state.matchContentFile(
		ContentStoreName,
		file,
		info,
		info.Size(),
	); err != nil {
		return nil, err
	}
	if err := state.contentStoreReady(ctx); err != nil {
		return nil, err
	}
	return &ContentStoreMarker{state: state, identity: identity, info: info}, nil
}

func decodeContentStore(raw []byte) (ContentStoreIdentity, error) {
	if len(raw) > MaxContentStoreBytes {
		return ContentStoreIdentity{}, jobcontract.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ContentStoreIdentity{}, ErrContentCorrupt
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		allowed := name == "contract" || name == "store_id" || name == "installation_id" || name == "created_at"
		validKey := err == nil && ok && allowed && fields[name] == nil
		if !validKey {
			return ContentStoreIdentity{}, ErrContentCorrupt
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ContentStoreIdentity{}, ErrContentCorrupt
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return ContentStoreIdentity{}, ErrContentCorrupt
	}
	if len(fields) != 4 || decoder.Decode(&json.RawMessage{}) != io.EOF {
		return ContentStoreIdentity{}, ErrContentCorrupt
	}
	var identity ContentStoreIdentity
	if json.Unmarshal(raw, &identity) != nil {
		return ContentStoreIdentity{}, ErrContentCorrupt
	}
	if identity.Contract != jobcontract.CurrentContract {
		return ContentStoreIdentity{}, jobcontract.ErrUnsupported
	}
	validIDs := domain.IsUUID(identity.StoreID) && domain.IsUUID(identity.InstallationID)
	_, offset := identity.CreatedAt.Zone()
	validTime := !identity.CreatedAt.IsZero() && offset == 0
	if !validIDs || !validTime {
		return ContentStoreIdentity{}, ErrContentCorrupt
	}
	return identity, nil
}

func (state *State) createContentStore(ctx context.Context, identity ContentStoreIdentity) (err error) {
	raw, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if _, err := decodeContentStore(raw); err != nil {
		return err
	}
	temporary := ".content-store-" + rand.Text() + ".partial"
	file, err := openPrivate(state.root, temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	if err := state.registerContentFile(file); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, state.releaseContentFile(file)) }()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := state.syncContentFile(file); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	state.contentMu.Lock()
	defer state.contentMu.Unlock()
	if err := state.contentStoreReadyLocked(ctx); err != nil {
		return err
	}
	if err := state.matchContentFile(
		temporary,
		file,
		info,
		int64(len(raw)),
	); err != nil {
		return err
	}
	directory, err := state.root.Open(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	rawDirectory, err := directory.SyscallConn()
	if err != nil {
		return err
	}
	var renameErr error
	if err := rawDirectory.Control(func(parent uintptr) {
		renameErr = unix.Renameat2(
			int(parent),
			temporary,
			int(parent),
			ContentStoreName,
			unix.RENAME_NOREPLACE,
		)
	}); err != nil {
		return err
	}
	if renameErr != nil {
		return renameErr
	}
	if err := state.syncContentFile(directory); err != nil {
		return err
	}
	return state.contentStoreReadyLocked(ctx)
}

func (state *State) confirmContentStore(ctx context.Context, expected os.FileInfo) (err error) {
	file, err := openPrivate(state.root, ContentStoreName, os.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return err
	}
	if err := state.registerContentFile(file); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, state.releaseContentFile(file)) }()
	if err := state.matchContentFile(
		ContentStoreName,
		file,
		expected,
		expected.Size(),
	); err != nil {
		return err
	}
	if err := state.syncContentFile(file); err != nil {
		return err
	}
	if err := state.syncContentDirectory(); err != nil {
		return err
	}
	if err := state.contentStoreReady(ctx); err != nil {
		return err
	}
	return state.matchContentFile(
		ContentStoreName,
		file,
		expected,
		expected.Size(),
	)
}
