// Package reporting exposes the safe, presentation-neutral report read model
// used by overview and report adapters. Complete report documents deliberately
// remain behind the storage port.
package reporting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test/internal/domain"
)

const (
	CurrentSchemaVersion = 1
	// MaxSnapshotReports is the published latest-first report-list boundary.
	// Storage ports must never return more entries in one snapshot.
	MaxSnapshotReports = 100
)

var (
	ErrUnavailable  = errors.New("reporting service unavailable")
	ErrInconsistent = errors.New("reporting data is inconsistent")
)

// Catalog supplies one bounded storage read. ReportProjection contains only
// scalar values that the reporting application service is allowed to expose.
type Catalog interface {
	ListReportProjections(context.Context) ([]ReportProjection, error)
}

type ReportProjection struct {
	ID              string
	RunID           string
	GeneratedAt     time.Time
	RunStatus       domain.RunStatus
	PlanName        string
	ModelName       string
	ChannelName     string
	Passed          bool
	Verdict         string
	IssueCount      int64
	CaseCount       int64
	FailedCaseCount int64
	AttachmentCount int64
}

type Service struct {
	catalog Catalog
}

func New(catalog Catalog) Service {
	return Service{catalog: catalog}
}

type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Reports       []Summary `json:"reports"`
}

type Summary struct {
	ID              string           `json:"id"`
	RunID           string           `json:"run_id"`
	GeneratedAt     time.Time        `json:"generated_at"`
	RunStatus       domain.RunStatus `json:"run_status"`
	PlanName        string           `json:"plan_name"`
	ModelName       string           `json:"model_name"`
	ChannelName     string           `json:"channel_name"`
	Passed          bool             `json:"passed"`
	Verdict         string           `json:"verdict"`
	IssueCount      uint64           `json:"issue_count"`
	CaseCount       uint64           `json:"case_count"`
	FailedCaseCount uint64           `json:"failed_case_count"`
	AttachmentCount uint64           `json:"attachment_count"`
}

func (service Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if service.catalog == nil {
		return Snapshot{}, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	projections, err := service.catalog.ListReportProjections(ctx)
	if err != nil {
		return Snapshot{}, classifyPortError(ctx, err)
	}
	if len(projections) > MaxSnapshotReports {
		return Snapshot{}, classified(
			ErrInconsistent,
			fmt.Errorf("report catalog returned %d entries, limit is %d", len(projections), MaxSnapshotReports),
		)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	summaries := make([]Summary, 0, len(projections))
	reportIDs := make(map[string]struct{}, len(projections))
	runIDs := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := reportIDs[projection.ID]; duplicate {
			return Snapshot{}, classified(ErrInconsistent, fmt.Errorf("duplicate report id %q", projection.ID))
		}
		if _, duplicate := runIDs[projection.RunID]; duplicate {
			return Snapshot{}, classified(ErrInconsistent, fmt.Errorf("duplicate report run id %q", projection.RunID))
		}
		if err := projection.validate(); err != nil {
			return Snapshot{}, classified(ErrInconsistent, err)
		}
		reportIDs[projection.ID] = struct{}{}
		runIDs[projection.RunID] = struct{}{}
		summaries = append(summaries, projection.summary())
	}

	sort.Slice(summaries, func(left, right int) bool {
		if summaries[left].GeneratedAt.Equal(summaries[right].GeneratedAt) {
			return summaries[left].ID > summaries[right].ID
		}
		return summaries[left].GeneratedAt.After(summaries[right].GeneratedAt)
	})
	return Snapshot{SchemaVersion: CurrentSchemaVersion, Reports: summaries}, nil
}

func (projection ReportProjection) validate() error {
	if !domain.IsUUID(projection.ID) || !domain.IsUUID(projection.RunID) {
		return errors.New("report projection requires canonical report and run UUIDs")
	}
	if projection.GeneratedAt.IsZero() {
		return errors.New("report projection requires a generation timestamp")
	}
	_, offset := projection.GeneratedAt.Zone()
	if offset != 0 {
		return errors.New("report projection generation timestamp must be UTC")
	}
	if strings.TrimSpace(projection.PlanName) == "" || strings.TrimSpace(projection.ModelName) == "" ||
		strings.TrimSpace(projection.ChannelName) == "" || strings.TrimSpace(projection.Verdict) == "" {
		return errors.New("report projection requires plan, model, channel, and verdict labels")
	}
	switch projection.RunStatus {
	case domain.RunCompleted:
	case domain.RunFailed, domain.RunCancelled:
		if projection.Passed {
			return fmt.Errorf("%s run cannot have a passing report", projection.RunStatus)
		}
	default:
		return fmt.Errorf("report projection requires a terminal run status, got %q", projection.RunStatus)
	}
	if projection.IssueCount < 0 || projection.CaseCount < 0 || projection.FailedCaseCount < 0 || projection.AttachmentCount < 0 {
		return errors.New("report projection counts must be non-negative")
	}
	if projection.FailedCaseCount > projection.CaseCount {
		return errors.New("report projection failed case count exceeds case count")
	}
	if projection.Passed && projection.FailedCaseCount != 0 {
		return errors.New("passing report projection contains failed cases")
	}
	return nil
}

func (projection ReportProjection) summary() Summary {
	return Summary{
		ID: projection.ID, RunID: projection.RunID, GeneratedAt: projection.GeneratedAt,
		RunStatus: projection.RunStatus, PlanName: projection.PlanName,
		ModelName: projection.ModelName, ChannelName: projection.ChannelName,
		Passed: projection.Passed, Verdict: projection.Verdict,
		IssueCount: uint64(projection.IssueCount), CaseCount: uint64(projection.CaseCount),
		FailedCaseCount: uint64(projection.FailedCaseCount), AttachmentCount: uint64(projection.AttachmentCount),
	}
}

func classifyPortError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return classified(ErrUnavailable, err)
}

type classifiedError struct {
	kind  error
	cause error
}

func classified(kind, cause error) error {
	return classifiedError{kind: kind, cause: cause}
}

func (err classifiedError) Error() string {
	return err.kind.Error()
}

func (err classifiedError) Is(target error) bool {
	return errors.Is(err.kind, target) || errors.Is(err.cause, target)
}

func (err classifiedError) Unwrap() error {
	return err.cause
}
