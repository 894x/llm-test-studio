// Package remotesync imports committed node facts without owning execution.
package remotesync

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

const maxPagesPerObservation = 20

type Remote interface {
	Probe(context.Context) (jobcontract.RunnerInfo, error)
	ReadEvents(context.Context, runnerclient.EventQuery) (jobhost.EventPage, error)
	DownloadReport(context.Context, string, jobcontract.ReportReference, io.ReadWriteSeeker) error
	DownloadPerformanceArchive(context.Context, string, jobcontract.ReportReference, io.ReadWriteSeeker) error
}

type DocumentCache interface {
	Read(context.Context, jobcontract.ReportReference, func(context.Context, io.ReadWriteSeeker) error) ([]byte, error)
}

type Dependencies struct {
	Repository remoteresults.Repository
	Documents  DocumentCache
}

type Synchronizer struct {
	repository remoteresults.Repository
	documents  DocumentCache
}

type Request struct {
	Origin remoteresults.Origin
	JobID  string
	Remote Remote
}

type Outcome struct {
	Mirror remoteresults.Mirror
	// MoreEvents describes the observed page tail, not execution settlement or
	// a final artifact manifest. A later pass may discover newly committed facts.
	MoreEvents bool
}

func New(dependencies Dependencies) (*Synchronizer, error) {
	if dependencies.Repository == nil || dependencies.Documents == nil {
		return nil, remoteresults.ErrInvalid
	}
	return &Synchronizer{repository: dependencies.Repository, documents: dependencies.Documents}, nil
}

// Observe imports bounded event pages without waiting for any document.
// Cancellation stops observation only; committed per-job cursors remain durable.
func (synchronizer *Synchronizer) Observe(ctx context.Context, request Request) (Outcome, error) {
	validIdentity := request.Origin.Validate() == nil && domain.IsUUID(request.JobID)
	if ctx == nil || !validIdentity || request.Remote == nil {
		return Outcome{}, remoteresults.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	mirror, err := synchronizer.repository.GetRemoteMirror(ctx, request.JobID)
	if err != nil && !errors.Is(err, remoteresults.ErrNotFound) {
		return Outcome{}, err
	}
	if err == nil && mirror.Origin != request.Origin {
		return Outcome{}, remoteresults.ErrConflict
	}
	outcome := Outcome{Mirror: mirror}
	info, err := request.Remote.Probe(ctx)
	if err != nil {
		return outcome, err
	}
	validSource := info.NodeID == request.Origin.NodeID && info.NodeInstallationID == request.Origin.InstallationID
	if !validSource {
		return outcome, remoteresults.ErrConflict
	}
	for range maxPagesPerObservation {
		after := outcome.Mirror.Snapshot.LatestSequence
		page, err := request.Remote.ReadEvents(ctx, runnerclient.EventQuery{
			JobID: request.JobID, AfterSequence: after, Limit: 200,
		})
		if err != nil {
			return outcome, err
		}
		validPage := page.LastSequence >= after && page.LastSequence-after == uint64(len(page.Events))
		if !validPage || (page.HasMore && len(page.Events) == 0) {
			return outcome, runnerclient.ErrIntegrity
		}
		mirror, err := synchronizer.repository.ApplyRemoteBatch(ctx, remoteresults.Batch{
			Origin: request.Origin, JobID: request.JobID, AfterSequence: after, Events: page.Events,
		})
		if err != nil {
			return outcome, err
		}
		outcome.Mirror, outcome.MoreEvents = mirror, page.HasMore
		if !page.HasMore {
			break
		}
	}
	return outcome, nil
}

func (synchronizer *Synchronizer) importDocument(
	ctx context.Context,
	request Request,
	document remoteresults.DocumentReference,
) error {
	download := request.Remote.DownloadReport
	archive := document.Kind == jobcontract.EventReport && document.Reference.Kind == jobcontract.KindPerformance
	if archive {
		download = request.Remote.DownloadPerformanceArchive
	}
	raw, err := synchronizer.documents.Read(ctx, document.Reference, func(ctx context.Context, file io.ReadWriteSeeker) error {
		return download(
			ctx,
			request.JobID,
			document.Reference,
			file,
		)
	})
	if err != nil {
		return err
	}
	if document.Reference.Kind == jobcontract.KindRun {
		return synchronizer.repository.ImportRemoteRunReport(ctx, remoteresults.RunReportImport{
			Origin: request.Origin, JobID: request.JobID, Document: raw,
		})
	}
	return synchronizer.repository.ImportRemotePerformanceDocument(ctx, remoteresults.PerformanceDocumentImport{
		Origin: request.Origin, JobID: request.JobID, Kind: document.Kind, Document: raw,
	})
}
