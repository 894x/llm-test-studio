// Package jobhost owns complete-job admission, execution and durable control.
package jobhost

import (
	"context"
	"errors"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrNotFound = errors.New("job_not_found")
	ErrConflict = errors.New("job_conflict")
	ErrInvalid  = errors.New("invalid_job")
)

// Acceptance contains a keyed commitment, never task credential bytes.
type Acceptance struct {
	Spec             jobcontract.JobSpec
	Origin           domain.ExecutionOrigin
	SecretCommitment string
	AcceptedAt       time.Time
}

type Change struct {
	JobID            string
	ExpectedRevision uint64
	Status           domain.RunStatus
	Failure          *domain.RunFailure
	At               time.Time
}

type EventPage struct {
	Events       []jobcontract.Event `json:"events"`
	LastSequence uint64              `json:"last_sequence"`
	HasMore      bool                `json:"has_more"`
}

type Repository interface {
	AcceptJob(context.Context, Acceptance) (jobcontract.Snapshot, bool, error)
	LookupJob(context.Context, Acceptance) (jobcontract.Snapshot, error)
	GetJob(context.Context, string) (jobcontract.Snapshot, error)
	GetJobSpec(context.Context, string) (jobcontract.JobSpec, error)
	ChangeJob(context.Context, Change) (jobcontract.Snapshot, error)
	ApplyJobCommand(context.Context, jobcontract.Command, time.Time) (jobcontract.CommandConfirmation, bool, error)
	GetJobCommand(context.Context, string) (jobcontract.CommandConfirmation, error)
	JobEvents(context.Context, string, uint64, uint32) (EventPage, error)
	ActiveJobs(context.Context) ([]jobcontract.Snapshot, error)
	RecoveryJobs(context.Context) ([]jobcontract.Snapshot, error)
	RecordJobProgress(context.Context, string, quicktest.PerformanceProgress, time.Time) error
	MarkJobSettled(context.Context, string) error
}
