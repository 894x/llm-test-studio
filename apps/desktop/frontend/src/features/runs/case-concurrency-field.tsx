import { useId } from "react"
import { useTranslation } from "react-i18next"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { SearchableSelect } from "@/components/ui/searchable-select"

export function CaseConcurrencyField({
  value,
  onChange,
  disabled = false,
  labelHint,
}: {
  value: number
  onChange: (value: number) => void
  disabled?: boolean
  labelHint?: string
}) {
  const { t } = useTranslation("runs")
  const id = useId()
  const descriptionId = `${id}-description`
  // A restored run can have a smaller effective limit than the selected preset.
  const options = [...new Set([1, 2, 4, 8, value])].sort((a, b) => a - b)
  return (
    <Field className="min-w-0 flex-col items-stretch gap-2">
      <div className="flex min-w-0 items-baseline gap-2">
        <FieldLabel htmlFor={id} className="shrink-0 [overflow-wrap:anywhere]">{t("caseConcurrency.label")}</FieldLabel>
        {labelHint ? <FieldDescription id={descriptionId} className="mt-0 min-w-0 flex-1 [overflow-wrap:anywhere]">{labelHint}</FieldDescription> : null}
      </div>
      <SearchableSelect
        id={id}
        aria-label={t("caseConcurrency.label")}
        aria-describedby={descriptionId}
        value={String(value)}
        disabled={disabled}
        onValueChange={(next) => onChange(Number(next))}
        options={options.map((count) => ({
          value: String(count),
          label: t(count === 1 ? "caseConcurrency.serial" : "caseConcurrency.option", { count }),
        }))}
      />
      {!labelHint ? <FieldDescription id={descriptionId}>{t("caseConcurrency.hint")}</FieldDescription> : null}
    </Field>
  )
}
