package remotesync

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
)

func TestDocumentNotificationRequiresCommittedImportAndSupportsUnsubscribe(t *testing.T) {
	document := runDocument(1)
	fixture := newDocumentFixture(t, []remoteresults.DocumentReference{document})
	node := watcherNode(1)
	connection := &watcherConnection{node: node}
	watcher := sessionWatcher(t, node, connection)
	watcher.synchronizer = fixture.synchronizer
	work := remoteresults.DocumentWork{JobID: fixture.request.JobID, Document: document}
	notifications := 0
	unsubscribe := watcher.SubscribeChanges(func() {
		documents, err := fixture.repository.ListRemoteDocuments(t.Context(), work.JobID)
		if err != nil || !documents[0].Imported {
			t.Fatal("notification preceded committed report import")
		}
		notifications++
		// Consumers can read status without deadlocking the watcher.
		_ = watcher.Snapshot()
	})
	failure := errors.New("import failed")
	fixture.repository.importRun = func(context.Context, remoteresults.RunReportImport) error { return failure }
	result := watcher.syncDocument(
		t.Context(),
		node,
		connection,
		0,
		work,
	)
	if !errors.Is(result.err, failure) || notifications != 0 {
		t.Fatal("failed import published a successful change")
	}
	fixture.repository.importRun = nil
	result = watcher.syncDocument(
		t.Context(),
		node,
		connection,
		0,
		work,
	)
	if result.err != nil || !result.applied || notifications != 1 {
		t.Fatalf("committed import notification: result=%+v count=%d", result, notifications)
	}
	unsubscribe()
	unsubscribe()
	watcher.notifyChanged()
	if notifications != 1 {
		t.Fatal("unsubscribed observer still received changes")
	}
}

func TestChangeObserverCanUnsubscribeItself(t *testing.T) {
	watcher := &Watcher{}
	count := 0
	var unsubscribe func()
	unsubscribe = watcher.SubscribeChanges(func() { count++; unsubscribe() })
	watcher.notifyChanged()
	watcher.notifyChanged()
	if count != 1 {
		t.Fatal("self-unsubscribed observer was retained")
	}
}
