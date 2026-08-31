package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/894x/llm-studio/internal/application/caseimport"
	"github.com/894x/llm-studio/internal/domain"
)

func TestPruneUnreferencedTestCaseSnapshotsRemovesLegacyImportState(t *testing.T) {
	repository := openCaseImportRepository(t, filepath.Join(t.TempDir(), "case-prune.db"))
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "unreferenced"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}

	if err := repository.PruneUnreferencedTestCaseSnapshots(context.Background()); err != nil {
		t.Fatalf("PruneUnreferencedTestCaseSnapshots() error = %v", err)
	}
	remaining, err := repository.ListTestCases(context.Background())
	if err != nil {
		t.Fatalf("ListTestCases() error = %v", err)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}
	if len(remaining) != 0 || len(state.Sources) != 0 {
		t.Fatalf("remaining cases/sources = %d/%d, want 0/0", len(remaining), len(state.Sources))
	}
}

func TestPruneUnreferencedTestCaseSnapshotsKeepsPinnedLegacyRevision(t *testing.T) {
	repository := openCaseImportRepository(t, filepath.Join(t.TempDir(), "pinned-case-prune.db"))
	defer repository.Close()
	service := newSQLiteImportService(t, repository)
	bundle := fstest.MapFS{
		"openai-chat/T001/case.json": &fstest.MapFile{Data: []byte(caseImportLegacyFixture("T001", "pinned"))},
	}
	if _, err := service.Import(context.Background(), bundle); err != nil {
		t.Fatalf("seed Import() error = %v", err)
	}
	state, err := repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil || len(state.Cases) != 1 {
		t.Fatalf("seed import state = %#v, error = %v", state, err)
	}
	imported := state.Cases[0]
	suite := domain.Suite{
		EntityMeta: entityMeta("72000000-0000-4000-8000-000000000001", 1),
		Name:       "Pinned legacy case",
		Cases:      []domain.CaseRevisionRef{{CaseID: imported.ID, Revision: imported.Revision}},
	}
	if err := repository.CreateSuite(context.Background(), suite); err != nil {
		t.Fatalf("CreateSuite() error = %v", err)
	}

	if err := repository.PruneUnreferencedTestCaseSnapshots(context.Background()); err != nil {
		t.Fatalf("PruneUnreferencedTestCaseSnapshots() error = %v", err)
	}
	remaining, err := repository.ListTestCases(context.Background())
	if err != nil {
		t.Fatalf("ListTestCases() error = %v", err)
	}
	state, err = repository.LoadCaseImportState(context.Background(), caseimport.Namespace)
	if err != nil {
		t.Fatalf("LoadCaseImportState() error = %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != imported.ID || remaining[0].Revision != imported.Revision {
		t.Fatalf("remaining cases = %#v, want pinned %s revision %d", remaining, imported.ID, imported.Revision)
	}
	if len(state.Sources) != 0 {
		t.Fatalf("remaining legacy import sources = %d, want 0", len(state.Sources))
	}
}
