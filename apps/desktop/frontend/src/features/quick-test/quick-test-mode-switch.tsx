import { useTranslation } from "react-i18next"
import { useEffect, useRef } from "react"
import { RadioGroup as RadioGroupPrimitive } from "radix-ui"
import { RadioGroup } from "@/components/ui/radio-group"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

export type QuickTestMode = "functional" | "performance" | "plan"

export function QuickTestModeSwitch({ mode, onChange, onReady }: {
  mode: QuickTestMode
  onChange: (mode: QuickTestMode) => void
  onReady?: (root: HTMLDivElement) => void
}) {
  const { t } = useTranslation("quickTest")
  const root = useRef<HTMLDivElement>(null)
  useEffect(() => { if (root.current) onReady?.(root.current) }, [mode, onReady])
  return (
    <RadioGroup
      ref={root}
      value={mode}
      onValueChange={value => onChange(value as QuickTestMode)}
      aria-label={t("workspace.mode")}
      className="h-9 w-full shrink-0 grid-cols-3 gap-0 rounded-md bg-surface-subtle p-1"
    >
      {(["functional", "performance", "plan"] as const).map(value => (
        <RadioGroupPrimitive.Item key={value} value={value} asChild>
          <Button type="button" variant="ghost" aria-label={t(value === "functional" ? "workspace.functional" : `${value}.title`)}
          className={cn("h-full min-w-0 rounded-sm px-1 text-xs text-muted-foreground",
            mode === value && "bg-surface-active font-medium text-foreground")}
          >{t(`modes.${value}`)}</Button>
        </RadioGroupPrimitive.Item>
      ))}
    </RadioGroup>
  )
}
