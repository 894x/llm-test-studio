package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ historyrecycle.Repository = (*Repository)(nil)

func (repository *Repository) PreviewHistoryRecycle(
	ctx context.Context,
	target historyrecycle.Target,
) (historyrecycle.Preview, error) {
	if err := target.Validate(); err != nil {
		return historyrecycle.Preview{}, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return historyrecycle.Preview{}, err
	}
	defer tx.Rollback()
	preview, err := previewRecycleTx(ctx, tx, target)
	if err != nil {
		return historyrecycle.Preview{}, err
	}
	return preview, tx.Commit()
}

func (repository *Repository) RecycleHistory(
	ctx context.Context,
	target historyrecycle.Target,
	at time.Time,
) (historyrecycle.Entry, error) {
	if target.Validate() != nil || at.IsZero() {
		return historyrecycle.Entry{}, historyrecycle.ErrInvalid
	}
	// Repository connections use BEGIN IMMEDIATE, so terminal checks and the
	// local visibility change share one writer reservation.
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	defer tx.Rollback()
	if entry, found, err := recycledTargetTx(ctx, tx, target); err != nil || found {
		if err != nil {
			return historyrecycle.Entry{}, err
		}
		return entry, tx.Commit()
	}
	preview, err := previewRecycleTx(ctx, tx, target)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	if !preview.CanRecycle {
		if preview.BlockedReason == "unconfirmed" {
			return historyrecycle.Entry{}, historyrecycle.ErrUnconfirmed
		}
		return historyrecycle.Entry{}, historyrecycle.ErrActive
	}
	entry := preview.Entry
	entry.RemovedAt = at.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO history_recycle_entries(kind,id,title,removed_at,remote) VALUES(?,?,?,?,?)`,
		entry.Kind,
		entry.ID,
		entry.Title,
		entry.RemovedAt,
		entry.Remote,
	)
	if err != nil {
		return historyrecycle.Entry{}, fmt.Errorf("save history visibility: %w", err)
	}
	for _, member := range recycleMembers(entry) {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO history_recycle_members(owner_kind,owner_id,kind,id) VALUES(?,?,?,?)`,
			entry.Kind,
			entry.ID,
			member.Kind,
			member.ID,
		); err != nil {
			return historyrecycle.Entry{}, err
		}
	}
	return entry, tx.Commit()
}

