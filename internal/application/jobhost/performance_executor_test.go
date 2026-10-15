package jobhost_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func performanceCoreSubmission(
	t *testing.T,
	baseURL string,
	configure func(*quicktest.PerformanceCommand),
) jobcontract.Submission {
	t.Helper()
	command := quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: baseURL, APIKey: "host-only-secret", ModelID: "performance-model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 3000, InputTokens: 32, OutputTokens: 3,
	}
	if configure != nil {
		configure(&command)
	}
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(), command)
	if code != "" {
		t.Fatalf("prepare performance Core fixture: %s", code)
	}
	secret, err := lease.Bytes()
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	spec := testSubmission(t).Job
	spec.Spec = jobcontract.Payload{Performance: &prepared}
	return jobcontract.Submission{
		Job: spec, Secrets: []jobcontract.Secret{{Slot: prepared.ReportID, Value: secret}},
	}
}

func newPerformanceCore(t *testing.T, repository jobhost.PerformanceExecutionRepository) *jobhost.PerformanceExecutor {
	t.Helper()
	core, err := jobhost.NewPerformanceExecutor(jobhost.PerformanceExecutorDependencies{
		Repository: repository, Transport: http.DefaultTransport, Clock: wallClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func writePerformanceCoreResponse(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
	fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n")
	fmt.Fprint(writer, "data: [DONE]\n\n")
}

func waitPerformanceJob(
	t *testing.T,
	repository jobhost.Repository,
	id string,
	status domain.RunStatus,
) jobcontract.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var snapshot jobcontract.Snapshot
	var err error
	for time.Now().Before(deadline) {
		snapshot, err = repository.GetJob(t.Context(), id)
		if err == nil && jobcontract.Terminal(snapshot.Receipt.Status) {
			if snapshot.Receipt.Status != status {
				if performance, ok := repository.(jobhost.PerformanceRepository); ok {
					observation, readErr := performance.GetPerformanceObservation(t.Context(), id)
					t.Logf(
						"last checkpoint=%s/%s rev %d error=%v",
						observation.Checkpoint.Stage,
						observation.Checkpoint.Boundary,
						observation.Revision,
						readErr,
					)
				}
				t.Fatalf(
					"Performance terminal=%s failure=%+v, want %s",
					snapshot.Receipt.Status,
					snapshot.Failure,
					status,
				)
			}
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Performance remained %s before its observation deadline: %v", snapshot.Receipt.Status, err)
	return jobcontract.Snapshot{}
}

func TestPerformanceExecutorUsesRealCoreForEveryLoadPhaseWithoutSubmissionContext(t *testing.T) {
	for _, mode := range []string{"measured", "warmup-ramp", "capacity", "open-loop", "normal-workload"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Header.Get("Authorization") != "Bearer host-only-secret" {
					t.Error("Performance used a different credential")
				}
				writePerformanceCoreResponse(writer)
			}))
			defer server.Close()
			repository := openHostRepository(t)
			core := newPerformanceCore(t, repository)
			host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
			submission := performanceCoreSubmission(t, server.URL, func(command *quicktest.PerformanceCommand) {
				switch mode {
				case "warmup-ramp":
					command.WarmupRequests, command.RampDurationMS, command.RampRequestCap = 1, 20, 50
					command.SliceDurationMS = 10
				case "capacity":
					command.WarmupRequests, command.Concurrency = 1, 3
					command.CapacityEnabled = true
					command.CapacityStart, command.CapacityStep = 1, 1
					command.SLOE2EMS, command.SLOTargetPercent = 1000, 100
				case "open-loop":
					command.LoadMode, command.Concurrency = domain.LoadOpenLoop, 0
					command.RequestCount, command.DurationMS = 0, 30
					command.RatePerSecond, command.MaxInFlight = 100, 2
					command.WarmupRequests, command.RampDurationMS, command.SliceDurationMS = 1, 20, 10
				case "normal-workload":
					command.WorkloadMode, command.RandomSeed = quicktest.PerformanceWorkloadNormal, 7
					command.InputTokensStdDev, command.OutputTokensStdDev = 5, 1
				}
			})
			spec := submission.Job
			ctx, cancel := context.WithCancel(t.Context())
			if _, _, err := host.Submit(ctx, &submission); err != nil {
				cancel()
				t.Fatal(err)
			}
			cancel()
			waitPerformanceJob(
				t,
				repository,
				spec.JobID,
				domain.RunCompleted,
			)
			waitSettled(t, repository)
			sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
			if err != nil || sealed.Result.ObservationState != jobcontract.PerformanceAvailable {
				t.Fatalf("complete performance result missing: state=%s error=%v", sealed.Result.ObservationState, err)
			}
			report := sealed.Result.Observation.Checkpoint.Report
			if !report.Success || report.ReportID != spec.Spec.Performance.ReportID || report.Archived {
				t.Fatal("durable execution observation differs from the actual unarchived Core result")
			}
			expectedRequests := int64(report.Progress.Launched)
			if report.Warmup != nil {
				expectedRequests += int64(report.Warmup.Launched)
			}
			if report.Ramp != nil {
				expectedRequests += int64(report.Ramp.Traffic.Launched)
			}
			if report.CapacityResult != nil {
				expectedRequests = int64(report.Warmup.Launched)
				for _, rung := range report.CapacityResult.Rungs {
					expectedRequests += int64(rung.Progress.Launched)
				}
			}
			if requests.Load() != expectedRequests {
				t.Fatalf("actual HTTP requests=%d recorded=%d", requests.Load(), expectedRequests)
			}
			archive, err := repository.GetQuickPerformanceReport(t.Context(), report.ReportID)
			if err != nil || archive.GeneratedAt != sealed.Result.SealedAt.Format(time.RFC3339Nano) {
				t.Fatalf("statistical archive did not use its stable seal time: %v", err)
			}
			before, _ := repository.GetJob(t.Context(), spec.JobID)
			for range 2 {
				if err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted}); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := repository.GetJob(t.Context(), spec.JobID)
			if before.LatestSequence != after.LatestSequence || requests.Load() != expectedRequests {
				t.Fatal("settlement repeated HTTP traffic or created new report events")
			}
		})
	}
}

