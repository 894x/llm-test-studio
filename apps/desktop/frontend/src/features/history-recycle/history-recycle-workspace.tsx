import { useCallback, useEffect, useRef, useState } from "react"
import ArrowLeftIcon from "lucide-react/dist/esm/icons/arrow-left.mjs"
import RotateCcwIcon from "lucide-react/dist/esm/icons/rotate-ccw.mjs"
import { useTranslation } from "react-i18next"

import { publicDesktopErrorMessage, type DesktopClient } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatReportTimestamp } from "@/features/reports/report-timestamp"
import { PageFrame } from "@/features/shell/page-frame"

import type { HistoryChanged, RecycledHistoryEntry, RecycledHistoryPage } from "./data"

type RecycleClient = Pick<DesktopClient, "getRecycledHistory" | "restoreHistory">
type ListState = {
  entries: RecycledHistoryEntry[]
  nextCursor: string
  hasMore: boolean
  loaded: boolean
  loading: "initial" | "refresh" | "more" | ""
  error: string
  failedAfter: string
}

const INITIAL_STATE: ListState = {
  entries: [], nextCursor: "", hasMore: false, loaded: false,
  loading: "initial", error: "", failedAfter: "",
}

function entryKey(entry: Pick<RecycledHistoryEntry, "kind" | "id">): string {
  return `${entry.kind}:${entry.id}`
}

function removalKey(entry: RecycledHistoryEntry): string {
  return `${entryKey(entry)}:${entry.removed_at}`
}

function appendEntries(current: RecycledHistoryEntry[], next: RecycledHistoryEntry[]): RecycledHistoryEntry[] {
  const entries = new Map(current.map(entry => [entryKey(entry), entry]))
  for (const entry of next) entries.set(entryKey(entry), entry)
  return [...entries.values()]
}

