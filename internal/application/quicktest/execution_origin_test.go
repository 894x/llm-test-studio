package quicktest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPerformanceCheckpointValidatesOptionalExecutionOrigin(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(t.Context(), checkpointTestCommand())
	if code != "" {
		t.Fatal(code)
	}
	defer lease.Close()
	for _, test := range []struct {
		name   string
		origin *domain.ExecutionOrigin
		valid  bool
	}{
		{name: "current unmanaged or unsealed checkpoint", valid: true},
		{name: "recorded executor", origin: performanceExecutionOrigin(), valid: true},
		{name: "partial executor", origin: &domain.ExecutionOrigin{Kind: "remote"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkpoint := PerformanceCheckpoint{
				Stage: PerformanceStagePreparing, Boundary: PerformanceBoundaryEntered, Report: prepared.report(),
			}
			checkpoint.Report.ExecutionOrigin = test.origin
			if (checkpoint.ValidateEnvelope() == nil) != test.valid || (checkpoint.Validate() == nil) != test.valid {
				t.Fatal("checkpoint origin validation differs from the current contract")
			}
			document, err := json.Marshal(checkpoint.Report)
			if err != nil {
				t.Fatal(err)
			}
			if test.origin == nil && !strings.Contains(string(document), `"execution_origin":null`) {
				t.Fatal("the current checkpoint omitted explicit null provenance")
			}
		})
	}
}

func TestArchivedPerformanceReportValidatesOptionalExecutionOrigin(t *testing.T) {
	report := archivedPhaseFiveReport(t)
	for _, origin := range []*domain.ExecutionOrigin{nil, performanceExecutionOrigin()} {
		report.ExecutionOrigin = origin
		if _, err := ValidateArchivedPerformanceReport(report); err != nil {
			t.Fatalf("current archive origin rejected: %v", err)
		}
	}
	report.ExecutionOrigin = &domain.ExecutionOrigin{Kind: "remote"}
	if _, err := ValidateArchivedPerformanceReport(report); err == nil {
		t.Fatal("archive accepted incomplete executor metadata")
	}
}

func TestPerformanceCheckpointTransitionFreezesRecordedOrigin(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(t.Context(), checkpointTestCommand())
	if code != "" {
		t.Fatal(code)
	}
	defer lease.Close()
	before := PerformanceCheckpoint{
		Stage: PerformanceStagePreparing, Boundary: PerformanceBoundaryEntered, Report: prepared.report(),
	}
	next := before
	next.Boundary = PerformanceBoundaryCompleted
	next.Report.RequestBudget, _, _ = preparePerformanceProfile(next.Report.Profile)
	before.Report.ExecutionOrigin = performanceExecutionOrigin()
	next.Report.ExecutionOrigin = performanceExecutionOrigin()
	if err := ValidatePerformanceCheckpointTransition(&before, next); err != nil {
		t.Fatalf("unchanged recorded origin rejected: %v", err)
	}
	next.Report.ExecutionOrigin.RunnerVersion = "upgraded-runner"
	if err := ValidatePerformanceCheckpointTransition(&before, next); err == nil {
		t.Fatal("a later runner upgrade rewrote recorded checkpoint provenance")
	}
	next.Report.ExecutionOrigin = nil
	if err := ValidatePerformanceCheckpointTransition(&before, next); err == nil {
		t.Fatal("transition erased recorded checkpoint provenance")
	}
}

func performanceExecutionOrigin() *domain.ExecutionOrigin {
	return &domain.ExecutionOrigin{
		Kind: "remote", NodeID: "40000000-0000-4000-8000-000000000091",
		InstallationID: "40000000-0000-4000-8000-000000000092", EngineBuildID: strings.Repeat("a", 64),
		RunnerVersion: "recorded-runner", OS: "linux", Arch: "arm64",
	}
}
