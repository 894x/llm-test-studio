import { useId, useState, type ReactNode } from "react"
import FileChartColumnIcon from "lucide-react/dist/esm/icons/file-chart-column.mjs"
import RotateCcwIcon from "lucide-react/dist/esm/icons/rotate-ccw.mjs"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { TaskDraft } from "@/features/quick-test/task-draft"

export type RerunCredentialRequest = { sourceID: string; draft: TaskDraft; concurrency: number }

export function RunShortcutActions({ disabled, pending, waiting, error, reportID, newReport, credential, extraAction, controlButtons, feedback, onRerun, onOpenReport, onCredential, onCancelCredential }: {
  disabled: boolean
  pending: boolean
  waiting: boolean
  error?: string
  reportID?: string
  newReport: boolean
  credential: RerunCredentialRequest | null
  extraAction?: ReactNode
  controlButtons?: ReactNode
  feedback?: ReactNode
  onRerun: () => void
  onOpenReport: (reportID: string) => Promise<void>
  onCredential: (request: RerunCredentialRequest, apiKey: string) => Promise<boolean>
  onCancelCredential: () => void
}) {
  const { t } = useTranslation("runs")
  const { t: tj } = useTranslation("jobs")
  const [apiKey, setAPIKey] = useState("")
  const credentialID = useId()
  const rerunLabel = t(pending ? "shortcuts.starting" : "shortcuts.rerun")
  const reportLabel = t(newReport ? "shortcuts.viewNewReport" : "shortcuts.viewReport")
  return <TooltipProvider><div className="shrink-0 space-y-2 border-t border-divider px-4 py-3" role={controlButtons ? "region" : undefined} aria-label={controlButtons ? tj("executionControls") : t("shortcuts.title")}>
    <div className="flex items-center gap-2">
      <Tooltip>
        <TooltipTrigger asChild><span className="inline-flex rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring" tabIndex={disabled || credential ? 0 : undefined}>
          <Button size="icon-sm" disabled={disabled || !!credential} aria-label={rerunLabel} aria-description={t("shortcuts.hint")} aria-busy={pending} onClick={onRerun}>
            {pending ? <Spinner /> : <RotateCcwIcon />}
          </Button>
        </span></TooltipTrigger>
        <TooltipContent side="top" className="block max-w-64"><p className="font-medium">{rerunLabel}</p><p className="mt-1">{t("shortcuts.hint")}</p></TooltipContent>
      </Tooltip>
      {reportID ? <Tooltip>
        <TooltipTrigger asChild><Button variant="outline" size="icon-sm" aria-label={reportLabel} onClick={() => void onOpenReport(reportID)}><FileChartColumnIcon /></Button></TooltipTrigger>
        <TooltipContent side="top">{reportLabel}</TooltipContent>
      </Tooltip> : null}
      {extraAction}
      {controlButtons}
    </div>
    {feedback}
    {waiting ? <p className="text-[11px] text-muted-foreground" role="status">{t("shortcuts.waiting")}</p> : null}
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
  </div></TooltipProvider>
}
