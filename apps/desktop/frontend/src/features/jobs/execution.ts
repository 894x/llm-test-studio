import { DesktopClientError, type DesktopClient } from "@/app/desktop-client"
import type { ControlInput, JobView, LaunchPayload } from "./data"

export type JobClient = Pick<DesktopClient, "getRemoteNodes" | "launchJob" | "getJobs" | "getJob" | "getJobPerformanceResult" | "controlJob" | "getJobControls" | "retryJobControl">

export async function launchAtDefault(client: Pick<JobClient, "getRemoteNodes" | "launchJob">, payload: LaunchPayload): Promise<JobView> {
  const settings = await client.getRemoteNodes()
  return client.launchJob({ ...payload, location: settings.default })
}

export function jobControlRevision(job: JobView): number | null {
  if (job.execution && job.confirmation) return Math.max(job.execution.revision, job.confirmation.revision)
  return job.execution?.revision ?? job.confirmation?.revision ?? null
}

// Run history predates managed Jobs. Only a persisted submission can authorize
// a command; a run ID alone never identifies an executor or control revision.
export async function controlRun(client: JobClient, runID: string, kind: ControlInput["kind"]): Promise<JobView> {
  let cursor = ""
  const visited = new Set<string>()
  for (;;) {
    if (visited.has(cursor)) throw new DesktopClientError("job_query_unavailable")
    visited.add(cursor)
    const page = await client.getJobs(cursor, 25)
    const match = page.jobs.find(job => job.run_ids.includes(runID))
    if (match) {
      const current = await client.getJob(match.job_id)
      const revision = jobControlRevision(current)
      if (!revision) throw new DesktopClientError("job_confirmation_pending")
      await client.controlJob({ job_id: current.job_id, kind, expected_revision: revision })
      return current
    }
    if (!page.has_more) break
    cursor = page.next_cursor
  }
  throw new DesktopClientError("job_not_found")
}
