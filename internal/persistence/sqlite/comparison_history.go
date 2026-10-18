package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/domain"
)

// ComparisonHistory projects only a bounded window and explicitly refreshed
// active identities. It never loads report bodies into Go or validates history.
func (repository *Repository) ComparisonHistory(
	ctx context.Context,
	after string,
	refresh []string,
) (comparisons.HistoryPage, error) {
	page := comparisons.HistoryPage{SchemaVersion: 1, Comparisons: []comparisons.Summary{}}
	cursor, err := quickhistory.ParseCursor(after)
	if err != nil || len(refresh) > 100 || cursor.Kind != "" && cursor.Kind != "run" {
		return page, comparisons.ErrInvalid
	}
	for _, id := range refresh {
		if !domain.IsUUID(id) {
			return page, comparisons.ErrInvalid
		}
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, comparisonHistoryIDsQuery, cursor.Time, cursor.ID)
	if err != nil {
		return page, err
	}
	ids := []string{}
	keys := []string{}
	for rows.Next() {
		var id, stamp string
		if err := rows.Scan(&id, &stamp); err != nil {
			rows.Close()
			return page, err
		}
		ids = append(ids, id)
		keys = append(keys, stamp+"|run|"+id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	if len(ids) > 25 {
		ids = ids[:25]
		page.NextCursor = keys[24]
	}
	pageIDs := append([]string{}, ids...)
	ids = append(ids, refresh...)
	document, err := json.Marshal(ids)
	if err != nil {
		return page, err
	}
	rows, err = tx.QueryContext(ctx, comparisonHistoryQuery, string(document))
	if err != nil {
		return page, err
	}
	defer rows.Close()
	positions := map[string]int{}
	expectedCounts := map[string]int{}
	for rows.Next() {
		var summary comparisons.Summary
		var channel comparisons.ChannelResult
		var stamp, metrics string
		var valid, expectedCount, channelPosition int
		if err := rows.Scan(
			&summary.ID, &stamp, &summary.Status, &summary.PlanID, &summary.ModelID,
			&summary.PlanName, &summary.ModelName, &channel.ChannelID, &channel.ChannelName,
			&channel.RunID, &channel.RunStatus, &channel.ReportReady, &channel.Passed,
			&channel.Verdict, &metrics, &valid, &expectedCount, &channelPosition,
		); err != nil {
			return page, err
		}
		summary.CreatedAt, err = parseProjectionTimestamp(stamp)
		if err != nil || valid != 1 || expectedCount < 2 ||
			json.Unmarshal([]byte(metrics), &channel.Metrics) != nil || channel.Metrics == nil ||
			!domain.IsUUID(summary.ID) || !domain.IsUUID(summary.PlanID) || !domain.IsUUID(summary.ModelID) ||
			!domain.IsUUID(channel.ChannelID) || !domain.IsUUID(channel.RunID) ||
			strings.TrimSpace(summary.PlanName) == "" || strings.TrimSpace(summary.ModelName) == "" ||
			strings.TrimSpace(channel.ChannelName) == "" {
			return page, storageCorrupt("comparison summary")
		}
		for name, metric := range channel.Metrics {
			if err := metric.Validate(name); err != nil {
				return page, storageCorrupt("comparison metric")
			}
		}
		position, found := positions[summary.ID]
		if !found {
			position = len(page.Comparisons)
			positions[summary.ID] = position
			expectedCounts[summary.ID] = expectedCount
			summary.Channels = []comparisons.ChannelResult{}
			page.Comparisons = append(page.Comparisons, summary)
		}
		if len(page.Comparisons[position].Channels) != channelPosition ||
			page.Comparisons[position].PlanName != summary.PlanName ||
			page.Comparisons[position].ModelName != summary.ModelName {
			return page, storageCorrupt("comparison child window")
		}
		page.Comparisons[position].Channels = append(page.Comparisons[position].Channels, channel)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	for _, id := range pageIDs {
		if _, found := positions[id]; !found {
			return page, storageCorrupt("comparison history window")
		}
	}
	for _, summary := range page.Comparisons {
		if len(summary.Channels) != expectedCounts[summary.ID] {
			return page, storageCorrupt("comparison channel count")
		}
	}
	sort.Slice(page.Comparisons, func(i, j int) bool {
		if page.Comparisons[i].CreatedAt.Equal(page.Comparisons[j].CreatedAt) {
			return page.Comparisons[i].ID > page.Comparisons[j].ID
		}
		return page.Comparisons[i].CreatedAt.After(page.Comparisons[j].CreatedAt)
	})
	return page, tx.Commit()
}

const comparisonHistoryQuery = `SELECT root.id,root.created_at,current.status,current.plan_id,current.model_id,
 json_extract(revision.snapshot_json,'$.plan_document.name'), json_extract(revision.snapshot_json,'$.model.name'),
 child.channel_id,json_extract(revision.snapshot_json,'$.channel.name'),child.run_id,revision.status,
 report.id IS NOT NULL,COALESCE(json_extract(report.document_json,'$.conclusion.passed'),0),
 COALESCE(json_extract(report.document_json,'$.conclusion.verdict'),''),COALESCE(json_extract(report.document_json,'$.metrics'),'{}'),
 COALESCE(json_extract(revision.snapshot_json,'$.plan.id')=current.plan_id
 AND json_extract(revision.snapshot_json,'$.plan.revision')=current.plan_revision
 AND json_extract(revision.snapshot_json,'$.plan_document.id')=current.plan_id
 AND json_extract(revision.snapshot_json,'$.plan_document.revision')=current.plan_revision
 AND json_extract(revision.snapshot_json,'$.model.id')=current.model_id
 AND json_extract(revision.snapshot_json,'$.model.revision')=current.model_revision
 AND json_extract(revision.snapshot_json,'$.channel.id')=child.channel_id
 AND json_extract(revision.snapshot_json,'$.channel.revision')=child.channel_revision
 AND json_extract(current.document_json,'$.runs['||child.position||'].run_id')=child.run_id
 AND json_extract(current.document_json,'$.runs['||child.position||'].channel.id')=child.channel_id
 AND json_extract(current.document_json,'$.runs['||child.position||'].channel.revision')=child.channel_revision,0),
 COALESCE(json_array_length(current.document_json,'$.runs'),0), child.position
 FROM comparisons root
 JOIN comparison_revisions current ON current.comparison_id=root.id AND current.revision=root.current_revision
 JOIN comparison_runs child ON child.comparison_id=root.id AND child.comparison_revision=root.current_revision
 JOIN execution_runs run ON run.id=child.run_id
 JOIN execution_run_revisions revision ON revision.run_id=run.id AND revision.revision=run.current_revision
 LEFT JOIN reports report ON report.run_id=run.id
 WHERE root.id IN (SELECT value FROM json_each(?)) ORDER BY root.id,child.position`

var comparisonHistoryIDsQuery = `SELECT id, ` + historyTimeKey("created_at") + ` stamp FROM comparisons
 WHERE (` + historyTimeKey("created_at") + `) <= CASE WHEN ?1='' THEN '9999-12-31T23:59:59.999999999Z' ELSE ?1 END
 AND (?1='' OR (` + historyTimeKey("created_at") + `,id)<(?1,?2)) ORDER BY stamp DESC,id DESC LIMIT 26`
