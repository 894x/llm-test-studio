import { RefreshButton } from "@/components/ui/refresh-button"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import DownloadIcon from "lucide-react/dist/esm/icons/download.mjs"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { InspectorHeader, InspectorRow, PageFrame } from "@/features/shell/page-frame"
import { isDatasetBusy, type BenchmarkClient } from "./data"
import { useDatasetInventory } from "./use-dataset-inventory"

function formatDatasetBytes(bytes: number, locale: string) {
  const unit = bytes >= 1024 ** 3 ? "GiB" : bytes >= 1024 ** 2 ? "MiB" : "KiB"
  const divisor = unit === "GiB" ? 1024 ** 3 : unit === "MiB" ? 1024 ** 2 : 1024
  return `${(bytes / divisor).toLocaleString(locale, { maximumFractionDigits: 1 })} ${unit}`
}

export function DatasetWorkspace({ client, onTest }: { client: Partial<BenchmarkClient>; onTest: (datasetID: string) => void }) {
  const { t, i18n } = useTranslation("benchmarks")
  const { inventory, pending, error, refresh } = useDatasetInventory(client)
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState("all")
  const [selectedID, setSelectedID] = useState("")
  const [commandPending, setCommandPending] = useState(false)
  const [commandError, setCommandError] = useState("")
  const locale = i18n.resolvedLanguage ?? i18n.language
  const items = (inventory?.datasets ?? []).filter(item => (filter === "all" || item.download.state === filter) &&
    `${item.dataset.name} ${item.dataset.description} ${item.dataset.category}`.toLocaleLowerCase().includes(query.toLocaleLowerCase()))
  const selected = items.find(item => item.dataset.id === selectedID) ?? items[0]
  const manage = async (id: string, action: "download" | "cancel" | "verify" | "remove") => {
    if (commandPending || !client.manageBenchmarkDataset) return
    setCommandPending(true); setCommandError("")
    try { await client.manageBenchmarkDataset(id, action); await refresh() }
    catch (reason) { setCommandError(publicDesktopErrorMessage(reason, t("datasetManager.commandError"))) }
    finally { setCommandPending(false) }
  }
  const prepare = async () => {
    if (commandPending || !client.prepareBenchmarkRuntime) return
    setCommandPending(true); setCommandError("")
    try { await client.prepareBenchmarkRuntime(); await refresh() }
    catch (reason) { setCommandError(publicDesktopErrorMessage(reason, t("runtime.failed"))) }
    finally { setCommandPending(false) }
  }
  const runtimeReady = inventory?.runtime.state === "ready"
  const download = selected?.download
  return <PageFrame title={t("datasetManager.title")} description={t("datasetManager.description")} inspectorLabel={t("datasetManager.details")}
    actions={<><RefreshButton label={t("refresh")} refreshing={pending} onClick={() => void refresh()} /><Button size="sm" disabled={!selected} onClick={() => onTest(selected!.dataset.id)}>{t("datasetManager.test")}</Button></>}
    inspector={selected ? <div className="min-w-0 [overflow-wrap:anywhere]">
      <InspectorHeader title={selected.dataset.name} subtitle={t(`datasetManager.categories.${selected.dataset.category}`, { defaultValue: selected.dataset.category })} />
      <div className="space-y-4 px-4 pb-4"><p className="text-xs leading-relaxed">{t(`datasetDescriptions.${selected.dataset.id}`, { defaultValue: selected.dataset.description })}</p>
        <dl><InspectorRow label={t("datasetManager.status")} value={t(`datasetManager.states.${download!.state}`)} />
          <InspectorRow label={t("datasetManager.size")} value={formatDatasetBytes(download!.bytes, locale)} />
          <InspectorRow label={t("datasetManager.samples")} value={download!.samples ? download!.samples.toLocaleString(locale) : "—"} />
          <InspectorRow label={t("datasetManager.files")} value={download!.files.toLocaleString(locale)} />
          <InspectorRow label={t("datasetManager.taskVersion")} value={download!.task_version || "—"} />
          <InspectorRow label={t("datasetManager.verified")} value={download!.verified_at ? new Date(download!.verified_at).toLocaleString(locale) : "—"} />
          <InspectorRow label={t("datasetManager.checksum")} value={download!.cache_sha256 || "—"} />
        </dl>
        <a href={selected.dataset.source_url} target="_blank" rel="noreferrer" className="inline-block text-xs underline underline-offset-4">{t("datasetManager.source")}</a>
        {download!.error_code ? <p role="alert" className="text-xs text-destructive">{t(`datasetManager.errors.${download!.error_code}`)}</p> : null}
        {isDatasetBusy(download!.state) ? <p role="status" className="flex items-center gap-2 text-xs"><Spinner />{t("datasetManager.transferred", { size: formatDatasetBytes(download!.bytes, locale) })}</p> : null}
        <div className="flex flex-wrap gap-2">
          <Button size="sm" disabled={!runtimeReady || commandPending || isDatasetBusy(download!.state) || download!.state === "ready"} onClick={() => void manage(selected.dataset.id, "download")}><DownloadIcon />{t("datasetManager.download")}</Button>
          {isDatasetBusy(download!.state) ? <Button size="sm" variant="secondary" disabled={commandPending || download!.state === "removing"} onClick={() => void manage(selected.dataset.id, "cancel")}>{t("datasetManager.cancel")}</Button> : null}
          <Button size="sm" variant="secondary" disabled={commandPending || download!.state !== "ready"} onClick={() => void manage(selected.dataset.id, "verify")}>{t("datasetManager.verify")}</Button>
          <AlertDialog><AlertDialogTrigger asChild><Button size="sm" variant="secondary" disabled={commandPending || isDatasetBusy(download!.state) || download!.state === "missing"}>{t("datasetManager.remove")}</Button></AlertDialogTrigger>
            <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("datasetManager.removeTitle")}</AlertDialogTitle><AlertDialogDescription>{t("datasetManager.removeHint", { name: selected.dataset.name })}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>{t("datasetManager.keep")}</AlertDialogCancel><AlertDialogAction onClick={() => void manage(selected.dataset.id, "remove")}>{t("datasetManager.remove")}</AlertDialogAction></AlertDialogFooter></AlertDialogContent>
          </AlertDialog>
        </div>
      </div>
    </div> : <p className="p-4 text-xs text-muted-foreground">{t("datasetManager.choose")}</p>}>
    <div className="shrink-0 space-y-3 px-4 pb-3">
      <p className="text-xs text-muted-foreground">{t("datasetManager.autoHint")}</p>
      {inventory && !runtimeReady ? <div className="flex flex-wrap items-center gap-3 text-xs"><span>{t(`runtime.${inventory.runtime.state}`)}</span><Button size="sm" variant="secondary" disabled={commandPending || inventory.runtime.state === "preparing"} onClick={() => void prepare()}>{inventory.runtime.state === "preparing" ? <Spinner /> : null}{t("runtime.prepare")}</Button><span className="text-muted-foreground">{t("runtime.hint")}</span></div> : null}
      {error || commandError ? <Alert variant="destructive"><AlertDescription>{commandError || t("loadError")}<Button size="sm" variant="ghost" onClick={() => void refresh()}>{t("retry")}</Button></AlertDescription></Alert> : null}
      <div className="flex min-w-0 gap-2"><Input value={query} onChange={event => setQuery(event.target.value)} aria-label={t("searchDatasets")} placeholder={t("searchDatasets")} />
        <SearchableSelect aria-label={t("datasetManager.status")} value={filter} onValueChange={setFilter} className="w-32 shrink-0" options={[{ value: "all", label: t("datasetManager.all") }, ...["missing", "ready", "downloading", "failed", "invalid"].map(value => ({ value, label: t(`datasetManager.states.${value}`) }))]} />
      </div>
    </div>
    <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1 px-4">
      <Table className="table-fixed"><TableHeader><TableRow><TableHead className="w-[34%]">{t("datasets")}</TableHead><TableHead>{t("datasetManager.status")}</TableHead><TableHead>{t("datasetManager.size")}</TableHead><TableHead>{t("datasetManager.samples")}</TableHead></TableRow></TableHeader>
        <TableBody>{items.map(item => <TableRow key={item.dataset.id} data-state={selected?.dataset.id === item.dataset.id ? "selected" : undefined} onClick={() => setSelectedID(item.dataset.id)}>
          <TableCell className="whitespace-normal [overflow-wrap:anywhere]"><Button size="sm" variant="link" className="h-auto whitespace-normal p-0 text-left" onClick={() => setSelectedID(item.dataset.id)}>{item.dataset.name}</Button><p className="mt-1 text-[11px] text-muted-foreground">{t(`datasetManager.categories.${item.dataset.category}`, { defaultValue: item.dataset.category })}</p></TableCell>
          <TableCell className="whitespace-normal text-xs">{isDatasetBusy(item.download.state) ? <Spinner className="mr-1 inline-block" /> : null}{t(`datasetManager.states.${item.download.state}`)}</TableCell>
          <TableCell className="text-xs tabular-nums">{formatDatasetBytes(item.download.bytes, locale)}</TableCell><TableCell className="text-xs tabular-nums">{item.download.samples ? item.download.samples.toLocaleString(locale) : "—"}</TableCell>
        </TableRow>)}</TableBody>
      </Table>
      {!items.length ? <p role="status" className="py-8 text-center text-xs text-muted-foreground">{pending ? t("datasetManager.loading") : t("noDatasets")}</p> : null}
    </ScrollArea>
    <p className="shrink-0 px-4 py-2 text-[11px] text-muted-foreground">{t("datasetManager.count", { count: items.length })}</p>
  </PageFrame>
}
