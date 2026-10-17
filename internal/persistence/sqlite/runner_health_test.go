package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestRunnerWriteProbeRejectsReadonlyStorageWithoutChangingTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "runner-health-test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	before, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CheckWritable(t.Context()); err != nil {
		t.Fatal(err)
	}
	readonlyURI := "file:" + filepath.ToSlash(path) + "?mode=ro"
	readonly, err := sqlite.OpenRepository(t.Context(), readonlyURI, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if err := readonly.CheckWritable(t.Context()); err == nil {
		t.Fatal("readonly storage passed the writer probe")
	}
	after, err := repository.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"health probe task",
		before,
		after,
	)
}
