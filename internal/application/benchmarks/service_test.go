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

	"github.com/894x/llm-test-studio/internal/domain"
)

const testCatalog = `{"schema_version":1,"datasets":[{"id":"one","name":"One","module":"test","factory":"one"},{"id":"two","name":"Two","module":"test","factory":"two"}]}`

type testRuntime struct {
	execute func(context.Context, WorkerRequest, func(Event) error) error
	ready   bool
}

func (r *testRuntime) Ready() bool                   { return r.ready }
func (r *testRuntime) Prepare(context.Context) error { return nil }
func (r *testRuntime) Execute(ctx context.Context, input WorkerRequest, receive func(Event) error) error {
	return r.execute(ctx, input, receive)
}

func testCommand() StartCommand {
	return StartCommand{DatasetIDs: []string{"one", "two"},
		Connection: Connection{Protocol: domain.ProtocolOpenAIChat, Model: "test-model", BaseURL: "http://localhost:8080/v1", APIKey: "private-test-key"},
		Options:    Options{SampleLimit: 2, Epochs: 1, MaxTokens: 32, Concurrency: 2, TimeoutSeconds: 5, Seed: 1}}
}

func newTestService(t *testing.T, directory string, runtime *testRuntime) *Service {
	t.Helper()
	s, err := New(t.Context(), Dependencies{Directory: directory, CatalogJSON: []byte(testCatalog), Runtime: runtime, Resolve: DirectConnection})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func completeRuntime(_ context.Context, input WorkerRequest, receive func(Event) error) error {
	sequence := 0
	emit := func(event Event) error {
		sequence++
		event.SchemaVersion, event.RunID, event.ReportID, event.Sequence = 1, input.RunID, input.ReportID, sequence
		return receive(event)
	}
	if err := emit(Event{Kind: "dataset", Total: 2}); err != nil {
		return err
	}
	for _, id := range []string{"a", "b"} {
		sample := &Sample{ID: id, Epoch: 1, Status: "completed", Scores: []Score{}, Input: "Question", Target: "42", Output: "42 " + input.APIKey}
		if err := emit(Event{Kind: "sample", Sample: sample}); err != nil {
			return err
		}
	}
	return emit(Event{Kind: "result", Total: 2, Completed: 2, Status: "completed",
		Metrics:    []Metric{{Scorer: "match", Name: "accuracy", Value: 1}},
		Provenance: &Provenance{InspectAI: "test", InspectEvals: "test", InputSHA256: strings.Repeat("a", 64)}})
}

func waitRun(t *testing.T, s *Service, id string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if !Active(run.Status) {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("benchmark did not terminate")
	return Run{}
}

func TestDurableDatasetReportsAndSecretFreeHistory(t *testing.T) {
	directory := t.TempDir()
	runtime := &testRuntime{ready: true, execute: completeRuntime}
	s := newTestService(t, directory, runtime)
	run, err := s.Start(t.Context(), testCommand())
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, run.ID)
	if run.Status != "completed" || len(run.Datasets) != 2 {
		t.Fatalf("bad run: %+v", run)
	}
	if run.Datasets[0].ReportID == run.Datasets[1].ReportID {
		t.Fatal("dataset reports shared identity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newTestService(t, directory, runtime)
	page, err := reopened.Page("")
	if err != nil || len(page.Runs) != 1 {
		t.Fatalf("page: %+v, %v", page, err)
	}
	for _, dataset := range run.Datasets {
		report, err := reopened.Report(run.ID, dataset.ReportID, "")
		if err != nil || len(report.Samples) != 2 || report.Result.Completed != 2 {
			t.Fatalf("report: %+v, %v", report, err)
		}
		if len(report.Result.Metrics) != 1 || report.Result.Metrics[0].Value != 1 {
			t.Fatal("lost frozen metrics")
		}
		export, err := reopened.Export(run.ID, dataset.ReportID)
		if err != nil || strings.Contains(string(export), "private-test-key") {
			t.Fatalf("unsafe export: %v", err)
		}
		if !strings.Contains(string(export), "[redacted]") {
			t.Fatal("expected redacted sample evidence")
		}
	}
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "private-test-key") {
			t.Errorf("secret persisted in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCancelActiveAndQueuedExecutionsPreservesProgress(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	runtime := &testRuntime{ready: true, execute: func(ctx context.Context, input WorkerRequest, receive func(Event) error) error {
		calls.Add(1)
		if err := receive(Event{SchemaVersion: 1, RunID: input.RunID, ReportID: input.ReportID, Sequence: 1, Kind: "dataset", Total: 2}); err != nil {
			return err
		}
		if err := receive(Event{SchemaVersion: 1, RunID: input.RunID, ReportID: input.ReportID, Sequence: 2, Kind: "sample", Sample: &Sample{ID: "partial", Epoch: 1, Status: "completed", Scores: []Score{}}}); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	s := newTestService(t, t.TempDir(), runtime)
	first, err := s.Start(t.Context(), testCommand())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := s.Start(t.Context(), testCommand())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(second.ID); err != nil {
		t.Fatal(err)
	}
	if run := waitRun(t, s, second.ID); run.Status != "cancelled" {
		t.Fatalf("queued cancel: %s", run.Status)
	}
	if err := s.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	run := waitRun(t, s, first.ID)
	if run.Status != "cancelled" || calls.Load() != 1 || run.Datasets[0].Completed != 1 {
		t.Fatalf("lost partial result: %+v", run)
	}
	report, err := s.Report(run.ID, run.Datasets[0].ReportID, "")
	if err != nil || len(report.Samples) != 1 {
		t.Fatalf("lost sample: %v", err)
	}
}

func TestRejectedFormatsAndMalformedWorkerDoNotBecomeSuccess(t *testing.T) {
	runtime := &testRuntime{ready: true, execute: func(_ context.Context, input WorkerRequest, receive func(Event) error) error {
		return receive(Event{SchemaVersion: 99, RunID: input.RunID, ReportID: input.ReportID, Sequence: 1, Kind: "result", Status: "completed"})
	}}
	directory := t.TempDir()
	s := newTestService(t, directory, runtime)
	run, err := s.Start(t.Context(), testCommand())
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, run.ID)
	if run.Status != "failed" {
		t.Fatal("malformed worker accepted")
	}
	path := s.store.runPath(run.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["schema_version"] = 99
	rejected, _ := json.Marshal(document)
	if err := os.WriteFile(path, rejected, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(run.ID); !errors.Is(err, ErrFormat) {
		t.Fatalf("newer format: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(rejected) {
		t.Fatal("rejected data mutated")
	}
}

func TestAdmissionRejectsUnsafeConnectionsAndDatasetSelections(t *testing.T) {
	s := newTestService(t, t.TempDir(), &testRuntime{ready: true, execute: completeRuntime})
	for _, base := range []string{"https://key@example.com/v1", "https://example.com/v1?api_key=secret", "file:///tmp/eval"} {
		command := testCommand()
		command.Connection.BaseURL = base
		if _, err := s.Start(t.Context(), command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted URL %s", base)
		}
	}
	for _, ids := range [][]string{{}, {"one", "one"}, {"../../worker"}} {
		command := testCommand()
		command.DatasetIDs = ids
		if _, err := s.Start(t.Context(), command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted datasets %v", ids)
		}
	}
	command := testCommand()
	command.Options.Concurrency = 65
	if _, err := s.Start(t.Context(), command); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted unbounded concurrency")
	}
}

func TestInterruptedRunReadsAreNonMutatingAndMissingFieldsAreRejected(t *testing.T) {
	directory := t.TempDir()
	runtime := &testRuntime{ready: true, execute: completeRuntime}
	s := newTestService(t, directory, runtime)
	run, err := s.Start(t.Context(), testCommand())
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, run.ID)
	run.Status = "running"
	run.Datasets[0].Status = "running"
	run.Datasets[0].Stage = "running"
	if err := s.store.writeRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.store.runPath(run.ID))
	reopened := newTestService(t, directory, runtime)
	observed, err := reopened.Get(run.ID)
	if err != nil || observed.Status != "interrupted" || observed.Datasets[0].Status != "interrupted" {
		t.Fatalf("interruption: %+v %v", observed, err)
	}
	after, _ := os.ReadFile(s.store.runPath(run.ID))
	if string(before) != string(after) {
		t.Fatal("reopen rewrote interrupted history")
	}
	var document map[string]any
	_ = json.Unmarshal(before, &document)
	delete(document, "finished_at")
	missing, _ := json.Marshal(document)
	if err := os.WriteFile(s.store.runPath(run.ID), missing, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(run.ID); !errors.Is(err, ErrFormat) {
		t.Fatalf("accepted missing current-version field: %v", err)
	}
}

func TestHistoryPaginationDoesNotDropOlderExecutions(t *testing.T) {
	s := newTestService(t, t.TempDir(), &testRuntime{ready: true, execute: completeRuntime})
	for i := 0; i < 27; i++ {
		run, err := s.Start(t.Context(), testCommand())
		if err != nil {
			t.Fatal(err)
		}
		waitRun(t, s, run.ID)
	}
	first, err := s.Page("")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Runs) != 25 || first.NextCursor == "" {
		t.Fatalf("bad first page: %+v", first)
	}
	second, err := s.Page(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Runs) != 2 || second.NextCursor != "" {
		t.Fatalf("bad second page: %+v", second)
	}
	oldest := second.Runs[len(second.Runs)-1]
	owner, err := s.FindReport(t.Context(), oldest.Datasets[0].ReportID)
	if err != nil || owner.ID != oldest.ID {
		t.Fatalf("report owner beyond the first page: %s, %v", owner.ID, err)
	}
	seen := map[string]bool{}
	for _, run := range append(first.Runs, second.Runs...) {
		if seen[run.ID] {
			t.Fatal("duplicate execution across pages")
		}
		seen[run.ID] = true
	}
}
