package sqlite

import "strings"

// kind and identity are repository-owned SQL fragments, never caller input.
func (repository *Repository) historyVisibleSQL(kind string, identity string) string {
	if !repository.hideRecycled {
		return "1"
	}
	return "NOT EXISTS (SELECT 1 FROM history_recycle_members AS recycled" +
		" WHERE recycled.kind = '" + kind + "' AND recycled.id = " + identity + ")"
}

func (repository *Repository) reportVisibleSQL(reportID string, runID string) string {
	return repository.historyVisibleSQL("report", reportID) +
		" AND " + repository.historyVisibleSQL("run", runID)
}

func (repository *Repository) visibleRunProjectionQuery(query string) string {
	if !repository.hideRecycled {
		return query
	}
	const currentRevision = "revision.revision = root.current_revision\n"
	return strings.Replace(query, currentRevision,
		currentRevision+"    AND "+repository.historyVisibleSQL("run", "root.id")+"\n", 1)
}
