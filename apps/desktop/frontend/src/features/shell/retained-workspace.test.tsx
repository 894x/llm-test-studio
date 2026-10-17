import { act, fireEvent, render, screen } from "@testing-library/react"
import { useEffect, useState } from "react"
import { afterEach, expect, it, vi } from "vitest"
import { RetainedWorkspace } from "./retained-workspace"

afterEach(() => vi.useRealTimers())

it("visits lazily, preserves controls and DOM, and pauses effects while hidden", async () => {
  vi.useFakeTimers()
  const poll = vi.fn()
  function Page() {
    const [query, setQuery] = useState("")
    useEffect(() => {
      poll()
      const timer = setInterval(poll, 1000)
      return () => clearInterval(timer)
    }, [])
    return <main><input aria-label="query" value={query} onChange={e => setQuery(e.target.value)} /></main>
  }
  const page = (active: boolean) => <RetainedWorkspace active={active} fallback="Loading"><Page /></RetainedWorkspace>
  const view = render(page(false))
  expect(screen.queryByRole("main", { hidden: true })).not.toBeInTheDocument()
  expect(poll).not.toHaveBeenCalled()
  view.rerender(page(true))
  const input = screen.getByRole("textbox")
  fireEvent.change(input, { target: { value: "saved filter" } })
  expect(poll).toHaveBeenCalledTimes(1)
  view.rerender(page(false))
  expect(input).not.toBeVisible()
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(poll).toHaveBeenCalledTimes(1)
  view.rerender(page(true))
  expect(screen.getByRole("textbox")).toBe(input)
  expect(input).toHaveValue("saved filter")
  expect(poll).toHaveBeenCalledTimes(2)
  expect(screen.queryByText("Loading")).not.toBeInTheDocument()
})
