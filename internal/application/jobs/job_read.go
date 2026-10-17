package jobs

import (
	"context"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// JobRead contains facts read from one storage snapshot. A missing Submission
// identifies a discovered remote job, never an inferred local delivery record.
type JobRead struct {
	Spec       jobcontract.JobSpec
	Location   Location
	Submission *SubmissionRecord
	Execution  *jobcontract.Snapshot
}

// JobReader resolves immutable job input and actual execution without network
// activity. Control creation must revalidate these facts in its write transaction.
type JobReader interface {
	ReadJob(context.Context, string) (JobRead, error)
}

func (read JobRead) Validate() error {
	if read.Spec.Validate() != nil || read.Location.Validate() != nil || read.Spec.Target.Kind != read.Location.Kind {
		return ErrInvalid
	}
	if record := read.Submission; record != nil {
		if record.Validate() != nil || recordLocation(*record) != read.Location {
			return ErrInvalid
		}
		digest, err := read.Spec.Digest()
		if err != nil || digest != record.SpecSHA256 {
			return ErrConflict
		}
		if record.State == SubmissionPrepared && read.Execution != nil {
			return ErrConflict
		}
	} else if read.Location.Kind != jobcontract.TargetRemote || read.Execution == nil {
		return ErrInvalid
	}
	if read.Execution == nil {
		return nil
	}
	if read.Execution.Validate(read.Spec) != nil {
		return ErrConflict
	}
	if record := read.Submission; record != nil && record.Receipt != nil {
		first, next := *record.Receipt, read.Execution.Receipt
		if next.Revision < first.Revision && read.Location.Kind == jobcontract.TargetRemote {
			first, next = next, first
		}
		if !confirmedExecutionMatches(first, next) {
			return ErrConflict
		}
	}
	return nil
}

func (read JobRead) acceptanceReceipt() *jobcontract.Receipt {
	if read.Execution != nil {
		return &read.Execution.Receipt
	}
	if read.Submission != nil {
		return read.Submission.Receipt
	}
	return nil
}
