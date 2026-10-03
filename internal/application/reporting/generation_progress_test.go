package reporting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestGenerationProgressTracksWorkAndBecomesReadyOnlyAfterPersistence(t *testing.T) {
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	repository := &fakeReportRepository{run: run, results: results, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	progress := []GenerationProgress{}
	unsubscribe := generator.SubscribeProgress(func(item GenerationProgress) {
		if item.Phase == "ready" && (repository.report.ID == "" || repository.report.ID != item.ReportID) {
			t.Error("report announced before durable persistence")
		}
		progress = append(progress, item)
	})
	if err := generator.Generate(context.Background(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	if len(progress) < 6 || progress[0].Phase != "loading_run" || progress[len(progress)-1].Phase != "ready" {
		t.Fatalf("generation phases = %+v", progress)
	}
	for index, item := range progress {
		if item.RunID != run.Meta().ID || item.Processed > item.Total || (index > 0 && item.Sequence <= progress[index-1].Sequence) {
			t.Fatalf("invalid correlated progress = %+v", item)
		}
	}
	snapshot := generator.GenerationSnapshot()
	snapshot.Runs[0].Phase = "failed"
	if generator.GenerationSnapshot().Runs[0].Phase != "ready" {
		t.Fatal("snapshot mutation leaked into generator")
	}
	unsubscribe()
	generator.publishProgress(GenerationProgress{RunID: run.Meta().ID, Phase: "failed"})
	if progress[len(progress)-1].Phase != "ready" {
		t.Fatal("unsubscribed observer received progress")
	}
}

type failingGenerationRepository struct{ *fakeReportRepository }

func (repository failingGenerationRepository) CreateReport(context.Context, domain.Report) error {
	return errors.New("private storage failure")
}

func TestGenerationPersistenceFailureAndCancellationPublishFailedState(t *testing.T) {
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	run, results := multiSuiteReportFixture(t, now)
	for _, cancelled := range []bool{false, true} {
		repository := failingGenerationRepository{&fakeReportRepository{run: run, results: results, evidence: []domain.Evidence{}}}
		generator, err := NewGenerator(GeneratorDependencies{Repository: repository, Clock: fixedReportClock{now: now.Add(time.Minute)}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		err = generator.Generate(ctx, run.Meta().ID)
		cancel()
		if err == nil {
			t.Fatal("generation succeeded")
		}
		items := generator.GenerationSnapshot().Runs
		if len(items) != 1 || items[0].Phase != "failed" || items[0].ReportID != "" {
			t.Fatalf("failed state = %+v", items)
		}
	}
}
