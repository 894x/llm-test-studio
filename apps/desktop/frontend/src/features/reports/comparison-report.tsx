import { Fragment, useId, useState } from "react"
import { useTranslation } from "react-i18next"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { TooltipProvider } from "@/components/ui/tooltip"
import type { ComparisonChannelResult, ComparisonSummary } from "@/features/comparisons/data"
import type { FormalReportDetail, ReportCaseDetail, ReportEntryDetail, ReportSnapshot } from "./data"
import { ExecutionDetails, VerificationBadge } from "./entry-case-table"
import { CaseSeverityBadge } from "./case-severity-badge"
import { ComparisonChannelHeader, ComparisonTableColumns } from "./comparison-channel-header"

export type ChannelReports = Record<string, { reportID: string; detail?: FormalReportDetail; error?: boolean }>
const PAGE_SIZE = 50
export function ComparisonReportBody({ comparison, channels, loaded, reports, onRetry, paginate = true }: {
  comparison: ComparisonSummary
  channels: ComparisonChannelResult[]
  loaded: ChannelReports
  reports: Map<string, ReportSnapshot["reports"][number]>
  onRetry?: () => void
  paginate?: boolean
}) {
  const { t } = useTranslation("reports")
  const entries = new Map<string, ReportEntryDetail>()
  for (const channel of channels) for (const entry of loaded[channel.channel_id]?.detail?.entries ?? []) {
    if (!entries.has(entry.entry_id)) entries.set(entry.entry_id, entry)
  }
  const pendingChannels = channels.filter(channel => !loaded[channel.channel_id]?.detail || loaded[channel.channel_id]?.error)
  return <section aria-label={t("comparison.title")} className="min-w-0 space-y-4">
    <header><h2 className="text-sm font-semibold">{t("comparison.title")}</h2>
      <p className="mt-1 text-xs text-muted-foreground [overflow-wrap:anywhere]">{comparison.plan_name} · {comparison.model_name}</p>
    </header>
    {pendingChannels.length ? <div className="flex flex-wrap gap-x-6 gap-y-2">
      {pendingChannels.map(channel => {
        const detail = loaded[channel.channel_id]?.detail
        return <div key={channel.channel_id} className="min-w-0 text-xs [overflow-wrap:anywhere]">
          <span className="font-medium">{channel.channel_name}</span>
          {loaded[channel.channel_id]?.error ? <div role="alert" className="mt-1 text-destructive">{t("detailError")}
            {onRetry ? <Button variant="outline" size="xs" className="ml-2" onClick={onRetry}>{t("comparison.retry")}</Button> : null}
          </div> : !detail ? <div className="mt-1 text-muted-foreground" role="status">
            {reports.has(channel.run_id) ? <><Spinner className="mr-1 inline-block" />{t("detailLoading")}</> : t("comparison.pending")}
          </div> : null}
        </div>
      })}
    </div> : null}
    {[...entries.values()].map(entry => {
      const channelEntries = channels.map(channel => loaded[channel.channel_id]?.detail?.entries.find(item => item.entry_id === entry.entry_id))
      return <section key={entry.entry_id} aria-label={entry.name} className="min-w-0 space-y-2">
        <h3 className="text-xs font-semibold [overflow-wrap:anywhere]">{entry.name}</h3>
        <ComparisonCaseTable channels={channels} entries={channelEntries} loaded={loaded} paginate={paginate} />
      </section>
    })}
    {!entries.size && channels.every(channel => loaded[channel.channel_id]?.detail) ? <p className="text-xs text-muted-foreground">{t("hierarchy.noRecordedCases")}</p> : null}
    {channels.map(channel => {
      const results = loaded[channel.channel_id]?.detail?.unassigned_request_results ?? []
      return results.length ? <section key={channel.channel_id} className="space-y-2">
        <h3 className="text-xs font-semibold">{channel.channel_name} · {t("comparison.unassigned")}</h3>
        <ExecutionDetails results={results} paginate={paginate} />
      </section> : null
    })}
  </section>
}

