import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Empty, EmptyDescription } from "@/components/ui/empty"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { QuickPerformanceReportPanel } from "@/features/quick-test/quick-performance-sheet"
import type { ReportSnapshot } from "@/features/reports/data"
import type { JobPage, JobView } from "./data"
import type { JobClient } from "./execution"
import { JobDetails } from "./job-details"

export function JobsMenu({ client, preferredJob, reports, onOpenReport, onJobChange }: {
  client: JobClient; preferredJob: JobView | null; reports: ReportSnapshot
  onOpenReport: (id: string) => void | Promise<void>
  onJobChange?: (job: JobView) => void
}) {
  const { t } = useTranslation("jobs")
  const [open, setOpen] = useState(false)
  const [cursor, setCursor] = useState("")
  const [back, setBack] = useState<string[]>([])
  const [pageState, setPage] = useState<{ cursor: string; value: JobPage } | null>(null)
  const page = pageState?.cursor === cursor ? pageState.value : null
  const [selectedID, setSelectedID] = useState("")
  const [selectedSnapshot, setSelectedSnapshot] = useState<JobView | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [retry, setRetry] = useState(0)
  const [shownJobID, setShownJobID] = useState("")
  if (preferredJob && preferredJob.job_id !== shownJobID) {
    setShownJobID(preferredJob.job_id); setSelectedID(preferredJob.job_id); setCursor(""); setBack([]); setOpen(true)
  }
  useEffect(() => {
    if (!open) return
    let active = true
    let timer: number | undefined
    const read = async () => {
      try { const result = await client.getJobs(cursor, 25); if (active) { setPage({ cursor, value: result }); setError(null) } }
      catch (reason) { if (active) setError(reason) }
      finally { if (active) timer = window.setTimeout(() => void read(), 2_000) }
    }
    void read()
    return () => { active = false; window.clearTimeout(timer) }
  }, [client, open, cursor, retry])
  const selected = page?.jobs.find(job => job.job_id === selectedID) ?? (preferredJob?.job_id === selectedID ? preferredJob : undefined) ?? (selectedSnapshot?.job_id === selectedID ? selectedSnapshot : undefined) ?? page?.jobs[0]
  const availableReportIDs = selected ? reports.reports.filter(report => selected.report_ids.includes(report.id) || (report.run_id && selected.run_ids.includes(report.run_id))).map(report => report.id) : []
  const openReport = (id: string) => { setOpen(false); return onOpenReport(id) }
  return <Sheet open={open} onOpenChange={setOpen}>
    <SheetTrigger asChild><Button variant="outline" size="sm">{t("title")}</Button></SheetTrigger>
    <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-5xl">
      <SheetHeader><SheetTitle>{t("title")}</SheetTitle><SheetDescription>{t("description")}</SheetDescription></SheetHeader>
      <div className="grid min-h-0 flex-1 grid-cols-[240px_minmax(0,1fr)] gap-4 px-4">
        <div className="flex min-h-0 min-w-0 flex-col">
          <ScrollArea className="min-h-0 flex-1">
            {error ? <div className="flex flex-col gap-2 py-3 text-xs"><Alert variant="destructive"><AlertDescription>{publicDesktopErrorMessage(error, t("readFailed"))}</AlertDescription></Alert><Button size="sm" variant="outline" onClick={() => setRetry(value => value + 1)}>{t("refresh")}</Button></div> : null}
            {!page && !error ? <Spinner className="my-4" /> : null}
            {page?.jobs.length === 0 ? <Empty><EmptyDescription>{t("empty")}</EmptyDescription></Empty> : null}
            {page?.jobs.map(job => <Button key={job.job_id} variant={selected?.job_id === job.job_id ? "secondary" : "ghost"} className="my-1 h-auto w-full justify-start whitespace-normal px-2 py-2 text-left text-xs [overflow-wrap:anywhere]" data-state={selected?.job_id === job.job_id ? "selected" : undefined} aria-pressed={selected?.job_id === job.job_id} onClick={() => { setSelectedID(job.job_id); setSelectedSnapshot(job) }}>
              <span className="min-w-0"><span className="block font-medium">{t(`kinds.${job.kind}`)} · {t(job.location.kind)}</span><span className="mt-1 block text-[11px] text-muted-foreground">{job.job_id}</span><span className="mt-1 block text-[11px]">{job.execution ? t(`statuses.${job.execution.status}`) : job.submission ? t(`deliveryStates.${job.submission.delivery}`) : null}</span>{!job.submission ? <span className="mt-1 block text-[11px] text-muted-foreground">{t("sharedNode")}</span> : null}</span>
            </Button>)}
          </ScrollArea>
          <div className="flex flex-wrap gap-2 py-2">
            <Button size="sm" variant="ghost" disabled={!back.length} onClick={() => { setCursor(back.at(-1)!); setBack(value => value.slice(0, -1)); setSelectedID("") }}>{t("previous")}</Button>
            <Button size="sm" variant="ghost" disabled={!page?.has_more} onClick={() => { setBack(value => [...value, cursor]); setCursor(page!.next_cursor); setSelectedID("") }}>{t("next")}</Button>
          </div>
        </div>
        <ScrollArea className="min-h-0 min-w-0">
          {selected ? <JobDetails key={selected.job_id} client={client} jobID={selected.job_id} initialJob={selected} reportIDs={availableReportIDs} onJobChange={onJobChange} onOpenReport={openReport} renderPerformance={report => <QuickPerformanceReportPanel report={report} onOpenReport={openReport} />} /> : null}
        </ScrollArea>
      </div>
    </SheetContent>
  </Sheet>
}
