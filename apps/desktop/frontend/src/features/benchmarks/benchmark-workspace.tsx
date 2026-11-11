import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import PlayIcon from "lucide-react/dist/esm/icons/play.mjs"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { FieldGroup } from "@/components/ui/field"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import type { CatalogSnapshot, SaveQuickTestTargetCommand, SavedQuickTestTarget } from "@/features/catalog/data"
import type { PlanRunConnection } from "@/features/runs/data"
import { PlanConnectionFields } from "@/features/quick-test/plan-connection-fields"
import { QuickTestField } from "@/features/quick-test/quick-test-field"
import { connectionURLHint } from "@/features/quick-test/connection-url"
import { DEFAULT_BENCHMARK_OPTIONS, isBenchmarkActive, type BenchmarkCatalog, type BenchmarkClient, type BenchmarkCommand, type BenchmarkDatasetRun, type BenchmarkOptions, type BenchmarkProtocol, type BenchmarkRun } from "./data"
import { useBenchmarks } from "./use-benchmarks"
import { useDatasetInventory } from "./use-dataset-inventory"

const PROTOCOLS = ["openai-chat", "openai-responses", "anthropic-messages"] as const

export function BenchmarkProgress({ dataset }: { dataset: BenchmarkDatasetRun }) {
  const { t } = useTranslation("benchmarks")
  return <div className="min-w-0 space-y-1 text-[11px]">
    <span>{dataset.stage === "loading" ? t("loadingDataset") : t("progress", dataset)}</span>
    {dataset.total > 0 ? <Progress value={dataset.completed / dataset.total * 100} className="h-1" aria-label={t("progress", dataset)} /> : null}
    {dataset.in_flight > 0 ? <p className="text-muted-foreground">{t("inFlight", { count: dataset.in_flight })}</p> : null}
    {dataset.failed > 0 ? <p className="text-destructive">{t("failedSamples", { count: dataset.failed })}</p> : null}
  </div>
}

export function BenchmarkStatus({ status }: { status: BenchmarkRun["status"] }) {
  const { t } = useTranslation("benchmarks")
  if (status === "completed") return <Badge variant="secondary">{t(`statuses.${status}`)}</Badge>
  return <span className={`text-xs ${status === "failed" ? "text-destructive" : isBenchmarkActive(status) ? "text-info-strong" : "text-muted-foreground"}`}>{t(`statuses.${status}`)}</span>
}

