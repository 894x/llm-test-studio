package remotesync

import (
	"context"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type DocumentRequest struct {
	Request  Request
	Document remoteresults.DocumentReference
}

type DocumentOutcome struct {
	Imported bool
}

// ImportDocument authenticates one persisted immutable reference before reading
// bytes. Candidate pages are hints, never authority to download another owner.
func (synchronizer *Synchronizer) ImportDocument(
	ctx context.Context,
	input DocumentRequest,
) (DocumentOutcome, error) {
	request := input.Request
	validIdentity := request.Origin.Validate() == nil && domain.IsUUID(request.JobID)
	validRequest := validIdentity && request.Remote != nil
	if ctx == nil || !validRequest {
		return DocumentOutcome{}, remoteresults.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return DocumentOutcome{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	mirror, err := synchronizer.repository.GetRemoteMirror(ctx, request.JobID)
	if err != nil {
		return DocumentOutcome{}, err
	}
	if mirror.Origin != request.Origin {
		return DocumentOutcome{}, remoteresults.ErrConflict
	}
	documents, err := synchronizer.repository.ListRemoteDocuments(ctx, request.JobID)
	if err != nil {
		return DocumentOutcome{}, err
	}
	index := slices.IndexFunc(documents, func(document remoteresults.DocumentReference) bool {
		return document.Kind == input.Document.Kind && document.Reference == input.Document.Reference
	})
	if index < 0 {
		return DocumentOutcome{}, remoteresults.ErrConflict
	}
	document := documents[index]
	if document.Imported {
		return DocumentOutcome{Imported: true}, nil
	}
	archive := document.Kind == jobcontract.EventReport && document.Reference.Kind == jobcontract.KindPerformance
	if archive {
		if err := synchronizer.requirePerformanceResult(ctx, mirror, document, documents); err != nil {
			return DocumentOutcome{}, err
		}
	}
	info, err := request.Remote.Probe(ctx)
	if err != nil {
		return DocumentOutcome{}, err
	}
	validSource := info.NodeID == request.Origin.NodeID && info.NodeInstallationID == request.Origin.InstallationID
	if !validSource {
		return DocumentOutcome{}, remoteresults.ErrConflict
	}
	if err := synchronizer.importDocument(ctx, request, document); err != nil {
		return DocumentOutcome{}, err
	}
	return DocumentOutcome{Imported: true}, nil
}

func (synchronizer *Synchronizer) requirePerformanceResult(
	ctx context.Context,
	mirror remoteresults.Mirror,
	archive remoteresults.DocumentReference,
	documents []remoteresults.DocumentReference,
) error {
	imported := slices.ContainsFunc(documents, func(document remoteresults.DocumentReference) bool {
		return document.Kind == jobcontract.EventPerformanceResult && document.Imported &&
			document.Reference.ID == archive.Reference.ID && document.Reference.Kind == jobcontract.KindPerformance
	})
	if !imported {
		return remoteresults.ErrNotFound
	}
	result, err := synchronizer.repository.ReadRemotePerformanceResult(ctx, mirror.Snapshot.Receipt.JobID)
	if err != nil {
		return err
	}
	validOwner := result.JobID == mirror.Snapshot.Receipt.JobID && result.ReportID == archive.Reference.ID
	receipt, current := result.Terminal.Receipt, mirror.Snapshot.Receipt
	validAcceptance := receipt.Origin == current.Origin && receipt.AcceptedAt.Equal(current.AcceptedAt)
	validChildren := slices.Equal(receipt.RunIDs, current.RunIDs) && slices.Equal(receipt.ReportIDs, current.ReportIDs)
	validTerminal := result.Terminal.Validate(mirror.Spec) == nil && validAcceptance && validChildren
	validResult := validOwner && validTerminal && result.Validate() == nil
	if !validResult {
		return remoteresults.ErrConflict
	}
	return nil
}
