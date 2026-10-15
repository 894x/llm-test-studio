import { useRef, type AriaAttributes, type ReactNode } from "react"
import { Combobox } from "@base-ui/react/combobox"
import CheckIcon from "lucide-react/dist/esm/icons/check.mjs"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import { useTranslation } from "react-i18next"
import { cn } from "@/lib/utils"
import { useFloatingPortalContainer } from "./use-floating-portal-container"

export type SelectOption = { value: string; label: string; disabled?: boolean; icon?: ReactNode }

export function SearchableSelect({ value, onValueChange, options, disabled, id, className, placeholder, ...aria }: AriaAttributes & {
  value: string
  onValueChange: (value: string) => void
  options: SelectOption[]
  disabled?: boolean
  id?: string
  className?: string
  placeholder?: string
}) {
  const { t } = useTranslation("common")
  const container = useRef<HTMLDivElement>(null)
  const portalContainer = useFloatingPortalContainer(container)
  const selectedOption = options.find(option => option.value === value)
  const selected = selectedOption ? value : null
  return <div ref={container} className={cn("min-w-0 w-full", className)}>
    <Combobox.Root items={options.map(option => option.value)} value={selected} disabled={disabled} openOnInputClick
      itemToStringLabel={(id) => options.find(option => option.value === id)?.label ?? ""}
      onValueChange={(next) => { if (next !== null && options.some(option => option.value === next && !option.disabled)) onValueChange(next) }}>
      <Combobox.InputGroup className="flex h-8 min-w-0 items-center rounded-md border border-input bg-surface-control transition-colors focus-within:border-ring focus-within:ring-0 focus-within:ring-ring/50 has-[input:disabled]:bg-input/50 has-[input:disabled]:text-text-disabled has-[input[aria-invalid=true]]:border-destructive">
        {selectedOption?.icon && <span aria-hidden="true" className="ml-2.5 flex shrink-0 items-center">{selectedOption.icon}</span>}
        <Combobox.Input id={id} {...aria} placeholder={placeholder ?? t("select.search")}
          className={cn("h-full min-w-0 flex-1 bg-transparent px-2.5 text-xs outline-none disabled:cursor-not-allowed disabled:text-text-disabled disabled:placeholder:text-text-disabled", selectedOption?.icon && "pl-2")} />
        <Combobox.Trigger aria-label={t("actions.showOptions")} className="mr-0.5 flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-surface-hover focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none">
          <ChevronDownIcon className="size-4" />
        </Combobox.Trigger>
      </Combobox.InputGroup>
      <Combobox.Portal container={portalContainer}>
        <Combobox.Positioner positionMethod="fixed" sideOffset={4} align="start" className="z-50 isolate">
          <Combobox.Popup className="w-[var(--anchor-width)] max-w-[var(--available-width)] max-h-[var(--available-height)] overflow-hidden rounded-lg bg-popover text-popover-foreground shadow-md ring-1 ring-foreground/10">
            <Combobox.Empty className="px-3 py-2 text-xs text-muted-foreground empty:hidden">{t("select.empty")}</Combobox.Empty>
            <Combobox.List className="max-h-60 overflow-y-auto overscroll-contain p-1 [scrollbar-width:thin] data-empty:p-0">
              {(id: string) => { const option = options.find(option => option.value === id)!; return <Combobox.Item key={id} value={id} disabled={option.disabled}
                className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-xs outline-none data-highlighted:bg-surface-hover data-selected:bg-surface-active data-disabled:opacity-50">
                {option.icon && <span aria-hidden="true" className="flex shrink-0 items-center">{option.icon}</span>}
                <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{option.label}</span>
                <Combobox.ItemIndicator><CheckIcon className="size-3.5" /></Combobox.ItemIndicator>
              </Combobox.Item> }}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  </div>
}
