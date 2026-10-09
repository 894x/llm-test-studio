import { useLayoutEffect, useRef, useState, type ComponentProps } from "react"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

// The completion is a presentation layer, never part of the editable value.
export function ConnectionURLInput({
  value,
  suffix,
  className,
  onChange,
  disabled,
  ...props
}: Omit<ComponentProps<typeof Input>, "value"> & { value: string; suffix: string }) {
  const input = useRef<HTMLInputElement>(null)
  const [scrollLeft, setScrollLeft] = useState(0)
  useLayoutEffect(() => {
    setScrollLeft(input.current?.scrollLeft ?? 0)
  }, [value, suffix])
  return (
    <div className="relative min-w-0">
      <Input
        {...props}
        ref={input}
        value={value}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        className={cn("font-mono", className)}
        onChange={onChange}
        onScroll={(event) => setScrollLeft(event.currentTarget.scrollLeft)}
      />
      <div
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-y-px inset-x-2.5 flex items-center overflow-hidden font-mono text-base md:text-sm",
          disabled && "opacity-50",
        )}
      >
        <span className="whitespace-pre" style={{ transform: `translateX(-${scrollLeft}px)` }}>
          <span className="invisible">{value}</span>
          <span data-slot="connection-url-suffix" className="select-none text-muted-foreground">{suffix}</span>
        </span>
      </div>
    </div>
  )
}