func (repository *Repository) RestoreHistory(ctx context.Context, target historyrecycle.Target) (historyrecycle.Entry, error) {
	if err := target.Validate(); err != nil {
		return historyrecycle.Entry{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	defer tx.Rollback()
	entry, found, err := recycledTargetTx(ctx, tx, target)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	if !found {
		preview, err := previewRecycleTx(ctx, tx, target)
		if err != nil {
			return historyrecycle.Entry{}, err
		}
		return preview.Entry, tx.Commit()
	}
	// Only client-local visibility metadata is removed. Evidence, reports and
	// remote synchronization facts never participate in this mutation.
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM history_recycle_entries WHERE kind=? AND id=?`,
		entry.Kind,
		entry.ID,
	); err != nil {
		return historyrecycle.Entry{}, err
	}
	entry.RemovedAt = ""
	return entry, tx.Commit()
}

func (repository *Repository) ListRecycledHistory(ctx context.Context, after string) (historyrecycle.Page, error) {
	stamp, cursor := "", historyrecycle.Target{}
	if after != "" {
		var err error
		stamp, cursor, err = historyrecycle.ParseCursor(after)
		if err != nil {
			return historyrecycle.Page{}, err
		}
		parsed, _ := time.Parse(time.RFC3339Nano, stamp)
		stamp = parsed.Format("2006-01-02T15:04:05.000000000Z")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return historyrecycle.Page{}, err
	}
	defer tx.Rollback()
	query := `SELECT kind,id FROM history_recycle_entries
	 WHERE (?1='' OR (` + historyTimeKey("removed_at") + `,kind,id)<(?1,?2,?3))
	 ORDER BY ` + historyTimeKey("removed_at") + ` DESC,kind DESC,id DESC LIMIT ?4`
	rows, err := tx.QueryContext(
		ctx,
		query,
		stamp,
		cursor.Kind,
		cursor.ID,
		historyrecycle.PageSize+1,
	)
	if err != nil {
		return historyrecycle.Page{}, err
	}
	defer rows.Close()
	ids := []historyrecycle.Target{}
	for rows.Next() {
		var target historyrecycle.Target
		if err := rows.Scan(&target.Kind, &target.ID); err != nil {
			rows.Close()
			return historyrecycle.Page{}, err
		}
		ids = append(ids, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return historyrecycle.Page{}, err
	}
	if err := rows.Close(); err != nil {
		return historyrecycle.Page{}, err
	}
	page := historyrecycle.Page{
		SchemaVersion: 1, Entries: []historyrecycle.Entry{}, HasMore: len(ids) > historyrecycle.PageSize,
	}
	if page.HasMore {
		ids = ids[:historyrecycle.PageSize]
	}
	for _, id := range ids {
		entry, err := readRecycledEntryTx(ctx, tx, id)
		if err != nil {
			return historyrecycle.Page{}, err
		}
		page.Entries = append(page.Entries, entry)
		page.NextCursor = entry.RemovedAt + "|" + entry.Kind + "|" + entry.ID
	}
	if !page.HasMore {
		page.NextCursor = ""
	}
	return page, tx.Commit()
}

func previewRecycleTx(ctx context.Context, tx *sql.Tx, target historyrecycle.Target) (historyrecycle.Preview, error) {
	entry, found, err := recycledTargetTx(ctx, tx, target)
	if err != nil {
		return historyrecycle.Preview{}, err
	}
	if found {
		return historyrecycle.Preview{SchemaVersion: 1, Entry: entry, CanRecycle: true}, nil
	}
	canonical, err := canonicalRecycleTarget(ctx, tx, target)
	if err != nil {
		return historyrecycle.Preview{}, err
	}
	entry = emptyRecycleEntry(canonical)
	reason := ""
	switch canonical.Kind {
	case "job":
		read, err := queryExecution(ctx, tx, canonical.ID)
		if err != nil {
			return historyrecycle.Preview{}, recycleLookupError(err)
		}
		entry.JobIDs = append(entry.JobIDs, canonical.ID)
		entry.Remote = read.Spec.Target.Kind == jobcontract.TargetRemote
		switch {
		case read.Execution == nil:
			reason = "unconfirmed"
		case !jobcontract.Terminal(read.Execution.Receipt.Status):
			reason = "active"
		}
		if !entry.Remote && reason == "" {
			var settled bool
			if err := tx.QueryRowContext(ctx, `SELECT settled FROM execution_jobs WHERE id=?`, canonical.ID).Scan(&settled); err != nil {
				return historyrecycle.Preview{}, err
			}
			if !settled {
				reason = "active"
			}
		}
		for _, input := range read.Spec.PreparedRuns() {
			entry.RunIDs = append(entry.RunIDs, input.Meta.ID)
			if input.Snapshot.PlanDocument != nil {
				entry.Title = input.Snapshot.PlanDocument.Name
			}
		}
		if read.Spec.Kind == jobcontract.KindPerformance {
			entry.ReportIDs = append(entry.ReportIDs, read.Spec.Spec.Performance.ReportID)
		}
		if read.Spec.Kind == jobcontract.KindComparison {
			entry.ComparisonIDs = append(entry.ComparisonIDs, read.Spec.Spec.Comparison.Meta.ID)
		}
	case "run":
		entry.RunIDs = append(entry.RunIDs, canonical.ID)
	case "comparison":
		comparison, err := queryCurrentComparison(ctx, tx, canonical.ID)
		if err != nil {
			return historyrecycle.Preview{}, recycleLookupError(err)
		}
		entry.ComparisonIDs = append(entry.ComparisonIDs, canonical.ID)
		if string(comparison.Status()) == "running" {
			reason = "active"
		}
		for _, child := range comparison.Runs() {
			entry.RunIDs = append(entry.RunIDs, child.RunID)
		}
	case "report":
		entry.ReportIDs = append(entry.ReportIDs, canonical.ID)
	}
	for _, runID := range entry.RunIDs {
		var status, title string
		err := tx.QueryRowContext(ctx, `SELECT revision.status,COALESCE(json_extract(revision.snapshot_json,'$.plan_document.name'),'')
		 FROM execution_runs root JOIN execution_run_revisions revision ON revision.run_id=root.id AND revision.revision=root.current_revision
		 WHERE root.id=?`, runID).Scan(&status, &title)
		// Remote terminal facts can precede local hydration; frozen membership
		// still hides those identities when their documents arrive later.
		pendingRemoteHydration := canonical.Kind == "job" && entry.Remote
		if errors.Is(err, sql.ErrNoRows) && pendingRemoteHydration {
			continue
		}
		if err != nil {
			return historyrecycle.Preview{}, recycleLookupError(err)
		}
		if !jobcontract.Terminal(domain.RunStatus(status)) {
			reason = "active"
		}
		if title != "" {
			entry.Title = title
		}
		reports, err := recycleStringIDs(
			ctx,
			tx,
			`SELECT id FROM reports WHERE run_id=?`,
			runID,
		)
		if err != nil {
			return historyrecycle.Preview{}, err
		}
		entry.ReportIDs = append(entry.ReportIDs, reports...)
	}
	if canonical.Kind == "job" {
		refs, err := recycleStringIDs(
			ctx,
			tx,
			`SELECT report_id FROM remote_job_report_refs WHERE job_id=?`,
			canonical.ID,
		)
		if err != nil {
			return historyrecycle.Preview{}, err
		}
		entry.ReportIDs = append(entry.ReportIDs, refs...)
	}
	normalizeRecycleEntry(&entry)
	return historyrecycle.Preview{
		SchemaVersion: 1, Entry: entry, CanRecycle: reason == "", BlockedReason: reason,
	}, nil
}

func canonicalRecycleTarget(ctx context.Context, tx *sql.Tx, target historyrecycle.Target) (historyrecycle.Target, error) {
	if target.Kind == "job" {
		return target, nil
	}
	if target.Kind == "report" {
		var runID string
		err := tx.QueryRowContext(ctx, `SELECT run_id FROM reports WHERE id=?`, target.ID).Scan(&runID)
		if err == nil {
			return canonicalRecycleTarget(ctx, tx, historyrecycle.Target{Kind: "run", ID: runID})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return historyrecycle.Target{}, err
		}
		query := `SELECT id FROM execution_jobs WHERE kind='performance'
		 AND json_extract(spec_json,'$.spec.performance.report_id')=?1
		 UNION SELECT job_id FROM remote_job_mirrors WHERE performance_report_id=?1
		 UNION SELECT job_id FROM remote_job_report_refs WHERE report_id=?1`
		owners, err := recycleStringIDs(
			ctx,
			tx,
			query,
			target.ID,
		)
		if err != nil {
			return historyrecycle.Target{}, err
		}
		if len(owners) > 1 {
			return historyrecycle.Target{}, storageCorrupt("history report owners")
		}
		if len(owners) == 1 {
			return historyrecycle.Target{Kind: "job", ID: owners[0]}, nil
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM quick_performance_reports WHERE id=?`, target.ID).Scan(&exists); err != nil {
			return historyrecycle.Target{}, err
		}
		if exists != 1 {
			return historyrecycle.Target{}, historyrecycle.ErrNotFound
		}
		return target, nil
	}
	if target.Kind == "run" {
		owners, err := recycleStringIDs(
			ctx,
			tx,
			`SELECT job_id FROM execution_job_runs WHERE run_id=?1
		 UNION SELECT job_id FROM remote_job_runs WHERE run_id=?1`,
			target.ID,
		)
		if err != nil {
			return historyrecycle.Target{}, err
		}
		if len(owners) > 1 {
			return historyrecycle.Target{}, storageCorrupt("history run owners")
		}
		if len(owners) == 1 {
			return historyrecycle.Target{Kind: "job", ID: owners[0]}, nil
		}
		groups, err := recycleStringIDs(
			ctx,
			tx,
			`SELECT DISTINCT child.comparison_id FROM comparison_runs child JOIN comparisons root
		 ON root.id=child.comparison_id AND root.current_revision=child.comparison_revision WHERE child.run_id=?`,
			target.ID,
		)
		if err != nil {
			return historyrecycle.Target{}, err
		}
		if len(groups) > 1 {
			return historyrecycle.Target{}, storageCorrupt("history comparison owners")
		}
		if len(groups) == 1 {
			return canonicalRecycleTarget(ctx, tx, historyrecycle.Target{Kind: "comparison", ID: groups[0]})
		}
		return target, nil
	}
	owners, err := recycleStringIDs(
		ctx,
		tx,
		`SELECT id FROM execution_jobs WHERE kind='comparison'
	 AND json_extract(spec_json,'$.spec.comparison.meta.id')=?1
	 UNION SELECT job_id FROM remote_job_mirrors WHERE comparison_id=?1`,
		target.ID,
	)
	if err != nil {
		return historyrecycle.Target{}, err
	}
	if len(owners) > 1 {
		return historyrecycle.Target{}, storageCorrupt("history comparison job owners")
	}
	if len(owners) == 1 {
		return historyrecycle.Target{Kind: "job", ID: owners[0]}, nil
	}
	return target, nil
}

