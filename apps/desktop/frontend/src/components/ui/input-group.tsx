import type { ComponentProps } from "react"

import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

function InputGroup({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="input-group"
      className={cn(
        "flex h-8 min-w-0 items-center gap-1 rounded-md border border-input bg-transparent px-1 transition-colors focus-within:border-ring focus-within:ring-0 focus-within:ring-ring/50",
        className,
      )}
      {...props}
    />
  )
}

function InputGroupInput({ className, ...props }: ComponentProps<typeof Input>) {
  return (
    <Input
      data-slot="input-group-control"
      className={cn("h-full min-w-0 flex-1 rounded-none border-0 bg-transparent px-1 shadow-none focus-visible:ring-0", className)}
      {...props}
    />
  )
}

function InputGroupAddon({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="input-group-addon" className={cn("flex shrink-0 items-center text-muted-foreground", className)} {...props} />
}

export { InputGroup, InputGroupInput, InputGroupAddon }
