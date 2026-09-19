package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
)

type recordingQuickPerformanceRunner struct {
	performanceCommand quicktest.PerformanceCommand
	performanceReport  quicktest.PerformanceReport
	err                error
	performanceCalls   int
	ctx                context.Context
	progress           []quicktest.PerformanceProgress
}

func (runner *recordingQuickPerformanceRunner) RunPerformance(ctx context.Context, command quicktest.PerformanceCommand) (quicktest.PerformanceReport, error) {
	runner.performanceCalls++
	runner.ctx = ctx
	runner.performanceCommand = command
	return runner.performanceReport, runner.err
}

func (runner *recordingQuickPerformanceRunner) RunPerformanceWithProgress(ctx context.Context, command quicktest.PerformanceCommand, onProgress func(quicktest.PerformanceProgress)) (quicktest.PerformanceReport, error) {
	runner.performanceCalls++
	runner.ctx = ctx
	runner.performanceCommand = command
	for _, progress := range runner.progress {
		onProgress(progress)
	}
	return runner.performanceReport, runner.err
}

func TestRunQuickPerformanceReturnsStableErrorsWithoutLeakingRunnerDetails(t *testing.T) {
	const sensitive = "sk-should-never-be-reported"
	tests := []struct {
		name string
		err  error
		want string
		leak string
	}{
		{name: "unknown runner error", err: errors.New(sensitive), want: desktopCodeOperationFailed, leak: sensitive},
		{name: "cancelled", err: context.Canceled, want: desktopCodeOperationCancelled},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: desktopCodeOperationCancelled},
		{name: "service unavailable", err: quicktest.ErrServiceUnavailable, want: desktopCodeQuickTestMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingQuickPerformanceRunner{err: test.err}
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{quickTests: runner}, nil
			})
			var reported error
			app.setErrorReporter(func(err error) { reported = err })
			app.onStartup(context.Background())

			_, err := app.RunQuickPerformanceTest(quicktest.PerformanceCommand{}, "")
			assertBindingErrorCode(t, err, test.want)
			if reported == nil {
				t.Fatal("expected the original failure to be reported internally")
			}
			if test.leak != "" && strings.Contains(reported.Error(), test.leak) {
				t.Fatalf("reported error leaked runner detail: %v", reported)
			}
		})
	}

	missing := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, nil
	})
	missing.onStartup(context.Background())
	_, err := missing.RunQuickPerformanceTest(quicktest.PerformanceCommand{}, "")
	assertBindingErrorCode(t, err, desktopCodeQuickTestMissing)
}

func TestRunQuickPerformanceTestDelegatesThroughLifecycleContext(t *testing.T) {
	command := quicktest.PerformanceCommand{
		AddressMode: quicktest.AddressModeBaseURL, URL: "https://api.example.test/v1",
		APIKey: "sk-ephemeral", ModelID: "upstream-model", RequestCount: 10,
		Concurrency: 2, TimeoutMS: 30_000, InputTokens: 100, OutputTokens: 100,
	}
	runner := &recordingQuickPerformanceRunner{performanceReport: quicktest.PerformanceReport{
		SchemaVersion: quicktest.PerformanceSchemaVersion, Success: true,
		AddressMode: quicktest.AddressModeBaseURL, Endpoint: "https://api.example.test/v1/chat/completions",
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{quickTests: runner}, nil
	})
	app.onStartup(context.Background())

	report, err := app.RunQuickPerformanceTest(command, "")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Endpoint != runner.performanceReport.Endpoint {
		t.Fatalf("report = %#v", report)
	}
	if runner.performanceCalls != 1 || runner.performanceCommand != command {
		t.Fatalf("runner calls = %d, command = %#v", runner.performanceCalls, runner.performanceCommand)
	}
	if runner.ctx == nil || runner.ctx != app.ctx {
		t.Fatal("quick performance test did not receive the desktop lifecycle context")
	}
}

func TestRunQuickPerformanceTestPublishesCorrelatedProgressEvents(t *testing.T) {
	const progressID = "88888888-8888-4888-8888-888888888888"
	runner := &recordingQuickPerformanceRunner{
		performanceReport: quicktest.PerformanceReport{SchemaVersion: quicktest.PerformanceSchemaVersion},
		progress: []quicktest.PerformanceProgress{
			{Phase: "sending", Planned: 4, Completed: 1, PeakInFlight: 2, Succeeded: 1},
			{Phase: "completed", Planned: 4, Completed: 4, PeakInFlight: 2, Succeeded: 4, TotalDurationMS: 320},
		},
	}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{quickTests: runner}, nil
	})
	type emittedEvent struct {
		name    string
		payload quickPerformanceProgressEvent
	}
	events := make([]emittedEvent, 0)
	app.setEventEmitter(func(_ context.Context, name string, data ...interface{}) {
		if len(data) != 1 {
			t.Fatalf("event data = %#v", data)
		}
		payload, ok := data[0].(quickPerformanceProgressEvent)
		if !ok {
			t.Fatalf("event payload = %#v", data[0])
		}
		events = append(events, emittedEvent{name: name, payload: payload})
	})
	app.onStartup(context.Background())

	if _, err := app.RunQuickPerformanceTest(quicktest.PerformanceCommand{}, progressID); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if event.name != quickPerformanceProgressEventName || event.payload.ProgressID != progressID {
			t.Fatalf("event = %#v", event)
		}
	}
	if events[0].payload.Progress.Completed != 1 || events[1].payload.Progress.Phase != "completed" {
		t.Fatalf("progress events = %#v", events)
	}
}
