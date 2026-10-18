import { useCallback, useEffect, useRef, useState } from "react"
import type { DesktopClient, RunHistoryPage } from "@/app/desktop-client"
import type { JobView } from "@/features/jobs/data"
import { isRunActive, type WorkspaceSnapshot } from "./data"

export type RunHistoryClient = Pick<DesktopClient, "getRunHistory" | "getRunSummaries" | "getAvailableReports">
const empty: RunHistoryPage = { schema_version: 1, runs: [], next_cursor: "" }

export function useRunHistory(client: Partial<RunHistoryClient>, jobs: JobView[], preferredRunID = "") {
  const [page, setPage] = useState(empty)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [refreshError, setRefreshError] = useState<unknown>(null)
  const retained = useRef(empty)
  const loaded = useRef(false)
  const observedJobs = useRef(new Map<string, string>())
  const requestedIDs = useRef(new Set<string>())
  const requests = useRef(new Map<string, Promise<void>>())
  const merge = useCallback((next: RunHistoryPage, cursor?: string) => {
    const runs = new Map(retained.current.runs.map(run => [run.id, run]))
    next.runs.forEach(run => {
      const previous = runs.get(run.id)
      if (!previous || run.revision > previous.revision || (run.revision === previous.revision && (!previous.report_id || run.report_id))) runs.set(run.id, run)
    })
    retained.current = { schema_version: 1, runs: [...runs.values()], next_cursor: cursor ?? retained.current.next_cursor }
    setPage(retained.current)
  }, [])
  const read = useCallback((append: boolean) => {
    if (!client.getRunHistory) return Promise.resolve()
    const cursor = append ? retained.current.next_cursor : ""
    if (append && !cursor) return Promise.resolve()
    const key = `page:${cursor}`
    if (requests.current.has(key)) return requests.current.get(key)!
    setPending(true)
    const request = Promise.resolve().then(() => client.getRunHistory!(cursor)).then(next => {
      const known = new Set(retained.current.runs.map(run => run.id))
      const overlaps = next.runs.some(run => known.has(run.id))
      merge(next, append || !loaded.current || !overlaps ? next.next_cursor : retained.current.next_cursor)
      loaded.current = true
      setError(null)
    }).catch(setError).finally(() => { requests.current.delete(key); setPending(false) })
    requests.current.set(key, request)
    return request
  }, [client, merge])
  const refreshIDs = useCallback(async (ids: string[]) => {
    if (!client.getRunSummaries) return
    const unique = [...new Set(ids)].sort()
    unique.forEach(id => requestedIDs.current.add(id))
    for (let offset = 0; offset < unique.length; offset += 100) {
      const batch = unique.slice(offset, offset + 100)
      const key = batch.join(",")
      let request = requests.current.get(key)
      if (!request) {
        request = client.getRunSummaries(batch).then(next => { next.runs.forEach(run => requestedIDs.current.delete(run.id)); merge(next); setRefreshError(null) })
          .catch(setRefreshError).finally(() => { requests.current.delete(key) })
        requests.current.set(key, request)
      }
      await request
    }
  }, [client, merge])
  useEffect(() => {
    if (!refreshError) return
    const timer = window.setTimeout(() => void refreshIDs([...requestedIDs.current]), 2000)
    return () => window.clearTimeout(timer)
  }, [refreshError, refreshIDs])
  useEffect(() => { void read(false) }, [read])
  const jobRuns = JSON.stringify(jobs.map(job => ({ id: job.job_id, ids: job.run_ids, sequence: job.execution?.latest_sequence, status: job.execution?.status })))
  const activeIDs = page.runs.filter(run => isRunActive(run.status)).map(run => run.id).sort().join(",")
  useEffect(() => {
    let active = true
    let timer: number | undefined
    const read = async () => {
      const observations = JSON.parse(jobRuns) as { id: string; ids: string[]; sequence?: number; status?: string }[]
      observations.forEach(job => {
        const version = JSON.stringify(job)
        if (observedJobs.current.get(job.id) !== version) {
          job.ids.forEach(id => requestedIDs.current.add(id))
          observedJobs.current.set(job.id, version)
        }
      })
      const existing = new Map(retained.current.runs.map(run => [run.id, run]))
      const ids = observations.flatMap(job => job.ids.filter(id => !existing.has(id) || isRunActive(existing.get(id)!.status)))
      ids.push(...requestedIDs.current)
      ids.push(...retained.current.runs.filter(run => isRunActive(run.status)).map(run => run.id))
      if (preferredRunID && !existing.has(preferredRunID)) ids.push(preferredRunID)
      await refreshIDs(ids)
      if (active && (requestedIDs.current.size > 0 || ids.some(id => !retained.current.runs.some(run => run.id === id)) ||
        retained.current.runs.some(run => isRunActive(run.status)))) timer = window.setTimeout(() => void read(), 2000)
    }
    void read()
    return () => { active = false; window.clearTimeout(timer) }
  }, [jobRuns, preferredRunID, refreshIDs, activeIDs])
  const snapshot: WorkspaceSnapshot = { schema_version: 1, plans: [], runs: page.runs }
  return { snapshot, pending, error: error || refreshError, hasMore: !!page.next_cursor, loadMore: () => read(true), refresh: () => read(false), refreshIDs }
}
