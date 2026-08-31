import "@testing-library/jest-dom/vitest"
import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

afterEach(cleanup)

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }),
})

Object.defineProperty(HTMLElement.prototype, "hasPointerCapture", {
  value: () => false,
})

Object.defineProperty(HTMLElement.prototype, "setPointerCapture", {
  value: () => undefined,
})

Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", {
  value: () => undefined,
})

Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
  value: () => undefined,
})

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(window, "ResizeObserver", {
  writable: true,
  value: ResizeObserverStub,
})
