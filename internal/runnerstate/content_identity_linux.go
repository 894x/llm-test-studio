package runnerstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"syscall"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type ContentFileIdentity struct {
	IncarnationID string
	WitnessSHA256 string
	ChangeToken   string
}

const contentStatxMask = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_NLINK |
	unix.STATX_UID | unix.STATX_GID | unix.STATX_INO | unix.STATX_SIZE |
	unix.STATX_MTIME | unix.STATX_CTIME | unix.STATX_BTIME

// PhysicalIdentity binds the verified open file to its kernel birth identity.
// Unsupported filesystems fail explicitly; ctime never substitutes for btime.
func (reader *ContentReader) PhysicalIdentity(storeID string) (ContentFileIdentity, error) {
	if !domain.IsUUID(storeID) {
		return ContentFileIdentity{}, ErrInvalid
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if err := reader.ready(); err != nil {
		return ContentFileIdentity{}, err
	}
	var stat unix.Statx_t
	err := unix.Statx(
		int(reader.file.Fd()),
		"",
		unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW,
		contentStatxMask,
		&stat,
	)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return ContentFileIdentity{}, errors.Join(jobcontract.ErrUnsupported, err)
	}
	if err != nil {
		return ContentFileIdentity{}, err
	}
	identity, err := contentKernelIdentity(storeID, stat, reader.info)
	if err != nil {
		return ContentFileIdentity{}, err
	}
	if err := reader.ready(); err != nil {
		return ContentFileIdentity{}, err
	}
	return identity, nil
}

func (reader *ContentReader) Check() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.ready()
}

func contentKernelIdentity(storeID string, stat unix.Statx_t, before os.FileInfo) (ContentFileIdentity, error) {
	if stat.Mask&contentStatxMask != contentStatxMask {
		return ContentFileIdentity{}, jobcontract.ErrUnsupported
	}
	nonzeroBirth := stat.Btime.Sec != 0 || stat.Btime.Nsec != 0
	validTimes := stat.Btime.Nsec < 1e9 && stat.Mtime.Nsec < 1e9 && stat.Ctime.Nsec < 1e9
	if !nonzeroBirth || !validTimes {
		return ContentFileIdentity{}, jobcontract.ErrUnsupported
	}
	old, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !validContentInfo(before, before.Size()) {
		return ContentFileIdentity{}, ErrContentCorrupt
	}
	sameDevice := stat.Dev_major == unix.Major(uint64(old.Dev)) && stat.Dev_minor == unix.Minor(uint64(old.Dev))
	sameFile := stat.Ino == old.Ino && stat.Size == uint64(before.Size())
	sameOwner := stat.Uid == old.Uid && stat.Gid == old.Gid
	sameShape := stat.Nlink == uint32(old.Nlink) && stat.Mode == uint16(old.Mode)
	sameModified := stat.Mtime.Sec == old.Mtim.Sec && stat.Mtime.Nsec == uint32(old.Mtim.Nsec)
	sameChanged := stat.Ctime.Sec == old.Ctim.Sec && stat.Ctime.Nsec == uint32(old.Ctim.Nsec)
	validFile := sameDevice && sameFile && sameOwner && sameShape
	unchangedTimes := sameModified && sameChanged
	if !validFile || !unchangedTimes {
		return ContentFileIdentity{}, ErrContentCorrupt
	}
	stable := bytes.NewBufferString("llm-test-content-incarnation\x00")
	for _, value := range []uint64{
		uint64(stat.Dev_major), uint64(stat.Dev_minor), stat.Ino,
		uint64(stat.Btime.Sec), uint64(stat.Btime.Nsec),
	} {
		_ = binary.Write(stable, binary.BigEndian, value)
	}
	namespace, err := uuid.Parse(storeID)
	if err != nil {
		return ContentFileIdentity{}, ErrInvalid
	}
	incarnation := uuid.NewSHA1(namespace, stable.Bytes()).String()
	witness := bytes.NewBufferString("llm-test-content-witness\x00")
	_, _ = witness.Write(namespace[:])
	_, _ = witness.Write(stable.Bytes())
	for _, value := range []uint64{
		uint64(stat.Mode), uint64(stat.Uid), uint64(stat.Gid), uint64(stat.Nlink), stat.Size,
		uint64(stat.Mtime.Sec), uint64(stat.Mtime.Nsec),
	} {
		_ = binary.Write(witness, binary.BigEndian, value)
	}
	witnessSum := sha256.Sum256(witness.Bytes())
	change := bytes.NewBufferString("llm-test-content-change\x00")
	_, _ = change.Write(witnessSum[:])
	_ = binary.Write(change, binary.BigEndian, uint64(stat.Ctime.Sec))
	_ = binary.Write(change, binary.BigEndian, uint64(stat.Ctime.Nsec))
	changeSum := sha256.Sum256(change.Bytes())
	return ContentFileIdentity{
		IncarnationID: incarnation, WitnessSHA256: hex.EncodeToString(witnessSum[:]),
		ChangeToken: hex.EncodeToString(changeSum[:]),
	}, nil
}
