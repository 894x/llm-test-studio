package runnerinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
	"golang.org/x/sys/unix"
)

type upgradeBackup struct {
	Request  UpgradeRequest      `json:"request"`
	Config   string              `json:"config_sha256"`
	Unit     string              `json:"unit_sha256"`
	Program  string              `json:"program_sha256"`
	Database sqlite.RunnerBackup `json:"database"`
}

type upgradeRuntimeLock struct {
	file *os.File
	once sync.Once
	err  error
}

func (lock *upgradeRuntimeLock) Close() error {
	lock.once.Do(func() { lock.err = lock.file.Close() })
	return lock.err
}

func lockUpgradeRuntime(paths layout, owner account) (*upgradeRuntimeLock, error) {
	if err := trustedDirectory(paths.state, owner.uid, true); err != nil {
		return nil, err
	}
	file, err := openOwned(filepath.Join(paths.state, "runner.lock"), owner.uid, true)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &upgradeRuntimeLock{file: file}, nil
}

func (paths layout) replaceCurrent(buildID string) error {
	if err := verifyProgram(paths.programPath(buildID), buildID); err != nil {
		return err
	}
	if _, err := paths.currentBuild(); err != nil {
		return err
	}
	temporary := filepath.Join(paths.program, ".current-"+rand.Text())
	if err := os.Symlink("releases/"+buildID, temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, filepath.Join(paths.program, "current")); err != nil {
		return err
	}
	return syncDirectory(paths.program)
}

