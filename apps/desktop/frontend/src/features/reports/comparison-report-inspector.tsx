import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { InspectorHeader, InspectorRow } from "@/features/shell/page-frame"
import type { ComparisonSummary } from "@/features/comparisons/data"
import type { ReportSnapshot } from "./data"
import { channelVerdict } from "./report-list-items"

export function ComparisonReportInspector({ comparison, snapshot, onOpen, exportControls }: {
  comparison: ComparisonSummary
  snapshot: ReportSnapshot
  onOpen?: () => void
  exportControls: ReactNode
}) {
  const { t } = useTranslation("reports")
  return <>
    <InspectorHeader title={t("comparison.title")} subtitle={comparison.model_name} />
    <dl className="space-y-3 px-4 py-2">
      <InspectorRow label={t("inspector.plan")} value={comparison.plan_name} />
      <InspectorRow label={t("comparison.status")} value={t(`common:status.${comparison.status}`)} />
      {comparison.channels.map(channel => <InspectorRow key={channel.channel_id} label={channel.channel_name}
        value={channelVerdict(channel, t, snapshot.reports.find(report => report.run_id === channel.run_id))} />)}
    </dl>
    {onOpen ? <div className="px-4 py-2"><Button variant="outline" size="sm" onClick={onOpen}>{t("comparison.view")}</Button></div> : null}
    <Separator />
    {exportControls}
  </>
}
