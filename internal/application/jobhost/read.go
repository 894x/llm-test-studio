package jobhost

import (
	"math"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type JobListQuery struct {
	InstallationID string
	Target         jobcontract.ExecutionTarget
	After          uint64
	Limit          uint32
}

func (query JobListQuery) Validate() error {
	validIdentity := domain.IsUUID(query.InstallationID) && query.Target.Validate() == nil
	validPage := query.After <= math.MaxInt64 && query.Limit >= 1 && query.Limit <= 200
	if !validIdentity || !validPage {
		return ErrInvalid
	}
	return nil
}

type JobPage struct {
	Contract   int                    `json:"contract"`
	Jobs       []jobcontract.Snapshot `json:"jobs"`
	NextCursor string                 `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
}

type RunReportDocument struct {
	Report    domain.Report
	Reference jobcontract.ReportReference
	Document  []byte
}
