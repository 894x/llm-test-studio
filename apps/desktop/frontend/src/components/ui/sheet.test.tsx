import { fireEvent, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it } from "vitest"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "./sheet"
import { SearchableSelect } from "./searchable-select"

it("settles its own entrance animation and keeps nested selects inside the same open sheet", async () => {
  const user = userEvent.setup()
  render(<Sheet><SheetTrigger>Open editor</SheetTrigger><SheetContent aria-describedby={undefined}>
    <SheetTitle>Editor</SheetTitle>
    <SearchableSelect aria-label="Case type" value="a" onValueChange={() => {}} options={[{ value: "a", label: "Alpha" }]} />
  </SheetContent></Sheet>)
  await user.click(screen.getByText("Open editor"))
  const dialog = screen.getByRole("dialog")
  // JSDOM lacks AnimationEvent; React selects the WebKit event name here.
  fireEvent(screen.getByRole("combobox"), new Event("webkitAnimationEnd", { bubbles: true }))
  expect(dialog).not.toHaveClass("data-open:animate-none")
  fireEvent(dialog, new Event("webkitAnimationEnd", { bubbles: true }))
  expect(dialog).toHaveClass("data-open:animate-none")
  await user.click(screen.getByRole("combobox"))
  expect(screen.getByRole("option", { name: "Alpha" })).toBeInTheDocument()
  expect(screen.getByRole("dialog")).toBe(dialog)
  await user.click(screen.getByRole("option", { name: "Alpha" }))
  expect(dialog).toHaveAttribute("data-state", "open")
  await user.click(screen.getByRole("button", { name: "关闭" }))
  await user.click(screen.getByText("Open editor"))
  expect(screen.getByRole("dialog")).not.toHaveClass("data-open:animate-none")
})
