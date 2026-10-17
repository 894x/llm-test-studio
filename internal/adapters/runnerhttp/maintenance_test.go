package runnerhttp_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type memoryMaintenanceStore struct {
	mu     sync.Mutex
	record *jobcontract.MaintenanceRecord
}

func (store *memoryMaintenanceStore) Read(context.Context) (*jobcontract.MaintenanceRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.record == nil {
		return nil, nil
	}
	copy := *store.record
	return &copy, nil
}

func (store *memoryMaintenanceStore) Write(_ context.Context, record jobcontract.MaintenanceRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.record = &record
	return nil
}

func (*memoryMaintenanceStore) Sync(context.Context) error { return nil }

func TestHTTPMaintenanceBlocksNewWorkAndAllowsAlreadyAcceptedWorkToFinish(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	submission := fixture.submission(t, nil)
	fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	command := jobcontract.MaintenanceCommand{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		Action: jobcontract.MaintenancePause,
	}
	response := fixture.request(
		t,
		http.MethodPost,
		"/runner/maintenance",
		encode(t, command),
	)
	if response.StatusCode != http.StatusOK {
		t.Fatal("maintenance was not persisted")
	}
	status := fixture.host.Status()
	validStatus := status.Healthy && !status.Ready && status.Pending == 1 &&
		status.MaintenanceOperationID == command.OperationID
	if !validStatus {
		t.Fatal("paused node did not retain health, ownership and pending work")
	}
	retry := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, submission),
	)
	if retry.StatusCode != http.StatusOK {
		t.Fatal("pause rejected confirmation of an already accepted job")
	}
	newSubmission := fixture.submission(t, nil)
	rejected := fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, newSubmission),
	)
	if rejected.StatusCode != http.StatusLocked {
		t.Fatal("new work was accepted after pause confirmation")
	}
	fixture.release()
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	status = fixture.host.Status()
	if status.Pending != 0 || !status.Healthy || status.Ready {
		t.Fatal("finished maintenance drain changed the intake gate")
	}
	command.Action = jobcontract.MaintenanceResume
	resumed := fixture.request(
		t,
		http.MethodPost,
		"/runner/maintenance",
		encode(t, command),
	)
	if resumed.StatusCode != http.StatusOK || !fixture.host.Status().Ready {
		t.Fatal("owner could not resume the node")
	}
	response = fixture.request(
		t,
		http.MethodPost,
		"/jobs",
		encode(t, newSubmission),
	)
	if response.StatusCode != http.StatusAccepted {
		t.Fatal("resume did not reopen intake")
	}
	waitHTTPJob(
		t,
		fixture,
		newSubmission.Job.JobID,
		domain.RunCompleted,
	)
}
