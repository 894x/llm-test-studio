import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronLeft } from "lucide-react"
import DownloadIcon from "lucide-react/dist/esm/icons/download.mjs"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { InspectorHeader, InspectorRow } from "@/features/shell/page-frame"
import { formatReportTimestamp } from "@/features/reports/report-timestamp"
import { BenchmarkProgress, BenchmarkStatus } from "./benchmark-workspace"
import { isBenchmarkActive, type BenchmarkClient, type BenchmarkDatasetRun, type BenchmarkReport, type BenchmarkRun } from "./data"

export function BenchmarkExecutionDetails({ run, client, onChange, onOpenReport }: {
  run: BenchmarkRun; client: Partial<BenchmarkClient>; onChange: () => void; onOpenReport: (id: string) => void | Promise<void>
}) {
  const { t } = useTranslation("benchmarks")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const cancel = async () => {
    if (!client.cancelBenchmark || pending) return
    setPending(true); setError("")
    try { await client.cancelBenchmark(run.id); onChange() }
    catch (reason) { setError(publicDesktopErrorMessage(reason, t("loadError"))) }
    finally { setPending(false) }
  }
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    <InspectorHeader title={t("kind")} subtitle={run.target.model} trailing={<BenchmarkStatus status={run.status} />} />
    <ScrollArea contentWidth="viewport" className="min-h-0 flex-1 px-4">
      <dl><InspectorRow label={t("local")} value={run.target.channel_name} /><InspectorRow label="Base URL" value={run.target.base_url} /></dl>
      <div className="space-y-4 py-3">{run.datasets.map(dataset => <section key={dataset.report_id} className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-xs font-semibold [overflow-wrap:anywhere]">{dataset.dataset.name}</h3><BenchmarkStatus status={dataset.status} /></div>
        <BenchmarkProgress dataset={dataset} />
        {dataset.error_code ? <p className="text-xs text-destructive">{t("evaluationFailed")}</p> : null}
        {!isBenchmarkActive(dataset.status) ? <Button size="sm" variant="secondary" onClick={() => void onOpenReport(dataset.report_id)}>{t("report")}</Button> : null}
      </section>)}</div>
      {error ? <p role="alert" className="py-2 text-xs text-destructive">{error}</p> : null}
    </ScrollArea>
    <div className="shrink-0 px-4 py-3"><Button size="sm" variant="secondary" disabled={!isBenchmarkActive(run.status) || run.status === "cancelling" || pending} onClick={() => void cancel()}>{pending ? <Spinner /> : null}{t(pending || run.status === "cancelling" ? "cancelPending" : "cancel")}</Button></div>
  </div>
}

export function BenchmarkReportInspector({ run, result, onOpen }: { run: BenchmarkRun; result: BenchmarkDatasetRun; onOpen?: () => void }) {
  const { t, i18n } = useTranslation("benchmarks")
  return <div className="min-w-0"><InspectorHeader title={result.dataset.name} subtitle={t("kind")} />
    <dl className="px-4"><InspectorRow label={t("local")} value={run.target.channel_name} /><InspectorRow label="Model ID" value={run.target.model} />
      <InspectorRow label={t("progress", result)} value={t(`statuses.${result.status}`)} />
      <InspectorRow label={t("created")} value={formatReportTimestamp(run.created_at, i18n.resolvedLanguage ?? i18n.language)} />
      {result.finished_at ? <InspectorRow label={t("finished")} value={formatReportTimestamp(result.finished_at, i18n.resolvedLanguage ?? i18n.language)} /> : null}</dl>
    {onOpen ? <div className="px-4 py-3"><Button size="sm" onClick={onOpen}>{t("report")}</Button></div> : null}
  </div>
}

