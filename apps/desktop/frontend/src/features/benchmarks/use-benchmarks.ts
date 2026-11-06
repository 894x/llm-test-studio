import { useCallback, useEffect, useRef, useState } from "react"
import { isBenchmarkActive, type BenchmarkClient, type BenchmarkRun } from "./data"

export function useBenchmarks(client: Partial<BenchmarkClient>) {
  const [runs, setRuns] = useState<BenchmarkRun[]>([])
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [next, setNext] = useState("")
  const nextRef = useRef("")
  const loaded = useRef(false)
  const current = useRef(runs)
  const alive = useRef(false)
  const request = useRef<Promise<void> | null>(null)
  const receive = useCallback((incoming: BenchmarkRun[]) => {
    setRuns(existing => {
      const map = new Map(existing.map(run => [run.id, run]))
      incoming.forEach(run => {
        const old = map.get(run.id)
        if (old && !isBenchmarkActive(old.status) && isBenchmarkActive(run.status)) return
        map.set(run.id, run)
      })
      const merged = [...map.values()].sort((a, b) => b.id.localeCompare(a.id))
      current.current = merged
      return merged
    })
  }, [])
  const read = useCallback((more: boolean) => {
    if (!client.getBenchmarks) return Promise.resolve()
    if (request.current) return request.current
    if (more && !nextRef.current) return Promise.resolve()
    setPending(true)
    const operation = (async () => {
      try {
        const page = await client.getBenchmarks!(more ? nextRef.current : "")
        const oldActive = current.current.filter(run => isBenchmarkActive(run.status) && !page.runs.some(item => item.id === run.id))
        const refreshed = client.getBenchmark ? await Promise.all(oldActive.map(run => client.getBenchmark!(run.id))) : []
        if (!alive.current) return
        receive([...page.runs, ...refreshed])
        if (more || !loaded.current) {
          nextRef.current = page.next_cursor
          setNext(page.next_cursor)
        }
        loaded.current = true
        setError(null)
      } catch (reason) { if (alive.current) setError(reason) }
      finally { if (alive.current) setPending(false); request.current = null }
    })()
    request.current = operation
    return operation
  }, [client, receive])
  useEffect(() => {
    alive.current = true
    void read(false)
    const timer = window.setInterval(() => void read(false), 2000)
    return () => { alive.current = false; window.clearInterval(timer) }
  }, [read])
  return { runs, pending, error, next, receive, refresh: useCallback(() => read(false), [read]), loadMore: useCallback(() => read(true), [read]) }
}
