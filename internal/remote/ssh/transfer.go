package ssh

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/pkg/sftp"
)

type Upload struct {
	Path   string
	Size   int64
	SHA256 string
	Mode   os.FileMode
	Source io.Reader
}

const maxUploadBytes = 256 << 20

// UploadNew validates both the transmitted bytes and a separate SFTP readback.
// It never overwrites a destination. The installer owns directory creation,
// installation locking, directory durability and activation of the uploaded file.
func (client *Client) UploadNew(ctx context.Context, upload Upload) error {
	validSize := upload.Size > 0 && upload.Size <= maxUploadBytes
	validMode := upload.Mode == 0600 || upload.Mode == 0700
	valid := safeRemotePath(upload.Path) && validSize && validMode && jobcontract.IsSHA256(upload.SHA256)
	if !valid || upload.Source == nil {
		return ErrInvalid
	}
	return client.withSFTP(ctx, func(remote *sftp.Client) error {
		if err := verifyParents(remote, upload.Path); err != nil {
			return err
		}
		parent, err := remote.Lstat(path.Dir(upload.Path))
		if err != nil || parent.Mode().Perm()&0077 != 0 {
			return ErrInvalid
		}
		if _, err := remote.Lstat(upload.Path); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return ErrExists
			}
			return ErrTransfer
		}
		if version, ok := remote.HasExtension("fsync@openssh.com"); !ok || version != "1" {
			return ErrUnsupported
		}
		temporary := path.Join(path.Dir(upload.Path), ".upload-"+rand.Text())
		file, err := remote.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return ErrTransfer
		}
		defer remote.Remove(temporary)
		defer file.Close()
		if err := file.Chmod(upload.Mode); err != nil {
			return ErrTransfer
		}
		digest := sha256.New()
		written, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(upload.Source, upload.Size+1))
		if err != nil {
			return ErrTransfer
		}
		if written != upload.Size || hex.EncodeToString(digest.Sum(nil)) != upload.SHA256 {
			return ErrIntegrity
		}
		if err := file.Sync(); err != nil {
			return ErrTransfer
		}
		if err := file.Close(); err != nil {
			return ErrTransfer
		}
		if err := verifyUpload(remote, temporary, upload); err != nil {
			return err
		}
		if err := remote.Rename(temporary, upload.Path); err != nil {
			return ErrTransfer
		}
		return nil
	})
}

func verifyUpload(remote *sftp.Client, temporary string, upload Upload) error {
	file, err := remote.Open(temporary)
	if err != nil {
		return ErrTransfer
	}
	defer file.Close()
	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(file, upload.Size+1))
	if err != nil {
		return ErrTransfer
	}
	if read != upload.Size || hex.EncodeToString(digest.Sum(nil)) != upload.SHA256 {
		return ErrIntegrity
	}
	return nil
}

// ReadPrivate is used for the root-owned pairing secret, never for a general
// remote file browser. The caller must clear its returned bytes after use.
func (client *Client) ReadPrivate(ctx context.Context, name string, owner uint32) ([]byte, error) {
	if !safeRemotePath(name) {
		return nil, ErrInvalid
	}
	document := []byte{}
	err := client.withSFTP(ctx, func(remote *sftp.Client) error {
		if err := verifyParents(remote, name); err != nil {
			return err
		}
		info, err := remote.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		if err != nil {
			return ErrTransfer
		}
		stat, ok := info.Sys().(*sftp.FileStat)
		private := ok && stat.UID == owner && info.Mode().Perm()&0077 == 0
		bounded := info.Size() > 0 && info.Size() <= 1<<20
		valid := private && bounded && info.Mode().IsRegular()
		if !valid {
			return ErrInvalid
		}
		file, err := remote.Open(name)
		if err != nil {
			return ErrTransfer
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil {
			return ErrTransfer
		}
		if opened.Size() != info.Size() || opened.Mode() != info.Mode() {
			return ErrIntegrity
		}
		openedStat, ok := opened.Sys().(*sftp.FileStat)
		if !ok || openedStat.UID != owner {
			return ErrInvalid
		}
		document, err = io.ReadAll(io.LimitReader(file, info.Size()+1))
		if err != nil || int64(len(document)) != info.Size() {
			return ErrTransfer
		}
		return nil
	})
	if err != nil {
		clear(document)
		return nil, err
	}
	return document, nil
}

func safeRemotePath(name string) bool {
	valid := len(name) > 1 && len(name) <= 4096 && path.IsAbs(name) && path.Clean(name) == name
	return valid && !strings.ContainsAny(name, "\\\x00\r\n")
}

func verifyParents(remote *sftp.Client, name string) error {
	for parent := path.Dir(name); ; parent = path.Dir(parent) {
		info, err := remote.Lstat(parent)
		if err != nil {
			return ErrTransfer
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if parent == "/" {
			return nil
		}
	}
}

func (client *Client) withSFTP(ctx context.Context, operation func(*sftp.Client) error) error {
	if client == nil || ctx == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// SFTP methods have no context. Closing the SSH connection interrupts blocked
	// channel creation and file I/O; accepted runner tasks remain independent.
	stop := context.AfterFunc(ctx, func() { _ = client.connection.Close() })
	defer stop()
	remote, err := sftp.NewClient(client.connection)
	if err != nil {
		return connectionError(ctx, err)
	}
	defer remote.Close()
	err = operation(remote)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
