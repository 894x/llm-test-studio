import { Switch } from "radix-ui"
import { useTranslation } from "react-i18next"

export function QuickTestModeSwitch({ performance, onChange }: {
  performance: boolean
  onChange: (performance: boolean) => void
}) {
  const { t } = useTranslation("quickTest")
  return (
    <Switch.Root
      checked={performance}
      onCheckedChange={onChange}
      aria-label={t("workspace.mode")}
      className="relative grid h-9 w-full shrink-0 grid-cols-2 items-center rounded-md bg-surface-subtle p-1 text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
    >
      <span aria-hidden="true" className={performance ? undefined : "invisible"}>{t("workspace.functional")}</span>
      <span aria-hidden="true" className={performance ? "invisible" : undefined}>{t("performance.title")}</span>
      <Switch.Thumb className="absolute inset-y-1 left-1 flex w-[calc(50%-4px)] items-center justify-center rounded-md bg-surface-active px-1 font-medium text-foreground transition-transform duration-200 data-[state=checked]:translate-x-full motion-reduce:transition-none">
        <span aria-hidden="true">{performance ? t("performance.title") : t("workspace.functional")}</span>
      </Switch.Thumb>
    </Switch.Root>
  )
}