export function BenchmarkReportBody({ runID, result, client, saveExport }: {
  runID: string; result: BenchmarkDatasetRun; client: Partial<BenchmarkClient>;
  saveExport: (filename: string, mediaType: string, data: string, locale: string) => Promise<boolean>
}) {
  const { t, i18n } = useTranslation("benchmarks")
  const [report, setReport] = useState<BenchmarkReport | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [after, setAfter] = useState("")
  const [previous, setPrevious] = useState<string[]>([])
  const [selected, setSelected] = useState("")
  const [exporting, setExporting] = useState(false)
  const read = useCallback(async (cursor: string) => {
    if (!client.getBenchmarkReport) return
    setPending(true); setError("")
    try { const value = await client.getBenchmarkReport(runID, result.report_id, cursor); setReport(value); setSelected("") }
    catch (reason) { setError(publicDesktopErrorMessage(reason, t("loadError"))) }
    finally { setPending(false) }
  }, [client, runID, result.report_id, t])
  useEffect(() => { void Promise.resolve().then(() => read(after)) }, [read, after])
  const sample = report?.samples.find(item => `${item.id}:${item.epoch}` === selected) ?? report?.samples[0]
  const exported = async () => {
    if (!client.exportBenchmarkReport || exporting) return
    setExporting(true); setError("")
    try { const file = await client.exportBenchmarkReport(runID, result.report_id); await saveExport(file.filename, file.media_type, file.data_base64, i18n.resolvedLanguage ?? i18n.language) }
    catch (reason) { setError(publicDesktopErrorMessage(reason, t("loadError"))) }
    finally { setExporting(false) }
  }
  return <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1 px-4">
    <article className="min-w-0 space-y-5 pb-6 [overflow-wrap:anywhere]">
      <div className="flex flex-wrap items-center justify-between gap-3"><div><h2 className="text-base font-semibold">{t("reportTitle", { dataset: result.dataset.name })}</h2><p className="mt-1 text-xs text-muted-foreground">{report?.target.model} · {report?.target.channel_name}</p></div>
        <TooltipProvider><Tooltip><TooltipTrigger asChild><Button size="icon-sm" variant="secondary" aria-label={t(exporting ? "exporting" : "export")} disabled={exporting || isBenchmarkActive(result.status)} onClick={() => void exported()}>{exporting ? <Spinner /> : <DownloadIcon aria-hidden="true" />}</Button></TooltipTrigger><TooltipContent>{t(exporting ? "exporting" : "export")}</TooltipContent></Tooltip></TooltipProvider></div>
      {error ? <Alert variant="destructive"><AlertDescription>{error}<Button size="sm" variant="ghost" onClick={() => void read(after)}>{t("retry")}</Button></AlertDescription></Alert> : null}
      {pending ? <div role="status" className="flex items-center gap-2 text-xs"><Spinner />{t("samples")}</div> : null}
      <div className="flex flex-wrap items-center gap-4"><BenchmarkStatus status={result.status} /><BenchmarkProgress dataset={result} /></div>
      {result.status !== "completed" ? <p className="text-xs text-muted-foreground">{t("evaluationFailed")}</p> : null}
      <section className="space-y-2"><h3 className="text-sm font-semibold">{t("metrics")}</h3><p className="text-xs text-muted-foreground">{t("metricHint")}</p>
        {result.metrics.length ? <Table className="table-fixed"><TableHeader><TableRow><TableHead>{t("score")}</TableHead><TableHead>{t("metrics")}</TableHead></TableRow></TableHeader><TableBody>{result.metrics.map(metric => <TableRow key={`${metric.scorer}:${metric.name}`}><TableCell className="whitespace-normal text-xs">{metric.scorer} / {metric.name}</TableCell><TableCell className="tabular-nums">{metric.value.toLocaleString(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: 6 })}</TableCell></TableRow>)}</TableBody></Table> : <p className="text-xs text-muted-foreground">{t("noMetrics")}</p>}
      </section>
      {report ? <section className="space-y-2"><h3 className="text-sm font-semibold">{t("configuration")}</h3><dl className="grid grid-cols-2 gap-x-4 text-xs">
        {([ ["sampleLimit", report.options.sample_limit], ["epochs", report.options.epochs], ["concurrency", report.options.concurrency], ["maxTokens", report.options.max_tokens], ["temperature", report.options.temperature ?? t("default")], ["timeout", report.options.timeout_seconds], ["seed", report.options.seed], ["reasoning", report.options.reasoning_effort || t("default")] ] as const).map(([label, value]) => <InspectorRow key={label} label={t(label)} value={String(value)} />)}
      </dl></section> : null}
      {result.provenance ? <section className="space-y-2"><h3 className="text-sm font-semibold">{t("provenance")}</h3><dl>
        <InspectorRow label="Inspect AI / Inspect Evals" value={`${result.provenance.inspect_ai} / ${result.provenance.inspect_evals}`} />
        <InspectorRow label={t("task")} value={`${result.provenance.task} / ${result.provenance.task_version}`} />
        <InspectorRow label={t("datasets")} value={result.provenance.dataset_name} />
        <InspectorRow label={t("fingerprint")} value={result.provenance.input_sha256} />
      </dl><p className="text-[11px] text-muted-foreground">{t("fingerprintHint")}</p></section> : null}
      <section className="space-y-2"><h3 className="text-sm font-semibold">{t("samples")}</h3>
        <Table className="table-fixed"><TableHeader><TableRow><TableHead>{t("sampleID")}</TableHead><TableHead>{t("epoch")}</TableHead><TableHead>{t("score")}</TableHead><TableHead>{t("tokens")}</TableHead></TableRow></TableHeader>
          <TableBody>{report?.samples.map(item => <TableRow key={`${item.id}:${item.epoch}`} data-state={sample?.id === item.id && sample.epoch === item.epoch ? "selected" : undefined} onClick={() => setSelected(`${item.id}:${item.epoch}`)}>
            <TableCell className="whitespace-normal"><Button size="xs" variant="link" className="h-auto whitespace-normal p-0 text-left [overflow-wrap:anywhere]" onClick={() => setSelected(`${item.id}:${item.epoch}`)}>{item.id}</Button></TableCell><TableCell>{item.epoch}</TableCell><TableCell className="whitespace-normal text-xs">{item.status === "error" ? t("statuses.error") : item.scores.map(score => `${score.name}: ${score.value}`).join(" · ")}</TableCell><TableCell className="text-xs tabular-nums">{item.input_tokens} / {item.output_tokens}</TableCell>
          </TableRow>)}</TableBody></Table>
        <div className="flex justify-end gap-2"><Button size="sm" variant="secondary" disabled={pending || !previous.length} onClick={() => { setAfter(previous[previous.length-1]); setPrevious(current => current.slice(0,-1)) }}><ChevronLeft aria-hidden="true" />{t("previous")}</Button><Button size="sm" variant="secondary" disabled={pending || !report?.next_cursor} onClick={() => { setPrevious(current => [...current, after]); setAfter(report!.next_cursor) }}>{t("more")}</Button></div>
        {sample ? <div className="min-w-0 space-y-4 pt-3">
          {sample.error_code ? <p className="text-xs text-destructive">{t("sampleError")}</p> : null}
          {([ ["input", sample.input], ["target", sample.target], ["output", sample.output] ] as const).map(([label, value]) => <section key={label}><h4 className="text-xs font-semibold">{t(label)}</h4><p className="mt-2 whitespace-pre-wrap text-xs leading-relaxed">{value || "—"}</p></section>)}
          {sample.scores.map(score => <section key={score.name} className="space-y-2 text-xs"><h4 className="font-semibold">{score.name}: {score.value}</h4><dl><InspectorRow label={t("answer")} value={score.answer || "—"} /><InspectorRow label={t("explanation")} value={score.explanation || "—"} /></dl></section>)}
        </div> : null}
      </section>
    </article>
  </ScrollArea>
}