export function HistoryRecycleWorkspace({ client, onHistoryChanged, onBack }: {
  client: RecycleClient
  onHistoryChanged: HistoryChanged
  onBack?: () => void
}) {
  const { t, i18n } = useTranslation("historyRecycle")
  const [list, setList] = useState<ListState>(INITIAL_STATE)
  const [restoring, setRestoring] = useState("")
  const [restoreError, setRestoreError] = useState("")
  const [restoredTitle, setRestoredTitle] = useState("")
  const requestSequence = useRef(0)
  const listPending = useRef(false)
  const restorePending = useRef(false)
  const restoredRemovals = useRef(new Set<string>())

  const load = useCallback(async (after = "") => {
    if (listPending.current) return
    listPending.current = true
    const sequence = ++requestSequence.current
    setList(previous => ({
      ...previous, loading: after ? "more" : previous.loaded ? "refresh" : "initial",
      error: "", failedAfter: "",
    }))
    try {
      const page: RecycledHistoryPage = await client.getRecycledHistory(after)
      if (sequence !== requestSequence.current) return
      const entries = page.entries.filter(entry => !restoredRemovals.current.has(removalKey(entry)))
      setList(previous => ({
        entries: after ? appendEntries(previous.entries, entries) : entries,
        nextCursor: page.next_cursor, hasMore: page.has_more, loaded: true,
        loading: "", error: "", failedAfter: "",
      }))
    } catch (error) {
      if (sequence !== requestSequence.current) return
      setList(previous => ({
        ...previous, loading: "", error: publicDesktopErrorMessage(error, t("loadFailed")), failedAfter: after,
      }))
    } finally {
      if (sequence === requestSequence.current) listPending.current = false
    }
  }, [client, t])

  useEffect(() => {
    listPending.current = false
    void load()
    return () => { requestSequence.current += 1 }
  }, [load])

  const restore = async (entry: RecycledHistoryEntry) => {
    if (restorePending.current || listPending.current) return
    restorePending.current = true
    setRestoring(entryKey(entry))
    setRestoreError("")
    setRestoredTitle("")
    try {
      const restored = await client.restoreHistory({ kind: entry.kind, id: entry.id })
      restoredRemovals.current.add(removalKey(entry))
      restoredRemovals.current.add(`${entryKey(restored)}:${entry.removed_at}`)
      setList(previous => ({
        ...previous, entries: previous.entries.filter(row => entryKey(row) !== entryKey(entry) && entryKey(row) !== entryKey(restored)),
      }))
      setRestoredTitle(restored.title)
      try {
        await onHistoryChanged(restored, "restore")
      } catch {
        setRestoreError(t("restoredRefreshFailed"))
      }
      await load()
    } catch (error) {
      setRestoreError(publicDesktopErrorMessage(error, t("restoreFailed")))
    } finally {
      restorePending.current = false
      setRestoring("")
    }
  }

  const busy = !!list.loading || !!restoring
  return (
    <PageFrame title={t("title")} description={t("description")} actions={
      <>
        {onBack ? <Button variant="ghost" size="sm" onClick={onBack}><ArrowLeftIcon />{t("back")}</Button> : null}
        <Button variant="outline" size="sm" disabled={busy} onClick={() => void load()}>
          {list.loading === "refresh" ? <Spinner /> : <RotateCcwIcon />}{t("refresh")}
        </Button>
      </>
    }>
      <ScrollArea contentWidth="viewport" className="min-h-0 flex-1 px-4">
        <p className="mb-3 text-xs leading-5 text-muted-foreground [overflow-wrap:anywhere]">{t("scopeHint")}</p>
        {restoredTitle ? <p role="status" className="mb-3 text-xs text-success-strong [overflow-wrap:anywhere]">{t("restored", { title: restoredTitle })}</p> : null}
        {restoreError ? <Alert variant="destructive" className="mb-3"><AlertDescription>{restoreError}</AlertDescription></Alert> : null}
        {list.error ? <Alert variant="destructive" className="mb-3">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span className="min-w-0 [overflow-wrap:anywhere]">{list.error}</span>
            <Button variant="outline" size="sm" disabled={busy} onClick={() => void load(list.failedAfter)}>{t("retry")}</Button>
          </AlertDescription>
        </Alert> : null}
        {!list.loaded && list.loading ? <Empty role="status" aria-busy="true"><Spinner /><EmptyDescription>{t("loading")}</EmptyDescription></Empty>
          : list.loaded && list.entries.length === 0 ? <Empty><EmptyTitle>{t("empty")}</EmptyTitle><EmptyDescription>{t("emptyHint")}</EmptyDescription></Empty>
          : list.entries.length > 0 ? <Table aria-label={t("tableAria")} className="table-fixed">
            <TableHeader><TableRow>
              <TableHead className="h-8 w-[15%] text-[11px]">{t("columns.kind")}</TableHead>
              <TableHead className="h-8 text-[11px]">{t("columns.record")}</TableHead>
              <TableHead className="h-8 w-[24%] text-[11px]">{t("columns.removedAt")}</TableHead>
              <TableHead className="h-8 w-[100px] text-right text-[11px]">{t("columns.action")}</TableHead>
            </TableRow></TableHeader>
            <TableBody>{list.entries.map(entry => <TableRow key={entryKey(entry)}>
              <TableCell className="text-xs">{t(`kind.${entry.kind}`)}</TableCell>
              <TableCell>
                <div className="text-xs font-medium [overflow-wrap:anywhere]">{entry.title}</div>
                <div className="mt-0.5 font-mono text-[10px] text-muted-foreground [overflow-wrap:anywhere]">{entry.id}</div>
                <div className="mt-0.5 text-[11px] text-muted-foreground">{t("affected", { runs: entry.run_ids.length, reports: entry.report_ids.length })}{entry.remote ? ` · ${t("remoteRecord")}` : ""}</div>
              </TableCell>
              <TableCell className="text-xs tabular-nums">{formatReportTimestamp(entry.removed_at, i18n.resolvedLanguage ?? i18n.language)}</TableCell>
              <TableCell className="text-right"><Button variant="outline" size="sm" disabled={busy} aria-label={t("restoreAria", { title: entry.title })} onClick={() => void restore(entry)}>
                {restoring === entryKey(entry) ? <Spinner /> : null}{t("restore")}
              </Button></TableCell>
            </TableRow>)}</TableBody>
          </Table> : null}
        {list.hasMore ? <div className="flex justify-center py-4"><Button variant="outline" size="sm" disabled={busy} onClick={() => void load(list.nextCursor)}>
          {list.loading === "more" ? <Spinner /> : null}{list.loading === "more" ? t("loading") : t("loadMore")}
        </Button></div> : null}
      </ScrollArea>
    </PageFrame>
  )
}
