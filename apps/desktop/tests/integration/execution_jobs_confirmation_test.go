package integration

import (
	"context"
	"errors"
	"testing"

	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/jobs"
)

func TestDesktopLaunchKeepsDurablePendingIdentityOnConfirmationStorageFailure(t *testing.T) {
	record := desktopSubmissionFixture(t)
	failure := errors.New("injected confirmation storage failure")
	launcher := &recordingJobLauncher{record: record, err: errors.Join(jobs.ErrPending, failure)}
	query := &recordingJobQuery{err: errors.New("confirmation storage is unavailable")}
	app := bridge.New(func(context.Context) (bridge.Dependencies, error) {
		return bridge.Dependencies{Launcher: launcher, JobQuery: query}, nil
	})
	bridge.Startup(app, t.Context())
	t.Cleanup(func() { _ = bridge.Shutdown(app) })
	view, err := app.LaunchJob(desktopLaunchFixture())
	if err != nil || view.JobID != record.Prepared.Spec.JobID || view.Submission == nil || view.Submission.Delivery != jobs.SubmissionPending {
		t.Fatalf("accepted upstream work was reported as a failed new launch: %v", err)
	}
	if view.Submission.DeliveryRevision != record.Revision || view.Execution != nil || view.Submission.Confirmation != nil {
		t.Fatal("unpersisted confirmation was presented as authoritative execution")
	}
	if launcher.calls != 1 || len(query.calls) != 0 {
		t.Fatal("confirmation storage failure caused resubmission or depended on a later read")
	}
}
