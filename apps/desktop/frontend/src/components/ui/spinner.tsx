import LoaderCircleIcon from "lucide-react/dist/esm/icons/loader-circle.mjs"

import { cn } from "@/lib/utils"

function Spinner({ className }: { className?: string }) {
  return (
    <LoaderCircleIcon
      data-slot="spinner"
      aria-hidden="true"
      className={cn("size-3.5 animate-spin text-muted-foreground", className)}
    />
  )
}

export { Spinner }
