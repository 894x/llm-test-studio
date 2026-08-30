import LoaderCircleIcon from "lucide-react/dist/esm/icons/loader-circle.mjs"
import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

function Spinner({ className, ...props }: ComponentProps<typeof LoaderCircleIcon>) {
  return (
    <LoaderCircleIcon
      data-slot="spinner"
      aria-hidden="true"
      className={cn("size-3.5 animate-spin text-muted-foreground", className)}
      {...props}
    />
  )
}

export { Spinner }
