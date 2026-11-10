import { useCallback, useEffect, useRef, useState } from "react"
import type { BenchmarkClient, BenchmarkDatasetInventory } from "./data"

export function useDatasetInventory(client: Partial<BenchmarkClient>) {
  const [inventory, setInventory] = useState<BenchmarkDatasetInventory | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [pending, setPending] = useState(false)
  const alive = useRef(false), reading = useRef(false)
  const refresh = useCallback(async () => {
    if (!client.getBenchmarkDatasets || reading.current) return
    reading.current = true; setPending(true)
    try {
      const next = await client.getBenchmarkDatasets()
      if (alive.current) { setInventory(next); setError(null) }
    } catch (reason) { if (alive.current) setError(reason) }
    finally { reading.current = false; if (alive.current) setPending(false) }
  }, [client])
  useEffect(() => {
    alive.current = true
    void Promise.resolve().then(refresh)
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => { alive.current = false; window.clearInterval(timer) }
  }, [refresh])
  return { inventory, pending, error, refresh }
}