func digestOwned(name string, owner int, private bool) (string, error) {
	file, err := openOwned(name, owner, private)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func backupOwnedFile(source, destination string, owner int, private bool) (string, error) {
	digest, err := digestOwned(source, owner, private)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(destination); err == nil {
		existing, err := digestOwned(destination, os.Geteuid(), true)
		if err != nil || existing != digest {
			return "", ErrIntegrity
		}
		return digest, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := openOwned(source, owner, private)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := writeNewFile(destination, file, 0600); err != nil {
		return "", err
	}
	return digest, nil
}

func (installer upgrader) backupFiles(ctx context.Context, request UpgradeRequest, owner account) error {
	directory := filepath.Join(installer.layout.install, upgradeDataName(request.OperationID))
	if err := createDirectory(directory, account{uid: os.Geteuid(), gid: os.Getegid()}, 0700); err != nil {
		return err
	}
	manifest := upgradeBackup{Request: request}
	var err error
	manifest.Config, err = backupOwnedFile(
		filepath.Join(installer.layout.state, runnerprocess.ConfigName),
		filepath.Join(directory, "runner.json"),
		owner.uid,
		true,
	)
	if err != nil {
		return err
	}
	manifest.Unit, err = backupOwnedFile(installer.layout.unit, filepath.Join(directory, "runner.service"), os.Geteuid(), false)
	if err != nil {
		return err
	}
	manifest.Program, err = backupOwnedFile(
		installer.layout.programPath(request.ExpectedBuildID),
		filepath.Join(directory, "runner.program"),
		os.Geteuid(),
		false,
	)
	if err != nil || manifest.Program != request.ExpectedBuildID {
		return ErrIntegrity
	}
	database := filepath.Join(directory, "runner.db")
	if _, err := os.Lstat(database); errors.Is(err, os.ErrNotExist) {
		manifest.Database, err = installer.backup(ctx, filepath.Join(installer.layout.state, "runner.db"), database)
		if err != nil {
			return ErrUpgradeRequired
		}
	} else if err == nil {
		manifest.Database, err = installer.verifyDB(ctx, database)
		if err != nil {
			return ErrUpgradeRequired
		}
	} else {
		return err
	}
	document, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	name := filepath.Join(directory, "backup.json")
	if _, err := os.Lstat(name); err == nil {
		stored, err := readUpgradePrivate(name, MaxRecordBytes)
		if err != nil || !bytes.Equal(document, stored) {
			return ErrIntegrity
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeNewFile(name, bytes.NewReader(document), 0600); err != nil {
			return err
		}
	} else {
		return err
	}
	return installer.verifyBackup(ctx, request, owner)
}

func readUpgradePrivate(name string, limit int64) ([]byte, error) {
	file, err := openOwned(name, os.Geteuid(), true)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	document, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(document)) > limit {
		return nil, ErrIntegrity
	}
	return document, nil
}

func (installer upgrader) verifyBackup(ctx context.Context, request UpgradeRequest, owner account) error {
	directory := filepath.Join(installer.layout.install, upgradeDataName(request.OperationID))
	if err := trustedDirectory(directory, os.Geteuid(), true); err != nil {
		return err
	}
	document, err := readUpgradePrivate(filepath.Join(directory, "backup.json"), MaxRecordBytes)
	if err != nil {
		return err
	}
	var manifest upgradeBackup
	if json.Unmarshal(document, &manifest) != nil || manifest.Request != request || manifest.Program != request.ExpectedBuildID {
		return ErrIntegrity
	}
	canonical, _ := json.Marshal(manifest)
	if !bytes.Equal(document, canonical) {
		return ErrIntegrity
	}
	for name, expected := range map[string]string{
		"runner.json": manifest.Config, "runner.service": manifest.Unit, "runner.program": request.ExpectedBuildID,
	} {
		actual, err := digestOwned(filepath.Join(directory, name), os.Geteuid(), true)
		if err != nil || actual != expected {
			return ErrIntegrity
		}
	}
	proof, err := installer.verifyDB(ctx, filepath.Join(directory, "runner.db"))
	if err != nil {
		return ErrUpgradeRequired
	}
	proofBytes, _ := json.Marshal(proof)
	expectedBytes, _ := json.Marshal(manifest.Database)
	if !bytes.Equal(proofBytes, expectedBytes) {
		return ErrIntegrity
	}
	return nil
}

func (paths layout) checkUpgradeDataDirectory(name string) error {
	id := strings.TrimPrefix(name, "upgrade-data-")
	if !domain.IsUUID(id) {
		return ErrUnownedPath
	}
	directory := filepath.Join(paths.install, name)
	if err := trustedDirectory(directory, os.Geteuid(), true); err != nil {
		return err
	}
	journal, err := readUpgradePrivate(filepath.Join(paths.install, upgradeOperationName(id)), MaxRecordBytes)
	if err != nil {
		return ErrUnownedPath
	}
	operation, err := DecodeUpgradeOperation(journal)
	if err != nil || operation.Request.OperationID != id {
		return ErrUnownedPath
	}
	entries, err := boundedEntries(directory)
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		"runner.json": true, "runner.service": true, "runner.program": true, "runner.db": true,
		"runner.db-wal": true, "runner.db-shm": true, "runner.db-journal": true, "backup.json": true,
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] && !validTemporaryName(entry.Name()) {
			return ErrUnownedPath
		}
		file, err := openOwned(filepath.Join(directory, entry.Name()), os.Geteuid(), true)
		if err != nil {
			return ErrUnownedPath
		}
		_ = file.Close()
	}
	return nil
}

type upgradeContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader upgradeContextReader) Read(document []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(document)
}

func restoreUpgradeFile(ctx context.Context, source, destination string, owner account, mode os.FileMode) error {
	file, err := openOwned(source, os.Geteuid(), true)
	if err != nil {
		return err
	}
	defer file.Close()
	if existing, err := openOwned(destination, owner.uid, mode&0077 == 0); err == nil {
		_ = existing.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := filepath.Join(filepath.Dir(destination), ".upgrade-"+rand.Text())
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	defer output.Close()
	if _, err := io.Copy(output, upgradeContextReader{ctx: ctx, source: file}); err != nil {
		return err
	}
	if err := output.Chown(owner.uid, owner.gid); err != nil {
		return err
	}
	if err := output.Chmod(mode); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}