func TestPerformanceExecutorStopAndCancelPreserveActualPhaseObservations(t *testing.T) {
	for _, phase := range []string{"warmup", "ramp", "measured", "capacity"} {
		for _, kind := range []jobcontract.CommandKind{jobcontract.CommandStopSending, jobcontract.CommandCancel} {
			t.Run(phase+"/"+string(kind), func(t *testing.T) {
				entered, aborted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				releaseRequest := sync.OnceFunc(func() { close(release) })
				var requests atomic.Int64
				blockAt := int64(1)
				if phase == "ramp" {
					blockAt = 2
				}
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					_, _ = io.Copy(io.Discard, request.Body)
					if requests.Add(1) == blockAt {
						close(entered)
						select {
						case <-request.Context().Done():
							close(aborted)
							return
						case <-release:
						}
					}
					writePerformanceCoreResponse(writer)
				}))
				defer server.Close()
				defer releaseRequest()
				repository := openHostRepository(t)
				core := newPerformanceCore(t, repository)
				host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
				submission := performanceCoreSubmission(t, server.URL, func(command *quicktest.PerformanceCommand) {
					switch phase {
					case "warmup":
						command.WarmupRequests = 3
						command.RampDurationMS, command.RampRequestCap = 20, 50
					case "ramp":
						command.WarmupRequests = 1
						command.RampDurationMS, command.RampRequestCap = 20, 50
					case "capacity":
						command.CapacityEnabled, command.Concurrency = true, 3
						command.CapacityStart, command.CapacityStep = 1, 1
						command.SLOE2EMS, command.SLOTargetPercent = 1000, 100
					}
				})
				spec := submission.Job
				if _, _, err := host.Submit(t.Context(), &submission); err != nil {
					t.Fatal(err)
				}
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("Performance phase did not send its request")
				}
				current, _ := repository.GetJob(t.Context(), spec.JobID)
				if _, _, err := host.ApplyCommand(t.Context(), jobcontract.Command{
					Contract: jobcontract.CurrentContract, CommandID: coreMeta(t).ID, JobID: spec.JobID,
					Kind: kind, ExpectedRevision: current.Receipt.Revision,
				}); err != nil {
					t.Fatal(err)
				}
				wantStatus := domain.RunCompleted
				if kind == jobcontract.CommandCancel {
					wantStatus = domain.RunCancelled
					select {
					case <-aborted:
					case <-time.After(3 * time.Second):
						t.Fatal("cancel did not abort the real request")
					}
				}
				releaseRequest()
				waitPerformanceJob(
					t,
					repository,
					spec.JobID,
					wantStatus,
				)
				waitSettled(t, repository)
				sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
				if err != nil || sealed.Result.ObservationState != jobcontract.PerformanceAvailable {
					t.Fatalf("controlled phase observation missing: %v", err)
				}
				checkpoint := sealed.Result.Observation.Checkpoint
				samePhase := string(checkpoint.Stage) == phase && checkpoint.Report.Progress.Stopped
				if !samePhase || requests.Load() != blockAt {
					t.Fatalf(
						"control advanced to another phase: stage=%s requests=%d",
						checkpoint.Stage,
						requests.Load(),
					)
				}
				if phase != "measured" {
					_, err := repository.GetQuickPerformanceReport(t.Context(), spec.Spec.Performance.ReportID)
					if !errors.Is(err, quicktest.ErrPerformanceArchiveNotFound) {
						t.Fatalf("incomplete phase became a statistical archive: %v", err)
					}
				}
			})
		}
	}
}

