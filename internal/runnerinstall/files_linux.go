package runnerinstall

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	runnerdeployment "github.com/894x/llm-test-studio/deploy/runner"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
	"golang.org/x/sys/unix"
)

const maxProgramBytes = 256 << 20

func (paths layout) verifyBases() error {
	for _, name := range []string{paths.program, paths.state, paths.install, paths.unit} {
		parent := filepath.Dir(name)
		if err := trustedDirectory(parent, os.Geteuid(), false); err != nil {
			return err
		}
	}
	return nil
}

func trustedDirectory(name string, owner int, private bool) error {
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || resolved != name {
		return ErrUnownedPath
	}
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || !ownedMode(info, owner, private) {
		return ErrUnownedPath
	}
	return nil
}

func ownedMode(info os.FileInfo, owner int, private bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(owner) {
		return false
	}
	mask := os.FileMode(0022)
	if private {
		mask = 0077
	}
	return info.Mode().Perm()&mask == 0
}

func createDirectory(name string, owner account, mode os.FileMode) error {
	if info, err := os.Lstat(name); err == nil {
		if info.Mode().Perm() != mode {
			return ErrUnownedPath
		}
		if err := trustedDirectory(name, owner.uid, mode&0077 == 0); err != nil {
			return err
		}
		if err := syncDirectory(name); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(name))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := filepath.Join(filepath.Dir(name), ".dir-"+rand.Text())
	if err := os.Mkdir(temporary, 0700); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Chown(temporary, owner.uid, owner.gid); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		return err
	}
	if err := syncDirectory(temporary); err != nil {
		return err
	}
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		temporary,
		unix.AT_FDCWD,
		name,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(name))
}

func (paths layout) prepareState(owner account) error {
	if err := createDirectory(paths.state, owner, 0700); err != nil {
		return err
	}
	_, err := os.Lstat(filepath.Join(paths.state, runnerprocess.ConfigName))
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory, err := os.Open(paths.state)
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "runner.lock" {
			return ErrUnownedPath
		}
		file, err := openOwned(filepath.Join(paths.state, entry.Name()), owner.uid, true)
		if err != nil {
			return err
		}
		_ = file.Close()
	}
	return nil
}

func (paths layout) readConfig(owner account) (runnerconfig.Config, bool, error) {
	file, err := openOwned(filepath.Join(paths.state, runnerprocess.ConfigName), owner.uid, true)
	if errors.Is(err, os.ErrNotExist) {
		return runnerconfig.Config{}, false, nil
	}
	if err != nil {
		return runnerconfig.Config{}, false, err
	}
	defer file.Close()
	config, err := runnerconfig.Decode(file)
	if err != nil {
		return runnerconfig.Config{}, false, err
	}
	if err := file.Sync(); err != nil {
		config.Clear()
		return runnerconfig.Config{}, false, err
	}
	if err := syncDirectory(paths.state); err != nil {
		config.Clear()
		return runnerconfig.Config{}, false, err
	}
	return config, true, nil
}

func openOwned(name string, owner int, private bool) (*os.File, error) {
	// Parent directories are checked independently. Refuse the final symlink
	// without os.Root's intentional within-root link resolution.
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedMode(info, owner, private) {
		_ = file.Close()
		return nil, ErrUnownedPath
	}
	return file, nil
}

func sourceDigest(source io.ReadSeeker) (string, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(source, maxProgramBytes+1))
	if err != nil {
		return "", err
	}
	if size == 0 || size > maxProgramBytes {
		return "", ErrIntegrity
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (installer provisioner) publishProgram(buildID, operationID string) error {
	owner := account{uid: os.Geteuid(), gid: os.Getegid()}
	if err := installer.layout.publishProgramRoot(operationID); err != nil {
		return err
	}
	directories := []string{
		filepath.Join(installer.layout.program, "releases"),
		filepath.Join(installer.layout.program, "releases", buildID),
	}
	for _, name := range directories {
		if err := createDirectory(name, owner, 0755); err != nil {
			return err
		}
	}
	name := installer.layout.programPath(buildID)
	if _, err := os.Lstat(name); err == nil {
		return verifyProgram(name, buildID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := installer.source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := writeNewFile(name, installer.source, 0755); err != nil {
		return err
	}
	return verifyProgram(name, buildID)
}

func (paths layout) publishProgramRoot(operationID string) error {
	entries, err := boundedEntries(paths.program)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return createDirectory(paths.program, account{uid: os.Geteuid(), gid: os.Getegid()}, 0755)
	}
	temporary := filepath.Join(filepath.Dir(paths.program), ".dir-"+rand.Text())
	if err := os.Mkdir(temporary, 0700); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(filepath.Join(temporary, ".owner.json"))
		_ = os.Remove(temporary)
	}()
	marker := programOwner{Contract: jobcontract.CurrentContract, OperationID: operationID}
	document, _ := json.Marshal(marker)
	if err := writeNewFile(filepath.Join(temporary, ".owner.json"), bytes.NewReader(document), 0644); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0755); err != nil {
		return err
	}
	if err := syncDirectory(temporary); err != nil {
		return err
	}
	// A directory rename publishes the marker and root together. Linux permits
	// replacing only an empty destination directory, preserving unknown contents.
	if err := os.Rename(temporary, paths.program); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(paths.program))
}

