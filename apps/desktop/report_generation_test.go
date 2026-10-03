package main

import (
	"context"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/reporting"
)

type testGenerationSource struct {
	observer func(reporting.GenerationProgress)
	progress reporting.GenerationProgress
}

func (source *testGenerationSource) GenerationSnapshot() reporting.GenerationSnapshot {
	return reporting.GenerationSnapshot{SchemaVersion: 1, Runs: []reporting.GenerationProgress{source.progress}}
}
func (source *testGenerationSource) SubscribeProgress(observer func(reporting.GenerationProgress)) func() {
	source.observer = observer
	return func() { source.observer = nil }
}

func TestReportGenerationBindingPublishesProgressAndUnsubscribesOnShutdown(t *testing.T) {
	source := &testGenerationSource{progress: reporting.GenerationProgress{
		Sequence: 1, RunID: "88888888-8888-4888-8888-888888888888", Phase: "loading_results",
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{reportGeneration: source}, nil
	})
	events := []reporting.GenerationProgress{}
	app.setEventEmitter(func(_ context.Context, name string, data ...interface{}) {
		if name != "report-generation-progress" || len(data) != 1 {
			t.Fatal("invalid event")
		}
		events = append(events, data[0].(reporting.GenerationProgress))
	})
	app.onStartup(context.Background())
	snapshot, err := app.GetReportGeneration()
	if err != nil || len(snapshot.Runs) != 1 || snapshot.Runs[0].RunID != source.progress.RunID {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	source.observer(source.progress)
	if len(events) != 1 || events[0].Phase != "loading_results" {
		t.Fatal("progress was not emitted")
	}
	if err := app.shutdown(); err != nil {
		t.Fatal(err)
	}
	if source.observer != nil {
		t.Fatal("shutdown retained progress subscription")
	}
}
