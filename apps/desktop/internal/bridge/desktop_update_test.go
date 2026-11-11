package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

type updateJobQuery struct {
	recordingJobQuery
	pages []jobs.JobPage
	calls int
	err   error
}

func (query *updateJobQuery) Jobs(_ context.Context, _ string, _ uint32) (jobs.JobPage, error) {
	index := query.calls
	query.calls++
	if query.err != nil {
		return jobs.JobPage{}, query.err
	}
	if index >= len(query.pages) {
		return jobs.JobPage{Jobs: []jobs.JobView{}}, nil
	}
	return query.pages[index], nil
}

func TestUpdateActivityIncludesLaterPagesAndUnknownDelivery(t *testing.T) {
	for _, test := range []struct {
		job  jobs.JobView
		want string
	}{
		{jobs.JobView{Execution: &jobs.ExecutionView{Status: domain.RunCompleted}}, ""},
		{jobs.JobView{Execution: &jobs.ExecutionView{Status: domain.RunDraining}}, "tasks_active"},
		{jobs.JobView{}, "tasks_unknown"},
		{jobs.JobView{Submission: &jobs.SubmissionView{Confirmation: &jobs.ConfirmationView{Status: domain.RunQueued}}}, "tasks_active"},
	} {
		query := &updateJobQuery{pages: []jobs.JobPage{
			{Jobs: []jobs.JobView{}, HasMore: true, NextCursor: "next"}, {Jobs: []jobs.JobView{test.job}},
		}}
		if got := updateActivity(context.Background(), query, &recordingWorkspaceQuery{}); got != test.want || query.calls != 2 {
			t.Fatalf("activity = %q, calls = %d; want %q", got, query.calls, test.want)
		}
	}
	query := &updateJobQuery{err: errors.New("storage unavailable")}
	if got := updateActivity(context.Background(), query, &recordingWorkspaceQuery{}); got != "tasks_unknown" {
		t.Fatal("query error was treated as idle")
	}
}

func TestUpdateActivityIncludesStandaloneRunsAndBlocksNewLeases(t *testing.T) {
	workspace := &recordingWorkspaceQuery{snapshot: workspace.Snapshot{Runs: []workspace.RunSummary{{Status: domain.RunRunning}}}}
	if got := updateActivity(context.Background(), &updateJobQuery{}, workspace); got != "tasks_active" {
		t.Fatal("standalone run was ignored")
	}
	app := NewDesktopApp(&recordingWorkspaceQuery{}, nil)
	Startup(app, context.Background())
	defer Shutdown(app)
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.updatePreparing = true
	app.mu.Unlock()
	if _, err := app.acquire(desktopRequirements{}); !errors.Is(err, ErrDesktopStopped) {
		t.Fatal("new command lease accepted during update preparation")
	}
	lease.release()
}
