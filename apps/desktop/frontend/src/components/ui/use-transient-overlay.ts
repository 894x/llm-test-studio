import { createContext, useContext, useLayoutEffect, useMemo, useReducer, useRef, useState } from "react"

export type OverlayRegistry = {
  register: (close: () => void) => () => void
  dismiss: () => void
}
export type OverlayVisibility = { dismissed: boolean; wasDismissed: () => boolean }
export const RegistryContext = createContext<OverlayRegistry | null>(null)
export const OverlayContext = createContext<OverlayVisibility>({ dismissed: false, wasDismissed: () => false })

export function useDismissTransientOverlays() {
  return useContext(RegistryContext)?.dismiss
}

export function useTransientOverlay({ open, defaultOpen = false, onDismiss }: {
  open?: boolean
  defaultOpen?: boolean
  onDismiss?: () => void
} = {}) {
  const registry = useContext(RegistryContext)
  const [localOpen, setLocalOpen] = useState(defaultOpen)
  const [dismissed, setDismissed] = useState(false)
  const [dismissalKey, reset] = useReducer(value => value + 1, 0)
  const [previousOpen, setPreviousOpen] = useState(open)
  if (previousOpen !== open) {
    setPreviousOpen(open)
    // A new controlled open request may come from a button outside the Root.
    if (open) setDismissed(false)
  }
  const dismissedRef = useRef(false)
  const current = useRef({ open: open ?? localOpen, onDismiss })
  useLayoutEffect(() => {
    dismissedRef.current = dismissed
    current.current = { open: !dismissed && (open ?? localOpen), onDismiss }
  })
  useLayoutEffect(() => registry?.register(() => {
    dismissedRef.current = true
    reset() // Tooltip uses this to cancel its private hover-delay timer.
    setDismissed(true)
    setLocalOpen(false)
    if (current.current.open) current.current.onDismiss?.()
  }), [registry])

  return {
    dismissalKey,
    open: !dismissed && (open ?? localOpen),
    setOpen(next: boolean) {
      dismissedRef.current = false
      setDismissed(false)
      setLocalOpen(next)
    },
    visibility: useMemo(() => ({ dismissed, wasDismissed: () => dismissedRef.current }), [dismissed]),
  }
}

export function useTransientOverlayVisibility() { return useContext(OverlayContext) }
