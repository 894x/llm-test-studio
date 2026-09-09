import { Autocomplete as AutocompletePrimitive } from "@base-ui/react/autocomplete"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import { useTranslation } from "react-i18next"

import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import { useFloatingPortalContainer } from "./use-floating-portal-container"

const Autocomplete = AutocompletePrimitive.Root

function AutocompleteInput({
  className,
  triggerLabel,
  triggerDisabled = false,
  ...props
}: AutocompletePrimitive.Input.Props & {
  triggerLabel?: string
  triggerDisabled?: boolean
}) {
  const { t } = useTranslation("common")

  return (
    <AutocompletePrimitive.InputGroup
      data-slot="autocomplete-input-group"
      className={cn(
        "group/autocomplete relative flex h-8 w-full min-w-0 items-center rounded-lg border border-input bg-surface-control transition-colors outline-none has-[[data-slot=autocomplete-input]:focus-visible]:border-ring has-[[data-slot=autocomplete-input]:focus-visible]:ring-0 has-[[data-slot=autocomplete-input]:focus-visible]:ring-ring/50 has-[[data-slot=autocomplete-input][aria-invalid=true]]:border-destructive has-[[data-slot=autocomplete-input][aria-invalid=true]]:ring-0 has-[[data-slot=autocomplete-input][aria-invalid=true]]:ring-destructive/20 has-[[data-slot=autocomplete-input]:disabled]:bg-input/50 has-[[data-slot=autocomplete-input]:disabled]:opacity-50",
        className,
      )}
    >
      <AutocompletePrimitive.Input
        data-slot="autocomplete-input"
        render={<Input />}
        autoComplete="off"
        className="flex-1 rounded-none border-0 bg-transparent pr-1 shadow-none focus-visible:border-transparent focus-visible:ring-0 disabled:bg-transparent aria-invalid:ring-0"
        {...props}
      />
      <AutocompletePrimitive.Trigger
        data-slot="autocomplete-trigger"
        type="button"
        aria-label={triggerLabel ?? t("actions.showOptions")}
        disabled={triggerDisabled || props.disabled}
        className="mr-0.5 flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors outline-none hover:bg-surface-hover hover:text-foreground focus-visible:outline-1 focus-visible:outline-ring data-popup-open:bg-surface-hover disabled:pointer-events-none disabled:text-text-disabled"
      >
        <ChevronDownIcon className="pointer-events-none size-4" />
      </AutocompletePrimitive.Trigger>
    </AutocompletePrimitive.InputGroup>
  )
}

function AutocompleteContent({
  className,
  side = "bottom",
  sideOffset = 4,
  align = "start",
  container,
  ...props
}: AutocompletePrimitive.Popup.Props &
  Pick<AutocompletePrimitive.Positioner.Props, "side" | "align" | "sideOffset"> &
  Pick<AutocompletePrimitive.Portal.Props, "container">) {
  const portalContainer = useFloatingPortalContainer(container)
  return (
    <AutocompletePrimitive.Portal container={portalContainer}>
      <AutocompletePrimitive.Positioner
        positionMethod="fixed"
        side={side}
        sideOffset={sideOffset}
        align={align}
        className="isolate z-50"
      >
        <AutocompletePrimitive.Popup
          data-slot="autocomplete-content"
          className={cn(
            "relative max-h-[var(--available-height)] w-[var(--anchor-width)] max-w-[var(--available-width)] origin-[var(--transform-origin)] overflow-hidden rounded-lg bg-popover text-popover-foreground shadow-md ring-1 ring-foreground/10 duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
            className,
          )}
          {...props}
        />
      </AutocompletePrimitive.Positioner>
    </AutocompletePrimitive.Portal>
  )
}

function AutocompleteList({ className, ...props }: AutocompletePrimitive.List.Props) {
  return (
    <AutocompletePrimitive.List
      data-slot="autocomplete-list"
      className={cn(
        "max-h-60 scroll-py-1 overflow-y-auto overscroll-contain p-1 [scrollbar-color:color-mix(in_srgb,var(--foreground)_20%,transparent)_transparent] [scrollbar-width:thin] data-empty:p-0",
        className,
      )}
      {...props}
    />
  )
}

function AutocompleteItem({
  className,
  ...props
}: AutocompletePrimitive.Item.Props) {
  return (
    <AutocompletePrimitive.Item
      data-slot="autocomplete-item"
      className={cn(
        "relative flex w-full cursor-default items-center rounded-md px-2 py-1.5 text-sm outline-hidden select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground data-disabled:pointer-events-none data-disabled:opacity-50",
        className,
      )}
      {...props}
    />
  )
}

function AutocompleteEmpty({
  className,
  ...props
}: AutocompletePrimitive.Empty.Props) {
  return (
    <AutocompletePrimitive.Empty
      data-slot="autocomplete-empty"
      className={cn(
        "px-2 py-2 text-sm text-muted-foreground empty:hidden",
        className,
      )}
      {...props}
    />
  )
}

export {
  Autocomplete,
  AutocompleteContent,
  AutocompleteEmpty,
  AutocompleteInput,
  AutocompleteItem,
  AutocompleteList,
}
