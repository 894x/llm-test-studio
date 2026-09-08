import { useId, useRef, useState } from "react"
import XIcon from "lucide-react/dist/esm/icons/x.mjs"

import { Autocomplete, AutocompleteContent, AutocompleteEmpty, AutocompleteInput, AutocompleteItem, AutocompleteList } from "@/components/ui/autocomplete"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldDescription, FieldLabel } from "@/components/ui/field"

type TagOption = { value: string; label: string }

export function TagAutocomplete({
  label, description, placeholder, emptyText, options, value, onChange, addLabel, removeLabel, disabled,
}: {
  label: string
  description: string
  placeholder: string
  emptyText: string
  options: TagOption[]
  value: string[]
  onChange: (value: string[]) => void
  addLabel: (value: string) => string
  removeLabel: (value: string) => string
  disabled?: boolean
}) {
  const id = useId()
  const container = useRef<HTMLDivElement>(null)
  const [query, setQuery] = useState("")
  const term = query.trim()
  const candidates = options.filter((option) => !value.includes(option.value))
  const matches = candidates.filter((option) => `${option.label} ${option.value}`.toLocaleLowerCase().includes(term.toLocaleLowerCase()))
  const canCreate = term !== "" && !options.some((option) => option.value === term) && !value.includes(term)
  const items = canCreate ? [...matches, { value: term, label: addLabel(term) }] : matches
  const add = (tag: string) => {
    if (tag && !value.includes(tag)) onChange([...value, tag])
    setQuery("")
  }

  return <Field ref={container} className="block">
    <FieldLabel htmlFor={id}>{label}</FieldLabel>
    <FieldContent>
      <Autocomplete items={items} filter={null} value={query} openOnInputClick
        itemToStringValue={(item: TagOption) => item.value}
        onValueChange={(next, details) => {
          if (details.reason === "item-press") add(next)
          else setQuery(next)
        }}>
        <AutocompleteInput id={id} aria-describedby={`${id}-hint`} placeholder={placeholder} disabled={disabled} />
        <AutocompleteContent container={container}>
          <AutocompleteEmpty>{emptyText}</AutocompleteEmpty>
          <AutocompleteList>{(item: TagOption) => <AutocompleteItem key={item.value} value={item}>
            <span className="min-w-0 break-words [overflow-wrap:anywhere]">{item.label}</span>
          </AutocompleteItem>}</AutocompleteList>
        </AutocompleteContent>
      </Autocomplete>
      {value.length > 0 && <div className="flex flex-wrap gap-1" role="group" aria-label={label}>
        {value.map((tag) => <Button key={tag} type="button" size="sm" variant="secondary"
          className="h-auto min-h-7 max-w-full py-1" disabled={disabled} aria-label={removeLabel(tag)}
          onClick={() => onChange(value.filter((item) => item !== tag))}>
          <span className="min-w-0 whitespace-normal text-left [overflow-wrap:anywhere]">{options.find((option) => option.value === tag)?.label ?? tag}</span>
          <XIcon className="size-3 shrink-0" />
        </Button>)}
      </div>}
      <FieldDescription id={`${id}-hint`}>{description}</FieldDescription>
    </FieldContent>
  </Field>
}
