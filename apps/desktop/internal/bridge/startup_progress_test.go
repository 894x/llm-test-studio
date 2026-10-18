package bridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartupProgressDoesNotWaitForInitializationAndRetainsFailedStage(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	app := New(func(ctx context.Context) (Dependencies, error) {
		ReportStartupProgress(ctx, "database_backing_up", 1, 2)
		close(entered)
		<-release
		ReportStartupProgress(ctx, "database_validating", 1, 2)
		return Dependencies{}, errors.New("private storage failure")
	})
	go func() { app.onStartup(t.Context()); close(done) }()
	<-entered
	progressRead := make(chan StartupProgress, 1)
	go func() { progressRead <- app.GetStartupProgress() }()
	select {
	case progress := <-progressRead:
		if progress.Status != "running" || progress.Stage != "database_backing_up" || progress.FromVersion != 1 {
			t.Fatalf("progress = %+v", progress)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("progress query waited for initialization")
	}
	close(release)
	<-done
	progress := app.GetStartupProgress()
	if progress.Status != "failed" || progress.Stage != "database_validating" {
		t.Fatalf("failed startup lost its last stage: %+v", progress)
	}
	_ = app.shutdown()
}
