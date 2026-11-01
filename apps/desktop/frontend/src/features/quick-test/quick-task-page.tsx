import { useCallback, useEffect, useRef, type ComponentProps } from "react"
import { useTranslation } from "react-i18next"
import type { DesktopClient } from "@/app/desktop-client"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { ReportGenerationProgress } from "@/features/reports/generation-progress"
import { QuickTaskWorkspace } from "./quick-task-workspace"
import { useQuickHistory } from "./use-quick-history"

type Props = Omit<ComponentProps<typeof QuickTaskWorkspace>, "workspace" | "reports" | "refresh" | "historyFooter" | "historyReady"> & {
  actions: DesktopClient
  historyRevision: Record<string, ReportGenerationProgress>
}

export function QuickTaskPage({ actions, historyRevision, onJobChange, onPerformanceArchived, historyChanges = [], onHistoryChanged, ...props }: Props) {
  const { t } = useTranslation("quickTest")
  const { data, loaded, loading, error, refresh, loadMore, retry, invalidate, applyHistoryChange } = useQuickHistory(actions)
  const appliedHistoryChanges = useRef(0)
  useEffect(() => {
    historyChanges.slice(appliedHistoryChanges.current).forEach(({ entry, action }) => void applyHistoryChange(entry, action))
    appliedHistoryChanges.current = historyChanges.length
  }, [historyChanges, applyHistoryChange])
  const end = useRef<HTMLDivElement>(null)
  const revision = useRef(historyRevision)
  useEffect(() => {
    if (revision.current === historyRevision) return
    const ready = Object.values(historyRevision).filter(item => item.phase === "ready" && item.sequence !== revision.current[item.run_id]?.sequence)
    revision.current = historyRevision
    if (ready.length) invalidate(ready.map(item => item.run_id))
  }, [historyRevision, invalidate])
  useEffect(() => {
    const target = end.current
    if (!target || !data.next_cursor || loading || error) return
    const root = target.closest('[data-slot="scroll-area-viewport"]')
    const observer = new IntersectionObserver(entries => {
      if (entries.some(entry => entry.isIntersecting)) void loadMore()
    }, { root, rootMargin: "0px 0px 80px 0px" })
    observer.observe(target)
    return () => observer.disconnect()
  }, [data.next_cursor, loading, error, loadMore])
  const receiveJob = useCallback<NonNullable<Props["onJobChange"]>>(job => {
    onJobChange?.(job)
    invalidate(job.run_ids)
  }, [onJobChange, invalidate])
  const receiveArchive = useCallback((id: string) => {
    void onPerformanceArchived(id)
    invalidate()
  }, [onPerformanceArchived, invalidate])
  const footer = <div ref={end} className="py-3 text-center text-xs text-muted-foreground" aria-live="polite">
    {loading ? <span role="status" className="inline-flex items-center gap-2"><Spinner />{t("historyPage.loading")}</span>
      : error ? <div role="alert"><p>{t("historyPage.error")}</p><Button variant="ghost" size="sm" onClick={() => void retry()}>{t("historyPage.retry")}</Button></div>
      : data.next_cursor ? <Button variant="ghost" size="sm" onClick={() => void loadMore()}>{t("historyPage.more")}</Button>
      : loaded && data.runs.length + data.reports.length > 0 ? t("historyPage.end") : null}
  </div>
  return <QuickTaskWorkspace {...props} actions={actions} historyChanges={historyChanges}
    onHistoryChanged={async (entry, action) => { await applyHistoryChange(entry, action); await onHistoryChanged?.(entry, action) }}
    workspace={{ schema_version: 1, plans: [], runs: data.runs }}
    reports={{ schema_version: 1, reports: data.reports }} refresh={refresh} historyFooter={footer} historyReady={loaded}
    onJobChange={receiveJob}
    onPerformanceArchived={receiveArchive} />
}
