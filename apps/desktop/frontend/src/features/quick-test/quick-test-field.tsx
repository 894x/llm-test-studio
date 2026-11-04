import type { ReactNode } from "react"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { cn } from "@/lib/utils"

export function QuickTestField({ id, label, labelHint, error, children, className }: {
  id: string
  label: string
  labelHint?: ReactNode
  error?: string
  children: ReactNode
  className?: string
}) {
  return <Field className={cn("min-w-0 flex-col items-stretch gap-2", className)} data-invalid={!!error || undefined}>
    <div className="flex min-w-0 items-baseline gap-2">
      <FieldLabel htmlFor={id} className="shrink-0 [overflow-wrap:anywhere]">{label}</FieldLabel>
      {labelHint || error ? <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-1 [overflow-wrap:anywhere]">
        {labelHint}
        {error ? <FieldError id={`${id}-error`} className="min-w-0 text-[11px] font-normal">{error}</FieldError> : null}
      </div> : null}
    </div>
    {children}
  </Field>
}
