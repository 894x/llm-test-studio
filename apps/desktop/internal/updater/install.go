package updater

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type installPlan struct {
	SchemaVersion int    `json:"schema_version"`
	ParentID      int    `json:"parent_id"`
	Target        string `json:"target"`
	Payload       string `json:"payload"`
	SHA256        string `json:"sha256"`
	Installer     bool   `json:"installer"`
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, maxPackageSize+1))
	if err != nil || n > maxPackageSize {
		return "", errors.New("cannot hash update package")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractExecutable(path, directory string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	if len(reader.File) != 1 {
		return "", errors.New("unexpected package contents")
	}
	entry := reader.File[0]
	invalidEntry := entry.Name != "llm-test-studio.exe" || entry.Mode()&os.ModeType != 0
	if invalidEntry || entry.UncompressedSize64 < 2 || entry.UncompressedSize64 > uint64(maxPackageSize) {
		return "", errors.New("invalid update executable")
	}
	input, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer input.Close()
	path = filepath.Join(directory, "payload.exe")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", err
	}
	defer output.Close()
	n, err := io.Copy(output, io.LimitReader(input, maxPackageSize+1))
	if err != nil || n != int64(entry.UncompressedSize64) {
		return "", errors.New("truncated update executable")
	}
	if output.Sync() != nil || output.Close() != nil {
		return "", errors.New("cannot save update executable")
	}
	return path, nil
}

// Install prepares a helper, but never authorizes it to mutate the installation
// until Finalize is called after successful Wails shutdown and resource cleanup.
func (s *Service) Install() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.view.Status != "ready" || s.staged == "" || s.selected == nil {
		return s.view
	}
	hash, err := fileHash(s.staged)
	if err != nil || hash != s.selected.SHA256 {
		s.view.Status, s.view.ErrorCode = "error", "integrity"
		return s.view
	}
	commit, err := prepareInstall(s.staged)
	if err != nil {
		s.view.ErrorCode = "install_failed"
		return s.view
	}
	if s.view.InstallKind == "manual" {
		s.view.ErrorCode = ""
		return s.view
	}
	s.commit = commit
	s.view.Status, s.view.ErrorCode = "installing", ""
	return s.view
}

func (s *Service) Finalize() error {
	s.mu.Lock()
	commit := s.commit
	s.commit = nil
	s.mu.Unlock()
	if commit != nil {
		return commit()
	}
	return nil
}

// RunHelper is recognized before any Wails, credential, or database startup.
// The helper is a copy of the already installed program in its private staging
// directory, and accepts only a matching plan in that same directory.
func RunHelper(args []string) (bool, error) {
	if len(args) < 1 || args[0] != "--desktop-apply-update" {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("invalid update helper arguments")
	}
	self, err := os.Executable()
	if err != nil {
		return true, err
	}
	directory := filepath.Dir(self)
	if filepath.Base(self) != "update-helper.exe" || args[1] != filepath.Join(directory, "install.json") {
		return true, errors.New("invalid update helper location")
	}
	data, err := os.ReadFile(args[1])
	if err != nil || len(data) > 16<<10 {
		return true, errors.New("cannot read update plan")
	}
	var plan installPlan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(new(any)) != io.EOF {
		return true, errors.New("invalid update plan")
	}
	validTarget := filepath.IsAbs(plan.Target) && filepath.Base(plan.Target) == "llm-test-studio.exe"
	validPayload := filepath.Dir(plan.Payload) == directory && filepath.IsAbs(plan.Payload)
	if plan.SchemaVersion != 1 || !validTarget || !validPayload || strings.EqualFold(plan.Target, self) {
		return true, errors.New("invalid update paths")
	}
	hash, err := fileHash(plan.Payload)
	if err != nil || hash != plan.SHA256 {
		return true, errors.New("update payload verification failed")
	}
	wait, release, err := waitForParent(plan.ParentID)
	if err != nil {
		return true, err
	}
	defer release()
	if err := os.WriteFile(filepath.Join(directory, "helper-ready"), []byte("ready"), 0600); err != nil {
		return true, err
	}
	ready := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, 16)).ReadString('\n')
		ready <- err == nil && line == "apply\n"
	}()
	select {
	case authorized := <-ready:
		if !authorized {
			return true, errors.New("update was not authorized by shutdown")
		}
	case <-time.After(90 * time.Second):
		return true, errors.New("application did not finish shutdown")
	}
	if err := wait(); err != nil {
		recordInstallResult(plan, true)
		return true, err
	}
	return true, applyPlan(plan)
}

func prepareHelper(packagePath string) (func() error, error) {
	target, err := os.Executable()
	if err != nil || filepath.Base(target) != "llm-test-studio.exe" {
		return nil, errors.New("unsupported application executable")
	}
	directory, err := os.MkdirTemp(filepath.Dir(packagePath), "install-*")
	if err != nil {
		return nil, err
	}
	installer := strings.HasSuffix(packagePath, "-installer.exe")
	payload := packagePath
	if !installer {
		payload, err = extractExecutable(packagePath, directory)
		if err != nil {
			return nil, err
		}
	} else {
		payload = filepath.Join(directory, "installer.exe")
		if err := copyFile(packagePath, payload); err != nil {
			return nil, err
		}
	}
	hash, err := fileHash(payload)
	if err != nil {
		return nil, err
	}
	helper := filepath.Join(directory, "update-helper.exe")
	if err := copyFile(target, helper); err != nil {
		return nil, err
	}
	plan := installPlan{SchemaVersion: 1, ParentID: os.Getpid(), Target: target, Payload: payload, SHA256: hash, Installer: installer}
	data, _ := json.Marshal(plan)
	if err := writeAtomic(directory, "install.json", data); err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	command := exec.Command(helper, "--desktop-apply-update", filepath.Join(directory, "install.json"))
	command.Stdin = read
	hideHelper(command)
	if err := command.Start(); err != nil {
		read.Close()
		write.Close()
		return nil, err
	}
	read.Close()
	go func() { _ = command.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(directory, "helper-ready")); err == nil {
			return func() error {
				defer write.Close()
				_, err := write.Write([]byte("apply\n"))
				return err
			}, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	write.Close()
	return nil, errors.New("update helper failed to start")
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	return output.Close()
}

func replaceExecutable(plan installPlan) error {
	// Keep both the previous binary and failed candidate for manual recovery.
	backup := plan.Target + ".previous-" + filepath.Base(filepath.Dir(plan.Payload))
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return errors.New("previous application backup already exists")
	}
	// Stage the complete candidate and backup before replacing the target. A
	// disk/copy failure leaves the installed executable at its original path.
	candidate := plan.Target + ".update"
	if err := copyFile(plan.Payload, candidate); err != nil {
		return err
	}
	defer os.Remove(candidate)
	if err := copyFile(plan.Target, backup); err != nil {
		return err
	}
	// Publish on the target filesystem. The old file remains available until
	// the candidate is complete, and the backup survives a failed publication.
	deadline := time.Now().Add(15 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		err = os.Rename(candidate, plan.Target)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}