func verifyProgram(name, expected string) error {
	if err := trustedDirectory(filepath.Dir(name), os.Geteuid(), false); err != nil {
		return err
	}
	file, err := openOwned(name, os.Geteuid(), false)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0755 {
		return ErrUnownedPath
	}
	digest, err := sourceDigest(file)
	if err != nil || digest != expected {
		return ErrIntegrity
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(name)); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(filepath.Dir(name)))
}

func (paths layout) currentBuild() (string, error) {
	if _, err := os.Lstat(paths.program); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err := trustedDirectory(paths.program, os.Geteuid(), false); err != nil {
		return "", err
	}
	current := filepath.Join(paths.program, "current")
	info, err := os.Lstat(current)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	ownedLink := ok && stat.Uid == uint32(os.Geteuid()) && info.Mode()&os.ModeSymlink != 0
	if !ownedLink {
		return "", ErrUnownedPath
	}
	target, err := os.Readlink(current)
	if err != nil {
		return "", err
	}
	buildID := strings.TrimPrefix(target, "releases/")
	if !jobcontract.IsSHA256(buildID) || target != "releases/"+buildID {
		return "", ErrUnownedPath
	}
	if err := verifyProgram(paths.programPath(buildID), buildID); err != nil {
		return "", err
	}
	if err := syncDirectory(paths.program); err != nil {
		return "", err
	}
	return buildID, nil
}

func (paths layout) selectCurrent(buildID string) error {
	temporary := filepath.Join(paths.program, ".current-"+rand.Text())
	if err := os.Symlink("releases/"+buildID, temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		temporary,
		unix.AT_FDCWD,
		filepath.Join(paths.program, "current"),
		unix.RENAME_NOREPLACE,
	); err != nil {
		return err
	}
	return syncDirectory(paths.program)
}

func (paths layout) unitBytes() []byte {
	// Production uses only the canonical paths. Isolated filesystem tests render
	// the same unit against their private layout, without creating host services.
	document := strings.ReplaceAll(string(runnerdeployment.Unit), productionLayout().program, paths.program)
	document = strings.ReplaceAll(document, productionLayout().state, paths.state)
	return []byte(document)
}

func (paths layout) checkUnit() (bool, error) {
	file, err := openOwned(paths.unit, os.Geteuid(), false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	document, err := io.ReadAll(io.LimitReader(file, MaxRecordBytes+1))
	if err != nil || len(document) > MaxRecordBytes {
		return false, ErrUnownedPath
	}
	if !bytes.HasPrefix(document, []byte("# Managed by LLM Test Studio runner installer.")) {
		return false, ErrUnownedPath
	}
	expected := map[string]string{
		"Type": "notify", "User": "llm-test-runner", "Group": "llm-test-runner",
		"StateDirectory": "llm-test-runner", "WorkingDirectory": paths.state,
		"ExecStart": paths.program + "/current/llm-test-runner serve --state " + paths.state,
	}
	seen := map[string]bool{}
	section := ""
	for _, line := range strings.Split(string(document), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Service]" {
			continue
		}
		key, value, present := strings.Cut(line, "=")
		if required, ok := expected[key]; ok && present {
			if seen[key] || value != required {
				return false, ErrUnownedPath
			}
			seen[key] = true
		}
	}
	if len(seen) != len(expected) {
		return false, ErrUnownedPath
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := syncDirectory(filepath.Dir(paths.unit)); err != nil {
		return false, err
	}
	return true, nil
}

func (paths layout) writeUnit() error {
	return writeNewFile(paths.unit, bytes.NewReader(paths.unitBytes()), 0644)
}

func writeNewFile(name string, source io.Reader, mode os.FileMode) error {
	temporary := filepath.Join(filepath.Dir(name), ".new-"+rand.Text())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	defer file.Close()
	written, err := io.Copy(file, io.LimitReader(source, maxProgramBytes+1))
	if err != nil {
		return err
	}
	if written > maxProgramBytes {
		return ErrIntegrity
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary, name); err != nil {
		return err
	}
	if err := os.Remove(temporary); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(name))
}

func syncDirectory(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