function ComparisonCaseTable({ channels, entries, loaded, paginate }: {
  channels: ComparisonChannelResult[]
  entries: (ReportEntryDetail | undefined)[]
  loaded: ChannelReports
  paginate: boolean
}) {
  const { t } = useTranslation("reports")
  const detailsID = useId()
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState("")
  const cases = new Map<string, { name: string; results: Map<string, ReportCaseDetail> }>()
  entries.forEach((entry, index) => entry?.cases.forEach(item => {
    let row = cases.get(item.case_id)
    if (!row) { row = { name: item.name, results: new Map() }; cases.set(item.case_id, row) }
    row.results.set(channels[index].channel_id, item)
  }))
  const all = [...cases.entries()]
  const currentPage = Math.min(page, Math.max(0, Math.ceil(all.length / PAGE_SIZE) - 1))
  const visible = paginate ? all.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE) : all
  return <TooltipProvider delayDuration={250}>
    <Table aria-label={t("comparison.caseTable")} className="table-fixed text-xs">
      <ComparisonTableColumns channels={channels.length} />
      <TableHeader><TableRow>
        <TableHead>{t("protocolDesign.caseName")}</TableHead>
        {channels.map(channel => <ComparisonChannelHeader key={channel.channel_id} channel={channel.channel_name} exporting={!paginate} />)}
      </TableRow></TableHeader>
      <TableBody>{visible.map(([id, row]) => <Fragment key={id}>
        <TableRow data-state={expanded === id ? "selected" : undefined}>
          <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]">
            {paginate ? <Button variant="ghost" size="sm" className="h-auto min-h-7 w-full min-w-0 max-w-full justify-start whitespace-normal px-0 py-1 text-left"
              aria-expanded={expanded === id} aria-controls={`${detailsID}-${id}`} onClick={() => setExpanded(previous => previous === id ? "" : id)}>
              {expanded === id ? <ChevronDownIcon aria-hidden="true" /> : <ChevronRightIcon aria-hidden="true" />}
              <span className="min-w-0 [overflow-wrap:anywhere]">{row.name}</span>
            </Button> : <div className="font-medium">{row.name}</div>}
          </TableCell>
          {channels.map(channel => {
            const result = row.results.get(channel.channel_id)
            return <TableCell key={channel.channel_id} className="whitespace-normal align-top [overflow-wrap:anywhere]">
              {result ? <div className="flex min-h-7 flex-wrap items-center gap-2 py-1">
                <CaseSeverityBadge severity={result.severity} />
                <VerificationBadge status={result.verification.status} />
              </div> : <span className="text-muted-foreground">{t(loaded[channel.channel_id]?.error ? "comparison.unavailable" : loaded[channel.channel_id]?.detail ? "comparison.noResult" : "comparison.pending")}</span>}
            </TableCell>
          })}
        </TableRow>
        {expanded === id ? <TableRow id={`${detailsID}-${id}`}><TableCell colSpan={channels.length + 1} className="py-3">
          <div className="grid min-w-0 gap-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 240px), 1fr))" }}>
            {channels.map(channel => <section key={channel.channel_id} className="min-w-0 space-y-2">
              <h4 className="text-xs font-medium [overflow-wrap:anywhere]">{channel.channel_name}</h4>
              <ExecutionDetails results={row.results.get(channel.channel_id)?.request_results ?? []} paginate={paginate} />
            </section>)}
          </div>
        </TableCell></TableRow> : null}
      </Fragment>)}</TableBody>
    </Table>
    {paginate && all.length > PAGE_SIZE ? <div className="flex items-center justify-end gap-2 py-2 text-xs">
      <span>{currentPage + 1} / {Math.ceil(all.length / PAGE_SIZE)}</span>
      <Button variant="outline" size="xs" disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>{t("protocolDesign.previous")}</Button>
      <Button variant="outline" size="xs" disabled={(currentPage + 1) * PAGE_SIZE >= all.length} onClick={() => setPage(currentPage + 1)}>{t("protocolDesign.next")}</Button>
    </div> : null}
  </TooltipProvider>
}
