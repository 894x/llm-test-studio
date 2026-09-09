import * as React from "react"
import * as ScrollAreaPrimitive from "radix-ui/scroll-area"

import { cn } from "@/lib/utils"

function ScrollArea({
  className,
  children,
  contentWidth = "intrinsic",
  ...props
}: React.ComponentProps<typeof ScrollAreaPrimitive.Root> & {
  contentWidth?: "intrinsic" | "viewport"
}) {
  return (
    <ScrollAreaPrimitive.Root
      data-slot="scroll-area"
      className={cn("relative min-h-0 min-w-0 overflow-hidden", className)}
      type="always"
      {...props}
    >
      <ScrollAreaPrimitive.Viewport
        data-slot="scroll-area-viewport"
        className={cn(
          "size-full rounded-[inherit] transition-[color,box-shadow] outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1",
          // Radix uses a display:table wrapper to measure horizontal overflow.
          // Text panels must instead wrap within the viewport's assigned width.
          contentWidth === "viewport" && "[&>div]:!block [&>div]:!min-w-0 [&>div]:w-full [overflow-wrap:anywhere]",
        )}
      >
        {children}
      </ScrollAreaPrimitive.Viewport>
      <ScrollBar />
      <ScrollBar orientation="horizontal" />
      <ScrollAreaPrimitive.Corner />
    </ScrollAreaPrimitive.Root>
  )
}

function ScrollBar({
  className,
  orientation = "vertical",
  style,
  ...props
}: React.ComponentProps<typeof ScrollAreaPrimitive.ScrollAreaScrollbar>) {
  const insetStyle: React.CSSProperties =
    orientation === "vertical"
      ? { top: "4px", right: "4px", bottom: "4px" }
      : { right: "4px", bottom: "4px", left: "4px" }

  return (
    <ScrollAreaPrimitive.ScrollAreaScrollbar
      data-slot="scroll-area-scrollbar"
      data-orientation={orientation}
      orientation={orientation}
      forceMount
      className={cn(
        "z-20 flex touch-none select-none transition-colors duration-200",
        orientation === "vertical"
          ? "w-[5px]"
          : "h-[5px] flex-col",
        className,
      )}
      style={{ ...insetStyle, ...style }}
      {...props}
    >
      <ScrollAreaPrimitive.ScrollAreaThumb
        data-slot="scroll-area-thumb"
        className="relative flex-1 rounded-full bg-foreground/20 hover:bg-foreground/30"
      />
    </ScrollAreaPrimitive.ScrollAreaScrollbar>
  )
}

export { ScrollArea, ScrollBar }
