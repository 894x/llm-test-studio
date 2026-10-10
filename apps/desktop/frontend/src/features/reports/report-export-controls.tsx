import { useId } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import type { ReportExportFormat } from "./data"

export function ReportExportControls({ watermark, onWatermarkChange, exporting, error, disabled = false, onExport, onCopyPNG }: {
  watermark: string
  onWatermarkChange: (value: string) => void
  exporting: ReportExportFormat | "copy" | ""
  error: string
  disabled?: boolean
  onExport: (format: ReportExportFormat) => Promise<void>
  onCopyPNG: () => Promise<void>
}) {
  const { t } = useTranslation("reports")
  const watermarkID = useId()
  return <>
    <div className="grid grid-cols-2 gap-2 px-4 py-3" aria-label={t("inspector.exportAria")}>
      <Field className="col-span-2 block space-y-1">
        <FieldLabel htmlFor={watermarkID}>{t("inspector.watermark")}</FieldLabel>
        <Input id={watermarkID} value={watermark} maxLength={64} disabled={Boolean(exporting)} onChange={event => onWatermarkChange(event.target.value)} placeholder="rhzs" />
      </Field>
      {(["json", "html", "png", "pdf"] as const).map(format => <Button key={format} variant="outline" size="sm" disabled={disabled || Boolean(exporting)} onClick={() => void onExport(format)}>{exporting === format ? t("inspector.generating") : format.toUpperCase()}</Button>)}
      <Button className="col-span-2" variant="outline" size="sm" disabled={disabled || Boolean(exporting)} onClick={() => void onCopyPNG()}>{t(exporting === "copy" ? "inspector.copying" : "inspector.copyPng")}</Button>
    </div>
    {error ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{error}</div> : null}
  </>
}
