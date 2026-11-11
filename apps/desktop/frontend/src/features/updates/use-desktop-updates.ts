import { useCallback, useEffect, useRef, useState } from "react"
import type { DesktopUpdate, DesktopUpdateClient } from "./data"

export function useDesktopUpdates(client: DesktopUpdateClient) {
  const [snapshot, setSnapshot] = useState<DesktopUpdate | null>(null)
  const [pending, setPending] = useState(false)
  const [readError, setReadError] = useState(false)
  const generation = useRef(0)
  const alive = useRef(true)
  const invalidate = useCallback(() => { generation.current++ }, [])
  const run = useCallback(async (command: () => Promise<DesktopUpdate>) => {
    const current = ++generation.current
    setPending(true)
    setReadError(false)
    try {
      const next = await command()
      if (alive.current && current === generation.current) setSnapshot(next)
    } catch {
      if (alive.current && current === generation.current) setReadError(true)
    } finally {
      if (alive.current && current === generation.current) setPending(false)
    }
  }, [])

  useEffect(() => {
    alive.current = true
    let active = true
    // Defer startup checks so StrictMode's discarded effect never issues a
    // second network check or competes with the retained effect's snapshot.
    const timer = setTimeout(() => {
      if (!active) return
      void run(async () => {
        const first = await client.getDesktopUpdate()
        if (active && first.auto_check && first.status === "idle") return client.checkDesktopUpdate()
        return first
      })
    }, 0)
    return () => { active = false; alive.current = false; clearTimeout(timer); invalidate() }
  }, [client, run, invalidate])

  useEffect(() => {
    if (snapshot?.status !== "downloading" || pending) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      const current = generation.current
      try {
        const next = await client.getDesktopUpdate()
        if (active && current === generation.current) {
          setSnapshot(next)
          setReadError(false)
        }
      } catch { if (active) setReadError(true) }
      if (active) timer = setTimeout(() => void poll(), 500)
    }
    timer = setTimeout(() => void poll(), 500)
    return () => { active = false; clearTimeout(timer) }
  }, [client, snapshot?.status, pending])

  return { snapshot, pending, readError, run, client }
}
export type DesktopUpdates = ReturnType<typeof useDesktopUpdates>
