package sqlite

import (
	"context"
	"database/sql"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
)

// DeleteRecycledHistory removes only the exact, local recycling operations the
// user confirmed. BEGIN IMMEDIATE reserves the writer for checks, DDL and data.
func (repository *Repository) DeleteRecycledHistory(
	ctx context.Context,
	removals []historyrecycle.Removal,
) ([]historyrecycle.Entry, error) {
	if err := historyrecycle.ValidateRemovals(removals); err != nil {
		return nil, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	entries := []historyrecycle.Entry{}
	for _, removal := range removals {
		entry, err := deletableHistoryTx(ctx, tx, removal)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	triggers, err := suspendHistoryDeleteTriggers(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := deleteHistoryEntryTx(ctx, tx, entry); err != nil {
			return nil, err
		}
	}
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, trigger.ddl); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return entries, nil
}

func deletableHistoryTx(ctx context.Context, tx *sql.Tx, removal historyrecycle.Removal) (historyrecycle.Entry, error) {
	entry, err := readRecycledEntryTx(ctx, tx, removal.Target)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	if entry.RemovedAt != removal.RemovedAt {
		return historyrecycle.Entry{}, historyrecycle.ErrInvalid
	}
	if entry.Remote {
		return historyrecycle.Entry{}, historyrecycle.ErrDeleteUnsupported
	}
	preview, err := previewUnrecycledTx(ctx, tx, removal.Target)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	if !preview.CanRecycle {
		return historyrecycle.Entry{}, historyrecycle.ErrActive
	}
	fresh := preview.Entry
	sameOwner := fresh.Kind == entry.Kind && fresh.ID == entry.ID && !fresh.Remote
	sameMembers := slices.Equal(fresh.JobIDs, entry.JobIDs) && slices.Equal(fresh.RunIDs, entry.RunIDs) &&
		slices.Equal(fresh.ReportIDs, entry.ReportIDs) && slices.Equal(fresh.ComparisonIDs, entry.ComparisonIDs)
	if !sameOwner || !sameMembers {
		return historyrecycle.Entry{}, historyrecycle.ErrInvalid
	}
	// Archives and remote synchronization retain ownership outside this local
	// group. Never remove their backing documents or lifecycle facts.
	for _, member := range recycleMembers(entry) {
		queries := map[string]string{
			"job": `SELECT EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id=?1)
			 OR EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE job_id=?1)`,
			"run": `SELECT EXISTS(SELECT 1 FROM remote_job_runs WHERE run_id=?1)
			 OR EXISTS(SELECT 1 FROM upgrade_history_runs WHERE run_id=?1)`,
			"report": `SELECT EXISTS(SELECT 1 FROM remote_job_report_refs WHERE report_id=?1)
			 OR EXISTS(SELECT 1 FROM remote_job_mirrors WHERE performance_report_id=?1)
			 OR EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE performance_report_id=?1)`,
			"comparison": `SELECT EXISTS(SELECT 1 FROM remote_job_mirrors WHERE comparison_id=?1)
			 OR EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE comparison_id=?1)`,
		}
		var shared bool
		if err := tx.QueryRowContext(ctx, queries[member.Kind], member.ID).Scan(&shared); err != nil {
			return historyrecycle.Entry{}, err
		}
		if shared {
			return historyrecycle.Entry{}, historyrecycle.ErrDeleteUnsupported
		}
	}
	for _, id := range entry.JobIDs {
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_control_outbox WHERE job_id=? AND state='pending')`, id).
			Scan(&pending); err != nil {
			return historyrecycle.Entry{}, err
		}
		if pending {
			return historyrecycle.Entry{}, historyrecycle.ErrActive
		}
		if _, err := queryJobContent(ctx, tx, id); err != nil {
			return historyrecycle.Entry{}, err
		}
	}
	return entry, nil
}

func suspendHistoryDeleteTriggers(ctx context.Context, tx *sql.Tx) ([]schemaObject, error) {
	names := []string{"quick_performance_reports_no_delete", "trg_execution_run_revisions_no_delete", "comparison_revisions_no_delete"}
	triggers := []schemaObject{}
	for _, name := range names {
		for _, object := range currentSchemaObjects {
			if object.name != name || object.kind != "trigger" {
				continue
			}
			var actual string
			if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, name).
				Scan(&actual); err != nil {
				return nil, err
			}
			if normalizeDDL(actual) != normalizeDDL(object.ddl) {
				return nil, storageCorrupt("history delete trigger")
			}
			triggers = append(triggers, object)
		}
	}
	if len(triggers) != len(names) {
		return nil, storageCorrupt("history delete triggers")
	}
	// All names are fixed above. DDL changes are private to this transaction;
	// rollback restores the original triggers together with the deleted data.
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER "`+trigger.name+`"`); err != nil {
			return nil, err
		}
	}
	return triggers, nil
}

func deleteHistoryEntryTx(ctx context.Context, tx *sql.Tx, entry historyrecycle.Entry) error {
	jobQueries := []string{
		`DELETE FROM job_control_outbox WHERE job_id=?`,
		`DELETE FROM job_control_targets WHERE job_id=?`,
		`DELETE FROM job_submissions WHERE job_id=?`,
		`DELETE FROM execution_performance_observations WHERE job_id=?`,
		`DELETE FROM execution_performance_results WHERE job_id=?`,
		`DELETE FROM execution_job_commands WHERE job_id=?`,
		`DELETE FROM execution_job_events WHERE job_id=?`,
		`DELETE FROM execution_job_content WHERE job_id=?`,
		`DELETE FROM execution_job_runs WHERE job_id=?`,
	}
	for _, id := range entry.JobIDs {
		for _, query := range jobQueries {
			if _, err := tx.ExecContext(ctx, query, id); err != nil {
				return err
			}
		}
	}
	groups := []struct {
		ids     []string
		queries []string
	}{
		{ids: entry.ComparisonIDs, queries: []string{`DELETE FROM comparisons WHERE id=?`}},
		{ids: entry.ReportIDs, queries: []string{`DELETE FROM reports WHERE id=?`, `DELETE FROM quick_performance_reports WHERE id=?`}},
		{ids: entry.RunIDs, queries: []string{`DELETE FROM execution_runs WHERE id=?`}},
		{ids: entry.JobIDs, queries: []string{`DELETE FROM execution_jobs WHERE id=?`}},
	}
	for _, group := range groups {
		for _, id := range group.ids {
			for _, query := range group.queries {
				if _, err := tx.ExecContext(ctx, query, id); err != nil {
					return err
				}
			}
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM history_recycle_entries WHERE kind=? AND id=?`, entry.Kind, entry.ID)
	return err
}
