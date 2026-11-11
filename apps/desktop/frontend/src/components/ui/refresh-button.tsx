import type { ComponentProps } from "react"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"

export function RefreshButton({ label, refreshing = false, disabled, variant = "ghost", ...props }:
  Omit<ComponentProps<typeof Button>, "children" | "asChild" | "size" | "aria-label"> & { label: string; refreshing?: boolean }) {
  const unavailable = disabled || refreshing
  return <TooltipProvider><Tooltip><TooltipTrigger asChild>
    <span className="inline-flex" tabIndex={unavailable ? 0 : undefined}>
      <Button {...props} variant={variant} size="icon-sm" aria-label={label} aria-busy={refreshing} disabled={unavailable}>
        {refreshing ? <Spinner /> : <RefreshCwIcon aria-hidden="true" />}
      </Button>
    </span>
  </TooltipTrigger><TooltipContent>{label}</TooltipContent></Tooltip></TooltipProvider>
}
