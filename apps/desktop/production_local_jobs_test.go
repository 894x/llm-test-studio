package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func productionLocalPerformance(
	t *testing.T,
	dependencies desktopDependencies,
	endpoint string,
) (jobs.SubmissionRecord, *credentials.Lease) {
	t.Helper()
	preparer, ok := dependencies.performancePreparation.(*quicktest.Service)
	if !ok {
		t.Fatal("performance preparation is unavailable")
	}
	prepared, lease, code := preparer.BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: endpoint, APIKey: "local-owner-fixture-secret", ModelID: "local-fixture",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 10_000, InputTokens: 8, OutputTokens: 2,
	})
	if code != "" {
		t.Fatal(code)
	}
	t.Cleanup(func() { _ = lease.Close() })
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	record, err := dependencies.jobs.Prepare(t.Context(), jobs.PrepareCommand{
		Location: jobs.Location{Kind: jobcontract.TargetLocal}, JobID: meta.ID,
		Kind: jobcontract.KindPerformance, Payload: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return record, lease
}

func TestProductionLocalOwnerBlocksAnotherDesktopBeforeRecoveryAndKeepsIdentity(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	releaseProvider := sync.OnceFunc(func() { close(release) })
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		close(entered)
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	defer releaseProvider()
	root, executableRoot := t.TempDir(), t.TempDir()
	options := productionOptions{
		userConfigDir: func() (string, error) { return root, nil }, appVersion: "local-owner-test",
		executablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		credentialStore: credentials.NewMemoryStore(), protocolTransport: provider.Client().Transport,
	}
	first, err := newProductionInitializer(options)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	prepared, lease := productionLocalPerformance(t, first, provider.URL)
	info := prepared.Prepared.Runner
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH || info.RunnerVersion != options.appVersion {
		t.Fatal("production local metadata does not describe this process")
	}
	if info.Budget.MaxActiveJobs != 128 || info.Budget.MaxConcurrency != 65_536 {
		t.Fatal("production local executor inherited the remote single-job policy")
	}
	jobID := prepared.Prepared.Spec.JobID
	accepted, err := first.jobs.Submit(t.Context(), jobID,
		map[string]*credentials.Lease{prepared.Prepared.Spec.Spec.Performance.ReportID: lease})
	if err != nil || accepted.State != jobs.SubmissionAccepted {
		t.Fatalf("production local submission: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("production local Core did not issue its request")
	}
	second, err := newProductionInitializer(options)(t.Context())
	if err == nil {
		_ = second.close()
		t.Fatal("another desktop acquired the running database")
	}
	if !errors.Is(err, fileconfig.ErrLockBusy) {
		t.Fatalf("second desktop reached data startup instead of the ownership gate: %v", err)
	}
	database := filepath.Join(root, "llm-test-studio", "llm-test-studio.db")
	repository, err := sqlite.OpenRepository(t.Context(), database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	snapshot, err := repository.GetJob(t.Context(), jobID)
	if err != nil || snapshot.Receipt.Status != domain.RunRunning {
		t.Fatal("another desktop canceled or recovered this owner's running job")
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repository.GetJob(t.Context(), jobID)
	if err != nil || snapshot.Receipt.Status != domain.RunCancelled {
		t.Fatal("application owner did not cancel and settle local execution before releasing storage")
	}
	unsettled, err := repository.RecoveryJobs(t.Context())
	if err != nil || len(unsettled) != 0 {
		t.Fatal("owner released its lock before execution settlement")
	}
	options.appVersion = "local-owner-next-release"
	second, err = newProductionInitializer(options)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	next, _ := productionLocalPerformance(t, second, provider.URL)
	nextInfo := next.Prepared.Runner
	stableIdentity := nextInfo.NodeID == info.NodeID && nextInfo.NodeInstallationID == info.NodeInstallationID
	stableBuild := nextInfo.EngineBuildID == info.EngineBuildID && jobcontract.IsSHA256(nextInfo.EngineBuildID)
	if !stableIdentity || !stableBuild || nextInfo.RunnerVersion != options.appVersion {
		t.Fatal("reopening local execution changed the persistent owner or mixed version with build identity")
	}
	stored, err := repository.GetSubmission(t.Context(), jobID)
	if err != nil || stored.Prepared.Runner.RunnerVersion != info.RunnerVersion {
		t.Fatal("new startup rewrote the original execution metadata")
	}
}

func TestProductionStartupFailureReleasesOwnershipForRetry(t *testing.T) {
	root, executableRoot := t.TempDir(), t.TempDir()
	options := productionOptions{
		userConfigDir: func() (string, error) { return root, nil }, appVersion: "local-owner-test",
		executablePath:  func() (string, error) { return "", errors.New("fixture path failure") },
		credentialStore: credentials.NewMemoryStore(),
	}
	if dependencies, err := newProductionInitializer(options)(t.Context()); err == nil {
		_ = dependencies.close()
		t.Fatal("startup path failure was ignored")
	}
	options.executablePath = func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil }
	dependencies, err := newProductionInitializer(options)(t.Context())
	if err != nil {
		t.Fatalf("failed initialization retained execution ownership: %v", err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}
}
