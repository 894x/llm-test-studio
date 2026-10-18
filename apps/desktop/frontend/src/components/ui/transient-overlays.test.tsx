import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState, type ReactNode } from "react"
import { expect, it, vi } from "vitest"
import { RetainedWorkspace } from "@/features/shell/retained-workspace"
import { TransientOverlayProvider } from "./transient-overlays"
import { useDismissTransientOverlays } from "./use-transient-overlay"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./tooltip"
import { Sheet, SheetContent, SheetDescription, SheetTitle, SheetTrigger } from "./sheet"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./dropdown-menu"
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogTitle, AlertDialogTrigger } from "./alert-dialog"
import { SearchableSelect } from "./searchable-select"
import { Autocomplete, AutocompleteContent, AutocompleteInput, AutocompleteItem, AutocompleteList } from "./autocomplete"

function Navigation({ children, chrome }: { children: ReactNode; chrome?: ReactNode }) {
  const [active, setActive] = useState(true)
  const dismiss = useDismissTransientOverlays()
  return <>
    <button onClick={() => { dismiss?.(); setActive(value => !value) }}>Switch menu</button>
    {chrome}
    <RetainedWorkspace active={active} fallback="Loading"><main>{children}</main></RetainedWorkspace>
  </>
}

function fixture(children: ReactNode, chrome?: ReactNode) {
  return render(<TransientOverlayProvider><TooltipProvider delayDuration={0}>
    <Navigation chrome={chrome}>{children}</Navigation>
  </TooltipProvider></TransientOverlayProvider>)
}

async function switchMenu() {
  const button = screen.getByText("Switch menu")
  // Programmatic navigation must work even when a modal blocks pointer input.
  fireEvent.click(button)
  await act(async () => {})
}

it("removes portaled tooltips while retaining page data, filters, pagination and scroll", async () => {
  const initialize = vi.fn(() => ["saved data"])
  function Page() {
    const [data] = useState(initialize)
    const [query, setQuery] = useState("")
    const [page, setPage] = useState(1)
    return <div data-testid="scroll-region" style={{ overflow: "auto", height: 100 }}>
      <input aria-label="Filter" value={query} onChange={event => setQuery(event.target.value)} />
      <button onClick={() => setPage(page + 1)}>Next page</button><p>Page {page}: {data[0]}</p>
      <Tooltip><TooltipTrigger>Case name</TooltipTrigger><TooltipContent>Dynamic tools case</TooltipContent></Tooltip>
    </div>
  }
  fixture(<Page />)
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "saved filter" } })
  fireEvent.click(screen.getByText("Next page"))
  const scroll = screen.getByTestId("scroll-region")
  scroll.scrollTop = 240
  fireEvent.focus(screen.getByText("Case name"))
  expect(await screen.findByRole("tooltip")).toBeVisible()
  await switchMenu()
  expect(screen.queryByRole("tooltip", { hidden: true })).not.toBeInTheDocument()
  expect(screen.queryByRole("main")).not.toBeInTheDocument()
  await switchMenu()
  expect(screen.getByRole("textbox")).toHaveValue("saved filter")
  expect(screen.getByText("Page 2: saved data")).toBeVisible()
  expect(screen.getByTestId("scroll-region")).toBe(scroll)
  expect(scroll.scrollTop).toBe(240)
  expect(initialize).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()
  fireEvent.focus(screen.getByText("Case name"))
  expect(await screen.findByRole("tooltip")).toBeVisible()
  fireEvent.keyDown(document, { key: "Escape" })
  await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument())
})

it.each(["sheet", "confirmation", "menu"] as const)("dismisses a controlled %s and releases its modal locks", async kind => {
  const onClose = vi.fn()
  function Overlay() {
    const [open, setOpen] = useState(false)
    const change = (value: boolean) => { setOpen(value); if (!value) onClose() }
    if (kind === "sheet") return <>
      <button onClick={() => setOpen(true)}>Open overlay</button>
      <Sheet open={open} onOpenChange={change}><SheetContent><SheetTitle>Editor</SheetTitle><SheetDescription>Details</SheetDescription></SheetContent></Sheet>
    </>
    if (kind === "confirmation") return <AlertDialog open={open} onOpenChange={change}>
      <AlertDialogTrigger>Open overlay</AlertDialogTrigger><AlertDialogContent><AlertDialogTitle>Confirm</AlertDialogTitle><AlertDialogDescription>Details</AlertDialogDescription><AlertDialogCancel>Cancel</AlertDialogCancel></AlertDialogContent>
    </AlertDialog>
    return <DropdownMenu open={open} onOpenChange={change}><DropdownMenuTrigger>Open overlay</DropdownMenuTrigger><DropdownMenuContent><DropdownMenuItem>Action</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
  }
  const user = userEvent.setup()
  fixture(<Overlay />)
  const role = kind === "sheet" ? "dialog" : kind === "confirmation" ? "alertdialog" : "menu"
  await user.click(screen.getByText("Open overlay"))
  expect(await screen.findByRole(role)).toBeVisible()
  // A real hash/programmatic transition does not need to click through the modal.
  fireEvent.click(screen.getByText("Switch menu"))
  await waitFor(() => expect(screen.queryByRole(role, { hidden: true })).not.toBeInTheDocument())
  expect(document.body.style.pointerEvents).not.toBe("none")
  expect(document.body).not.toHaveAttribute("data-scroll-locked")
  expect(onClose).toHaveBeenCalledTimes(1)
  await switchMenu()
  expect(screen.queryByRole(role)).not.toBeInTheDocument()
  await user.click(screen.getByText("Open overlay"))
  expect(await screen.findByRole(role)).toBeVisible()
})

