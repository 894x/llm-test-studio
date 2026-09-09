import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it } from "vitest"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "./sheet"
import { SearchableSelect } from "./searchable-select"
import { ScrollArea } from "./scroll-area"

it("portals nested selects outside the scrolling form while retaining the sheet focus boundary", async () => {
  const user = userEvent.setup()
  render(<Sheet><SheetTrigger>Open editor</SheetTrigger><SheetContent aria-describedby={undefined}>
    <SheetTitle>Editor</SheetTitle>
    <ScrollArea><SearchableSelect aria-label="Case type" value="a" onValueChange={() => {}} options={[{ value: "a", label: "Alpha" }]} /></ScrollArea>
  </SheetContent></Sheet>)
  await user.click(screen.getByText("Open editor"))
  const dialog = screen.getByRole("dialog")
  await user.click(screen.getByRole("combobox"))
  expect(screen.getByRole("option", { name: "Alpha" })).toBeInTheDocument()
  expect(dialog).toContainElement(screen.getByRole("listbox"))
  expect(screen.getByRole("listbox").closest('[data-slot="scroll-area-viewport"]')).toBeNull()
  expect(screen.getByRole("dialog")).toBe(dialog)
  await user.click(screen.getByRole("option", { name: "Alpha" }))
  expect(dialog).toHaveAttribute("data-state", "open")
  await user.click(screen.getByRole("button", { name: "关闭" }))
  await user.click(screen.getByText("Open editor"))
  await user.click(screen.getByRole("combobox"))
  expect(screen.getByRole("dialog")).toContainElement(screen.getByRole("listbox"))
})
