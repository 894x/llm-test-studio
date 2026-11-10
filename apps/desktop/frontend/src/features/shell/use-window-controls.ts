import { useEffect, useState } from "react"

type NativeWindowRuntime = Partial<{
  WindowMinimise: () => void
  WindowToggleMaximise: () => void
  WindowIsMaximised: () => Promise<boolean>
  Quit: () => void
}>

export function useWindowControls() {
  const runtime = (window as Window & { runtime?: NativeWindowRuntime }).runtime
  const [maximised, setMaximised] = useState(false)

  useEffect(() => {
    const isMaximised = runtime?.WindowIsMaximised
    if (!isMaximised) return
    let active = true
    let request = 0
    const refresh = () => {
      const current = ++request
      void isMaximised().then(value => {
        if (active && current === request) setMaximised(value)
      }).catch(() => {
        // Retain the last confirmed state if the native window is unavailable.
      })
    }
    refresh()
    window.addEventListener("resize", refresh)
    window.addEventListener("focus", refresh)
    return () => {
      active = false
      window.removeEventListener("resize", refresh)
      window.removeEventListener("focus", refresh)
    }
  }, [runtime])

  return {
    maximised,
    minimise: runtime?.WindowMinimise,
    toggleMaximise: runtime?.WindowToggleMaximise,
    close: runtime?.Quit,
  }
}
