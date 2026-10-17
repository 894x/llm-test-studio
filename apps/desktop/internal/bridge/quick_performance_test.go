package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type recordingPerformanceJobLauncher struct {
	command jobs.LaunchCommand
	err     error
	ctx     context.Context
	calls   int
}

func (launcher *recordingPerformanceJobLauncher) Launch(
	ctx context.Context,
	command jobs.LaunchCommand,
) (jobs.SubmissionRecord, error) {
	launcher.calls++
	launcher.ctx = ctx
	launcher.command = command
	return jobs.SubmissionRecord{}, launcher.err
}

func TestLaunchPerformancePreparationErrorsKeepSafePublicMeaning(t *testing.T) {
	const sensitive = "sk-should-never-be-reported"
	command := quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: "https://api.example.test/v1", APIKey: sensitive, ModelID: "upstream-model",
		RequestCount: 10, Concurrency: 2, TimeoutMS: 30_000, InputTokens: 100, OutputTokens: 100,
	}
	for _, test := range []struct {
		name string
		code domain.ErrorCode
		want string
	}{
		{name: "credential required", code: quicktest.ErrorCredentialRequired, want: desktopCodeQuickTaskCredential},
		{name: "invalid request", code: quicktest.ErrorInvalidRequest, want: desktopCodeJobInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := &recordingPerformanceJobLauncher{
				err: fmt.Errorf("%s: %w", sensitive, jobs.PreparationError{Code: test.code}),
			}
			app := New(func(context.Context) (Dependencies, error) {
				return Dependencies{Launcher: launcher}, nil
			})
			var reported error
			app.setErrorReporter(func(err error) { reported = err })
			type contextKey struct{}
			app.onStartup(context.WithValue(context.Background(), contextKey{}, "desktop"))

			view, err := app.LaunchJob(jobs.LaunchCommand{
				Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchPerformance,
				Performance: &command,
			})
			assertBindingErrorCode(t, err, test.want)
			if view.JobID != "" || strings.Contains(err.Error(), sensitive) {
				t.Fatal("preparation failure exposed an accepted identity or sensitive detail")
			}
			if reported == nil || strings.Contains(reported.Error(), sensitive) {
				t.Fatalf("preparation diagnostic did not preserve a safe reason: %v", reported)
			}
			if launcher.calls != 1 || launcher.command.Performance != &command {
				t.Fatal("performance command was changed or automatically retried")
			}
			if launcher.ctx == nil || launcher.ctx.Value(contextKey{}) != "desktop" {
				t.Fatal("performance preparation did not receive the desktop lifecycle context")
			}
			if errors.Is(err, launcher.err) {
				t.Fatal("public error wrapped the private preparation failure")
			}
		})
	}
}
