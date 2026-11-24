import { useId } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Checkbox } from "@/components/ui/checkbox"
import { PROTOCOL_LABELS } from "@/features/catalog/protocols"
import type { useProtocolComparisonPreview } from "./use-protocol-comparison-preview"

export function ProtocolComparisonSwitch({ checked, disabled, onChange }: {
  checked: boolean
  disabled: boolean
  onChange: (value: boolean) => void
}) {
  const { t } = useTranslation("quickTest")
  const id = useId()
  return <label htmlFor={id} className="flex min-w-0 items-center gap-2 text-xs font-medium">
    <Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={value => onChange(value === true)} />
    {t("protocolComparison.title")}
  </label>
}

export function ProtocolComparisonPreviewFields({ preview }: {
  preview: ReturnType<typeof useProtocolComparisonPreview>
}) {
  const { t } = useTranslation("quickTest")
  if (preview.loading) return <div role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Spinner />{t("protocolComparison.loading")}</div>
  if (preview.error) return <div role="alert" className="flex items-center gap-2 text-xs text-destructive">
    {t("protocolComparison.previewFailed")}<Button type="button" variant="ghost" size="xs" onClick={preview.retry}>{t("historyPage.retry")}</Button>
  </div>
  if (!preview.data) return null
  return <ul aria-label={t("protocolComparison.title")} className="space-y-1 text-xs">
    {preview.data.targets.map(target => <li key={target.protocol} className="flex flex-wrap items-baseline justify-between gap-x-2">
      <span>{PROTOCOL_LABELS[target.protocol]}</span>
      {target.available ? <span className="tabular-nums text-muted-foreground">
        {t("task.caseCount", { count: target.cases })}
        {target.requests !== null ? ` · ${t("protocolComparison.requests", { count: target.requests })}` : ""}
      </span> : <span className="text-destructive [overflow-wrap:anywhere]">
        {target.missing_item ? t("protocolComparison.missingItem", { name: target.missing_item }) : t("protocolComparison.unavailable")}
      </span>}
    </li>)}
  </ul>
}
