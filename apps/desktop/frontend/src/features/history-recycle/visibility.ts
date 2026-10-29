import type { HistoryVisibilityChange } from "./data"

// These are authoritative affected identities returned by Core after a mutation.
export function historyVisibility(changes: HistoryVisibilityChange[]) {
  const result = { jobs: new Set<string>(), runs: new Set<string>(), reports: new Set<string>(), comparisons: new Set<string>() }
  for (const { entry, action } of changes) {
    for (const [ids, hidden] of [[entry.job_ids, result.jobs], [entry.run_ids, result.runs], [entry.report_ids, result.reports], [entry.comparison_ids, result.comparisons]] as const) {
      ids.forEach(id => { if (action === "recycle") hidden.add(id); else hidden.delete(id) })
    }
  }
  return result
}
