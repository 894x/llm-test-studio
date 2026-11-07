package benchmarks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const maxDatasetBytes int64 = 8 << 30

type DatasetTransfer struct {
	SchemaVersion int    `json:"schema_version"`
	DatasetID     string `json:"dataset_id"`
	Kind          string `json:"kind"`
	Bytes         int64  `json:"bytes"`
	Files         int    `json:"files"`
	Samples       int    `json:"samples"`
	InputSHA256   string `json:"input_sha256"`
	TaskVersion   string `json:"task_version"`
}

// DatasetRuntime only prepares registered task data; it receives no model key.
type DatasetRuntime interface {
	DatasetKey(string) string
	DownloadDataset(context.Context, string, string, func(DatasetTransfer) error) error
}

type DatasetDownload struct {
	State       string `json:"state"`
	Bytes       int64  `json:"bytes"`
	Files       int    `json:"files"`
	Samples     int    `json:"samples"`
	InputSHA256 string `json:"input_sha256"`
	CacheSHA256 string `json:"cache_sha256"`
	TaskVersion string `json:"task_version"`
	UpdatedAt   string `json:"updated_at"`
	VerifiedAt  string `json:"verified_at"`
	ErrorCode   string `json:"error_code"`
}

type DatasetItem struct {
	Dataset  Dataset         `json:"dataset"`
	Download DatasetDownload `json:"download"`
}

type DatasetInventory struct {
	SchemaVersion int           `json:"schema_version"`
	Datasets      []DatasetItem `json:"datasets"`
	Runtime       RuntimeStatus `json:"runtime"`
}

type datasetRecord struct {
	SchemaVersion int             `json:"schema_version"`
	DatasetID     string          `json:"dataset_id"`
	Identity      string          `json:"identity"`
	Download      DatasetDownload `json:"download"`
}

type datasetOperation struct {
	cancel    context.CancelFunc
	done      chan struct{}
	record    datasetRecord
	action    string
	cachePath string
	runtime   DatasetRuntime
}

func (s *Service) datasetDefinition(id string) (Dataset, error) {
	for _, dataset := range s.datasets {
		if dataset.ID == id {
			return dataset, nil
		}
	}
	return Dataset{}, ErrNotFound
}

