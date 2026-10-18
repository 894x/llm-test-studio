import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

export function RunInspectorFrame({ title, id, status, tab, onTabChange, execution, summary, failures, artifacts, footer }: {
  title: string; id: string; status: ReactNode
  tab: string; onTabChange: (tab: string) => void
  execution?: ReactNode; summary: ReactNode; failures: ReactNode; artifacts: ReactNode; footer?: ReactNode
}) {
  const { t } = useTranslation("runs")
  const panels = [
    ...(execution ? [{ value: "execution", content: execution }] : []),
    { value: "summary", content: summary },
    { value: "failures", content: failures },
    { value: "artifacts", content: artifacts },
  ]
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    <div className="shrink-0 px-4 pb-3 pt-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-semibold [overflow-wrap:anywhere]">{title}</div>
          <div className="mt-1 [overflow-wrap:anywhere] font-mono text-[10px] text-muted-foreground">{id}</div>
        </div>
        {status}
      </div>
    </div>
    <Tabs value={panels.some(panel => panel.value === tab) ? tab : "summary"} onValueChange={onTabChange} className="min-h-0 min-w-0 flex-1 gap-0">
      <TabsList variant="line" className="mx-4 h-8 shrink-0">
        {panels.map(panel => <TabsTrigger key={panel.value} value={panel.value} className="text-xs">{t(`inspector.${panel.value}`)}</TabsTrigger>)}
      </TabsList>
      {panels.map(panel => <TabsContent key={panel.value} value={panel.value} className="min-h-0 min-w-0 flex-1">
        <ScrollArea contentWidth="viewport" className="min-h-0 h-full px-4 py-2 text-xs [overflow-wrap:anywhere]">{panel.content}</ScrollArea>
      </TabsContent>)}
    </Tabs>
    {footer}
  </div>
}

export function InspectorDefinitionRow({ label, value }: { label: string; value: string }) {
  return <div data-slot="inspector-definition-row" className="min-w-0 py-2 [overflow-wrap:anywhere]">
    <dt className="text-[11px] text-muted-foreground">{label}</dt>
    <dd className="mt-1 min-w-0 whitespace-normal text-xs font-medium" title={value}>{value}</dd>
  </div>
}