export function BenchmarkWorkspace({ client, modeControl, catalog, connectionsReady, connection, onConnectionChange, onOpenExecution, onOpenReport, onSaveChannel, selectionIntent, onManageDatasets }: {
  selectionIntent?: { sequence: number; datasetID: string }
  onManageDatasets?: () => void
  client: Partial<BenchmarkClient>
  modeControl: ReactNode
  catalog: CatalogSnapshot
  connectionsReady: boolean
  connection: PlanRunConnection
  onConnectionChange: (next: PlanRunConnection) => void
  onOpenExecution: (id: string) => void
  onOpenReport: (id: string) => void | Promise<void>
  onSaveChannel?: (command: SaveQuickTestTargetCommand) => Promise<SavedQuickTestTarget>
}) {
  const { t, i18n } = useTranslation("benchmarks")
  const history = useBenchmarks(client)
  const datasetInventory = useDatasetInventory(client)
  const [definition, setDefinition] = useState<BenchmarkCatalog | null>(null)
  const [catalogError, setCatalogError] = useState(false)
  const [datasetIDs, setDatasetIDs] = useState<string[]>(selectionIntent?.datasetID ? [selectionIntent.datasetID] : [])
  const [seenSelection, setSeenSelection] = useState(selectionIntent?.sequence ?? 0)
  if ((selectionIntent?.sequence ?? 0) !== seenSelection) {
    setSeenSelection(selectionIntent?.sequence ?? 0)
    setDatasetIDs(selectionIntent?.datasetID ? [selectionIntent.datasetID] : [])
  }
  const [options, setOptions] = useState<BenchmarkOptions>({ ...DEFAULT_BENCHMARK_OPTIONS })
  const [query, setQuery] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [errors, setErrors] = useState<Record<string, string>>({})
  const inFlight = useRef(false)
  const readCatalog = useCallback(async () => {
    try { if (!client.getBenchmarkCatalog) throw new Error(); setDefinition(await client.getBenchmarkCatalog()); setCatalogError(false) }
    catch { setCatalogError(true) }
  }, [client])
  useEffect(() => { void Promise.resolve().then(readCatalog) }, [readCatalog])
  useEffect(() => {
    if (definition?.runtime.state !== "preparing") return
    const timer = window.setInterval(() => void readCatalog(), 1500)
    return () => window.clearInterval(timer)
  }, [definition?.runtime.state, readCatalog])
  const prepare = async () => {
    if (inFlight.current || !client.prepareBenchmarkRuntime) return
    inFlight.current = true
    setPending(true); setError("")
    try { await client.prepareBenchmarkRuntime(); await readCatalog() }
    catch (reason) { setError(publicDesktopErrorMessage(reason, t("nativeOnly"))) }
    finally { inFlight.current = false; setPending(false) }
  }
  const updateOptions = (patch: Partial<BenchmarkOptions>) => { setOptions(current => ({ ...current, ...patch })); setError("") }
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (inFlight.current || !client.startBenchmark) return
    const nextErrors: Record<string, string> = {}
    if (!connection.model.trim()) nextErrors.model = t("invalid")
    if (!connection.channel_id && connectionURLHint(connection.base_url?.trim() ?? "", "base_url")) nextErrors.base_url = t("invalid")
    if (!connection.channel_id && !connection.api_key?.trim()) nextErrors.api_key = t("invalid")
    const ranges: [keyof BenchmarkOptions, number, number][] = [["sample_limit", 0, 10000], ["epochs", 1, 32], ["concurrency", 1, 64], ["max_tokens", 1, 1048576], ["timeout_seconds", 1, 86400], ["seed", 0, 2147483647]]
    ranges.forEach(([key, min, max]) => { const value = options[key]; if (typeof value !== "number" || !Number.isSafeInteger(value) || value < min || value > max) nextErrors[key] = t("invalid") })
    if (options.temperature !== null && (!Number.isFinite(options.temperature) || options.temperature < 0 || options.temperature > 2)) nextErrors.temperature = t("invalid")
    if (!datasetIDs.length || !datasetIDs.every(id => definition?.datasets.some(item => item.id === id))) nextErrors.datasets = t("invalid")
    if (datasetIDs.length > 16) nextErrors.datasets = t("datasetLimit")
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length) { setError(t("invalid")); return }
    inFlight.current = true; setPending(true); setError("")
    const command: BenchmarkCommand = { dataset_ids: [...datasetIDs], options: { ...options }, connection: {
      protocol: selectedProtocol as BenchmarkProtocol, model: connection.model.trim(), channel_id: connection.channel_id ?? "",
      base_url: connection.channel_id ? "" : connection.base_url?.trim() ?? "", api_key: connection.channel_id ? "" : connection.api_key?.trim() ?? "",
    } }
    try { const run = await client.startBenchmark(command); history.receive([run]); void history.refresh() }
    catch (reason) { setError(publicDesktopErrorMessage(reason, t("nativeOnly"))) }
    finally { inFlight.current = false; setPending(false) }
  }
  const selectedProtocol = PROTOCOLS.includes(connection.protocol as BenchmarkProtocol) ? connection.protocol : "openai-chat"
  const runtimeState = definition?.runtime.state ?? "missing"
  const datasets = definition?.datasets.filter(item => `${item.name} ${item.description}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())) ?? []
  const numericFields: [keyof BenchmarkOptions, string, number, number][] = [["sample_limit", "sampleLimit", 0, 10000], ["epochs", "epochs", 1, 32], ["concurrency", "concurrency", 1, 64], ["max_tokens", "maxTokens", 1, 1048576], ["timeout_seconds", "timeout", 1, 86400], ["seed", "seed", 0, 2147483647]]
  return <main aria-label={t("title")} className="grid min-h-0 min-w-0 flex-1 grid-cols-[240px_minmax(0,1fr)] gap-4 overflow-hidden p-4 xl:grid-cols-[256px_minmax(0,1fr)]">
    <aside className="flex min-h-0 min-w-0 flex-col" aria-label={t("history")}>
      <div className="shrink-0 space-y-4 pb-4 pt-2">{modeControl}<div className="flex items-center justify-between gap-2"><h2 className="text-sm font-semibold">{t("history")}</h2><Button size="xs" variant="ghost" onClick={() => void history.refresh()} disabled={history.pending}>{t("refresh")}</Button></div></div>
      <ScrollArea contentWidth="viewport" className="min-h-0 flex-1">
        {!history.runs.length && !history.pending ? <Empty><EmptyTitle>{t("empty")}</EmptyTitle><EmptyDescription>{t("emptyHint")}</EmptyDescription></Empty> : null}
        <div className="space-y-2">{history.runs.map(run => <div key={run.id} className="min-w-0 rounded-md p-2 hover:bg-surface-hover">
          <Button variant="ghost" className="h-auto w-full justify-start whitespace-normal p-0 text-left text-xs" onClick={() => {
            setDatasetIDs(run.datasets.map(dataset => dataset.dataset.id)); setOptions({ ...run.options })
            onConnectionChange({ protocol: run.target.protocol, model: run.target.model, channel_id: run.target.channel_id || undefined, base_url: run.target.base_url, api_key: "" })
          }}>{run.datasets.map(dataset => dataset.dataset.name).join(" · ")}</Button>
          <p className="mt-1 text-[11px] [overflow-wrap:anywhere]">{run.target.model} · {run.target.channel_name}</p>
          <div className="mt-1 flex flex-wrap items-center gap-2"><BenchmarkStatus status={run.status} /><Button size="xs" variant="ghost" onClick={() => onOpenExecution(run.id)}>{t("details")}</Button></div>
          {run.datasets.map(dataset => <div key={dataset.report_id} className="mt-2 min-w-0"><BenchmarkProgress dataset={dataset} />{!isBenchmarkActive(dataset.status) ? <Button size="xs" variant="link" onClick={() => void onOpenReport(dataset.report_id)}>{dataset.dataset.name} · {t("report")}</Button> : null}</div>)}
          <p className="mt-1 text-[10px] tabular-nums text-muted-foreground">{new Date(run.created_at).toLocaleString(i18n.resolvedLanguage ?? i18n.language)}</p>
        </div>)}</div>
        {history.error ? <div role="alert" className="py-2 text-xs text-destructive">{t("loadError")}<Button size="xs" variant="ghost" onClick={() => void history.refresh()}>{t("retry")}</Button></div> : null}
        {history.pending ? <Spinner className="my-2" /> : null}
        {history.next ? <Button variant="ghost" size="sm" disabled={history.pending} onClick={() => void history.loadMore()}>{t("more")}</Button> : null}
      </ScrollArea>
    </aside>
    <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1">
      <section className="mx-auto flex w-full min-w-0 max-w-[800px] flex-col gap-4 py-2">
        <div className="flex items-center justify-between gap-2"><h2 className="flex h-7 items-center text-sm font-semibold">{t("title")}</h2>{onManageDatasets ? <Button size="sm" variant="secondary" onClick={onManageDatasets}>{t("datasetManager.manage")}</Button> : null}</div>
        <p className="text-[11px] text-muted-foreground">{t("localHint")}</p>
        {error ? <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert> : null}
        <div className="space-y-2"><div className="flex flex-wrap items-center justify-between gap-2"><span className="text-xs">{t(`runtime.${runtimeState}`)}</span>
          {runtimeState !== "ready" ? <Button size="sm" variant="secondary" disabled={pending || runtimeState === "preparing"} onClick={() => void prepare()}>{runtimeState === "preparing" || pending ? <Spinner /> : null}{t(runtimeState === "failed" ? "runtime.retry" : "runtime.prepare")}</Button> : null}</div>
          {runtimeState !== "ready" ? <p className="text-[11px] text-muted-foreground">{t("runtime.hint")}</p> : null}
        </div>
        <form onSubmit={event => void submit(event)} noValidate className="space-y-4">
          <FieldGroup>
            <PlanConnectionFields idPrefix="benchmark" supportedProtocols={PROTOCOLS} connection={{ ...connection, protocol: selectedProtocol, credential_run_id: undefined }}
              onChange={patch => onConnectionChange({ ...connection, ...patch, credential_run_id: undefined })} catalog={catalog} ready={connectionsReady} disabled={pending} errors={errors} onSaveChannel={onSaveChannel} />
            <fieldset disabled={pending} className="min-w-0 space-y-2"><legend className="text-xs font-medium">{t("chooseDatasets")}</legend>
              <p className="text-[11px] text-muted-foreground">{t("datasetHint")}</p>
              <Input aria-label={t("searchDatasets")} placeholder={t("searchDatasets")} value={query} onChange={event => setQuery(event.target.value)} />
              {catalogError ? <div role="alert" className="text-xs text-destructive">{t("loadError")}<Button type="button" variant="ghost" size="sm" onClick={() => void readCatalog()}>{t("retry")}</Button></div> : !definition ? <Spinner /> : null}
              <div className="grid min-w-0 grid-cols-1 gap-1 min-[1200px]:grid-cols-2">{datasets.map(dataset => <label key={dataset.id} className={`flex min-w-0 cursor-pointer items-start gap-2 rounded-md p-2 ${datasetIDs.includes(dataset.id) ? "bg-surface-active" : "hover:bg-surface-hover"}`}>
                <Checkbox checked={datasetIDs.includes(dataset.id)} onCheckedChange={checked => setDatasetIDs(current => checked ? [...current, dataset.id] : current.filter(id => id !== dataset.id))} aria-label={dataset.name} aria-invalid={!!errors.datasets} aria-describedby={errors.datasets ? "benchmark-datasets-error" : undefined} className="mt-0.5" />
                <span className="min-w-0 [overflow-wrap:anywhere]"><span className="text-xs font-medium">{dataset.name}</span><span className="mt-1 block text-[11px] text-muted-foreground">{t(`datasetDescriptions.${dataset.id}`, { defaultValue: dataset.description })}</span><span className="mt-1 block text-[10px] text-muted-foreground">{datasetInventory.inventory ? t(`datasetManager.states.${datasetInventory.inventory.datasets.find(item => item.dataset.id === dataset.id)?.download.state ?? "missing"}`) : t("datasetManager.loading")}</span></span>
              </label>)}</div>
              {definition && !datasets.length ? <p className="text-xs text-muted-foreground">{t("noDatasets")}</p> : null}
              <p className="text-[11px] text-muted-foreground" aria-live="polite">{t("selected", { count: datasetIDs.length })}</p>
              {errors.datasets ? <p id="benchmark-datasets-error" className="text-[11px] text-destructive">{errors.datasets}</p> : null}
            </fieldset>
            <div className="grid min-w-0 grid-cols-2 gap-4">{numericFields.map(([key, label, min, max]) => <QuickTestField key={key} id={`benchmark-${key}`} label={t(label)} error={errors[key]}>
              <Input id={`benchmark-${key}`} type="number" min={min} max={max} value={Number.isNaN(options[key]) ? "" : Number(options[key])} disabled={pending} aria-invalid={!!errors[key]} aria-describedby={errors[key] ? `benchmark-${key}-error` : undefined} onChange={event => updateOptions({ [key]: event.target.value === "" ? NaN : Number(event.target.value) })} />
              {key === "sample_limit" ? <p className="text-[11px] text-muted-foreground">{t("sampleHint")}</p> : null}
            </QuickTestField>)}</div>
            <div className="grid min-w-0 grid-cols-2 gap-4"><QuickTestField id="benchmark-temperature" label={t("temperature")} error={errors.temperature}><Input id="benchmark-temperature" type="number" min={0} max={2} step="0.1" value={options.temperature ?? ""} disabled={pending} aria-invalid={!!errors.temperature} aria-describedby={errors.temperature ? "benchmark-temperature-error" : undefined} onChange={event => updateOptions({ temperature: event.target.value === "" ? null : Number(event.target.value) })} /></QuickTestField>
              <QuickTestField id="benchmark-reasoning" label={t("reasoning")}><SearchableSelect id="benchmark-reasoning" value={options.reasoning_effort} disabled={pending} onValueChange={reasoning_effort => updateOptions({ reasoning_effort })} options={[{ value: "", label: t("default") }, ...["minimal", "low", "medium", "high", "max"].map(value => ({ value, label: value }))]} /></QuickTestField></div>
            <label className="flex items-center gap-2 text-xs"><Checkbox checked={options.stream} disabled={pending} onCheckedChange={checked => updateOptions({ stream: !!checked })} />{t("stream")}</label>
          </FieldGroup>
          <div className="flex justify-end"><Button type="submit" size="sm" disabled={pending || runtimeState !== "ready" || !connectionsReady} aria-busy={pending}>{pending ? <Spinner /> : <PlayIcon />}{t(pending ? "submitting" : "start")}</Button></div>
        </form>
      </section>
    </ScrollArea>
  </main>
}
