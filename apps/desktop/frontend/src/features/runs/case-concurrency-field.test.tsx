import { useState } from "react"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it } from "vitest"
import { CaseConcurrencyField } from "./case-concurrency-field"

it("offers serial and bounded concurrency presets with keyboard dismissal", async () => {
  const user = userEvent.setup()
  function Harness() {
    const [value, setValue] = useState(4)
    return <CaseConcurrencyField value={value} onChange={setValue} />
  }
  render(<Harness />)
  const control = screen.getByRole("combobox", { name: "Case 并发数" })
  expect(control).toHaveValue("4 个并发")
  for (const label of ["1 · 串行", "2 个并发", "4 个并发", "8 个并发"]) {
    await user.click(control)
    expect(screen.getAllByRole("option")).toHaveLength(4)
    await user.click(screen.getByRole("option", { name: label }))
    expect(control).toHaveValue(label)
  }
  await user.click(control)
  await user.keyboard("{Escape}")
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument()
  expect(control).toHaveFocus()
})

it("shows a restored effective limit and disables edits while pending", async () => {
  const user = userEvent.setup()
  const view = render(<CaseConcurrencyField value={3} onChange={() => {}} />)
  const control = screen.getByRole("combobox", { name: "Case 并发数" })
  expect(control).toHaveValue("3 个并发")
  await user.click(control)
  expect(screen.getByRole("option", { name: "3 个并发" })).toHaveAttribute("aria-selected", "true")
  await user.keyboard("{Escape}")
  view.rerender(<CaseConcurrencyField value={3} onChange={() => {}} disabled />)
  expect(control).toBeDisabled()
})

it("can associate a full-row hint without duplicating it inside the field", () => {
  render(<>
    <CaseConcurrencyField value={4} onChange={() => {}} externalDescriptionId="shared-hint" />
    <p id="shared-hint">全行并发说明</p>
  </>)
  expect(screen.getByRole("combobox", { name: "Case 并发数" })).toHaveAccessibleDescription("全行并发说明")
  expect(screen.queryByText(/套件内同时执行的 Case 数量/)).not.toBeInTheDocument()
})
