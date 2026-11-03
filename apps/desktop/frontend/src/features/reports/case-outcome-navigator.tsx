import { useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { VerificationStatus } from "@/features/protocols/types"
import type { ReportEntryDetail } from "./data"

export type ReportCaseTarget = {
  entryID: string
  caseID: string
}

export function CaseOutcomeNavigator({
  entries,
  selected,
  onSelect,
}: {
  entries: ReportEntryDetail[]
  selected: ReportCaseTarget | null
  onSelect: (target: ReportCaseTarget) => void
}) {
  const { t } = useTranslation("reports")
  const cases = entries.flatMap((entry) => entry.cases.map((caseItem) => ({
    entryID: entry.entry_id,
    caseID: caseItem.case_id,
    name: caseItem.name,
    status: caseItem.verification.status,
  })))
  const tileContainer = useRef<HTMLDivElement>(null)
  const [columnGap, setColumnGap] = useState(4)
  useLayoutEffect(() => {
    const container = tileContainer.current
    if (!container) return
    const updateGap = (width: number) => {
      const tile = container.firstElementChild as HTMLElement | null
      if (!tile || width <= 0 || tile.offsetWidth <= 0) return
      const tileWidth = tile.offsetWidth
      const minimumGap = Number.parseFloat(getComputedStyle(container).rowGap)
      const columns = Math.max(1, Math.floor((width + minimumGap) / (tileWidth + minimumGap)))
      const gap = columns > 1 ? (width - columns * tileWidth) / (columns - 1) : minimumGap
      // Round down to the browser's layout precision so the last tile still fits.
      setColumnGap(Math.floor(gap * 64) / 64)
    }
    updateGap(container.clientWidth)
    const observer = new ResizeObserver(([entry]) => updateGap(entry.contentRect.width))
    observer.observe(container)
    return () => observer.disconnect()
  }, [cases.length])
  if (!cases.length) return null

  return (
    <TooltipProvider delayDuration={250}>
    <nav aria-label={t("caseMap.aria")} className="space-y-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-muted-foreground">
        <OutcomeLegend className="bg-outcome-pass" label={t("caseMap.passed")} />
        <OutcomeLegend className="bg-outcome-review" label={t("caseMap.review")} />
        <OutcomeLegend className="bg-outcome-fail" label={t("caseMap.failed")} />
      </div>
      <div ref={tileContainer} className="flex flex-wrap gap-1" style={{ columnGap }}>
        {cases.map((caseItem) => {
          const pressed = selected?.entryID === caseItem.entryID && selected.caseID === caseItem.caseID
          const statusLabel = t(`protocolDesign.verdict.${caseItem.status}`)
          return (
            <Tooltip key={`${caseItem.entryID}-${caseItem.caseID}`}>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  className={`size-5 flex-none rounded-[4px] border p-0 ${outcomeClassName(caseItem.status)} aria-pressed:border-foreground aria-pressed:ring-2 aria-pressed:ring-ring/60`}
                  aria-label={t("caseMap.caseAria", { name: caseItem.name, status: statusLabel })}
                  aria-pressed={pressed}
                  onClick={() => onSelect({ entryID: caseItem.entryID, caseID: caseItem.caseID })}
                >
                  <span className="sr-only">{caseItem.name}</span>
                </Button>
              </TooltipTrigger>
              <TooltipContent sideOffset={4}>{caseItem.name}</TooltipContent>
            </Tooltip>
          )
        })}
      </div>
    </nav>
    </TooltipProvider>
  )
}

function OutcomeLegend({ className, label }: { className: string; label: string }) {
  return <span className="inline-flex items-center gap-1.5"><span aria-hidden className={`size-2.5 rounded-[3px] ${className}`} />{label}</span>
}

function outcomeClassName(status: VerificationStatus): string {
  if (status === "passed") return "border-outcome-pass/60 bg-outcome-pass hover:bg-outcome-pass/80"
  if (status === "failed") return "border-outcome-fail/60 bg-outcome-fail hover:bg-outcome-fail/80"
  return "border-outcome-review/60 bg-outcome-review hover:bg-outcome-review/80"
}
