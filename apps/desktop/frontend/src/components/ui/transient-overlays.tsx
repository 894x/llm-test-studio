import { useMemo, type ReactNode } from "react"
import { flushSync } from "react-dom"
import { RegistryContext, OverlayContext, type OverlayRegistry, type OverlayVisibility } from "./use-transient-overlay"

export function TransientOverlayProvider({ children }: { children: ReactNode }) {
  const registry = useMemo<OverlayRegistry>(() => {
    const closers = new Set<() => void>()
    return {
      register(close) {
        closers.add(close)
        return () => { closers.delete(close) }
      },
      dismiss() {
        // Finish closing portals and releasing modal locks before Activity hides
        // the page and disconnects its effects (including animation listeners).
        flushSync(() => { [...closers].forEach(close => close()) })
      },
    }
  }, [])
  return <RegistryContext value={registry}>{children}</RegistryContext>
}

export function TransientOverlayVisibility({ value, children }: { value: OverlayVisibility; children: ReactNode }) {
  return <OverlayContext value={value}>{children}</OverlayContext>
}
