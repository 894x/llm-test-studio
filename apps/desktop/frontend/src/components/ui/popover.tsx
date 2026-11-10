import * as React from "react"
import * as PopoverPrimitive from "radix-ui/popover"
import { cn } from "@/lib/utils"
import { ScrollAreaRoot, ScrollAreaViewport, ScrollBar } from "./scroll-area"
import { TransientOverlayVisibility } from "./transient-overlays"
import { useTransientOverlay, useTransientOverlayVisibility } from "./use-transient-overlay"

function Popover({ open, defaultOpen, onOpenChange, ...props }: React.ComponentProps<typeof PopoverPrimitive.Root>) {
  const overlay = useTransientOverlay({ open, defaultOpen, onDismiss: () => onOpenChange?.(false) })
  return <TransientOverlayVisibility value={overlay.visibility}>
    <PopoverPrimitive.Root data-slot="popover" {...props} open={overlay.open}
      onOpenChange={next => { overlay.setOpen(next); onOpenChange?.(next) }} />
  </TransientOverlayVisibility>
}

function PopoverTrigger(props: React.ComponentProps<typeof PopoverPrimitive.Trigger>) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />
}

function PopoverContent({ className, children, align = "center", sideOffset = 4, onCloseAutoFocus, ...props }: React.ComponentProps<typeof PopoverPrimitive.Content>) {
  const { dismissed, wasDismissed } = useTransientOverlayVisibility()
  if (dismissed) return null
  return <PopoverPrimitive.Portal>
    <PopoverPrimitive.Content data-slot="popover-content" align={align} sideOffset={sideOffset}
      className={cn("z-50 w-72 origin-(--radix-popover-content-transform-origin) overflow-hidden rounded-lg bg-popover text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-hidden duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95", className)}
      {...props} onCloseAutoFocus={event => {
        if (wasDismissed()) event.preventDefault()
        else onCloseAutoFocus?.(event)
      }} asChild>
      <ScrollAreaRoot>
        <ScrollAreaViewport contentWidth="viewport" className="max-h-[calc(var(--radix-popover-content-available-height)/var(--interface-zoom,1))]">{children}</ScrollAreaViewport>
        <ScrollBar />
      </ScrollAreaRoot>
    </PopoverPrimitive.Content>
  </PopoverPrimitive.Portal>
}

export { Popover, PopoverContent, PopoverTrigger }
