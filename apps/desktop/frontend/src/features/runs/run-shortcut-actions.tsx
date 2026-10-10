import { useId, useState } from "react"
import FileChartColumnIcon from "lucide-react/dist/esm/icons/file-chart-column.mjs"
import RotateCcwIcon from "lucide-react/dist/esm/icons/rotate-ccw.mjs"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import type { TaskDraft } from "@/features/quick-test/task-draft"

export type RerunCredentialRequest = { sourceID: string; draft: TaskDraft; concurrency: number }

export function RunShortcutActions({ disabled, pending, waiting, error, reportID, newReport, credential, onRerun, onOpenReport, onCredential, onCancelCredential }: {
  disabled: boolean
  pending: boolean
  waiting: boolean
  error?: string
  reportID?: string
  newReport: boolean
  credential: RerunCredentialRequest | null
  onRerun: () => void
  onOpenReport: (reportID: string) => Promise<void>
  onCredential: (request: RerunCredentialRequest, apiKey: string) => Promise<boolean>
  onCancelCredential: () => void
}) {
  const { t } = useTranslation("runs")
  const [apiKey, setAPIKey] = useState("")
  const credentialID = useId()
  return <div className="shrink-0 space-y-2 border-t border-divider px-4 py-3" aria-label={t("shortcuts.title")}>
    <div className="flex flex-wrap gap-2">
      <Button size="sm" disabled={disabled || !!credential} aria-busy={pending} onClick={onRerun}>
        {pending ? <Spinner /> : <RotateCcwIcon />}{t(pending ? "shortcuts.starting" : "shortcuts.rerun")}
      </Button>
      {reportID ? <Button variant="outline" size="sm" onClick={() => void onOpenReport(reportID)}><FileChartColumnIcon />{t(newReport ? "shortcuts.viewNewReport" : "shortcuts.viewReport")}</Button> : null}
    </div>
    <p className="text-[11px] text-muted-foreground" role={waiting ? "status" : undefined}>
      {t(waiting ? "shortcuts.waiting" : "shortcuts.hint")}
    </p>
    {credential ? <form className="space-y-2" onSubmit={event => {
      event.preventDefault()
      void onCredential(credential, apiKey).then(started => { if (started) setAPIKey("") })
    }}>
      <label className="block text-[11px] font-medium" htmlFor={credentialID}>{t("shortcuts.keyLabel")}</label>
      <Input id={credentialID} type="password" autoComplete="off" autoFocus value={apiKey} maxLength={16384} disabled={pending} onChange={event => setAPIKey(event.target.value)} />
      <p className="text-[11px] text-muted-foreground">{t("shortcuts.keyHint")}</p>
      <div className="flex gap-2">
        <Button size="sm" type="submit" disabled={pending || !apiKey.trim()} aria-busy={pending}>{pending ? <Spinner /> : <RotateCcwIcon />}{t("shortcuts.rerun")}</Button>
        <Button size="sm" type="button" variant="outline" disabled={pending} onClick={() => { setAPIKey(""); onCancelCredential() }}>{t("newRun.cancel")}</Button>
      </div>
    </form> : null}
    {error ? <p role="alert" className="text-[11px] text-destructive [overflow-wrap:anywhere]">{error}</p> : null}
  </div>
}
