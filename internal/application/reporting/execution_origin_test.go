package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestGeneratorPreservesExecutionOriginIndependentlyOfPreparedEnvironment(t *testing.T) {
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	origin := reportExecutionOrigin()
	repository := &fakeReportRepository{
		run: run, origin: &origin, results: results, evidence: []domain.Evidence{},
	}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	report := repository.report
	if report.ExecutionOrigin == nil || *report.ExecutionOrigin != origin {
		t.Fatalf("report execution origin = %+v", report.ExecutionOrigin)
	}
	if report.Environment != run.Snapshot().Environment {
		t.Fatal("executor metadata replaced the prepared environment")
	}
	origin.RunnerVersion = "upgraded-runner"
	if report.ExecutionOrigin.RunnerVersion != "recorded-runner" {
		t.Fatal("a later runner upgrade changed the historical report")
	}
}

func TestGeneratorRetainsExplicitNullOriginForUnmanagedRuns(t *testing.T) {
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	repository := &fakeReportRepository{run: run, results: results, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	if repository.report.ExecutionOrigin != nil {
		t.Fatal("an unmanaged run acquired fabricated executor metadata")
	}
	document, err := json.Marshal(repository.report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(document), `"execution_origin":null`) {
		t.Fatal("the current unmanaged report omitted its execution origin field")
	}
}

func TestGeneratorRejectsExecutionOriginReadAndValidationErrorsBeforePersistence(t *testing.T) {
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	readErr := errors.New("stored executor receipt cannot be read")
	for _, test := range []struct {
		name   string
		origin *domain.ExecutionOrigin
		err    error
	}{
		{name: "read error", err: readErr},
		{name: "invalid origin", origin: &domain.ExecutionOrigin{Kind: "remote"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeReportRepository{
				run: run, origin: test.origin, originErr: test.err, results: results, evidence: []domain.Evidence{},
			}
			generator, err := NewGenerator(GeneratorDependencies{
				Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = generator.Generate(context.Background(), run.Meta().ID)
			if err == nil || (test.err != nil && !errors.Is(err, test.err)) {
				t.Fatalf("Generate() error = %v", err)
			}
			if repository.report.ID != "" {
				t.Fatal("invalid executor provenance was persisted")
			}
		})
	}
}

func reportExecutionOrigin() domain.ExecutionOrigin {
	return domain.ExecutionOrigin{
		Kind: "remote", NodeID: "40000000-0000-4000-8000-000000000091",
		InstallationID: "40000000-0000-4000-8000-000000000092", EngineBuildID: strings.Repeat("a", 64),
		RunnerVersion: "recorded-runner", OS: "linux", Arch: "arm64",
	}
}
