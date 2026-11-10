package benchmarks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type datasetTestRuntime struct {
	*testRuntime
	transfers atomic.Int32
	block     bool
	started   chan struct{}
}

func (r *datasetTestRuntime) DatasetKey(id string) string { return strings.Repeat("a", 64) }
func (r *datasetTestRuntime) DownloadDataset(
	ctx context.Context, id, directory string, receive func(DatasetTransfer) error,
) error {
	r.transfers.Add(1)
	if err := os.WriteFile(filepath.Join(directory, "data.json"), []byte("prepared"), 0600); err != nil {
		return err
	}
	if r.block {
		close(r.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return receive(DatasetTransfer{SchemaVersion: 1, DatasetID: id, Kind: "ready",
		Bytes: 8, Files: 1, Samples: 2, InputSHA256: strings.Repeat("b", 64), TaskVersion: "1"})
}

func newDatasetTestService(t *testing.T, directory string, runtime *datasetTestRuntime) *Service {
	t.Helper()
	service, err := New(t.Context(), Dependencies{Directory: directory, CatalogJSON: []byte(testCatalog),
		Runtime: runtime, Resolve: DirectConnection})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func waitDataset(t *testing.T, service *Service, state string) DatasetDownload {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		inventory, err := service.DatasetInventory()
		if err != nil {
			t.Fatal(err)
		}
		if inventory.Datasets[0].Download.State == state {
			return inventory.Datasets[0].Download
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("dataset did not reach %s", state)
	return DatasetDownload{}
}

func logDatasetCacheFailure(t *testing.T, service *Service) {
	t.Helper()
	inventory, inventoryErr := service.DatasetInventory()
	_, cachePath, pathErr := service.datasetPaths("one")
	resolved, resolveErr := filepath.EvalSymlinks(cachePath)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	size, files, _, inspectErr := inspectDatasetCache(ctx, cachePath, true)
	t.Logf("dataset inventory: %+v, error: %v", inventory, inventoryErr)
	t.Logf(
		"dataset cache: path=%q, resolved=%q, size=%d, files=%d, errors: path=%v, resolve=%v, inspect=%v",
		cachePath,
		resolved,
		size,
		files,
		pathErr,
		resolveErr,
		inspectErr,
	)
}

func TestDatasetCacheInspectionRejectsLinksOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.json")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, _, _, err := inspectDatasetCache(t.Context(), root, true); !errors.Is(err, ErrFormat) {
		t.Fatalf("outside cache file accepted: %v", err)
	}
}

func TestDatasetAutoDownloadReuseVerifyAndCacheCleanupPreserveReports(t *testing.T) {
	directory := t.TempDir()
	runtime := &datasetTestRuntime{testRuntime: &testRuntime{ready: true, execute: completeRuntime}}
	runtime.execute = func(ctx context.Context, request WorkerRequest, receive func(Event) error) error {
		if _, err := os.Stat(filepath.Join(request.CacheDir, "data.json")); err != nil {
			t.Error("evaluation started without prepared data")
		}
		view := filepath.Join(request.CacheDir, "n", "inspect_ai", "view")
		if err := os.MkdirAll(view, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(view, "last-eval-result"), []byte("latest log"), 0600); err != nil {
			return err
		}
		return completeRuntime(ctx, request, receive)
	}
	service := newDatasetTestService(t, directory, runtime)
	command := testCommand()
	command.DatasetIDs = []string{"one"}
	run, err := service.Start(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitRun(t, service, run.ID); result.Status != "completed" {
		logDatasetCacheFailure(t, service)
		t.Fatalf("automatic download failed: %+v", result)
	}
	ready := waitDataset(t, service, "ready")
	if ready.Bytes != 8 || ready.Files != 1 || ready.Samples != 2 || len(ready.CacheSHA256) != 64 {
		t.Fatalf("unverified cache: %+v", ready)
	}
	again, err := service.Start(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, service, again.ID)
	if runtime.transfers.Load() != 1 {
		t.Fatal("prepared data was downloaded twice")
	}
	_, cachePath, _ := service.datasetPaths("one")
	if err := os.WriteFile(filepath.Join(cachePath, "data.json"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ManageDataset("one", "verify"); err != nil {
		t.Fatal(err)
	}
	if result := waitDataset(t, service, "invalid"); result.ErrorCode != "dataset_cache_invalid" {
		t.Fatal("cache corruption was accepted")
	}
	if _, err := service.ManageDataset("one", "remove"); err != nil {
		t.Fatal(err)
	}
	waitDataset(t, service, "missing")
	if _, err := service.Report(run.ID, waitRun(t, service, run.ID).Datasets[0].ReportID, ""); err != nil {
		t.Fatalf("cache cleanup destroyed report: %v", err)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache was not removed: %v", err)
	}
}

func TestDatasetDownloadCancellationAndRejectedMetadataAreRecoverable(t *testing.T) {
	directory := t.TempDir()
	runtime := &datasetTestRuntime{testRuntime: &testRuntime{ready: true, execute: completeRuntime},
		block: true, started: make(chan struct{})}
	service := newDatasetTestService(t, directory, runtime)
	if _, err := service.ManageDataset("one", "download"); err != nil {
		t.Fatal(err)
	}
	<-runtime.started
	if _, err := service.ManageDataset("one", "cancel"); err != nil {
		t.Fatal(err)
	}
	waitDataset(t, service, "cancelled")
	path, _, _ := service.datasetPaths("one")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record datasetRecord
	if err := json.Unmarshal(before, &record); err != nil {
		t.Fatal(err)
	}
	record.Download.State = "downloading"
	interrupted, _ := json.Marshal(record)
	if err := os.WriteFile(path, interrupted, 0600); err != nil {
		t.Fatal(err)
	}
	waitDataset(t, service, "interrupted")
	after, _ := os.ReadFile(path)
	if string(after) != string(interrupted) {
		t.Fatal("interrupted cache metadata was changed on read")
	}
	record.SchemaVersion = 99
	rejected, _ := json.Marshal(record)
	if err := os.WriteFile(path, rejected, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ManageDataset("one", "remove"); !errors.Is(err, ErrFormat) {
		t.Fatalf("unknown metadata accepted: %v", err)
	}
	after, _ = os.ReadFile(path)
	if string(after) != string(rejected) {
		t.Fatal("rejected cache metadata was mutated")
	}
	if _, err := service.ManageDataset("../outside", "remove"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid dataset identity: %v", err)
	}
}

func TestDatasetCacheCannotBeChangedWhileEvaluationUsesIt(t *testing.T) {
	started := make(chan struct{})
	runtime := &datasetTestRuntime{testRuntime: &testRuntime{ready: true}}
	runtime.execute = func(ctx context.Context, request WorkerRequest, receive func(Event) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	service := newDatasetTestService(t, t.TempDir(), runtime)
	command := testCommand()
	command.DatasetIDs = []string{"one"}
	run, err := service.Start(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		logDatasetCacheFailure(t, service)
		t.Fatal("evaluation did not start")
	}
	for _, action := range []string{"verify", "remove"} {
		if _, err := service.ManageDataset("one", action); !errors.Is(err, ErrBusy) {
			t.Fatalf("active cache accepted %s: %v", action, err)
		}
	}
	if err := service.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	waitRun(t, service, run.ID)
	if _, err := service.ManageDataset("one", "remove"); err != nil {
		t.Fatal(err)
	}
	waitDataset(t, service, "missing")
}
