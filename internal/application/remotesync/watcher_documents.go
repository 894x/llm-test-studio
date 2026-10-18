package remotesync

import (
	"cmp"
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type documentScan struct {
	cursor  remoteresults.DocumentCursor
	pending []remoteresults.DocumentWork
	index   int
	end     bool
}

func documentClass(index int) remoteresults.DocumentClass {
	if index == 0 {
		return remoteresults.DocumentFocus
	}
	return remoteresults.DocumentHistory
}

func documentCursor(work remoteresults.DocumentWork) remoteresults.DocumentCursor {
	return remoteresults.DocumentCursor{
		JobID: work.JobID, ReferenceType: work.Document.Kind, ReportID: work.Document.Reference.ID,
	}
}

func compareDocumentCursor(left, right remoteresults.DocumentCursor) int {
	if order := cmp.Compare(left.JobID, right.JobID); order != 0 {
		return order
	}
	if order := cmp.Compare(left.ReferenceType, right.ReferenceType); order != 0 {
		return order
	}
	return cmp.Compare(left.ReportID, right.ReportID)
}

func (watcher *Watcher) documentPage(
	ctx context.Context,
	query remoteresults.DocumentQuery,
) (remoteresults.DocumentPage, error) {
	if query.Validate() != nil {
		return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
	}
	release, err := watcher.acquireFacts(ctx, priorityLane)
	if err != nil {
		return remoteresults.DocumentPage{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	page, err := watcher.documentWork(ctx, query)
	if err != nil {
		return remoteresults.DocumentPage{}, err
	}
	if len(page.Items) > int(query.Limit) {
		return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
	}
	if page.HasMore && len(page.Items) == 0 {
		return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
	}
	previous := query.After
	for _, work := range page.Items {
		cursor := documentCursor(work)
		validReference := jobcontract.IsSHA256(work.Document.Reference.SHA256) && work.Document.Reference.Size > 0
		validWork := cursor.Validate() == nil && !work.Document.Imported && validReference
		if !validWork {
			return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
		}
		if compareDocumentCursor(cursor, previous) <= 0 {
			return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
		}
		previous = cursor
	}
	if page.NextCursor != previous {
		return remoteresults.DocumentPage{}, remoteresults.ErrInvalid
	}
	return page, nil
}

func (session *observationSession) fetchDocuments(index int) {
	scan := &session.scan.documents[index]
	occupied := session.documentInflight[index] || session.documentFetching[index] || session.documentFetched[index]
	unavailable := occupied || scan.index < len(scan.pending) || session.ctx.Err() != nil
	if unavailable {
		return
	}
	if scan.end {
		scan.cursor, scan.end = remoteresults.DocumentCursor{}, false
	}
	query := remoteresults.DocumentQuery{
		Origin: session.node.Origin, Class: documentClass(index), After: scan.cursor, Limit: documentPageSize,
	}
	session.documentFetching[index], session.documentFetched[index] = true, true
	session.workers.Go(func() {
		page, err := session.watcher.documentPage(session.ctx, query)
		session.publish(sessionEvent{document: true, metadata: true, documentIndex: index,
			documents: page, result: syncResult{err: err}})
	})
}

func (session *observationSession) scheduleDocuments() {
	for index := range session.scan.documents {
		if session.documentInflight[index] || session.documentStarted[index] {
			continue
		}
		scan := &session.scan.documents[index]
		if scan.index >= len(scan.pending) {
			continue
		}
		work, available := session.nextDocument(scan)
		if !available {
			continue
		}
		session.documentInflight[index], session.documentStarted[index] = true, true
		session.activeDocuments[index] = work
		session.workers.Go(func() {
			result := session.watcher.syncDocument(
				session.ctx,
				session.node,
				session.connection,
				index,
				work,
			)
			session.publish(sessionEvent{document: true, documentIndex: index, result: result})
		})
	}
}

func (session *observationSession) nextDocument(scan *documentScan) (remoteresults.DocumentWork, bool) {
	for scan.index < len(scan.pending) {
		work := scan.pending[scan.index]
		scan.index++
		// Failed turns and class changes advance the selection cursor. A
		// promoted history download must not consume the reserved Focus slot.
		scan.cursor = documentCursor(work)
		duplicate := false
		for _, active := range session.activeDocuments {
			sameOwner := active.JobID == work.JobID && active.Document.Kind == work.Document.Kind
			if sameOwner && active.Document.Reference == work.Document.Reference {
				duplicate = true
			}
		}
		if !duplicate {
			return work, true
		}
	}
	return remoteresults.DocumentWork{}, false
}

func (watcher *Watcher) syncDocument(
	ctx context.Context,
	node Node,
	connection Connection,
	index int,
	work remoteresults.DocumentWork,
) syncResult {
	classGate := watcher.focusDocumentGate
	if index == 1 {
		classGate = watcher.historyDocumentGate
	}
	release, err := acquireSlots(
		ctx,
		classGate,
		watcher.documentGate,
		watcher.gate,
	)
	if err != nil {
		return syncResult{err: err}
	}
	defer release()
	outcome, err := watcher.synchronizer.ImportDocument(ctx, DocumentRequest{
		Request: Request{Origin: node.Origin, JobID: work.JobID, Remote: connection}, Document: work.Document,
	})
	if err == nil && outcome.Imported {
		watcher.notifyChanged()
	}
	return syncResult{applied: outcome.Imported, err: err}
}
