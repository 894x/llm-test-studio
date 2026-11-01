import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState } from "react"
import { expect, it, vi } from "vitest"
import { SearchableSelect } from "./searchable-select"
import { Autocomplete, AutocompleteContent, AutocompleteInput, AutocompleteItem, AutocompleteList } from "./autocomplete"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./dropdown-menu"

const options = Array.from({ length: 30 }, (_, index) => ({ value: String(index), label: `Channel ${index + 1}` }))

it("keeps long select navigation, filtering, selection and Escape on the composed viewport", async () => {
  const user = userEvent.setup()
  const change = vi.fn()
  render(<SearchableSelect aria-label="Channel" value="0" onValueChange={change} options={options} />)
  const input = screen.getByRole("combobox", { name: "Channel" })
  await user.click(input)
  expect(screen.getByRole("listbox")).toHaveAttribute("data-radix-scroll-area-viewport")
  await user.keyboard("{ArrowDown}".repeat(29) + "{Enter}")
  expect(change).toHaveBeenCalledWith("29")
  await user.clear(input)
  await user.type(input, "Channel 30")
  expect(screen.getAllByRole("option")).toHaveLength(1)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("listbox")).not.toBeInTheDocument())
  expect(input).toHaveFocus()
})

it("keeps autocomplete suggestions keyboard reachable while accepting arbitrary text", async () => {
  const user = userEvent.setup()
  function Suggestions() {
    const [value, setValue] = useState("")
    return <Autocomplete openOnInputClick items={options.map(option => option.label)} value={value} onValueChange={setValue}>
      <AutocompleteInput aria-label="Model" />
      <AutocompleteContent><AutocompleteList>{(item: string) => <AutocompleteItem key={item} value={item}>{item}</AutocompleteItem>}</AutocompleteList></AutocompleteContent>
    </Autocomplete>
  }
  render(<Suggestions />)
  const input = screen.getByRole("combobox", { name: "Model" })
  await user.click(input)
  expect(screen.getByRole("listbox")).toHaveAttribute("data-radix-scroll-area-viewport")
  await user.keyboard("{ArrowDown}".repeat(30) + "{Enter}")
  expect(input).toHaveValue("Channel 30")
  await user.clear(input)
  await user.type(input, "custom-model")
  await user.keyboard("{Escape}")
  expect(input).toHaveValue("custom-model")
})

it("keeps long menus keyboard navigable and restores focus after selection", async () => {
  const user = userEvent.setup()
  const select = vi.fn()
  render(<DropdownMenu><DropdownMenuTrigger>Actions</DropdownMenuTrigger>
    <DropdownMenuContent>{options.map(option => <DropdownMenuItem key={option.value} onSelect={() => select(option.value)}>{option.label}</DropdownMenuItem>)}</DropdownMenuContent>
  </DropdownMenu>)
  const trigger = screen.getByRole("button", { name: "Actions" })
  await user.click(trigger)
  await user.keyboard("{End}")
  expect(screen.getByRole("menuitem", { name: "Channel 30" })).toHaveFocus()
  await user.keyboard("{Enter}")
  expect(select).toHaveBeenCalledWith("29")
  await waitFor(() => expect(trigger).toHaveFocus())
})
