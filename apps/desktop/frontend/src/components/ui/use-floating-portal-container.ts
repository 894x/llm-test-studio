import { useMemo, type RefObject } from "react"

type PortalContainer = HTMLElement | ShadowRoot | RefObject<HTMLElement | ShadowRoot | null> | null | undefined

export function useFloatingPortalContainer(container: PortalContainer) {
  return useMemo(() => ({
    get current() {
      const element = container && "current" in container ? container.current : container
      // Resolve after refs attach, inside the modal but outside its scrolling form.
      return element instanceof HTMLElement ? element.closest<HTMLElement>('[data-slot="sheet-content"]') ?? element : element ?? null
    },
  }), [container])
}
