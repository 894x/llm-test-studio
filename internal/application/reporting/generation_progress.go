package reporting

import (
	"sort"
	"time"
)

// GenerationProgress describes real work; an unknown total is zero and must
// be presented as an indeterminate phase, never as an estimated percentage.
type GenerationProgress struct {
	Sequence  uint64 `json:"sequence"`
	RunID     string `json:"run_id"`
	Phase     string `json:"phase"`
	Processed uint64 `json:"processed"`
	Total     uint64 `json:"total"`
	ElapsedMS uint64 `json:"elapsed_ms"`
	ReportID  string `json:"report_id,omitempty"`
}

type GenerationSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	Runs          []GenerationProgress `json:"runs"`
}

func (generator *Generator) GenerationSnapshot() GenerationSnapshot {
	generator.progressMu.Lock()
	defer generator.progressMu.Unlock()
	items := make([]GenerationProgress, 0, len(generator.progress))
	for _, item := range generator.progress {
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool { return items[left].RunID < items[right].RunID })
	return GenerationSnapshot{SchemaVersion: 1, Runs: items}
}

func (generator *Generator) SubscribeProgress(observer func(GenerationProgress)) func() {
	generator.progressMu.Lock()
	generator.nextObserver++
	id := generator.nextObserver
	generator.observers[id] = observer
	generator.progressMu.Unlock()
	return func() {
		generator.progressMu.Lock()
		delete(generator.observers, id)
		generator.progressMu.Unlock()
	}
}

func (generator *Generator) publishProgress(progress GenerationProgress) {
	generator.progressMu.Lock()
	generator.nextProgress++
	progress.Sequence = generator.nextProgress
	if _, exists := generator.progress[progress.RunID]; !exists {
		generator.progressOrder = append(generator.progressOrder, progress.RunID)
	}
	generator.progress[progress.RunID] = progress
	for len(generator.progress) > MaxSnapshotReports {
		removed := false
		for index, id := range generator.progressOrder {
			phase := generator.progress[id].Phase
			if phase == "ready" || phase == "failed" {
				delete(generator.progress, id)
				generator.progressOrder = append(generator.progressOrder[:index], generator.progressOrder[index+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
	observers := make([]func(GenerationProgress), 0, len(generator.observers))
	for _, observer := range generator.observers {
		if observer != nil {
			observers = append(observers, observer)
		}
	}
	generator.progressMu.Unlock()
	for _, observer := range observers {
		observer(progress)
	}
}

type GenerationTiming struct {
	RunID    string
	Phase    string
	Duration time.Duration
}