func (s *Service) datasetPaths(id string) (string, string, error) {
	if _, err := s.datasetDefinition(id); err != nil {
		return "", "", err
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\:`) {
		return "", "", ErrInvalid
	}
	root := filepath.Join(s.store.directory, "datasets", id)
	return filepath.Join(root, "state.json"), filepath.Join(s.cacheDirectory, id), nil
}

func (s *Service) datasetRecord(id string) (datasetRecord, error) {
	statePath, cachePath, err := s.datasetPaths(id)
	if err != nil {
		return datasetRecord{}, err
	}
	if operation := s.downloads[id]; operation != nil {
		return operation.record, nil
	}
	record := datasetRecord{SchemaVersion: 1, DatasetID: id, Download: DatasetDownload{State: "missing"}}
	if err := readJSON(statePath, &record); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrNotFound) {
			return record, nil
		}
		return record, err
	}
	validIdentity := record.SchemaVersion == 1 && record.DatasetID == id
	validBytes := record.Download.Bytes >= 0 && record.Download.Bytes <= maxDatasetBytes
	validFiles := record.Download.Files >= 0 && record.Download.Files <= 100000
	validSamples := record.Download.Samples >= 0 && record.Download.Samples <= 100000
	validCounts := validFiles && validSamples
	validRecord := validIdentity && validBytes && validCounts
	if !validRecord {
		return record, ErrFormat
	}
	for _, value := range []string{record.Download.UpdatedAt, record.Download.VerifiedAt} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return record, ErrFormat
			}
		}
	}
	for _, value := range []string{record.Identity, record.Download.InputSHA256, record.Download.CacheSHA256} {
		if value != "" && !validSampleCursor(value) {
			return record, ErrFormat
		}
	}
	if !safeText(record.Download.TaskVersion, 256, true) {
		return record, ErrFormat
	}
	switch record.Download.ErrorCode {
	case "", "dataset_download_failed", "dataset_cache_invalid":
	default:
		return record, ErrFormat
	}
	switch record.Download.State {
	case "queued", "downloading", "verifying", "removing":
		record.Download.State = "interrupted"
	case "missing", "ready", "failed", "cancelled", "invalid", "interrupted":
	default:
		return record, ErrFormat
	}
	if record.Download.State == "ready" {
		validHashes := validSampleCursor(record.Download.CacheSHA256) && validSampleCursor(record.Download.InputSHA256)
		if !validHashes || record.Download.Samples == 0 {
			return record, ErrFormat
		}
		runtime, supported := s.runtime.(DatasetRuntime)
		if !supported || record.Identity != runtime.DatasetKey(id) {
			record.Download.State = "outdated"
		} else {
			bytes, files, _, err := inspectDatasetCache(s.ctx, cachePath, false)
			if err != nil || bytes != record.Download.Bytes || files != record.Download.Files {
				record.Download.State, record.Download.ErrorCode = "invalid", "dataset_cache_invalid"
			}
		}
	}
	return record, nil
}

func (s *Service) DatasetInventory() (DatasetInventory, error) {
	catalog := s.Catalog()
	result := DatasetInventory{SchemaVersion: 1, Runtime: catalog.Runtime, Datasets: []DatasetItem{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, dataset := range s.datasets {
		record, err := s.datasetRecord(dataset.ID)
		if err != nil {
			return result, err
		}
		result.Datasets = append(result.Datasets, DatasetItem{Dataset: dataset, Download: record.Download})
	}
	return result, nil
}

func (s *Service) datasetInUse(id string) bool {
	for _, entry := range s.active {
		for _, dataset := range entry.run.Datasets {
			if dataset.Dataset.ID == id {
				return true
			}
		}
	}
	return false
}

func (s *Service) ManageDataset(id, action string) (DatasetDownload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.storageError != nil {
		return DatasetDownload{}, ErrUnavailable
	}
	record, err := s.datasetRecord(id)
	if err != nil {
		return DatasetDownload{}, err
	}
	if action == "cancel" {
		if operation := s.downloads[id]; operation != nil {
			operation.cancel()
		}
		return record.Download, nil
	}
	if action != "download" && action != "verify" && action != "remove" {
		return DatasetDownload{}, ErrInvalid
	}
	if s.downloads[id] != nil {
		return record.Download, nil
	}
	runtime, supported := s.runtime.(DatasetRuntime)
	if !supported || (action == "download" && (s.preparing || !s.runtime.Ready())) {
		return DatasetDownload{}, ErrRuntime
	}
	cacheInUse := action != "download" && s.datasetInUse(id)
	if len(s.downloads) >= 4 || cacheInUse {
		return DatasetDownload{}, ErrBusy
	}
	if action == "download" && record.Download.State == "ready" {
		return record.Download, nil
	}
	if action == "verify" && record.Download.State != "ready" {
		return DatasetDownload{}, ErrInvalid
	}
	statePath, cachePath, _ := s.datasetPaths(id)
	record.Identity = runtime.DatasetKey(id)
	record.Download.ErrorCode = ""
	if action == "download" {
		record.Download.Samples = 0
		record.Download.InputSHA256, record.Download.CacheSHA256 = "", ""
	}
	states := map[string]string{"download": "downloading", "verify": "verifying", "remove": "removing"}
	record.Download.State = states[action]
	record.Download.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeJSON(s.ctx, statePath, record); err != nil {
		return DatasetDownload{}, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Hour)
	operation := &datasetOperation{cancel: cancel, done: make(chan struct{}), record: record,
		action: action, cachePath: cachePath, runtime: runtime}
	s.downloads[id] = operation
	s.workers.Add(1)
	go s.manageDataset(ctx, operation)
	return record.Download, nil
}

func (s *Service) manageDataset(ctx context.Context, operation *datasetOperation) {
	defer s.workers.Done()
	defer operation.cancel()
	record := operation.record
	id, action := record.DatasetID, operation.action
	cachePath, runtime := operation.cachePath, operation.runtime
	var err error
	switch action {
	case "download":
		err = fileconfig.EnsurePrivateDirectory(ctx, cachePath)
		if err == nil {
			err = runtime.DownloadDataset(ctx, id, cachePath, func(event DatasetTransfer) error {
				validIdentity := event.SchemaVersion == 1 && event.DatasetID == id
				validBytes := event.Bytes >= 0 && event.Bytes <= maxDatasetBytes
				validFiles := event.Files >= 0 && event.Files <= 100000
				validSize := validBytes && validFiles
				if !validIdentity || !validSize || (event.Kind != "progress" && event.Kind != "ready") {
					return ErrFormat
				}
				record.Download.Bytes, record.Download.Files = event.Bytes, event.Files
				if event.Kind == "ready" {
					validSamples := event.Samples >= 1 && event.Samples <= 100000
					validSource := validSampleCursor(event.InputSHA256) && safeText(event.TaskVersion, 256, true)
					if !validSamples || !validSource {
						return ErrFormat
					}
					record.Download.Samples = event.Samples
					record.Download.InputSHA256, record.Download.TaskVersion = event.InputSHA256, event.TaskVersion
				}
				s.mu.Lock()
				operation.record = record
				s.mu.Unlock()
				return nil
			})
		}
		if err == nil && record.Download.Samples == 0 {
			err = ErrFormat
		}
		if err == nil {
			bytes, files, checksum, inspectionErr := inspectDatasetCache(ctx, cachePath, true)
			record.Download.Bytes, record.Download.Files = bytes, files
			record.Download.CacheSHA256, err = checksum, inspectionErr
		}
	case "verify":
		var digest string
		_, _, digest, err = inspectDatasetCache(ctx, cachePath, true)
		if err == nil && digest != record.Download.CacheSHA256 {
			err = ErrFormat
		}
	case "remove":
		// Only remove this registered dataset's cache, never shared HF caches or reports.
		expected := filepath.Join(s.cacheDirectory, id)
		dataRoot, rootErr := filepath.EvalSymlinks(s.cacheDirectory)
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(cachePath))
		validParent := rootErr == nil && parentErr == nil && parent == dataRoot
		if !filepath.IsAbs(cachePath) || cachePath != expected || !validParent {
			err = ErrInvalid
		} else {
			err = os.RemoveAll(cachePath)
		}
	}
	if err != nil && action == "download" {
		inspectionCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		bytes, files, _, inspectionErr := inspectDatasetCache(inspectionCtx, cachePath, false)
		cancel()
		if inspectionErr == nil {
			record.Download.Bytes, record.Download.Files = bytes, files
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record.Download.State, record.Download.UpdatedAt = "ready", now
	if action == "remove" && err == nil {
		record.Download = DatasetDownload{State: "missing", UpdatedAt: now}
	} else if err == nil {
		record.Download.VerifiedAt = now
	} else {
		record.Download.State, record.Download.ErrorCode = "failed", "dataset_download_failed"
		if action == "verify" {
			record.Download.State, record.Download.ErrorCode = "invalid", "dataset_cache_invalid"
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			record.Download.State, record.Download.ErrorCode = "cancelled", ""
		}
	}
	s.mu.Lock()
	statePath, _, _ := s.datasetPaths(id)
	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if writeErr := writeJSON(writeCtx, statePath, record); writeErr != nil {
		s.storageError = writeErr
	}
	cancel()
	operation.record = record
	delete(s.downloads, id)
	close(operation.done)
	s.mu.Unlock()
	s.notify()
}

func (s *Service) ensureDataset(ctx context.Context, id string) (string, error) {
	if _, supported := s.runtime.(DatasetRuntime); !supported {
		return "", nil
	}
	if _, err := s.ManageDataset(id, "download"); err != nil {
		return "", err
	}
	s.mu.Lock()
	operation := s.downloads[id]
	s.mu.Unlock()
	if operation != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-operation.done:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.datasetRecord(id)
	if err != nil {
		return "", err
	}
	if record.Download.State != "ready" {
		return "", ErrUnavailable
	}
	_, cachePath, _ := s.datasetPaths(id)
	return cachePath, nil
}

func inspectDatasetCache(ctx context.Context, root string, checksum bool) (int64, int, string, error) {
	var size int64
	files := 0
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relativePath, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(relativePath), "/")
		if entry.IsDir() && len(parts) > 1 && parts[0] == "n" {
			if entry.Name() == "traces" || entry.Name() == "view" {
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".lock") || strings.HasSuffix(entry.Name(), ".log") {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrFormat
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return ErrFormat
		}
		size += info.Size()
		files++
		if size > maxDatasetBytes || files > 100000 {
			return ErrInvalid
		}
		if !checksum {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		name, _ := filepath.Rel(root, path)
		_, _ = fmt.Fprintf(digest, "%s\x00%d\x00", filepath.ToSlash(name), info.Size())
		_, err = io.Copy(digest, datasetReader{ctx: ctx, Reader: file})
		return err
	})
	return size, files, hex.EncodeToString(digest.Sum(nil)), err
}

type datasetReader struct {
	ctx context.Context
	io.Reader
}

func (r datasetReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(buffer)
}
