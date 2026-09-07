import type { ComponentProps } from "react"
import { cn } from "@/lib/utils"

function Alert({
  className,
  variant = "default",
  ...props
}: ComponentProps<"div"> & { variant?: "default" | "destructive" }) {
  return (
    <div
      role="alert"
      data-slot="alert"
      className={cn(
        "rounded-lg border p-3 text-xs",
        variant === "destructive"
          ? "border-destructive/25 bg-destructive-soft text-destructive"
          : "bg-surface-subtle",
        className,
      )}
      {...props}
    />
  )
}

function AlertDescription({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="alert-description" className={cn("leading-5", className)} {...props} />
}

export { Alert, AlertDescription }
