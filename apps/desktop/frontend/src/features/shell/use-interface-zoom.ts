import { useCallback, useEffect, useLayoutEffect, useState } from "react"

const STORAGE_KEY = "llm-test-studio:interface-zoom:v1"
const ZOOM_LEVELS = [80, 90, 100, 110, 125] as const

function loadZoom(): number {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "null")
    if (saved && typeof saved === "object" && "version" in saved && saved.version === 1 &&
      "percent" in saved && ZOOM_LEVELS.some(level => level === saved.percent)) return Number(saved.percent)
  } catch { /* Keep the default when optional interface preferences are unavailable. */ }
  return 100
}

export function useInterfaceZoom() {
  const [percent, setPercent] = useState(loadZoom)
  const [announcement, setAnnouncement] = useState(0)
  const change = useCallback((direction: -1 | 0 | 1) => {
    setPercent(previous => {
      const index = ZOOM_LEVELS.indexOf(previous as typeof ZOOM_LEVELS[number])
      return direction === 0 ? 100 : ZOOM_LEVELS[Math.max(0, Math.min(ZOOM_LEVELS.length - 1, index + direction))]
    })
    setAnnouncement(previous => previous + 1)
  }, [])

  useLayoutEffect(() => {
    const root = document.documentElement
    const factor = String(percent / 100)
    root.style.zoom = factor
    root.style.setProperty("--interface-zoom", factor)
    return () => {
      root.style.removeProperty("zoom")
      root.style.removeProperty("--interface-zoom")
    }
  }, [percent])

  useEffect(() => {
    if (!announcement) return
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ version: 1, percent })) }
    catch { /* The current session can still use zoom without persistent preferences. */ }
  }, [percent, announcement])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.altKey || (!event.ctrlKey && !event.metaKey)) return
      const direction = event.key === "+" || event.key === "=" ? 1 : event.key === "-" ? -1 : event.key === "0" ? 0 : undefined
      if (direction === undefined) return
      event.preventDefault()
      change(direction)
    }
    let lastWheel = 0
    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey || event.defaultPrevented || event.deltaY === 0) return
      event.preventDefault()
      const now = performance.now()
      if (lastWheel && now - lastWheel < 100) return
      lastWheel = now
      change(event.deltaY < 0 ? 1 : -1)
    }
    window.addEventListener("keydown", onKeyDown)
    window.addEventListener("wheel", onWheel, { passive: false })
    return () => {
      window.removeEventListener("keydown", onKeyDown)
      window.removeEventListener("wheel", onWheel)
    }
  }, [change])

  return { percent, announcement, change, canIncrease: percent < 125, canDecrease: percent > 80 }
}

export type InterfaceZoom = ReturnType<typeof useInterfaceZoom>
