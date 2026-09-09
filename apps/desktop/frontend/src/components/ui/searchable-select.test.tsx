import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import { SearchableSelect } from "./searchable-select"

it("filters labels and commits only an enabled option", async () => {
  const user = userEvent.setup()
  const change = vi.fn()
  const { rerender } = render(<SearchableSelect aria-label="渠道" value="a" onValueChange={change} options={[
    { value: "a", label: "Alpha" }, { value: "b", label: "Beta" },
    { value: "c", label: "Blocked", disabled: true },
  ]} />)
  const input = screen.getByRole("combobox", { name: "渠道" })
  await user.clear(input)
  await user.type(input, "no-match")
  expect(screen.getByText("没有匹配的选项")).toBeInTheDocument()
  await user.keyboard("{Enter}")
  expect(change).not.toHaveBeenCalled()
  await user.clear(input)
  await user.type(input, "Beta")
  rerender(<SearchableSelect aria-label="渠道" value="a" onValueChange={change} options={[
    { value: "a", label: "Alpha" }, { value: "b", label: "Beta" },
  ]} />)
  expect(input).toHaveValue("Beta")
  await user.click(screen.getByRole("option", { name: "Beta" }))
  expect(change).toHaveBeenCalledExactlyOnceWith("b")
})
