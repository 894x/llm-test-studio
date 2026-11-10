package benchmarks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Dependencies struct {
	Directory      string
	CacheDirectory string
	CatalogJSON    []byte
	Runtime        Runtime
	Resolve        Resolver
	Changed        func()
}

type execution struct {
	run     Run
	cancel  context.CancelFunc
	samples map[string]map[string]bool
}

type Service struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	store          store
	cacheDirectory string
	runtime        Runtime
	resolve        Resolver
	datasets       []Dataset
	active         map[string]*execution
	slot           chan struct{}
	closed         bool
	preparing      bool
	runtimeError   string
	storageError   error
	changed        func()
	downloads      map[string]*datasetOperation
}

func New(ctx context.Context, deps Dependencies) (*Service, error) {
	if ctx == nil || !filepath.IsAbs(deps.Directory) || deps.Runtime == nil || deps.Resolve == nil {
		return nil, ErrInvalid
	}
	cacheDirectory := deps.CacheDirectory
	if cacheDirectory == "" {
		cacheDirectory = filepath.Join(deps.Directory, "cache")
	}
	if !filepath.IsAbs(cacheDirectory) {
		return nil, ErrInvalid
	}
	var manifest struct {
		SchemaVersion int `json:"schema_version"`
		Datasets      []struct {
			Dataset
			Module  string `json:"module"`
			Factory string `json:"factory"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(deps.CatalogJSON, &manifest); err != nil || manifest.SchemaVersion != 1 || len(manifest.Datasets) == 0 {
		return nil, ErrFormat
	}
	datasets := []Dataset{}
	seen := map[string]bool{}
	for _, entry := range manifest.Datasets {
		if entry.ID == "" || seen[entry.ID] || !safeText(entry.Name, 256, false) {
			return nil, ErrFormat
		}
		seen[entry.ID] = true
		datasets = append(datasets, entry.Dataset)
	}
	lifecycle, cancel := context.WithCancel(ctx)
	return &Service{ctx: lifecycle, cancel: cancel, store: store{directory: deps.Directory},
		cacheDirectory: cacheDirectory,
		runtime:        deps.Runtime, resolve: deps.Resolve, datasets: datasets,
		active: map[string]*execution{}, downloads: map[string]*datasetOperation{},
		slot: make(chan struct{}, 1), changed: deps.Changed}, nil
}

func (s *Service) notify() {
	if s.changed != nil {
		s.changed()
	}
}

func (s *Service) Catalog() Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "missing"
	if s.runtime.Ready() {
		state = "ready"
	}
	if s.runtimeError != "" {
		state = "failed"
	}
	if s.preparing {
		state = "preparing"
	}
	return Catalog{SchemaVersion: 1, Datasets: append([]Dataset{}, s.datasets...), Runtime: RuntimeStatus{State: state, ErrorCode: s.runtimeError}}
}

func (s *Service) Prepare() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	if s.preparing {
		return nil
	}
	if len(s.active) > 0 || len(s.downloads) > 0 {
		return ErrBusy
	}
	s.preparing = true
	s.runtimeError = ""
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Minute)
		defer cancel()
		err := s.runtime.Prepare(ctx)
		s.mu.Lock()
		s.preparing = false
		if err != nil {
			s.runtimeError = "runtime_prepare_failed"
		}
		s.mu.Unlock()
		s.notify()
	}()
	return nil
}

func (s *Service) Start(ctx context.Context, command StartCommand) (Run, error) {
	if command.Options.Validate() != nil || ValidateConnection(command.Connection) != nil {
		return Run{}, ErrInvalid
	}
	if len(command.DatasetIDs) == 0 || len(command.DatasetIDs) > 16 {
		return Run{}, ErrInvalid
	}
	datasets := []DatasetRun{}
	seen := map[string]bool{}
	for _, id := range command.DatasetIDs {
		if seen[id] {
			return Run{}, ErrInvalid
		}
		seen[id] = true
		found := false
		for _, item := range s.datasets {
			if item.ID == id {
				datasets = append(datasets, DatasetRun{ReportID: uuid.NewString(), Dataset: item, Status: "queued", Stage: "queued", Metrics: []Metric{}})
				found = true
				break
			}
		}
		if !found {
			return Run{}, ErrInvalid
		}
	}
	connection, err := s.resolve(ctx, command.Connection)
	if err != nil {
		return Run{}, ErrUnavailable
	}
	defer func() {
		if connection.APIKey != nil {
			clear(connection.APIKey)
		}
	}()
	if ValidateBaseURL(connection.Target.BaseURL) != nil || len(connection.APIKey) == 0 {
		return Run{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Run{}, ErrUnavailable
	}
	if s.storageError != nil {
		return Run{}, ErrUnavailable
	}
	if s.preparing || !s.runtime.Ready() {
		return Run{}, ErrRuntime
	}
	if len(s.active) >= 16 {
		return Run{}, ErrBusy
	}
	now := time.Now().UTC()
	run := Run{SchemaVersion: 1, ID: newRunID(now, uuid.NewString()), Target: connection.Target,
		Options: command.Options, Status: "queued", CreatedAt: now.Format(time.RFC3339Nano), Datasets: datasets}
	if err := s.store.writeRun(ctx, run); err != nil {
		return Run{}, err
	}
	lifecycle, cancel := context.WithCancel(s.ctx)
	s.active[run.ID] = &execution{run: run, cancel: cancel, samples: map[string]map[string]bool{}}
	secret := append([]byte{}, connection.APIKey...)
	s.workers.Add(1)
	go s.execute(lifecycle, run.ID, secret)
	return cloneRun(run), nil
}

func DirectConnection(_ context.Context, connection Connection) (ResolvedConnection, error) {
	if connection.ChannelID != "" || ValidateConnection(connection) != nil {
		return ResolvedConnection{}, ErrInvalid
	}
	u, _ := url.Parse(connection.BaseURL)
	return ResolvedConnection{Target: Target{Protocol: connection.Protocol, Model: connection.Model,
		BaseURL: connection.BaseURL, ChannelName: u.Host}, APIKey: []byte(connection.APIKey)}, nil
}

func (s *Service) execute(ctx context.Context, id string, secret []byte) {
	defer s.workers.Done()
	defer clear(secret)
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	case <-ctx.Done():
		s.finish(id, "cancelled")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 7*24*time.Hour)
	defer cancel()
	s.mu.Lock()
	entry := s.active[id]
	entry.run.Status = "running"
	datasetCount := len(entry.run.Datasets)
	s.mu.Unlock()
	s.notify()
	for index := 0; index < datasetCount; index++ {
		if ctx.Err() != nil {
			s.finish(id, "cancelled")
			return
		}
		s.mu.Lock()
		dataset := &entry.run.Datasets[index]
		dataset.Status, dataset.Stage = "running", "loading"
		dataset.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		run := cloneRun(entry.run)
		s.mu.Unlock()
		reportID := run.Datasets[index].ReportID
		if err := s.persist(id); err != nil {
			s.finish(id, "failed")
			return
		}
		request := WorkerRequest{SchemaVersion: 1, RunID: id, ReportID: reportID,
			DatasetID: run.Datasets[index].Dataset.ID, Protocol: run.Target.Protocol,
			Model: run.Target.Model, BaseURL: run.Target.BaseURL, APIKey: string(secret), Options: run.Options,
			// Keep native log paths short: Inspect appends a timestamped filename,
			// and Windows filesystem adapters may still enforce MAX_PATH.
			LogDir: filepath.Join(s.store.directory, "inspect", reportID)}
		cacheDir, cacheErr := s.ensureDataset(ctx, request.DatasetID)
		if cacheErr != nil {
			if ctx.Err() != nil {
				s.finish(id, "cancelled")
				return
			}
			s.mu.Lock()
			dataset = &entry.run.Datasets[index]
			dataset.Status, dataset.Stage, dataset.ErrorCode = "failed", "failed", "evaluation_failed"
			dataset.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			s.mu.Unlock()
			if err := s.persist(id); err != nil {
				s.finish(id, "failed")
				return
			}
			continue
		}
		request.CacheDir = cacheDir
		lastSequence := 0
		resultReceived := false
		err := s.runtime.Execute(ctx, request, func(event Event) error {
			if event.SchemaVersion != 1 || event.RunID != id || event.ReportID != reportID || event.Sequence <= lastSequence || resultReceived {
				return ErrFormat
			}
			lastSequence = event.Sequence
			payload, err := json.Marshal(event)
			if err != nil {
				return ErrFormat
			}
			encodedSecret, _ := json.Marshal(string(secret))
			payload = bytes.ReplaceAll(payload, encodedSecret[1:len(encodedSecret)-1], []byte("[redacted]"))
			if json.Unmarshal(payload, &event) != nil {
				return ErrFormat
			}
			if err := s.applyEvent(id, index, event); err != nil {
				return err
			}
			resultReceived = event.Kind == "result"
			return nil
		})
		s.mu.Lock()
		dataset = &entry.run.Datasets[index]
		if ctx.Err() != nil {
			dataset.Status, dataset.Stage = "cancelled", "cancelled"
		} else if err != nil || !resultReceived {
			dataset.Status, dataset.Stage = "failed", "failed"
			dataset.ErrorCode = "evaluation_failed"
		}
		dataset.InFlight = 0
		dataset.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.mu.Unlock()
		if err := s.persist(id); err != nil {
			s.finish(id, "failed")
			return
		}
	}
	status := "completed"
	s.mu.Lock()
	for _, dataset := range entry.run.Datasets {
		if dataset.Status != "completed" {
			status = "failed"
		}
	}
	s.mu.Unlock()
	if ctx.Err() != nil {
		status = "cancelled"
	}
	s.finish(id, status)
}

func (s *Service) applyEvent(id string, index int, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.active[id]
	dataset := &entry.run.Datasets[index]
	validCounts := event.Total >= 0 && event.Total <= 100000 && event.Completed >= 0 && event.Completed <= event.Total
	validFailures := event.Failed >= 0 && event.Failed <= event.Completed && event.InFlight >= 0 && event.InFlight <= entry.run.Options.Concurrency
	if !validCounts || !validFailures {
		return ErrFormat
	}
	switch event.Kind {
	case "loading":
		dataset.Stage = "loading"
	case "dataset":
		if event.Total < 1 {
			return ErrFormat
		}
		dataset.Total, dataset.Stage = event.Total, "running"
	case "progress", "result":
		if event.Total != dataset.Total || event.Completed < dataset.Completed {
			return ErrFormat
		}
		dataset.Completed, dataset.Failed, dataset.InFlight = event.Completed, event.Failed, event.InFlight
		if event.Kind == "result" {
			if !validStatus(event.Status) || Active(event.Status) || event.Provenance == nil || len(event.Metrics) > 128 {
				return ErrFormat
			}
			for _, metric := range event.Metrics {
				if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
					return ErrFormat
				}
			}
			dataset.Status, dataset.Stage = event.Status, event.Status
			dataset.Metrics = append([]Metric{}, event.Metrics...)
			dataset.Provenance = event.Provenance
		}
	case "sample":
		if event.Sample == nil {
			return ErrFormat
		}
		if err := s.store.writeSample(s.ctx, id, dataset.ReportID, *event.Sample); err != nil {
			return err
		}
		if entry.samples[dataset.ReportID] == nil {
			entry.samples[dataset.ReportID] = map[string]bool{}
		}
		key := sampleKey(*event.Sample)
		previous, exists := entry.samples[dataset.ReportID][key]
		failed := event.Sample.Status == "error"
		if !exists {
			dataset.Completed++
		}
		if exists && previous {
			dataset.Failed--
		}
		if failed {
			dataset.Failed++
		}
		entry.samples[dataset.ReportID][key] = failed
	case "error":
		dataset.ErrorCode = "evaluation_failed"
		return ErrUnavailable
	default:
		return ErrFormat
	}
	if err := s.store.writeRun(s.ctx, entry.run); err != nil {
		return err
	}
	return nil
}

func (s *Service) persist(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.store.writeRun(ctx, s.active[id].run)
}

func (s *Service) finish(id, status string) {
	s.mu.Lock()
	entry := s.active[id]
	entry.run.Status = status
	entry.run.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	for i := range entry.run.Datasets {
		dataset := &entry.run.Datasets[i]
		if Active(dataset.Status) {
			dataset.Status, dataset.Stage, dataset.InFlight = status, status, 0
			dataset.FinishedAt = entry.run.FinishedAt
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := s.store.writeRun(ctx, entry.run); err != nil {
		s.storageError = err
	}
	cancel()
	entry.cancel()
	delete(s.active, id)
	s.mu.Unlock()
	s.notify()
}

func cloneRun(run Run) Run {
	payload, _ := json.Marshal(run)
	var cloned Run
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}

func (s *Service) readRun(id string) (Run, error) {
	if s.storageError != nil {
		return Run{}, ErrUnavailable
	}
	if active, exists := s.active[id]; exists {
		return cloneRun(active.run), nil
	}
	run, err := s.store.readRun(id)
	if err != nil {
		return Run{}, err
	}
	if Active(run.Status) {
		run.Status = "interrupted"
		for i := range run.Datasets {
			if Active(run.Datasets[i].Status) {
				run.Datasets[i].Status, run.Datasets[i].Stage, run.Datasets[i].InFlight = "interrupted", "interrupted", 0
			}
		}
	}
	return run, nil
}

func (s *Service) Get(id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readRun(id)
}

func (s *Service) Page(after string) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page, err := s.store.list(after)
	if err != nil {
		return page, err
	}
	for i := range page.Runs {
		page.Runs[i], err = s.readRun(page.Runs[i].ID)
		if err != nil {
			return page, err
		}
	}
	return page, nil
}

func (s *Service) Report(runID, reportID, after string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.readRun(runID)
	if err != nil {
		return Report{}, err
	}
	for _, dataset := range run.Datasets {
		if dataset.ReportID != reportID {
			continue
		}
		samples, next, err := s.store.samples(runID, reportID, after)
		if err != nil {
			return Report{}, err
		}
		return Report{SchemaVersion: 1, RunID: runID, Target: run.Target, Options: run.Options, Result: dataset, Samples: samples, NextCursor: next}, nil
	}
	return Report{}, ErrNotFound
}

func (s *Service) FindReport(ctx context.Context, reportID string) (Run, error) {
	if _, err := uuid.Parse(reportID); err != nil {
		return Run{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return Run{}, err
		}
		page, err := s.store.list(after)
		if err != nil {
			return Run{}, err
		}
		for _, run := range page.Runs {
			for _, dataset := range run.Datasets {
				if dataset.ReportID == reportID {
					return s.readRun(run.ID)
				}
			}
		}
		if page.NextCursor == "" {
			return Run{}, ErrNotFound
		}
		after = page.NextCursor
	}
}

func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validRunID(id) {
		return ErrInvalid
	}
	entry, exists := s.active[id]
	if !exists {
		_, err := s.store.readRun(id)
		return err
	}
	entry.run.Status = "cancelling"
	entry.cancel()
	return nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.workers.Wait()
	return s.storageError
}

func PublicError(err error) string {
	for _, code := range []error{ErrInvalid, ErrRuntime, ErrBusy, ErrNotFound, ErrFormat, ErrUnavailable} {
		if errors.Is(err, code) {
			return code.Error()
		}
	}
	return ErrUnavailable.Error()
}

func (s *Service) Export(runID, reportID string) ([]byte, error) {
	// Bound the desktop transfer; samples are encoded page by page.
	report, err := s.Report(runID, reportID, "")
	if err != nil {
		return nil, err
	}
	if Active(report.Result.Status) {
		return nil, ErrBusy
	}
	var output bytes.Buffer
	metadata := report
	metadata.Samples, metadata.NextCursor = []Sample{}, ""
	prefix, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	const marker = `"samples":[]`
	parts := bytes.SplitN(prefix, []byte(marker), 2)
	if len(parts) != 2 {
		return nil, ErrFormat
	}
	output.Write(parts[0])
	output.WriteString(`"samples":[`)
	first := true
	for {
		for _, sample := range report.Samples {
			payload, err := json.Marshal(sample)
			if err != nil {
				return nil, err
			}
			if output.Len()+len(payload) > 32*1024*1024 {
				return nil, ErrUnavailable
			}
			if !first {
				output.WriteByte(',')
			}
			first = false
			output.Write(payload)
		}
		if report.NextCursor == "" {
			break
		}
		report, err = s.Report(runID, reportID, report.NextCursor)
		if err != nil {
			return nil, err
		}
	}
	output.WriteByte(']')
	output.Write(parts[1])
	return output.Bytes(), nil
}