it.each(["select", "autocomplete"] as const)("closes %s suggestions without clearing the value", async kind => {
  function Field() {
    const [value, setValue] = useState("Alpha")
    if (kind === "select") return <SearchableSelect aria-label="Choice" value={value} onValueChange={setValue} options={[{ value: "Alpha", label: "Alpha" }, { value: "Beta", label: "Beta" }]} />
    return <Autocomplete items={["Alpha", "Beta"]} value={value} onValueChange={setValue}>
      <AutocompleteInput aria-label="Choice" /><AutocompleteContent><AutocompleteList>{(item: string) => <AutocompleteItem key={item} value={item}>{item}</AutocompleteItem>}</AutocompleteList></AutocompleteContent>
    </Autocomplete>
  }
  const user = userEvent.setup()
  fixture(<Field />)
  await user.click(screen.getByRole("button", { name: "显示候选" }))
  expect(await screen.findByRole("listbox")).toBeVisible()
  await switchMenu()
  expect(screen.queryByRole("listbox", { hidden: true })).not.toBeInTheDocument()
  await switchMenu()
  expect(screen.getByRole("combobox", { name: "Choice" })).toHaveValue("Alpha")
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "显示候选" }))
  expect(await screen.findByRole("listbox")).toBeVisible()
})

it("also closes shell tooltips outside retained pages", async () => {
  fixture(<p>Page content</p>, <Tooltip><TooltipTrigger>Header action</TooltipTrigger><TooltipContent>Header help</TooltipContent></Tooltip>)
  fireEvent.focus(screen.getByText("Header action"))
  expect(await screen.findByRole("tooltip")).toBeVisible()
  await switchMenu()
  expect(screen.queryByRole("tooltip", { hidden: true })).not.toBeInTheDocument()
})

it("closes an ordinary tooltip when focus leaves its trigger", async () => {
  const user = userEvent.setup()
  fixture(<><Tooltip><TooltipTrigger>Focus hint</TooltipTrigger><TooltipContent>Focus help</TooltipContent></Tooltip><button>Next control</button></>)
  await user.tab() // Navigation control.
  await user.tab() // Tooltip trigger.
  expect(await screen.findByRole("tooltip")).toBeVisible()
  await user.tab()
  expect(screen.getByText("Next control")).toHaveFocus()
  await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument())
})

it("closes an ordinary tooltip when the pointer leaves its trigger and content", async () => {
  const user = userEvent.setup()
  fixture(<Tooltip><TooltipTrigger>Hover hint</TooltipTrigger><TooltipContent>Hover help</TooltipContent></Tooltip>)
  const trigger = screen.getByText("Hover hint")
  await user.hover(trigger)
  expect(await screen.findByRole("tooltip")).toBeVisible()
  await user.unhover(trigger)
  // jsdom has zero-size anchors; move outside Radix's trigger/content grace area.
  await user.pointer({ target: document.body, coords: { clientX: 500, clientY: 500 } })
  await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument())
})

it.each([false, true])("cancels delayed tooltip opening on navigation (shell: %s)", async shell => {
  const hint = <TooltipProvider delayDuration={250}><Tooltip><TooltipTrigger>Delayed hint</TooltipTrigger><TooltipContent>Late help</TooltipContent></Tooltip></TooltipProvider>
  fixture(shell ? <p>Page content</p> : hint, shell ? hint : undefined)
  await userEvent.setup().hover(screen.getByText("Delayed hint"))
  expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()
  await switchMenu()
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 300)) })
  expect(screen.queryByRole("tooltip", { hidden: true })).not.toBeInTheDocument()
  await switchMenu()
  expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()
})

it("closes nested sheet suggestions together without restoring focus to the hidden page", async () => {
  const restoreFocus = vi.fn()
  fixture(<Sheet><SheetTrigger>Open editor</SheetTrigger>
    <SheetContent onCloseAutoFocus={restoreFocus}><SheetTitle>Editor</SheetTitle><SheetDescription>Details</SheetDescription>
      <SearchableSelect aria-label="Nested choice" value="a" onValueChange={() => {}} options={[{ value: "a", label: "Alpha" }]} />
    </SheetContent>
  </Sheet>)
  const user = userEvent.setup()
  await user.click(screen.getByText("Open editor"))
  await user.click(screen.getByRole("button", { name: "显示候选" }))
  expect(await screen.findByRole("listbox")).toBeVisible()
  await switchMenu()
  await waitFor(() => expect(document.body.style.pointerEvents).not.toBe("none"))
  expect(screen.queryByRole("dialog", { hidden: true })).not.toBeInTheDocument()
  expect(screen.queryByRole("listbox", { hidden: true })).not.toBeInTheDocument()
  expect(restoreFocus).not.toHaveBeenCalled()
  await switchMenu()
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  await user.click(screen.getByText("Open editor"))
  expect(await screen.findByRole("dialog")).toBeVisible()
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument()
})
