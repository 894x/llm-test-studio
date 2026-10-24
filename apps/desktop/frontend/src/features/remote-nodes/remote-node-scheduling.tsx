import { useId, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"

import type { RemoteNode, RemoteNodeTaskConcurrencyCommand, RemoteRunner } from "./data"

export function RemoteNodeScheduling({ node, runner, disabled, saving, onSave }: {
  node: RemoteNode; runner: RemoteRunner; disabled: boolean; saving: boolean
  onSave: (command: RemoteNodeTaskConcurrencyCommand) => void
}) {
  const { t } = useTranslation("remoteNodes")
  const id = useId()
  const [value, setValue] = useState(String(runner.max_active_jobs))
  const limit = Number(value)
  const valid = value.trim() !== "" && Number.isInteger(limit) && limit >= 1 && limit <= 128
  const tooLow = valid && limit < runner.active
  const error = !valid ? t("scheduling.invalid") : tooLow ? t("scheduling.busy", { active: runner.active }) : ""

  return <form className="space-y-1.5" onSubmit={event => {
    event.preventDefault()
    if (disabled || saving || error || limit === runner.max_active_jobs) return
    onSave({ id: node.id, expected_revision: node.revision,
      expected_max_active_jobs: runner.max_active_jobs, max_active_jobs: limit })
  }}>
    <label htmlFor={id} className="text-xs font-medium">{t("scheduling.label")}</label>
    <div className="flex items-center gap-2">
      <Input id={id} type="number" min={1} max={128} step={1} className="w-24 tabular-nums"
        value={value} disabled={disabled || saving} aria-invalid={Boolean(error)}
        aria-describedby={`${id}-help${error ? ` ${id}-error` : ""}`}
        onChange={event => setValue(event.target.value)} />
      <Button type="submit" variant="outline" size="sm"
        disabled={disabled || saving || Boolean(error) || limit === runner.max_active_jobs}>
        {saving && <Spinner />}{t(saving ? "scheduling.saving" : "scheduling.apply")}
      </Button>
    </div>
    <p id={`${id}-help`} className="text-[11px] text-muted-foreground">{t("scheduling.help")}</p>
    {error && <p id={`${id}-error`} role="alert" className="text-xs text-destructive">{error}</p>}
  </form>
}
