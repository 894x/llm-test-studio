import { useEffect, useMemo, useState } from "react"
import type { JobPage, JobView } from "@/features/jobs/data"
import type { JobClient } from "@/features/jobs/execution"

// Read only the history pages the user has requested. Cursor pages are merged by
// identity because a newly accepted execution can also occur in a loaded page.
export function useExecutionJobs(client: JobClient, preferredJob: JobView | null) {
  const [cursors, setCursors] = useState([""])
  const [pages, setPages] = useState<Map<string, JobPage>>(() => new Map())
  const [error, setError] = useState<unknown>(null)
  const [pending, setPending] = useState(true)
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    let active = true
    let timer: number | undefined
    const read = async () => {
      const reads = await Promise.allSettled(cursors.map(cursor => client.getJobs(cursor, 25)))
      if (!active) return
      setPages(previous => {
        const next = new Map(previous)
        reads.forEach((read, index) => { if (read.status === "fulfilled") next.set(cursors[index], read.value) })
        return next
      })
      const failed = reads.find(read => read.status === "rejected")
      setError(failed?.status === "rejected" ? failed.reason : null)
      setPending(false)
      timer = window.setTimeout(() => void read(), 2_000)
    }
    void read()
    return () => { active = false; window.clearTimeout(timer) }
  }, [client, cursors, refresh, preferredJob?.job_id])

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
    if (preferredJob && !byID.has(preferredJob.job_id)) byID.set(preferredJob.job_id, preferredJob)
    return [...byID.values()]
  }, [cursors, pages, preferredJob])
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
