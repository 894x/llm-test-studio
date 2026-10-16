package jobhost

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// Each port call owns its I/O deadline. Workflow and reconciliation callers
// retain their parent context, so a completed call cannot spend the next one's
// allowance or leave a readback holding an already expired operation context.
func storageRead[T any](ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	operation, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	return read(operation)
}

func storageWrite(ctx context.Context, write func(context.Context) error) error {
	operation, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	return write(operation)
}

type runStorage struct{ RunRepository }

var _ RunRepository = runStorage{}

func (repository runStorage) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return storageRead(ctx, func(operation context.Context) (domain.Run, error) {
		return repository.RunRepository.GetRun(operation, id)
	})
}

func (repository runStorage) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	return storageWrite(ctx, func(operation context.Context) error {
		return repository.RunRepository.UpdateRun(operation, revision, run)
	})
}

func (repository runStorage) ListReportsForRuns(ctx context.Context, ids []string) ([]domain.Report, error) {
	return storageRead(ctx, func(operation context.Context) ([]domain.Report, error) {
		return repository.RunRepository.ListReportsForRuns(operation, ids)
	})
}

func (repository runStorage) RunExecutionOrigin(ctx context.Context, id string) (*domain.ExecutionOrigin, error) {
	return storageRead(ctx, func(operation context.Context) (*domain.ExecutionOrigin, error) {
		return repository.RunRepository.RunExecutionOrigin(operation, id)
	})
}

func (repository runStorage) ListResults(ctx context.Context, id string) ([]domain.Result, error) {
	return storageRead(ctx, func(operation context.Context) ([]domain.Result, error) {
		return repository.RunRepository.ListResults(operation, id)
	})
}

func (repository runStorage) ListEvidence(ctx context.Context, id string) ([]domain.Evidence, error) {
	return storageRead(ctx, func(operation context.Context) ([]domain.Evidence, error) {
		return repository.RunRepository.ListEvidence(operation, id)
	})
}

func (repository runStorage) CreateReport(ctx context.Context, report domain.Report) error {
	return storageWrite(ctx, func(operation context.Context) error {
		return repository.RunRepository.CreateReport(operation, report)
	})
}

func (repository runStorage) GetComparison(ctx context.Context, id string) (domain.Comparison, error) {
	return storageRead(ctx, func(operation context.Context) (domain.Comparison, error) {
		return repository.RunRepository.GetComparison(operation, id)
	})
}

func (repository runStorage) IsLocalComparison(ctx context.Context, id string) (bool, error) {
	return storageRead(ctx, func(operation context.Context) (bool, error) {
		return repository.RunRepository.IsLocalComparison(operation, id)
	})
}

func (repository runStorage) UpdateComparison(ctx context.Context, revision uint64, comparison domain.Comparison) error {
	return storageWrite(ctx, func(operation context.Context) error {
		return repository.RunRepository.UpdateComparison(operation, revision, comparison)
	})
}

type performanceStorage struct{ PerformanceExecutionRepository }

var _ PerformanceExecutionRepository = performanceStorage{}

func (repository performanceStorage) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	return storageRead(ctx, func(operation context.Context) (jobcontract.Snapshot, error) {
		return repository.PerformanceExecutionRepository.GetJob(operation, id)
	})
}

func (repository performanceStorage) GetJobSpec(ctx context.Context, id string) (jobcontract.JobSpec, error) {
	return storageRead(ctx, func(operation context.Context) (jobcontract.JobSpec, error) {
		return repository.PerformanceExecutionRepository.GetJobSpec(operation, id)
	})
}

func (repository performanceStorage) RecordPerformanceObservation(ctx context.Context, input PerformanceObservationWrite) (jobcontract.PerformanceObservation, error) {
	return storageRead(ctx, func(operation context.Context) (jobcontract.PerformanceObservation, error) {
		return repository.PerformanceExecutionRepository.RecordPerformanceObservation(operation, input)
	})
}

func (repository performanceStorage) GetPerformanceObservation(ctx context.Context, id string) (jobcontract.PerformanceObservation, error) {
	return storageRead(ctx, func(operation context.Context) (jobcontract.PerformanceObservation, error) {
		return repository.PerformanceExecutionRepository.GetPerformanceObservation(operation, id)
	})
}

func (repository performanceStorage) SealPerformanceResult(ctx context.Context, id string, at time.Time) (PerformanceResultDocument, error) {
	return storageRead(ctx, func(operation context.Context) (PerformanceResultDocument, error) {
		return repository.PerformanceExecutionRepository.SealPerformanceResult(operation, id, at)
	})
}

func (repository performanceStorage) ReadPerformanceResult(ctx context.Context, id string) (PerformanceResultDocument, error) {
	return storageRead(ctx, func(operation context.Context) (PerformanceResultDocument, error) {
		return repository.PerformanceExecutionRepository.ReadPerformanceResult(operation, id)
	})
}

func (repository performanceStorage) GetQuickPerformanceReport(ctx context.Context, id string) (quicktest.PerformanceReport, error) {
	return storageRead(ctx, func(operation context.Context) (quicktest.PerformanceReport, error) {
		return repository.PerformanceExecutionRepository.GetQuickPerformanceReport(operation, id)
	})
}

func (repository performanceStorage) SaveQuickPerformanceReport(ctx context.Context, report quicktest.PerformanceReport) error {
	return storageWrite(ctx, func(operation context.Context) error {
		return repository.PerformanceExecutionRepository.SaveQuickPerformanceReport(operation, report)
	})
}
