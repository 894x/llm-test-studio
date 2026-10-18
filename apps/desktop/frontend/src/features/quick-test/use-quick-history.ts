import { useCallback, useEffect, useRef, useState } from "react"
import type { DesktopClient } from "@/app/desktop-client"
import { isRunActive } from "@/features/runs/data"
import type { QuickHistoryPage } from "./history-data"

const empty: QuickHistoryPage = { schema_version: 1, runs: [], reports: [], next_cursor: "" }

// The retained workspace owns this state. A hidden Activity stops effects while
// retaining records, the cursor, selection and the existing scroll viewport.
export function useQuickHistory(client: Pick<DesktopClient, "getQuickTestHistory" | "getRunSummaries">) {
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

  const read = useCallback(function readPage(append: boolean) {
    if (flight.current) return flight.current
    if (append && !state.current.data.next_cursor) return Promise.resolve()
    if (!append) invalidated.current = false
    setLoading(true)
    setError(false)
    const operation = async () => {
      try {
        let after = append ? state.current.data.next_cursor : ""
        const pages: QuickHistoryPage[] = []
        const count = 1
        for (let index = 0; index < count; index++) {
          const page = await client.getQuickTestHistory(after)
          if (page.next_cursor && page.next_cursor === after) throw new Error("Non-advancing history cursor")
          pages.push(page)
          after = page.next_cursor
          if (!after) break
        }
        if (!append) {
          const ids = [...new Set([...requestedIDs.current, ...state.current.data.runs.filter(run => isRunActive(run.status)).map(run => run.id)])]
          for (let offset = 0; offset < ids.length; offset += 100) {
            const updated = await client.getRunSummaries(ids.slice(offset, offset + 100))
            updated.runs.forEach(run => requestedIDs.current.delete(run.id))
            pages.push({ schema_version: 1, runs: updated.runs.filter(run => run.source === "quick_task"), reports: [], next_cursor: "" })
          }
          const known = new Set([...state.current.data.runs, ...state.current.data.reports].map(item => item.id))
          const head = pages[0]
          if ([...head.runs, ...head.reports].some(item => known.has(item.id))) after = state.current.data.next_cursor
        }
        // Merge by identity: refresh and new submissions must not erase loaded history.
        const runs = new Map(state.current.data.runs.map(run => [run.id, run]))
        const reports = new Map(state.current.data.reports.map(report => [report.id, report]))
        pages.forEach(page => {
          page.runs.forEach(run => runs.set(run.id, run))
          page.reports.forEach(report => reports.set(report.id, report))
        })
        const next: QuickHistoryPage = { schema_version: 1, runs: [...runs.values()], reports: [...reports.values()], next_cursor: after }
        state.current = { data: next, pages: append ? state.current.pages + 1 : pages.length }
        setData(next)
        setLoaded(true)
      } catch {
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
    ids.forEach(id => requestedIDs.current.add(id))
    if (flight.current) { invalidated.current = true; return }
    void read(false)
  }, [read])
  useEffect(() => {
    visible.current = true
    void refresh()
    return () => { visible.current = false }
  }, [refresh])
  const active = data.runs.some(run => isRunActive(run.status))
  useEffect(() => {
    if (!active) return
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => window.clearInterval(timer)
  }, [active, refresh])
  return { data, loaded, loading, error, refresh, loadMore, retry, invalidate }
}
