import { useEffect, useMemo, useRef, useState } from "react"
import { isJobTerminal, type JobPage, type JobView } from "@/features/jobs/data"
import type { JobClient } from "@/features/jobs/execution"

// Read only the history pages the user has requested. Cursor pages are merged by
// identity because a newly accepted execution can also occur in a loaded page.
export function useExecutionJobs(client: JobClient, preferredJob: JobView | null) {
  // Re-entering a hidden page may overlap a read started before it was hidden.
  const pendingReads = useRef<{ client: JobClient; requests: Map<string, Promise<JobPage>> } | null>(null)
  const [cursors, setCursors] = useState([""])
  const [pages, setPages] = useState<Map<string, JobPage>>(() => new Map())
  const [error, setError] = useState<unknown>(null)
  const [pending, setPending] = useState(true)
  const [refresh, setRefresh] = useState(0)
  const retained = useRef(pages)
  const observedRefresh = useRef(-1)
  const [observedPreferred, setObservedPreferred] = useState<JobView | null>(null)
  const preferredObservation = useRef<JobView | null>(null)
  useEffect(() => {
    if (pendingReads.current?.client !== client) {
      pendingReads.current = { client, requests: new Map() }
    }
    const requests = pendingReads.current.requests
    const readPage = (cursor: string) => {
      let request = requests.get(cursor)
      if (!request) {
        request = Promise.resolve().then(() => client.getJobs(cursor, 25))
          .finally(() => { requests.delete(cursor) })
        requests.set(cursor, request)
      }
      return request
    }
    let active = true
    let timer: number | undefined
    const read = async (initial = false) => {
      const manual = observedRefresh.current !== refresh
      observedRefresh.current = refresh
      const needed = cursors.filter(cursor => manual || !retained.current.has(cursor))
      const reads = await Promise.allSettled(needed.map(readPage))
      if (!active) return
      const next = new Map(retained.current)
      reads.forEach((read, index) => { if (read.status === "fulfilled") next.set(needed[index], read.value) })
      const byID = new Map([...next.values()].flatMap(page => page.jobs.map(job => [job.job_id, job] as const)))
      if (preferredJob && !byID.has(preferredJob.job_id)) byID.set(preferredJob.job_id, preferredObservation.current?.job_id === preferredJob.job_id ? preferredObservation.current : preferredJob)
      const pendingJobs = [...byID.values()].filter(job => !job.execution || !isJobTerminal(job.execution.status) ||
        (job.kind === "performance" && !job.performance_result_ready))
      // Newly fetched pages already contain current observations. Subsequent
      // ticks read only unsettled jobs, regardless of the loaded history size.
      const updates = !initial || needed.length === 0
        ? await Promise.allSettled(pendingJobs.map(job => client.getJob(job.job_id))) : []
      if (!active) return
      const changed = new Map(updates.flatMap(read => read.status === "fulfilled" ? [[read.value.job_id, read.value] as const] : []))
      if (preferredJob && changed.has(preferredJob.job_id)) {
        preferredObservation.current = changed.get(preferredJob.job_id)!
        setObservedPreferred(preferredObservation.current)
      }
      for (const [cursor, page] of next) next.set(cursor, { ...page, jobs: page.jobs.map(job => changed.get(job.job_id) ?? job) })
      retained.current = next
      setPages(next)
      const failed = [...reads, ...updates].find(read => read.status === "rejected")
      setError(failed?.status === "rejected" ? failed.reason : null)
      setPending(false)
      if (pendingJobs.length || failed) timer = window.setTimeout(() => void read(), 2_000)
    }
    void read(true)
    return () => { active = false; window.clearTimeout(timer) }
  }, [client, cursors, refresh, preferredJob])

  const jobs = useMemo(() => {
    const byID = new Map<string, JobView>()
    for (const cursor of cursors) for (const job of pages.get(cursor)?.jobs ?? []) {
      const previous = byID.get(job.job_id)
      // A failed page read can retain an older copy of an overlapping record.
      const sequence = job.execution?.latest_sequence ?? 0
      const previousSequence = previous?.execution?.latest_sequence ?? 0
      const delivery = job.submission?.delivery_revision ?? 0
      const previousDelivery = previous?.submission?.delivery_revision ?? 0
      const newerDelivery = delivery > previousDelivery || (delivery === previousDelivery &&
        (!previous?.performance_result_ready || job.performance_result_ready))
      if (!previous || sequence > previousSequence || (sequence === previousSequence && newerDelivery)) {
        byID.set(job.job_id, job)
      }
    }
    if (preferredJob && !byID.has(preferredJob.job_id)) byID.set(preferredJob.job_id, observedPreferred?.job_id === preferredJob.job_id ? observedPreferred : preferredJob)
    return [...byID.values()]
  }, [cursors, pages, preferredJob, observedPreferred])
  const last = pages.get(cursors[cursors.length - 1])
  const hasMore = !!last?.has_more
  return {
    jobs, pending, error, hasMore,
    retry: () => { setPending(true); setRefresh(value => value + 1) },
    loadMore: () => {
      if (!last?.has_more || cursors.includes(last.next_cursor)) return
      setPending(true)
      setCursors(previous => [...previous, last.next_cursor])
    },
  }
}