func emptyRecycleEntry(target historyrecycle.Target) historyrecycle.Entry {
	return historyrecycle.Entry{Kind: target.Kind, ID: target.ID, Title: target.ID,
		RunIDs: []string{}, ReportIDs: []string{}, ComparisonIDs: []string{}, JobIDs: []string{}}
}

func recycleMembers(entry historyrecycle.Entry) []historyrecycle.Target {
	members := []historyrecycle.Target{}
	groups := map[string][]string{
		"run": entry.RunIDs, "report": entry.ReportIDs, "comparison": entry.ComparisonIDs, "job": entry.JobIDs,
	}
	for kind, ids := range groups {
		for _, id := range ids {
			members = append(members, historyrecycle.Target{Kind: kind, ID: id})
		}
	}
	return members
}

func normalizeRecycleEntry(entry *historyrecycle.Entry) {
	for _, ids := range []*[]string{&entry.RunIDs, &entry.ReportIDs, &entry.ComparisonIDs, &entry.JobIDs} {
		slices.Sort(*ids)
		*ids = slices.Compact(*ids)
	}
}

func recycledTargetTx(ctx context.Context, tx *sql.Tx, target historyrecycle.Target) (historyrecycle.Entry, bool, error) {
	var owner historyrecycle.Target
	err := tx.QueryRowContext(
		ctx,
		`SELECT owner_kind,owner_id FROM history_recycle_members WHERE kind=? AND id=?`,
		target.Kind,
		target.ID,
	).Scan(&owner.Kind, &owner.ID)
	if errors.Is(err, sql.ErrNoRows) {
		canonical, resolveErr := canonicalRecycleTarget(ctx, tx, target)
		if resolveErr != nil {
			return historyrecycle.Entry{}, false, resolveErr
		}
		if canonical != target {
			return recycledTargetTx(ctx, tx, canonical)
		}
		return historyrecycle.Entry{}, false, nil
	}
	if err != nil {
		return historyrecycle.Entry{}, false, err
	}
	entry, err := readRecycledEntryTx(ctx, tx, owner)
	return entry, err == nil, err
}

