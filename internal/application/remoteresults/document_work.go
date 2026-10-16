package remoteresults

import (
	"context"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type DocumentClass string

const (
	DocumentFocus   DocumentClass = "focus"
	DocumentHistory DocumentClass = "history"
)

type DocumentCursor struct {
	JobID         string
	ReferenceType jobcontract.EventKind
	ReportID      string
}

func (cursor DocumentCursor) Validate() error {
	if cursor == (DocumentCursor{}) {
		return nil
	}
	validIDs := domain.IsUUID(cursor.JobID) && domain.IsUUID(cursor.ReportID)
	validKind := cursor.ReferenceType == jobcontract.EventReport ||
		cursor.ReferenceType == jobcontract.EventPerformanceResult
	if !validIDs || !validKind {
		return ErrInvalid
	}
	return nil
}

type DocumentQuery struct {
	Origin Origin
	Class  DocumentClass
	After  DocumentCursor
	Limit  uint32
}

func (query DocumentQuery) Validate() error {
	validClass := query.Class == DocumentFocus || query.Class == DocumentHistory
	validLimit := query.Limit > 0 && query.Limit <= 25
	validSource := query.Origin.Validate() == nil && query.After.Validate() == nil
	if !validClass || !validLimit || !validSource {
		return ErrInvalid
	}
	return nil
}

type DocumentWork struct {
	JobID    string
	Document DocumentReference
}

type DocumentPage struct {
	Items      []DocumentWork
	NextCursor DocumentCursor
	HasMore    bool
}

// WorkRepository returns scheduling hints. Import still authenticates the
// durable source, immutable reference, original bytes and typed document.
type WorkRepository interface {
	ListRemoteFactPriorityIDs(context.Context, Origin, string, uint32) (PriorityPage, error)
	ListRemoteDocumentWork(context.Context, DocumentQuery) (DocumentPage, error)
}
