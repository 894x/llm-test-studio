// Package updater checks and stages desktop releases. It never opens a workspace
// or migrates user data; the new application performs its normal version preflight.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type View struct {
	SchemaVersion  int    `json:"schema_version"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Status         string `json:"status"`
	ErrorCode      string `json:"error_code"`
	Notes          string `json:"notes"`
	Downloaded     int64  `json:"downloaded"`
	Total          int64  `json:"total"`
	AutoCheck      bool   `json:"auto_check"`
	SkippedVersion string `json:"skipped_version"`
	InstallKind    string `json:"install_kind"`
}

type preferences struct {
	SchemaVersion  int    `json:"schema_version"`
	AutoCheck      bool   `json:"auto_check"`
	SkippedVersion string `json:"skipped_version"`
}

type Service struct {
	mu        sync.Mutex
	client    *http.Client
	directory string
	platform  string
	view      View
	selected  *packageInfo
	staged    string
	cancel    context.CancelFunc
	worker    sync.WaitGroup
	closed    bool
	commit    func() error
}

func New(version, directory string) (*Service, error) {
	platform, kind := platformPackage()
	s := &Service{
		client: newHTTPClient(), directory: directory, platform: platform,
		view: View{SchemaVersion: 1, CurrentVersion: version, Status: "idle", AutoCheck: true, InstallKind: kind},
	}
	file, err := os.Open(filepath.Join(directory, "preferences.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		defer file.Close()
		data, readErr := readBounded(file, 4096)
		var saved preferences
		if readErr != nil || decodePreferences(data, &saved) != nil {
			return nil, errors.New("unsupported update preferences")
		}
		s.view.AutoCheck, s.view.SkippedVersion = saved.AutoCheck, saved.SkippedVersion
	}
	if _, valid := versionParts(version); !valid || platform == "" {
		s.view.Status, s.view.AutoCheck = "unsupported", false
		return s, nil
	}
	if failedInstall(directory) {
		s.view.ErrorCode, s.view.Status = "install_failed", "error"
	}
	return s, nil
}

func platformPackage() (string, string) {
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		if executable, err := os.Executable(); err == nil {
			if _, err := os.Stat(filepath.Join(filepath.Dir(executable), "uninstall.exe")); err == nil {
				return "windows-amd64-installer.exe", "restart"
			}
		}
		return "windows-amd64.zip", "restart"
	}
	if runtime.GOOS == "darwin" {
		return "macos-universal.dmg", "manual"
	}
	return "", "unavailable"
}

func (s *Service) Snapshot() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

func (s *Service) busy() bool {
	return s.closed || s.view.Status == "checking" || s.view.Status == "downloading" || s.view.Status == "installing"
}

func (s *Service) Check(ctx context.Context) View {
	s.mu.Lock()
	if s.busy() || s.view.Status == "unsupported" {
		defer s.mu.Unlock()
		return s.view
	}
	checkContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	s.cancel = cancel
	s.view.Status, s.view.ErrorCode = "checking", ""
	s.worker.Add(1)
	s.mu.Unlock()
	defer s.worker.Done()
	defer cancel()
	info, err := findPackage(checkContext, s.client, s.view.CurrentVersion, s.platform)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	if err != nil {
		s.view.Status, s.view.ErrorCode = "error", "network"
		return s.view
	}
	s.selected = info
	_ = os.Remove(filepath.Join(s.directory, "last-install-result.json"))
	s.view.LatestVersion, s.view.Notes, s.view.Downloaded, s.view.Total = "", "", 0, 0
	if info == nil {
		s.view.Status = "up_to_date"
		return s.view
	}
	s.view.LatestVersion, s.view.Notes, s.view.Total = info.Version, info.Notes, info.Asset.Size
	s.view.Status = "available"
	if info.Version == s.view.SkippedVersion {
		s.view.Status = "skipped"
	}
	return s.view
}

func (s *Service) Download(ctx context.Context) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy() || s.selected == nil {
		return s.view
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	s.cancel = cancel
	info := *s.selected
	s.view.Status, s.view.ErrorCode, s.view.Downloaded = "downloading", "", 0
	s.worker.Add(1)
	go func() {
		defer s.worker.Done()
		defer cancel()
		path, code := s.download(ctx, info)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.staged = path
		s.cancel = nil
		s.view.ErrorCode = code
		s.view.Status = "ready"
		if code != "" {
			s.view.Status = "error"
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			s.view.Status, s.view.ErrorCode = "available", ""
		}
	}()
	return s.view
}

func (s *Service) Cancel() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return s.view
}

func (s *Service) download(ctx context.Context, info packageInfo) (string, string) {
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return "", "storage"
	}
	dir, err := os.MkdirTemp(s.directory, "download-*")
	if err != nil {
		return "", "storage"
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(dir)
		}
	}()
	response, err := get(ctx, s.client, info.Asset.URL)
	if err != nil {
		return "", "network"
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != info.Asset.Size {
		return "", "integrity"
	}
	path := filepath.Join(dir, info.Asset.Name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "storage"
	}
	defer file.Close()
	hash := sha256.New()
	reader := io.LimitReader(response.Body, info.Asset.Size+1)
	buffer := make([]byte, 128<<10)
	var count int64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				return "", "storage"
			}
			hash.Write(buffer[:n])
			count += int64(n)
			s.mu.Lock()
			s.view.Downloaded = count
			s.mu.Unlock()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil || ctx.Err() != nil {
			return "", "network"
		}
	}
	if count != info.Asset.Size || hex.EncodeToString(hash.Sum(nil)) != info.SHA256 {
		return "", "integrity"
	}
	if file.Sync() != nil || file.Close() != nil {
		return "", "storage"
	}
	complete = true
	return path, ""
}

func (s *Service) SetPreferences(autoCheck bool, skip bool) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy() || s.view.Status == "unsupported" {
		return s.view
	}
	saved := preferences{SchemaVersion: 1, AutoCheck: autoCheck, SkippedVersion: s.view.SkippedVersion}
	if skip {
		if s.selected == nil {
			return s.view
		}
		saved.SkippedVersion = s.selected.Version
	}
	data, err := json.Marshal(saved)
	if err != nil || writeAtomic(s.directory, "preferences.json", data) != nil {
		s.view.ErrorCode = "storage"
		return s.view
	}
	s.view.AutoCheck, s.view.SkippedVersion, s.view.ErrorCode = saved.AutoCheck, saved.SkippedVersion, ""
	if skip {
		s.view.Status = "skipped"
	}
	return s.view
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.worker.Wait()
}