func readRecycledEntryTx(ctx context.Context, tx *sql.Tx, target historyrecycle.Target) (historyrecycle.Entry, error) {
	entry := emptyRecycleEntry(target)
	if err := tx.QueryRowContext(
		ctx,
		`SELECT title,removed_at,remote FROM history_recycle_entries WHERE kind=? AND id=?`,
		target.Kind,
		target.ID,
	).Scan(&entry.Title, &entry.RemovedAt, &entry.Remote); err != nil {
		return historyrecycle.Entry{}, recycleLookupError(err)
	}
	if _, _, err := historyrecycle.ParseCursor(entry.RemovedAt + "|" + entry.Kind + "|" + entry.ID); err != nil {
		return historyrecycle.Entry{}, storageCorrupt("history recycle entry")
	}
	rows, err := tx.QueryContext(
		ctx,
		`SELECT kind,id FROM history_recycle_members WHERE owner_kind=? AND owner_id=? ORDER BY kind,id`,
		entry.Kind,
		entry.ID,
	)
	if err != nil {
		return historyrecycle.Entry{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var member historyrecycle.Target
		if err := rows.Scan(&member.Kind, &member.ID); err != nil {
			return historyrecycle.Entry{}, err
		}
		if member.Validate() != nil {
			return historyrecycle.Entry{}, storageCorrupt("history recycle member")
		}
		switch member.Kind {
		case "run":
			entry.RunIDs = append(entry.RunIDs, member.ID)
		case "report":
			entry.ReportIDs = append(entry.ReportIDs, member.ID)
		case "comparison":
			entry.ComparisonIDs = append(entry.ComparisonIDs, member.ID)
		case "job":
			entry.JobIDs = append(entry.JobIDs, member.ID)
		}
	}
	return entry, rows.Err()
}

func recycleStringIDs(ctx context.Context, tx *sql.Tx, query string, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func recycleLookupError(err error) error {
	missingRow := errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound)
	if missingRow || errors.Is(err, jobs.ErrNotFound) {
		return historyrecycle.ErrNotFound
	}
	return err
}