func TestPerformanceExecutorArchivesFailedProviderMeasurementsWithoutLeakingCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"message":"controlled failure with host-only-secret"}}`))
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newPerformanceCore(t, repository)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := performanceCoreSubmission(t, server.URL, nil)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitPerformanceJob(
		t,
		repository,
		spec.JobID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
	if err != nil || strings.Contains(string(sealed.Document), "host-only-secret") {
		t.Fatalf("failed result missing or leaked its credential: %v", err)
	}
	archive, err := repository.GetQuickPerformanceReport(t.Context(), spec.Spec.Performance.ReportID)
	if err != nil || archive.Success || archive.Metrics.Failed != 2 {
		t.Fatalf("provider failure measurements were discarded: %v", err)
	}
}

func TestPerformanceExecutorRemoteShutdownAndDeadlinePreserveOwnerFailure(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "shutdown"}[shutdown], func(t *testing.T) {
			entered, aborted := make(chan struct{}), make(chan struct{})
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				requests.Add(1)
				close(entered)
				<-request.Context().Done()
				close(aborted)
			}))
			defer server.Close()
			repository := openHostRepository(t)
			core := newPerformanceCore(t, repository)
			dependencies := hostDependencies(repository, core, &countedGuard{})
			if !shutdown {
				dependencies.Policy.ExecutionTimeout = 2 * time.Second
			}
			host := newTestHost(t, dependencies)
			defer func() { _ = host.Close() }()
			submission := performanceCoreSubmission(t, server.URL, nil)
			spec := submission.Job
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start before owner interruption")
			}
			want := domain.ErrorCode("job_execution_timeout")
			if shutdown {
				want = "runner_interrupted"
				if err := host.Close(); err != nil {
					t.Fatal(err)
				}
			}
			job := waitPerformanceJob(
				t,
				repository,
				spec.JobID,
				domain.RunFailed,
			)
			waitSettled(t, repository)
			select {
			case <-aborted:
			case <-time.After(3 * time.Second):
				t.Fatal("owner interruption did not abort the actual request")
			}
			sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
			if err != nil || job.Failure == nil || job.Failure.ErrorCode != want ||
				sealed.Result.Terminal.Failure.ErrorCode != want || requests.Load() != 1 {
				t.Fatalf("owner cause or actual observation was lost: error=%v", err)
			}
		})
	}
}

func TestPerformanceExecutorQueuedCancellationSealsWithoutTrafficOrStatisticalArchive(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	releaseRequest := sync.OnceFunc(func() { close(release) })
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			<-release
		}
		writePerformanceCoreResponse(writer)
	}))
	defer server.Close()
	defer releaseRequest()
	repository := openHostRepository(t)
	core := newPerformanceCore(t, repository)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	first := performanceCoreSubmission(t, server.URL, nil)
	firstID := first.Job.JobID
	if _, _, err := host.Submit(t.Context(), &first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first job did not occupy the execution slot")
	}
	second := performanceCoreSubmission(t, server.URL, nil)
	spec := second.Job
	queued, _, err := host.Submit(t.Context(), &second)
	if err != nil || queued.Receipt.Status != domain.RunQueued {
		t.Fatalf("second job was not queued: %v", err)
	}
	if _, _, err := host.ApplyCommand(t.Context(), jobcontract.Command{
		Contract: jobcontract.CurrentContract, CommandID: coreMeta(t).ID, JobID: spec.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: queued.Receipt.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	waitPerformanceJob(
		t,
		repository,
		spec.JobID,
		domain.RunCancelled,
	)
	var sealed jobhost.PerformanceResultDocument
	waitHost(t, func() bool {
		sealed, err = repository.ReadPerformanceResult(t.Context(), spec.JobID)
		return err == nil
	})
	if sealed.Result.ObservationState != jobcontract.PerformanceNotStarted ||
		sealed.Result.ExecutionStarted || sealed.Result.Observation != nil || requests.Load() != 1 {
		t.Fatal("queued cancellation started traffic or fabricated an execution observation")
	}
	_, err = repository.GetQuickPerformanceReport(t.Context(), spec.Spec.Performance.ReportID)
	if !errors.Is(err, quicktest.ErrPerformanceArchiveNotFound) {
		t.Fatal("unstarted performance job became a measured statistical archive")
	}
	releaseRequest()
	waitPerformanceJob(
		t,
		repository,
		firstID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	if requests.Load() != 2 {
		t.Fatal("cancelled queued job sent requests after the execution slot became available")
	}
}
