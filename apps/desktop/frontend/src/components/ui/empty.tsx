import * as React from "react"

import { cn } from "@/lib/utils"

function Empty({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty"
      className={cn(
        "flex min-h-52 w-full flex-col items-center justify-center px-4 py-10 text-center",
        className,
      )}
      {...props}
    />
  )
}

function EmptyTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return (
    <h2
      data-slot="empty-title"
      className={cn("text-sm font-semibold", className)}
      {...props}
    />
  )
}

function EmptyDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="empty-description"
      className={cn("mt-1 max-w-sm text-[11px] text-muted-foreground", className)}
      {...props}
    />
  )
}

export { Empty, EmptyDescription, EmptyTitle }
