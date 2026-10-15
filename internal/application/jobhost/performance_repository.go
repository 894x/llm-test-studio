package jobhost

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type PerformanceObservationWrite struct {
	JobID            string
	ExpectedRevision uint64
	At               time.Time
	Checkpoint       quicktest.PerformanceCheckpoint
}

// Document is the stored canonical byte sequence. Its digest and download must
// use these bytes rather than re-encoding Result through another JSON writer.
type PerformanceResultDocument struct {
	Result    jobcontract.PerformanceResult
	Reference jobcontract.PerformanceResultReference
	Document  []byte
}

type PerformanceRepository interface {
	RecordPerformanceObservation(context.Context, PerformanceObservationWrite) (jobcontract.PerformanceObservation, error)
	GetPerformanceObservation(context.Context, string) (jobcontract.PerformanceObservation, error)
	SealPerformanceResult(context.Context, string, time.Time) (PerformanceResultDocument, error)
	ReadPerformanceResult(context.Context, string) (PerformanceResultDocument, error)
}
