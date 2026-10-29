import { useCallback, useEffect, useRef, useState } from "react"
import type { DesktopClient } from "@/app/desktop-client"
import { isRunActive } from "@/features/runs/data"
import type { QuickHistoryPage } from "./history-data"
import type { HistoryChanged } from "@/features/history-recycle/data"

const empty: QuickHistoryPage = { schema_version: 1, runs: [], reports: [], next_cursor: "" }

// The retained workspace owns this state. A hidden Activity stops effects while
// retaining records, the cursor, selection and the existing scroll viewport.
export function useQuickHistory(client: Pick<DesktopClient, "getQuickTestHistory" | "getRunSummaries" | "subscribeRemoteResults">) {
  const [data, setData] = useState(empty)
  const [loaded, setLoaded] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const state = useRef({ data: empty, pages: 0 })
  const flight = useRef<Promise<void> | null>(null)
  const failedAppend = useRef(false)
  const invalidated = useRef(false)
  const requestedIDs = useRef(new Set<string>())
  const visible = useRef(true)
  const hidden = useRef({ runs: new Set<string>(), reports: new Set<string>() })
  const restorePages = useRef(1)

  const read = useCallback(function readPage(append: boolean) {
    if (flight.current) return flight.current
    if (append && !state.current.data.next_cursor) return Promise.resolve()
    if (!append) invalidated.current = false
    setLoading(true)
    setError(false)
    const operation = async () => {
      const requestedPages = append ? 1 : restorePages.current
      try {
        let after = append ? state.current.data.next_cursor : ""
        const pages: QuickHistoryPage[] = []
        const count = requestedPages
        if (!append) restorePages.current = 1
        for (let index = 0; index < count; index++) {
          const page = await client.getQuickTestHistory(after)
          if (page.next_cursor && page.next_cursor === after) throw new Error("Non-advancing history cursor")
          pages.push(page)
          after = page.next_cursor
          if (!after) break
        }
        if (!append) {
          // Terminal execution can precede report import, including runs beyond
          // the first history page. Keep those identities in the bounded refresh.
          const ids = [...new Set([...requestedIDs.current, ...state.current.data.runs.filter(run => isRunActive(run.status) || !run.report_id).map(run => run.id)])].filter(id => !hidden.current.runs.has(id))
          for (let offset = 0; offset < ids.length; offset += 100) {
            const updated = await client.getRunSummaries(ids.slice(offset, offset + 100))
            updated.runs.forEach(run => requestedIDs.current.delete(run.id))
            pages.push({ schema_version: 1, runs: updated.runs.filter(run => run.source === "quick_task"), reports: [], next_cursor: "" })
          }
          const known = new Set([...state.current.data.runs, ...state.current.data.reports].map(item => item.id))
          const head = pages[0]
          if (count === 1 && [...head.runs, ...head.reports].some(item => known.has(item.id))) after = state.current.data.next_cursor
        }
        // Merge by identity: refresh and new submissions must not erase loaded history.
        const runs = new Map(state.current.data.runs.map(run => [run.id, run]))
        const reports = new Map(state.current.data.reports.map(report => [report.id, report]))
        pages.forEach(page => {
          page.runs.forEach(run => { if (!hidden.current.runs.has(run.id)) runs.set(run.id, run) })
          page.reports.forEach(report => { if (!hidden.current.reports.has(report.id)) reports.set(report.id, report) })
        })
        const next: QuickHistoryPage = { schema_version: 1, runs: [...runs.values()].filter(run => !hidden.current.runs.has(run.id)), reports: [...reports.values()].filter(report => !hidden.current.reports.has(report.id)), next_cursor: after }
        state.current = { data: next, pages: append ? state.current.pages + 1 : Math.max(state.current.pages, Math.min(count, pages.length)) }
        setData(next)
        setLoaded(true)
      } catch {
        if (!append) restorePages.current = Math.max(restorePages.current, requestedPages)
        failedAppend.current = append
        setError(true)
      } finally {
        setLoading(false)
        flight.current = null
        if (invalidated.current && visible.current) {
          invalidated.current = false
          void readPage(false)
        }
      }
    }
    const pending = Promise.resolve().then(operation)
    flight.current = pending
    return pending
  }, [client])
  const refresh = useCallback(() => read(false), [read])
  const loadMore = useCallback(() => read(true), [read])
  const retry = useCallback(() => read(failedAppend.current), [read])
  const invalidate = useCallback((ids: string[] = []) => {
    ids.filter(id => !hidden.current.runs.has(id)).forEach(id => requestedIDs.current.add(id))
    if (!visible.current || flight.current) { invalidated.current = true; return }
    void read(false)
  }, [read])
  const applyHistoryChange = useCallback<HistoryChanged>((entry, action) => {
    entry.run_ids.forEach(id => {
      if (action === "recycle") { hidden.current.runs.add(id); requestedIDs.current.delete(id) }
      else hidden.current.runs.delete(id)
    })
    entry.report_ids.forEach(id => { if (action === "recycle") hidden.current.reports.add(id); else hidden.current.reports.delete(id) })
    const next = { ...state.current.data, runs: state.current.data.runs.filter(run => !hidden.current.runs.has(run.id)), reports: state.current.data.reports.filter(report => !hidden.current.reports.has(report.id)) }
    state.current = { ...state.current, data: next }
    setData(next)
    if (action === "restore") {
      // A restored performance report may be older than the head page. Replay
      // only the history windows this workspace has already loaded.
      restorePages.current = Math.max(1, state.current.pages)
      invalidate(entry.run_ids)
    }
  }, [invalidate])
  useEffect(() => {
    visible.current = true
    void refresh()
    return () => { visible.current = false }
  }, [refresh])
  useEffect(() => {
    let active = true
    let timer: number | undefined
    const unsubscribe = client.subscribeRemoteResults(() => {
      if (!active || timer !== undefined) return
      timer = window.setTimeout(() => { timer = undefined; invalidate() }, 100)
    })
    return () => { active = false; window.clearTimeout(timer); unsubscribe() }
  }, [client, invalidate])
  const pending = data.runs.some(run => isRunActive(run.status) || !run.report_id)
  useEffect(() => {
    // Idle discovery also recovers missed events and other clients' results.
    const timer = window.setInterval(() => void refresh(), pending ? 2000 : 10_000)
    return () => window.clearInterval(timer)
  }, [pending, refresh])
  return { data, loaded, loading, error, refresh, loadMore, retry, invalidate, applyHistoryChange }
}
