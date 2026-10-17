package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const desktopJobID = "83000000-0000-4000-8000-000000000099"

type recordingJobLauncher struct {
	record  jobs.SubmissionRecord
	err     error
	calls   int
	command jobs.LaunchCommand
	ctx     context.Context
	launch  func(context.Context) (jobs.SubmissionRecord, error)
}

func (launcher *recordingJobLauncher) Launch(ctx context.Context, command jobs.LaunchCommand) (jobs.SubmissionRecord, error) {
	launcher.calls++
	launcher.ctx, launcher.command = ctx, command
	if launcher.launch != nil {
		return launcher.launch(ctx)
	}
	return launcher.record, launcher.err
}

func desktopLaunchFixture() jobs.LaunchCommand {
	return jobs.LaunchCommand{Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchPlan,
		Plan: &runs.StartCommand{PlanID: desktopJobID, ModelID: desktopJobID, ChannelID: desktopJobID}}
}

func desktopSubmissionFixture(t *testing.T) jobs.SubmissionRecord {
	t.Helper()
	root, executableRoot := t.TempDir(), t.TempDir()
	dependencies, err := NewInitializer(Options{
		UserConfigDir: func() (string, error) { return root, nil }, AppVersion: "desktop-job-test",
		ExecutablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		CredentialStore: credentials.NewMemoryStore(),
	})(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dependencies.Close(); err != nil {
			t.Error(err)
		}
	})
	record, _ := productionLocalPerformance(t, dependencies, "https://example.invalid/v1")
	record.State, record.Revision = jobs.SubmissionPending, 2
	record.Observation = jobs.ObservationUnreachable
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	return record
}
