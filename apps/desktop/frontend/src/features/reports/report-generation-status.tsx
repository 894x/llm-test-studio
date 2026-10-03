import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import type { ReportGenerationProgress } from "./generation-progress"

export function ReportGenerationStatus({ progress, onOpenLogs }: {
  progress: ReportGenerationProgress[]
  onOpenLogs: () => void
}) {
  const { t } = useTranslation("reports")
  if (!progress.length) return null
  return (
    <ScrollArea contentWidth="viewport" className="shrink-0 bg-surface-subtle"
      style={{ height: Math.min(progress.length, 3) * 48 + 16 }}>
      <div className="px-4 py-2">
      {progress.map((item) => {
        const failed = item.phase === "failed"
        const measurable = item.phase === "building" && item.total > 0
        return (
          <div key={item.run_id} role={failed ? "alert" : "status"} aria-busy={!failed}
            className="flex min-h-12 min-w-0 items-center gap-3 py-1 text-xs">
            {!failed ? <Spinner className="size-4 shrink-0" /> : null}
            <div className="min-w-0 flex-1">
              <div className={failed ? "text-destructive" : "text-foreground"}>
                {t(`generation.${item.phase}`)}
                <span className="ml-2 text-muted-foreground tabular-nums">{item.run_id.slice(0, 8)}</span>
              </div>
              {measurable ? (
                <div className="mt-1 flex items-center gap-2">
                  <Progress value={item.processed / item.total * 100} aria-label={t("generation.building")} className="h-1 max-w-64 flex-1" />
                  <span className="text-muted-foreground tabular-nums">{item.processed}/{item.total}</span>
                </div>
              ) : null}
            </div>
            {failed ? <Button variant="ghost" size="sm" onClick={onOpenLogs}>{t("generation.openLogs")}</Button> : null}
          </div>
        )
      })}
      </div>
    </ScrollArea>
  )
}
